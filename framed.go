package locmaf

import (
	"fmt"

	"github.com/Eyevinn/locmaf/vi64"
)

// Self-framed carriage (the draft's Use Outside MOQT section): outside
// MOQT, each LOCMAF Object is prefixed by its vi64 length, and the
// concatenation of a group's length-prefixed Objects is a LOCMAF
// segment — directly a file on disk. A self-contained file starts with
// a rawBoxes Object (see EncodeRaw) carrying the ftyp + moov bytes.

// AppendFramed appends one LOCMAF Object to dst, prefixed by its vi64
// length, and returns the extended slice.
func AppendFramed(dst, obj []byte) []byte {
	dst = vi64.Append(dst, uint64(len(obj)))
	return append(dst, obj...)
}

// NextFramed splits data into its first length-prefixed LOCMAF Object
// and the remaining bytes. Callers loop until rest is empty; obj is a
// subslice of data.
func NextFramed(data []byte) (obj, rest []byte, err error) {
	length, n, err := vi64.Parse(data)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid framed object length: %w", ErrMalformed)
	}
	if length > uint64(len(data)-n) {
		return nil, nil, fmt.Errorf("framed object length %d exceeds the %d available bytes: %w",
			length, len(data)-n, ErrMalformed)
	}
	return data[n : n+int(length)], data[n+int(length):], nil
}
