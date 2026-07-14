package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/Eyevinn/locmaf"
	"github.com/Eyevinn/mp4ff/mp4"
)

var (
	errNotFragmented = errors.New("no media segments (not fragmented CMAF)")
	errNoInit        = errors.New("no init segment in input; pass -init")
	errNoMoov        = errors.New("no moov")
	errMisaligned    = errors.New("direct and round-trip canonical bytes differ")
	errShortBox      = errors.New("box shorter than a header")
)

// chunkResult is the outcome for one CMAF fragment (one LOCMAF Object).
type chunkResult struct {
	Group  int `json:"group"`
	Object int `json:"object"`
	// Aligned: canonical bytes from the source (A) equal the canonical
	// bytes from the encode→decode round trip (B). This is the
	// conformance assertion.
	Aligned bool `json:"aligned"`
	// SourceIdentical: the source chunk bytes already equal the
	// canonical form (no normalization was needed).
	SourceIdentical bool `json:"sourceIdentical"`
	// Normalizations lists box-level differences between source and
	// canonical bytes. With Aligned true these are expected
	// normalizations (mfhd sequence, tfdt version, flag packing, box
	// reorder, redundant defaults) — the effective values match by
	// construction.
	Normalizations []string   `json:"normalizations,omitempty"`
	FirstDiff      *diffPoint `json:"firstDiff,omitempty"`
	Error          string     `json:"error,omitempty"`
	WireBytes      int        `json:"wireBytes"`
	SourceBytes    int        `json:"sourceBytes"`
}

type diffPoint struct {
	Offset    int    `json:"offset"`
	SourceHex string `json:"sourceHex"`
	CanonHex  string `json:"canonHex"`
}

type alignReport struct {
	Input    string        `json:"input"`
	Chunks   []chunkResult `json:"chunks"`
	Aligned  int           `json:"alignedChunks"`
	Diverged int           `json:"divergedChunks"`
}

func runAlign(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("align", flag.ContinueOnError)
	fs.SetOutput(stderr)
	initPath := fs.String("init", "", "separate init segment (ftyp+moov) when the input carries none")
	format := fs.String("report", formatText, "report format: text or json")
	canonOut := fs.String("canon-out", "", "write the canonical CMAF bytes to this path (\"-\" for stdout); only when every chunk aligns")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "align: exactly one input file expected")
		return 2
	}
	if *format != formatText && *format != formatJSON {
		fmt.Fprintf(stderr, "align: unknown report format %q\n", *format)
		return 2
	}

	report, canon, err := alignFile(fs.Arg(0), *initPath, *canonOut != "")
	if err != nil {
		fmt.Fprintf(stderr, "align: %v\n", err)
		return 2
	}

	// When the canonical bytes go to stdout, keep the report off stdout so
	// the binary and text streams do not interleave.
	reportOut := stdout
	if *canonOut == "-" {
		reportOut = stderr
	}
	if *format == formatJSON {
		enc := json.NewEncoder(reportOut)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			fmt.Fprintf(stderr, "align: %v\n", err)
			return 2
		}
	} else {
		printTextReport(reportOut, report)
	}

	if *canonOut != "" {
		switch {
		case report.Diverged > 0:
			fmt.Fprintf(stderr, "align: not writing canonical output; %d chunk(s) diverged\n", report.Diverged)
		case *canonOut == "-":
			if _, err := stdout.Write(canon); err != nil {
				fmt.Fprintf(stderr, "align: %v\n", err)
				return 2
			}
		default:
			if err := os.WriteFile(*canonOut, canon, 0o644); err != nil {
				fmt.Fprintf(stderr, "align: %v\n", err)
				return 2
			}
		}
	}

	if report.Diverged > 0 {
		return 1
	}
	return 0
}

