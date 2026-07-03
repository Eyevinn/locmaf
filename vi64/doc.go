// Package vi64 implements the MOQT variable-length integer encoding
// (Section 1.4.1 of draft-ietf-moq-transport-18) and the zigzag signed
// variant that LOCMAF (draft-einarsson-moq-locmaf) layers on top of it.
//
// The encoding uses the number of leading 1 bits of the first byte to
// indicate the length in bytes: 1 to 9 bytes covering the full uint64
// range (7 usable bits in the 1-byte form, then 14, 21, 28, 35, 42, 49,
// and 56 bits, and the full 64 bits in the 9-byte form whose first byte
// is 0xFF). The value occupies the remaining bits in network byte order.
//
// This is NOT the RFC 9000 QUIC variable-length integer used by MOQT
// drafts up to and including version 16: the two encodings agree only
// for values 0-63, and the QUIC varint cannot represent values above
// 2^62-1 at all. draft-ietf-moq-transport-17 introduced this encoding
// but forbade the 7-byte form (leading byte 0xFC or 0xFD); draft-18
// legalized it. This package implements draft-18, which LOCMAF
// references normatively.
//
// Append and Len always use the shortest form, as LOCMAF's canonical
// encoding requires on the encode side. Parse and Read accept
// non-minimal encodings, which MOQT explicitly permits; a caller
// checking canonical form can require that the consumed length equals
// Len of the parsed value.
//
// The zigzag mapping interleaves signed values so that small magnitudes
// of either sign get short encodings: 0, -1, 1, -2, 2, ... map to
// 0, 1, 2, 3, 4, .... LOCMAF uses zigzag vi64 values wherever a signed
// value is written.
package vi64
