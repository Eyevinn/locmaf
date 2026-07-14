package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Eyevinn/locmaf"
	"github.com/Eyevinn/locmaf/vi64"
	"github.com/Eyevinn/mp4ff/mp4"
)

// Object kinds reported by dump, matching the LOCMAF header element
// types plus the rawBoxes Object.
const (
	kindRawBoxes = "rawBoxes"
	kindFull     = "full"
	kindDelta    = "delta"
)

// dumpObject is one decoded LOCMAF Object in a .locmaf file.
type dumpObject struct {
	Index     int    `json:"index"`
	WireBytes int    `json:"wireBytes"`
	Kind      string `json:"kind"` // rawBoxes, full, or delta
	// Exactly one of Raw / Moof is set (unless Error is).
	Raw   *rawInfo  `json:"rawBoxes,omitempty"`
	Moof  *moofInfo `json:"moof,omitempty"`
	Error string    `json:"error,omitempty"`
}

// rawInfo describes a rawBoxes Object: the top-level ISO boxes it carries
// and whether it is a CMAF Header (has a moov).
type rawInfo struct {
	Boxes  []string `json:"boxes"`
	IsInit bool     `json:"isInit"`
}

// moofInfo summarizes a moof-carrying Object's decoded effective values.
type moofInfo struct {
	GenBoxes     []string `json:"genBoxes,omitempty"`
	SampleCount  int      `json:"sampleCount"`
	BMDT         uint64   `json:"bmdt"`
	PayloadBytes int      `json:"payloadBytes"`
}

type dumpReport struct {
	Input      string       `json:"input"`
	NumObjects int          `json:"numObjects"`
	Objects    []dumpObject `json:"objects"`
}

func runDump(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("dump", flag.ContinueOnError)
	fs.SetOutput(stderr)
	initPath := fs.String("init", "", "separate init segment (ftyp+moov) when the file carries none in-band")
	format := fs.String("report", formatText, "report format: text or json")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "dump: exactly one input file expected")
		return 2
	}
	if *format != formatText && *format != formatJSON {
		fmt.Fprintf(stderr, "dump: unknown report format %q\n", *format)
		return 2
	}

	report, err := dumpFile(fs.Arg(0), *initPath)
	if err != nil {
		fmt.Fprintf(stderr, "dump: %v\n", err)
		return 2
	}

	if *format == formatJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			fmt.Fprintf(stderr, "dump: %v\n", err)
			return 2
		}
	} else {
		printDumpReport(stdout, report)
	}
	for _, o := range report.Objects {
		if o.Error != "" {
			return 1
		}
	}
	return 0
}

// dumpFile walks the self-framed Objects of a .locmaf file, decoding each
// against a single running in-group State (a full header or rawBoxes
// Object re-anchors it, so group boundaries need no explicit marker).
func dumpFile(inputPath, initPath string) (*dumpReport, error) {
	data, err := os.ReadFile(inputPath)
	if err != nil {
		return nil, err
	}
	objs, err := splitFramed(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", inputPath, err)
	}

	var moov *mp4.MoovBox
	if initPath != "" {
		if moov, err = moovFromFile(initPath); err != nil {
			return nil, err
		}
	}

	report := &dumpReport{Input: inputPath, NumObjects: len(objs)}
	state := locmaf.NewState()
	for i, obj := range objs {
		rec := dumpObject{Index: i, WireBytes: len(obj)}
		kind, err := headerKind(obj)
		if err != nil {
			rec.Error = err.Error()
			report.Objects = append(report.Objects, rec)
			continue
		}
		rec.Kind = kind

		if moov == nil {
			// A self-contained file leads with a rawBoxes Object carrying
			// the init in-band; use it to resolve the moov before any
			// moof-carrying Object can be decoded.
			if kind != kindRawBoxes {
				return nil, fmt.Errorf("%s: object %d is a %s header but the file carries no in-band init: %w",
					inputPath, i, kind, errNoInit)
			}
			content, err := rawBoxesContent(obj)
			if err != nil {
				rec.Error = err.Error()
				report.Objects = append(report.Objects, rec)
				continue
			}
			rec.Raw = &rawInfo{Boxes: boxNames(content)}
			if m, mErr := moovFromBytes(content); mErr == nil {
				moov = m
				rec.Raw.IsInit = true
			}
			report.Objects = append(report.Objects, rec)
			continue
		}

		eff, raw, err := locmaf.Decode(obj, state, moov)
		if err != nil {
			rec.Error = err.Error()
			report.Objects = append(report.Objects, rec)
			continue
		}
		if raw != nil {
			rec.Raw = &rawInfo{Boxes: boxNames(raw)}
			if _, mErr := moovFromBytes(raw); mErr == nil {
				rec.Raw.IsInit = true
			}
			report.Objects = append(report.Objects, rec)
			continue
		}
		mi := &moofInfo{SampleCount: eff.SampleCount, BMDT: eff.BMDT, PayloadBytes: len(eff.MdatPayload)}
		for _, gb := range eff.GenBoxes {
			mi.GenBoxes = append(mi.GenBoxes, gb.Name)
		}
		rec.Moof = mi
		report.Objects = append(report.Objects, rec)
	}
	return report, nil
}

