package main

import (
	"fmt"
	"os"

	"github.com/Eyevinn/locmaf/conform"
)

// loadCMAF reads a fragmented CMAF file and resolves its init via the
// conform loader. When the input carries no inline moov, initPath must
// point at a separate init segment (ftyp+moov).
func loadCMAF(inputPath, initPath string) (*conform.LoadedCMAF, error) {
	raw, err := os.ReadFile(inputPath)
	if err != nil {
		return nil, err
	}
	var initBytes []byte
	if initPath != "" {
		if initBytes, err = os.ReadFile(initPath); err != nil {
			return nil, err
		}
	}
	lc, err := conform.LoadCMAF(raw, initBytes)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", inputPath, err)
	}
	return lc, nil
}