// alignFile verifies every fragment of inputPath. When collectCanon is
// set and no chunk diverges, it also returns the canonical CMAF bytes:
// the leading init/ftyp region unchanged, followed by each chunk's
// canonical form — a byte-for-byte canonicalization of the input that
// can be written out to generate reference files.
func alignFile(inputPath, initPath string, collectCanon bool) (*alignReport, []byte, error) {
	lc, err := loadCMAF(inputPath, initPath)
	if err != nil {
		return nil, nil, err
	}
	raw, f, moov, starts := lc.raw, lc.file, lc.moov, lc.starts

	report := &alignReport{Input: inputPath}
	// Everything before the first chunk (inline ftyp+moov, if any) passes
	// through unchanged; only the media chunks get canonicalized.
	var canonFile []byte
	if collectCanon {
		canonFile = append(canonFile, raw[:starts[0]]...)
	}
	chunkIdx := 0
	for g, seg := range f.Segments {
		tx, rx := locmaf.NewState(), locmaf.NewState()
		for o, frag := range seg.Fragments {
			res := chunkResult{Group: g, Object: o}
			srcStart := starts[chunkIdx]
			srcEnd := uint64(len(raw))
			if chunkIdx+1 < len(starts) {
				srcEnd = starts[chunkIdx+1]
			}
			chunkIdx++
			src := raw[srcStart:srcEnd]
			res.SourceBytes = len(src)

			canon, wire, err := alignFragment(seg, o, frag, tx, rx, moov)
			if err != nil {
				res.Error = err.Error()
				report.Diverged++
				report.Chunks = append(report.Chunks, res)
				continue
			}
			res.Aligned = true
			res.WireBytes = wire
			report.Aligned++
			if collectCanon {
				canonFile = append(canonFile, canon...)
			}

			if bytes.Equal(src, canon) {
				res.SourceIdentical = true
			} else {
				res.Normalizations = boxDiff(src, canon)
				if off := firstDiff(src, canon); off >= 0 {
					res.FirstDiff = &diffPoint{
						Offset:    off,
						SourceHex: hexWindow(src, off),
						CanonHex:  hexWindow(canon, off),
					}
				}
			}
			report.Chunks = append(report.Chunks, res)
		}
	}
	if !collectCanon || report.Diverged > 0 {
		return report, nil, nil
	}
	return report, canonFile, nil
}

// alignFragment runs both paths for one fragment and asserts A == B:
// (A) effective values straight from the source moof → canonical bytes;
// (B) canonical-encode to LOCMAF → decode against the in-group state →
// canonical bytes.
func alignFragment(seg *mp4.MediaSegment, fragIdx int, frag *mp4.Fragment,
	tx, rx *locmaf.State, moov *mp4.MoovBox) ([]byte, int, error) {
	genBoxes, err := fragmentGenBoxes(seg, fragIdx, frag)
	if err != nil {
		return nil, 0, err
	}
	var payload []byte
	if frag.Mdat != nil {
		payload = frag.Mdat.Data
	}

	effA, err := locmaf.ExtractEffective(genBoxes, frag.Moof, payload, moov)
	if err != nil {
		return nil, 0, fmt.Errorf("extract: %w", err)
	}
	canonA, err := locmaf.ReconstructCanonical(moov, effA)
	if err != nil {
		return nil, 0, fmt.Errorf("reconstruct (direct): %w", err)
	}

	obj, err := locmaf.EncodeCanonical(genBoxes, frag.Moof, payload, tx, moov)
	if err != nil {
		return nil, 0, fmt.Errorf("encode: %w", err)
	}
	effB, _, err := locmaf.Decode(obj, rx, moov)
	if err != nil {
		return nil, 0, fmt.Errorf("decode: %w", err)
	}
	canonB, err := locmaf.ReconstructCanonical(moov, effB)
	if err != nil {
		return nil, 0, fmt.Errorf("reconstruct (round trip): %w", err)
	}

	if !bytes.Equal(canonA, canonB) {
		off := firstDiff(canonA, canonB)
		return nil, 0, fmt.Errorf("%w at offset %d (A %d bytes, B %d bytes)",
			errMisaligned, off, len(canonA), len(canonB))
	}
	return canonA, len(obj), nil
}

// fragmentGenBoxes collects the chunk's pre-moof boxes as genBoxes: the
// segment styp on the first fragment, then every fragment-level box
// before the moof (emsg, prft, ...).
func fragmentGenBoxes(seg *mp4.MediaSegment, fragIdx int, frag *mp4.Fragment) ([]locmaf.GenBox, error) {
	var gbs []locmaf.GenBox
	if fragIdx == 0 && seg.Styp != nil {
		gb, err := toGenBox(seg.Styp)
		if err != nil {
			return nil, err
		}
		gbs = append(gbs, gb)
	}
	for _, child := range frag.Children {
		switch child.Type() {
		case "moof", "mdat":
			return gbs, nil
		default:
			gb, err := toGenBox(child)
			if err != nil {
				return nil, err
			}
			gbs = append(gbs, gb)
		}
	}
	return gbs, nil
}

// toGenBox re-serializes a parsed box and strips the 8-byte header,
// yielding the genBox payload (for uuid boxes the usertype leads the
// payload, as the draft requires).
func toGenBox(b mp4.Box) (locmaf.GenBox, error) {
	var buf bytes.Buffer
	if err := b.Encode(&buf); err != nil {
		return locmaf.GenBox{}, fmt.Errorf("re-encode %s: %w", b.Type(), err)
	}
	bb := buf.Bytes()
	if len(bb) < 8 {
		return locmaf.GenBox{}, fmt.Errorf("box %s: %w", b.Type(), errShortBox)
	}
	return locmaf.GenBox{Name: b.Type(), Payload: bb[8:]}, nil
}

