package vi64_test

import (
	"bytes"
	"errors"
	"io"
	"math"
	"testing"

	"github.com/Eyevinn/locmaf/vi64"
)

// draftExamples is Table 2 of draft-ietf-moq-transport-18, Section 1.4.1.
var draftExamples = []struct {
	enc     []byte
	value   uint64
	minimal bool
}{
	{[]byte{0x25}, 37, true},
	{[]byte{0x80, 0x25}, 37, false},
	{[]byte{0xbb, 0xbd}, 15293, true},
	{[]byte{0xed, 0x7f, 0x3e, 0x7d}, 226442877, true},
	{[]byte{0xfa, 0xa1, 0xa0, 0xe4, 0x03, 0xd8}, 2893212287960, true},
	{[]byte{0xfc, 0x89, 0x98, 0xab, 0xc6, 0x6b, 0xc0}, 151288809941952, true},
	{[]byte{0xfe, 0xfa, 0x31, 0x8f, 0xa8, 0xe3, 0xca, 0x11}, 70423237261249041, true},
	{[]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}, math.MaxUint64, true},
}

func TestDraftExamples(t *testing.T) {
	for _, tc := range draftExamples {
		v, n, err := vi64.Parse(tc.enc)
		if err != nil {
			t.Fatalf("Parse(% x): %v", tc.enc, err)
		}
		if v != tc.value || n != len(tc.enc) {
			t.Errorf("Parse(% x) = (%d, %d), want (%d, %d)", tc.enc, v, n, tc.value, len(tc.enc))
		}
		if tc.minimal {
			if got := vi64.Append(nil, tc.value); !bytes.Equal(got, tc.enc) {
				t.Errorf("Append(%d) = % x, want % x", tc.value, got, tc.enc)
			}
			if got := vi64.Len(tc.value); got != len(tc.enc) {
				t.Errorf("Len(%d) = %d, want %d", tc.value, got, len(tc.enc))
			}
		}
	}
}

// TestLenBoundaries pins the length steps of the encoding, including the
// points where it diverges from the RFC 9000 QUIC varint: 64..127 are
// one byte here (two bytes in RFC 9000) and values >= 2^62 are encodable
// at all.
func TestLenBoundaries(t *testing.T) {
	boundaries := []struct {
		below uint64 // largest value of the shorter form
		size  int    // Len(below); Len(below+1) must be size+1
	}{
		{1<<7 - 1, 1},
		{1<<14 - 1, 2},
		{1<<21 - 1, 3},
		{1<<28 - 1, 4},
		{1<<35 - 1, 5},
		{1<<42 - 1, 6},
		{1<<49 - 1, 7},
		{1<<56 - 1, 8},
	}
	for _, b := range boundaries {
		if got := vi64.Len(b.below); got != b.size {
			t.Errorf("Len(%d) = %d, want %d", b.below, got, b.size)
		}
		if got := vi64.Len(b.below + 1); got != b.size+1 {
			t.Errorf("Len(%d) = %d, want %d", b.below+1, got, b.size+1)
		}
	}
	for v, want := range map[uint64]int{0: 1, 63: 1, 64: 1, 127: 1, 128: 2,
		1<<62 - 1: 9, 1 << 62: 9, math.MaxUint64: 9} {
		if got := vi64.Len(v); got != want {
			t.Errorf("Len(%d) = %d, want %d", v, got, want)
		}
	}
}

func TestRoundTrip(t *testing.T) {
	var values []uint64
	for shift := 0; shift < 64; shift++ {
		v := uint64(1) << shift
		values = append(values, v-1, v, v+1)
	}
	values = append(values, math.MaxUint64)
	for _, v := range values {
		enc := vi64.Append(nil, v)
		if len(enc) != vi64.Len(v) {
			t.Errorf("Append(%d) produced %d bytes, Len says %d", v, len(enc), vi64.Len(v))
		}
		got, n, err := vi64.Parse(enc)
		if err != nil || got != v || n != len(enc) {
			t.Errorf("Parse(Append(%d)) = (%d, %d, %v)", v, got, n, err)
		}
		r, err := vi64.Read(bytes.NewReader(enc))
		if err != nil || r != v {
			t.Errorf("Read(Append(%d)) = (%d, %v)", v, r, err)
		}
	}
}

// TestNonMinimalAccepted widens value 1 to every encoding length; Parse
// and Read must accept all of them, per draft-18 ("any encoding length
// that can represent the value is valid").
func TestNonMinimalAccepted(t *testing.T) {
	for n := 2; n <= vi64.MaxLen; n++ {
		enc := make([]byte, n)
		if n == 9 {
			enc[0] = 0xFF
		} else {
			enc[0] = 0xFF << (9 - n)
		}
		enc[n-1] = 0x01
		v, got, err := vi64.Parse(enc)
		if err != nil || v != 1 || got != n {
			t.Errorf("Parse(% x) = (%d, %d, %v), want (1, %d, nil)", enc, v, got, err, n)
		}
	}
}

func TestTruncated(t *testing.T) {
	cases := [][]byte{
		{},
		{0x80},
		{0xc0, 0x00},
		{0xfe, 0x01, 0x02},
		{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
	}
	for _, enc := range cases {
		if _, _, err := vi64.Parse(enc); !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Errorf("Parse(% x) error = %v, want io.ErrUnexpectedEOF", enc, err)
		}
	}
	// Read: EOF before the first byte stays io.EOF ...
	if _, err := vi64.Read(bytes.NewReader(nil)); !errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("Read(empty) error = %v, want io.EOF", err)
	}
	// ... but an EOF inside the value is io.ErrUnexpectedEOF.
	for _, enc := range cases[1:] {
		if _, err := vi64.Read(bytes.NewReader(enc)); !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Errorf("Read(% x) error = %v, want io.ErrUnexpectedEOF", enc, err)
		}
	}
}

func TestReadSequential(t *testing.T) {
	values := []uint64{0, 37, 64, 15293, 1<<56 - 1, math.MaxUint64}
	var enc []byte
	for _, v := range values {
		enc = vi64.Append(enc, v)
	}
	r := bytes.NewReader(enc)
	for _, want := range values {
		got, err := vi64.Read(r)
		if err != nil || got != want {
			t.Fatalf("Read = (%d, %v), want %d", got, err, want)
		}
	}
	if _, err := vi64.Read(r); !errors.Is(err, io.EOF) {
		t.Errorf("Read past end = %v, want io.EOF", err)
	}
}
