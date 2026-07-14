package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/Eyevinn/locmaf"
	"github.com/Eyevinn/mp4ff/mp4"
)

// verifyObject is the conformance outcome for one Object of a .locmaf file.
type verifyObject struct {
	Index      int    `json:"index"`
	WireBytes  int    `json:"wireBytes"`
	Kind       string `json:"kind"`
	Conformant bool   `json:"conformant"`
	// Error is a rung 1/2 failure: the Object did not decode, or its
	// effective values did not reconstruct a canonical chunk.
	Error string `json:"error,omitempty"`
	// CanonBytes and FirstDiff describe a rung 3 failure: the Object
	// decodes and reconstructs but is not itself canonical — the
	// canonical re-encode differs from the wire bytes.
	CanonBytes int        `json:"canonBytes,omitempty"`
	FirstDiff  *diffPoint `json:"firstDiff,omitempty"`
}

type verifyReport struct {
	Input         string         `json:"input"`
	Strict        bool           `json:"strict"`
	NumObjects    int            `json:"numObjects"`
	Conformant    int            `json:"conformantObjects"`
	NonConformant int            `json:"nonConformantObjects"`
	Objects       []verifyObject `json:"objects"`
}

func runVerify(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	initPath := fs.String("init", "", "separate init segment (ftyp+moov) when the file carries none in-band")
	format := fs.String("report", formatText, "report format: text or json")
	decodable := fs.Bool("decodable", false, "check only that each Object decodes and reconstructs; skip the canonical-bytes check")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "verify: exactly one input file expected")
		return 2
	}
	if *format != formatText && *format != formatJSON {
		fmt.Fprintf(stderr, "verify: unknown report format %q\n", *format)
		return 2
	}

	report, err := verifyFile(fs.Arg(0), *initPath, !*decodable)
	if err != nil {
		fmt.Fprintf(stderr, "verify: %v\n", err)
		return 2
	}

	if *format == formatJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			fmt.Fprintf(stderr, "verify: %v\n", err)
			return 2
		}
	} else {
		printVerifyReport(stdout, report)
	}
	if report.NonConformant > 0 {
		return 1
	}
	return 0
}

// verifyFile walks the Objects of a .locmaf file and checks each against
// the conformance ladder: it decodes (rung 1), reconstructs a canonical
// CMAF chunk (rung 2), and — when strict — re-encodes that chunk and
// requires the result to be byte-identical to the wire Object (rung 3,
// the canonical-encoding conformance). A rawBoxes Object is carried
// verbatim, so it is canonical by construction once it decodes.
//
// Two in-group states run in parallel: rx follows the wire (deltas as
// received), tx follows the canonical re-encode. For a canonical stream
// they stay in lockstep. A non-canonical Object is flagged where it
// occurs; because delta headers chain, a later Object's verdict can be
// affected by an earlier non-canonical one — the first failure is the
// reliable signal.
func verifyFile(inputPath, initPath string, strict bool) (*verifyReport, error) {
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

	report := &verifyReport{Input: inputPath, Strict: strict, NumObjects: len(objs)}
	rx, tx := locmaf.NewState(), locmaf.NewState()
	for i, obj := range objs {
		rec := verifyObject{Index: i, WireBytes: len(obj)}
		kind, kerr := headerKind(obj)
		if kerr != nil {
			rec.Error = kerr.Error()
			report.NonConformant++
			report.Objects = append(report.Objects, rec)
			continue
		}
		rec.Kind = kind

		if moov == nil {
			// Resolve the moov from a leading in-band init rawBoxes.
			if kind != kindRawBoxes {
				return nil, fmt.Errorf("%s: object %d is a %s header but the file carries no in-band init: %w",
					inputPath, i, kind, errNoInit)
			}
			content, cerr := rawBoxesContent(obj)
			if cerr != nil {
				rec.Error = cerr.Error()
				report.NonConformant++
				report.Objects = append(report.Objects, rec)
				continue
			}
			m, merr := moovFromBytes(content)
			if merr != nil {
				rec.Error = fmt.Sprintf("leading rawBoxes is not a valid init: %v", merr)
				report.NonConformant++
				report.Objects = append(report.Objects, rec)
				continue
			}
			moov = m
			rec.Conformant = true // rawBoxes: carried verbatim, canonical by construction
			report.Conformant++
			report.Objects = append(report.Objects, rec)
			continue
		}

		conf, canonObj, verr := verifyObjectAt(obj, rx, tx, moov, strict)
		switch {
		case verr != nil:
			rec.Error = verr.Error()
			report.NonConformant++
		case conf:
			rec.Conformant = true
			report.Conformant++
		default:
			rec.CanonBytes = len(canonObj)
			if off := firstDiff(obj, canonObj); off >= 0 {
				rec.FirstDiff = &diffPoint{
					Offset:    off,
					SourceHex: hexWindow(obj, off),
					CanonHex:  hexWindow(canonObj, off),
				}
			}
			report.NonConformant++
		}
		report.Objects = append(report.Objects, rec)
	}
	return report, nil
}