// chunkStarts returns the file offset of every chunk in order: the
// segment start for each first fragment (so a leading styp belongs to
// that chunk), the fragment start otherwise.
func chunkStarts(f *mp4.File, fileSize uint64) []uint64 {
	var starts []uint64
	for _, seg := range f.Segments {
		for i, frag := range seg.Fragments {
			if i == 0 && seg.Styp != nil {
				starts = append(starts, seg.StartPos)
			} else {
				starts = append(starts, frag.StartPos)
			}
		}
	}
	sort.Slice(starts, func(i, j int) bool { return starts[i] < starts[j] })
	if len(starts) == 0 {
		starts = append(starts, fileSize)
	}
	return starts
}

// boxDiff walks the top-level boxes of both byte strings and reports
// per-box differences.
func boxDiff(src, canon []byte) []string {
	srcBoxes := walkBoxes(src)
	canonBoxes := walkBoxes(canon)
	var out []string
	i, j := 0, 0
	for i < len(srcBoxes) || j < len(canonBoxes) {
		switch {
		case i >= len(srcBoxes):
			b := canonBoxes[j]
			out = append(out, fmt.Sprintf("%s: only in canonical (%d bytes)", b.name, len(b.body)))
			j++
		case j >= len(canonBoxes):
			b := srcBoxes[i]
			out = append(out, fmt.Sprintf("%s: only in source (%d bytes)", b.name, len(b.body)))
			i++
		case srcBoxes[i].name != canonBoxes[j].name:
			out = append(out, fmt.Sprintf("%s (source) vs %s (canonical): box order or presence differs",
				srcBoxes[i].name, canonBoxes[j].name))
			i++
			j++
		case !bytes.Equal(srcBoxes[i].body, canonBoxes[j].body):
			out = append(out, fmt.Sprintf("%s: normalized (%d → %d bytes)",
				srcBoxes[i].name, len(srcBoxes[i].body), len(canonBoxes[j].body)))
			i++
			j++
		default:
			i++
			j++
		}
	}
	return out
}

type boxSpan struct {
	name string
	body []byte
}

// walkBoxes splits a byte string into its top-level ISO boxes; a
// malformed tail becomes a pseudo-box named "?".
func walkBoxes(data []byte) []boxSpan {
	var out []boxSpan
	pos := 0
	for pos+8 <= len(data) {
		size := int(uint32(data[pos])<<24 | uint32(data[pos+1])<<16 | uint32(data[pos+2])<<8 | uint32(data[pos+3]))
		if size < 8 || pos+size > len(data) {
			break
		}
		out = append(out, boxSpan{name: string(data[pos+4 : pos+8]), body: data[pos : pos+size]})
		pos += size
	}
	if pos < len(data) {
		out = append(out, boxSpan{name: "?", body: data[pos:]})
	}
	return out
}

func firstDiff(a, b []byte) int {
	n := min(len(a), len(b))
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	if len(a) != len(b) {
		return n
	}
	return -1
}

// hexWindow renders up to 16 bytes starting 8 bytes before off.
func hexWindow(data []byte, off int) string {
	start := max(off-8, 0)
	end := min(start+16, len(data))
	return fmt.Sprintf("@%d: % x", start, data[start:end])
}

func printTextReport(w io.Writer, r *alignReport) {
	fmt.Fprintf(w, "%s: %d chunk(s), %d aligned, %d diverged\n", r.Input, len(r.Chunks), r.Aligned, r.Diverged)
	for _, c := range r.Chunks {
		switch {
		case c.Error != "":
			fmt.Fprintf(w, "  g%03d o%03d: DIVERGED — %s\n", c.Group, c.Object, c.Error)
		case c.SourceIdentical:
			fmt.Fprintf(w, "  g%03d o%03d: aligned; source already canonical (%d bytes, %d on the wire)\n",
				c.Group, c.Object, c.SourceBytes, c.WireBytes)
		default:
			fmt.Fprintf(w, "  g%03d o%03d: aligned; %d source bytes → %d wire bytes; normalizations:\n",
				c.Group, c.Object, c.SourceBytes, c.WireBytes)
			for _, n := range c.Normalizations {
				fmt.Fprintf(w, "    - %s\n", n)
			}
			if c.FirstDiff != nil {
				fmt.Fprintf(w, "    first diff at offset %d\n      source %s\n      canon  %s\n",
					c.FirstDiff.Offset, c.FirstDiff.SourceHex, c.FirstDiff.CanonHex)
			}
		}
	}
}