// splitFramed splits a .locmaf byte stream into its length-prefixed
// Objects.
func splitFramed(data []byte) ([][]byte, error) {
	var objs [][]byte
	rest := data
	for len(rest) > 0 {
		obj, next, err := locmaf.NextFramed(rest)
		if err != nil {
			return nil, err
		}
		objs = append(objs, obj)
		rest = next
	}
	if len(objs) == 0 {
		return nil, fmt.Errorf("no framed objects: %w", locmaf.ErrMalformed)
	}
	return objs, nil
}

// headerKind classifies an Object by peeking its element sequence: the
// header element type (full or delta), or rawBoxes, without decoding it.
func headerKind(obj []byte) (string, error) {
	pos := 0
	for pos < len(obj) {
		et, n, err := vi64.Parse(obj[pos:])
		if err != nil {
			return "", fmt.Errorf("invalid element_type: %w", locmaf.ErrMalformed)
		}
		pos += n
		switch et {
		case locmaf.ElementTypeGenBox:
			boxSize, n, err := vi64.Parse(obj[pos:])
			if err != nil {
				return "", fmt.Errorf("invalid genBox box_size: %w", locmaf.ErrMalformed)
			}
			pos += n + int(boxSize)
		case locmaf.ElementTypeFullHeader:
			return kindFull, nil
		case locmaf.ElementTypeDeltaHeader:
			return kindDelta, nil
		case locmaf.ElementTypeRawBoxes:
			return kindRawBoxes, nil
		default:
			return "", fmt.Errorf("unknown element_type %d: %w", et, locmaf.ErrMalformed)
		}
	}
	return "", fmt.Errorf("object has no header element: %w", locmaf.ErrMalformed)
}

// rawBoxesContent returns the boxes carried by a rawBoxes Object (its
// payload after the leading element_type).
func rawBoxesContent(obj []byte) ([]byte, error) {
	et, n, err := vi64.Parse(obj)
	if err != nil {
		return nil, fmt.Errorf("invalid element_type: %w", locmaf.ErrMalformed)
	}
	if et != locmaf.ElementTypeRawBoxes {
		return nil, fmt.Errorf("not a rawBoxes object: %w", locmaf.ErrMalformed)
	}
	return obj[n:], nil
}

// boxNames lists the FourCC of every top-level ISO box in data.
func boxNames(data []byte) []string {
	var names []string
	for _, b := range walkBoxes(data) {
		names = append(names, b.name)
	}
	return names
}

func printDumpReport(w io.Writer, r *dumpReport) {
	fmt.Fprintf(w, "%s: %d object(s)\n", r.Input, r.NumObjects)
	for _, o := range r.Objects {
		switch {
		case o.Error != "":
			fmt.Fprintf(w, "  #%03d %-8s %d bytes: ERROR — %s\n", o.Index, o.Kind, o.WireBytes, o.Error)
		case o.Raw != nil:
			init := ""
			if o.Raw.IsInit {
				init = " (init)"
			}
			fmt.Fprintf(w, "  #%03d %-8s %d bytes: %s%s\n",
				o.Index, o.Kind, o.WireBytes, strings.Join(o.Raw.Boxes, ", "), init)
		case o.Moof != nil:
			gb := ""
			if len(o.Moof.GenBoxes) > 0 {
				gb = "; genBoxes: " + strings.Join(o.Moof.GenBoxes, ", ")
			}
			fmt.Fprintf(w, "  #%03d %-8s %d bytes: %d sample(s), bmdt=%d, %d payload bytes%s\n",
				o.Index, o.Kind, o.WireBytes, o.Moof.SampleCount, o.Moof.BMDT, o.Moof.PayloadBytes, gb)
		}
	}
}
