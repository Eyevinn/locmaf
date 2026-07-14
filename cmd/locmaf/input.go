package main

import (
	"bytes"
	"fmt"
	"os"

	"github.com/Eyevinn/mp4ff/mp4"
)

// loadedCMAF is a parsed fragmented CMAF input with its init resolved:
// the raw file bytes, the parsed file, the moov (from an inline init or a
// separate -init file), the ftyp+moov region as verbatim bytes (for
// carrying in-band as a rawBoxes Object), and the per-chunk start offsets.
type loadedCMAF struct {
	raw       []byte
	file      *mp4.File
	moov      *mp4.MoovBox
	initBytes []byte
	starts    []uint64
}

// loadCMAF reads a fragmented CMAF file and resolves its init. When the
// input carries no inline moov, initPath must point at a separate init
// segment (ftyp+moov).
func loadCMAF(inputPath, initPath string) (*loadedCMAF, error) {
	raw, err := os.ReadFile(inputPath)
	if err != nil {
		return nil, err
	}
	f, err := mp4.DecodeFile(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", inputPath, err)
	}
	if len(f.Segments) == 0 {
		return nil, fmt.Errorf("%s: %w", inputPath, errNotFragmented)
	}
	starts := chunkStarts(f, uint64(len(raw)))

	moov := f.Moov
	if f.Init != nil {
		moov = f.Init.Moov
	}
	if moov != nil {
		// Inline init: everything before the first chunk is ftyp+moov.
		return &loadedCMAF{raw: raw, file: f, moov: moov, initBytes: raw[:starts[0]], starts: starts}, nil
	}

	if initPath == "" {
		return nil, fmt.Errorf("%s: %w", inputPath, errNoInit)
	}
	ib, err := os.ReadFile(initPath)
	if err != nil {
		return nil, err
	}
	initFile, err := mp4.DecodeFile(bytes.NewReader(ib))
	if err != nil {
		return nil, fmt.Errorf("parse init %s: %w", initPath, err)
	}
	if initFile.Init == nil && initFile.Moov == nil {
		return nil, fmt.Errorf("%s: %w", initPath, errNoMoov)
	}
	moov = initFile.Moov
	if initFile.Init != nil {
		moov = initFile.Init.Moov
	}
	return &loadedCMAF{raw: raw, file: f, moov: moov, initBytes: ib, starts: starts}, nil
}

// moovFromBytes parses an init segment (ftyp+moov, or a bare moov) from a
// byte slice and returns its moov.
func moovFromBytes(b []byte) (*mp4.MoovBox, error) {
	f, err := mp4.DecodeFile(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	if f.Init != nil && f.Init.Moov != nil {
		return f.Init.Moov, nil
	}
	if f.Moov != nil {
		return f.Moov, nil
	}
	return nil, errNoMoov
}

// moovFromFile reads and parses a separate init segment file.
func moovFromFile(path string) (*mp4.MoovBox, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	m, err := moovFromBytes(b)
	if err != nil {
		return nil, fmt.Errorf("parse init %s: %w", path, err)
	}
	return m, nil
}