// verifyObjectAt runs one Object through the ladder, advancing the decode
// state rx and (in strict mode) the encode state tx. It returns whether
// the Object is conformant, the canonical re-encode (only when it differs
// from the wire bytes), and any rung 1/2 error.
func verifyObjectAt(obj []byte, rx, tx *locmaf.State, moov *mp4.MoovBox, strict bool) (bool, []byte, error) {
	eff, raw, err := locmaf.Decode(obj, rx, moov)
	if err != nil {
		return false, nil, err
	}
	if raw != nil {
		// A rawBoxes Object resets the in-group chain on both sides.
		tx.Reset()
		return true, nil, nil
	}
	chunk, err := locmaf.ReconstructCanonical(moov, eff)
	if err != nil {
		return false, nil, err
	}
	if !strict {
		return true, nil, nil
	}
	// Re-encode from the decoded content. The genBoxes and mdat payload
	// come straight from the decode (eff); only the moof needs to be
	// recovered by parsing the canonical chunk.
	moof, err := parseMoof(chunk)
	if err != nil {
		return false, nil, fmt.Errorf("re-parse canonical chunk: %w", err)
	}
	canonObj, err := locmaf.EncodeCanonical(eff.GenBoxes, moof, eff.MdatPayload, tx, moov)
	if err != nil {
		return false, nil, fmt.Errorf("canonical re-encode: %w", err)
	}
	if bytes.Equal(canonObj, obj) {
		return true, nil, nil
	}
	return false, canonObj, nil
}

// parseMoof recovers the moof box from a reconstructed canonical CMAF
// chunk (which may carry leading genBoxes and a trailing mdat).
func parseMoof(chunk []byte) (*mp4.MoofBox, error) {
	f, err := mp4.DecodeFile(bytes.NewReader(chunk))
	if err != nil {
		return nil, err
	}
	if len(f.Segments) == 0 || len(f.Segments[0].Fragments) == 0 {
		return nil, fmt.Errorf("reconstructed chunk has no fragment: %w", locmaf.ErrMalformed)
	}
	return f.Segments[0].Fragments[0].Moof, nil
}

func printVerifyReport(w io.Writer, r *verifyReport) {
	mode := "strict: canonical"
	if !r.Strict {
		mode = "decodable"
	}
	fmt.Fprintf(w, "%s: %d object(s), %d conformant, %d non-conformant [%s]\n",
		r.Input, r.NumObjects, r.Conformant, r.NonConformant, mode)
	for _, o := range r.Objects {
		switch {
		case o.Error != "":
			fmt.Fprintf(w, "  #%03d %-8s %d bytes: NON-CONFORMANT — %s\n", o.Index, o.Kind, o.WireBytes, o.Error)
		case o.Conformant:
			fmt.Fprintf(w, "  #%03d %-8s %d bytes: ok\n", o.Index, o.Kind, o.WireBytes)
		default:
			fmt.Fprintf(w, "  #%03d %-8s %d bytes: NON-CONFORMANT — not canonical (%d wire vs %d canonical bytes)\n",
				o.Index, o.Kind, o.WireBytes, o.WireBytes, o.CanonBytes)
			if o.FirstDiff != nil {
				fmt.Fprintf(w, "    first diff at offset %d\n      wire   %s\n      canon  %s\n",
					o.FirstDiff.Offset, o.FirstDiff.SourceHex, o.FirstDiff.CanonHex)
			}
		}
	}
}
