package vi64

// Zigzag maps a signed integer to its zigzag-encoded unsigned form:
// z = (n << 1) ^ (n >> 63), so 0, -1, 1, -2, 2, ... map to 0, 1, 2,
// 3, 4, ....
func Zigzag(n int64) uint64 {
	return uint64(n<<1) ^ uint64(n>>63)
}

// Unzigzag is the inverse of Zigzag: n = (z >> 1) ^ -(z & 1).
func Unzigzag(z uint64) int64 {
	return int64(z>>1) ^ -int64(z&1)
}

// AppendZigzag appends the shortest-form zigzag vi64 encoding of n to b
// and returns the extended slice.
func AppendZigzag(b []byte, n int64) []byte {
	return Append(b, Zigzag(n))
}

// ParseZigzag parses one zigzag vi64 at the start of b, returning the
// signed value and the number of bytes consumed.
func ParseZigzag(b []byte) (int64, int, error) {
	z, n, err := Parse(b)
	return Unzigzag(z), n, err
}
