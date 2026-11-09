package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/Eyevinn/locmaf"
	"github.com/Eyevinn/locmaf/conform"
	"github.com/Eyevinn/mp4ff/mp4"
)

// runPack encodes a fragmented CMAF file as a self-contained .locmaf
// file: a leading rawBoxes Object carrying the init (ftyp+moov) in-band,
// then one length-prefixed LOCMAF Object per CMAF chunk, and a trailing
// rawBoxes Object for any boxes after the last mdat. The file is a
// single group (one delta chain): the first chunk carries a full header
// and the rest delta headers, re-anchoring with a full header only at a
// timeline (BMDT) discontinuity. CMAF segment structure is preserved by
// the styp genBoxes, not by group boundaries.
func runPack(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("pack", flag.ContinueOnError)
	fs.SetOutput(stderr)
	initPath := fs.String("init", "", "separate init segment (ftyp+moov) when the input carries none")
	out := fs.String("o", "-", "output .locmaf path (\"-\" for stdout)")
	noInit := fs.Bool("no-init", false, "omit the leading in-band init rawBoxes Object (bare media; needs -init to decode)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "pack: exactly one input file expected")
		return 2
	}

	lc, err := loadCMAF(fs.Arg(0), *initPath)
	if err != nil {
		fmt.Fprintf(stderr, "pack: %v\n", err)
		return 2
	}
	data, objects, err := packFile(lc, !*noInit)
	if err != nil {
		fmt.Fprintf(stderr, "pack: %v\n", err)
		return 2
	}

	if *out == "-" {
		if _, err := stdout.Write(data); err != nil {
			fmt.Fprintf(stderr, "pack: %v\n", err)
			return 2
		}
		return 0
	}
	if err := os.WriteFile(*out, data, 0o644); err != nil {
		fmt.Fprintf(stderr, "pack: %v\n", err)
		return 2
	}
	fmt.Fprintf(stderr, "pack: wrote %d object(s), %d bytes to %s\n", objects, len(data), *out)
	return 0
}

// packFile builds the self-framed .locmaf byte stream from a loaded CMAF
// input and returns it together with the number of Objects written. When
// includeInit is set, the stream leads with a rawBoxes Object carrying
// the init in-band; otherwise it holds bare media Objects and a decoder
// must be given the init separately. The whole media is one group: a
// single running state chains deltas across every segment, so full
// headers appear only at the start and at BMDT discontinuities.
func packFile(lc *conform.LoadedCMAF, includeInit bool) ([]byte, int, error) {
	state := locmaf.NewState()
	var out []byte
	objects := 0
	if includeInit {
		initObj, err := locmaf.EncodeRaw(lc.InitBytes, state)
		if err != nil {
			return nil, 0, fmt.Errorf("encode init as rawBoxes: %w", err)
		}
		out = locmaf.AppendFramed(out, initObj)
		objects = 1
	}

	// Boxes after a segment's last mdat precede the next segment's first
	// moof in the group, so they ride as genBoxes of that chunk. Only
	// those after the last mdat of the file are trailing boxes, carried
	// as a rawBoxes Object at the end.
	var pending []mp4.Box
	for s, seg := range lc.File.Segments {
		for o, frag := range seg.Fragments {
			genBoxes, err := conform.FragmentGenBoxes(seg, o, frag)
			if err != nil {
				return nil, 0, fmt.Errorf("segment %d chunk %d: %w", s, o, err)
			}
			if len(pending) > 0 {
				lead := make([]locmaf.GenBox, 0, len(pending)+len(genBoxes))
				for _, b := range pending {
					gb, err := conform.ToGenBox(b)
					if err != nil {
						return nil, 0, fmt.Errorf("segment %d chunk %d: %w", s, o, err)
					}
					lead = append(lead, gb)
				}
				genBoxes = append(lead, genBoxes...)
				pending = nil
			}
			var payload []byte
			if frag.Mdat != nil {
				payload = frag.Mdat.Data
			}
			obj, err := locmaf.EncodeCanonical(genBoxes, frag.Moof, payload, state, lc.Moov)
			if err != nil {
				return nil, 0, fmt.Errorf("segment %d chunk %d encode: %w", s, o, err)
			}
			out = locmaf.AppendFramed(out, obj)
			objects++
			pending = append(pending, conform.FragmentTrailingBoxes(frag)...)
		}
	}
	if len(pending) > 0 {
		boxes, err := conform.EncodeBoxes(pending)
		if err != nil {
			return nil, 0, fmt.Errorf("trailing boxes: %w", err)
		}
		obj, err := locmaf.EncodeRaw(boxes, state)
		if err != nil {
			return nil, 0, fmt.Errorf("encode trailing boxes as rawBoxes: %w", err)
		}
		out = locmaf.AppendFramed(out, obj)
		objects++
	}
	return out, objects, nil
}
