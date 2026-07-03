package vi64

import (
	"errors"
	"io"
	"math/bits"
)

// MaxLen is the maximum encoded length of a vi64 in bytes.
const MaxLen = 9

// Len returns the length in bytes (1 to 9) of the shortest encoding of v.
func Len(v uint64) int {
	switch {
	case v < 1<<7:
		return 1
	case v < 1<<14:
		return 2
	case v < 1<<21:
		return 3
	case v < 1<<28:
		return 4
	case v < 1<<35:
		return 5
	case v < 1<<42:
		return 6
	case v < 1<<49:
		return 7
	case v < 1<<56:
		return 8
	default:
		return 9
	}
}

// Append appends the shortest-form encoding of v to b and returns the
// extended slice.
func Append(b []byte, v uint64) []byte {
	n := Len(v)
	prefix := byte(0xFF) << uint(9-n)
	b = append(b, prefix|byte(v>>uint(8*(n-1))))
	for shift := 8 * (n - 2); shift >= 0; shift -= 8 {
		b = append(b, byte(v>>uint(shift)))
	}
	return b
}

// Parse parses one vi64 at the start of b, returning the value and the
// number of bytes consumed. Non-minimal encodings are accepted. If b is
// empty or ends inside the value, Parse returns io.ErrUnexpectedEOF.
func Parse(b []byte) (uint64, int, error) {
	if len(b) == 0 {
		return 0, 0, io.ErrUnexpectedEOF
	}
	n := bits.LeadingZeros8(^b[0]) + 1
	if len(b) < n {
		return 0, 0, io.ErrUnexpectedEOF
	}
	v := uint64(b[0]) & (0xFF >> uint(n))
	for i := 1; i < n; i++ {
		v = v<<8 | uint64(b[i])
	}
	return v, n, nil
}

// Read reads one vi64 from r. An io.EOF before the first byte is
// returned as io.EOF; an EOF inside the value is io.ErrUnexpectedEOF.
func Read(r io.ByteReader) (uint64, error) {
	first, err := r.ReadByte()
	if err != nil {
		return 0, err
	}
	n := bits.LeadingZeros8(^first) + 1
	v := uint64(first) & (0xFF >> uint(n))
	for i := 1; i < n; i++ {
		c, err := r.ReadByte()
		if err != nil {
			if errors.Is(err, io.EOF) {
				err = io.ErrUnexpectedEOF
			}
			return 0, err
		}
		v = v<<8 | uint64(c)
	}
	return v, nil
}
