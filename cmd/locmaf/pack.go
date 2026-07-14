package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/Eyevinn/locmaf"
)

// runPack encodes a fragmented CMAF file as a self-contained .locmaf
// file: a leading rawBoxes Object carrying the init (ftyp+moov) in-band,
// then one length-prefixed LOCMAF Object per CMAF chunk. Each CMAF
// segment is one MOQT group, so its first chunk carries a full header and
// the rest carry delta headers.
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
// must be given the init separately.
func packFile(lc *loadedCMAF, includeInit bool) ([]byte, int, error) {
	state := locmaf.NewState()
	var out []byte
	objects := 0
	if includeInit {
		initObj, err := locmaf.EncodeRaw(lc.initBytes, state)
		if err != nil {
			return nil, 0, fmt.Errorf("encode init as rawBoxes: %w", err)
		}
		out = locmaf.AppendFramed(out, initObj)
		objects = 1
	}

	for s, seg := range lc.file.Segments {
		state.Reset() // each CMAF segment is a new MOQT group
		for o, frag := range seg.Fragments {
			genBoxes, err := fragmentGenBoxes(seg, o, frag)
			if err != nil {
				return nil, 0, fmt.Errorf("segment %d chunk %d: %w", s, o, err)
			}
			var payload []byte
			if frag.Mdat != nil {
				payload = frag.Mdat.Data
			}
			obj, err := locmaf.EncodeCanonical(genBoxes, frag.Moof, payload, state, lc.moov)
			if err != nil {
				return nil, 0, fmt.Errorf("segment %d chunk %d encode: %w", s, o, err)
			}
			out = locmaf.AppendFramed(out, obj)
			objects++
		}
	}
	return out, objects, nil
}
