package vi64_test

import (
	"bytes"
	"math"
	"testing"

	"github.com/Eyevinn/locmaf/vi64"
)

// TestZigzagMapping pins the mapping table from the LOCMAF draft's
// zigzag section: 0↔0, -1↔1, 1↔2, -2↔3, 2↔4, -3↔5, 3↔6.
func TestZigzagMapping(t *testing.T) {
	table := []struct {
		n int64
		z uint64
	}{
		{0, 0}, {-1, 1}, {1, 2}, {-2, 3}, {2, 4}, {-3, 5}, {3, 6},
		{math.MaxInt64, math.MaxUint64 - 1},
		{math.MinInt64, math.MaxUint64},
	}
	for _, tc := range table {
		if got := vi64.Zigzag(tc.n); got != tc.z {
			t.Errorf("Zigzag(%d) = %d, want %d", tc.n, got, tc.z)
		}
		if got := vi64.Unzigzag(tc.z); got != tc.n {
			t.Errorf("Unzigzag(%d) = %d, want %d", tc.z, got, tc.n)
		}
	}
}

func TestZigzagRoundTrip(t *testing.T) {
	var values []int64
	for shift := 0; shift < 63; shift++ {
		v := int64(1) << shift
		values = append(values, v-1, v, v+1, -v, -v-1, -v+1)
	}
	values = append(values, math.MinInt64, math.MaxInt64)
	for _, n := range values {
		if got := vi64.Unzigzag(vi64.Zigzag(n)); got != n {
			t.Errorf("Unzigzag(Zigzag(%d)) = %d", n, got)
		}
		enc := vi64.AppendZigzag(nil, n)
		got, size, err := vi64.ParseZigzag(enc)
		if err != nil || got != n || size != len(enc) {
			t.Errorf("ParseZigzag(AppendZigzag(%d)) = (%d, %d, %v)", n, got, size, err)
		}
	}
}

// TestZigzagShortForms checks that small magnitudes of either sign get
// the 1-byte form — the point of the zigzag mapping.
func TestZigzagShortForms(t *testing.T) {
	for n := int64(-64); n <= 63; n++ {
		if enc := vi64.AppendZigzag(nil, n); len(enc) != 1 {
			t.Errorf("AppendZigzag(%d) = % x (%d bytes), want 1 byte", n, enc, len(enc))
		}
	}
	if enc := vi64.AppendZigzag(nil, -1); !bytes.Equal(enc, []byte{0x01}) {
		t.Errorf("AppendZigzag(-1) = % x, want 01", enc)
	}
	if enc := vi64.AppendZigzag(nil, 64); !bytes.Equal(enc, []byte{0x80, 0x80}) {
		t.Errorf("AppendZigzag(64) = % x, want 80 80", enc)
	}
}
