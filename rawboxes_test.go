package locmaf

import (
	"encoding/hex"
	"testing"

	"github.com/Eyevinn/mp4ff/mp4"
	"github.com/stretchr/testify/require"
)

// testBoxes is a valid rawBoxes content: a 16-byte ftyp followed by an
// 8-byte free box.
func testBoxes(t *testing.T) []byte {
	t.Helper()
	boxes, err := hex.DecodeString(
		"00000010" + "66747970" + "636d6632" + "00000000" + // ftyp cmf2, minor 0
			"00000008" + "66726565") // free
	require.NoError(t, err)
	return boxes
}

// TestRawBoxesRoundTrip pins the rawBoxes wire framing and the verbatim
// passthrough on decode.
func TestRawBoxesRoundTrip(t *testing.T) {
	moov := buildSyntheticMoov(t, 0x01010000)
	boxes := testBoxes(t)

	tx := NewState()
	obj, err := EncodeRaw(boxes, tx)
	require.NoError(t, err)
	require.Equal(t, []byte{0x04}, obj[:1], "element_type 4, no length of its own")
	require.Equal(t, boxes, obj[1:], "the Object length delimits the boxes")

	rx := NewState()
	eff, raw, err := Decode(obj, rx, moov)
	require.NoError(t, err)
	require.Nil(t, eff)
	require.Equal(t, boxes, raw, "raw boxes pass through verbatim (their canonical form)")
}

// TestRawBoxesResetsDeltaState: a rawBoxes Object resets the in-group
// reference on both sides — the encoder re-anchors with a full header,
// and the receiver rejects a delta that follows rawBoxes directly.
func TestRawBoxesResetsDeltaState(t *testing.T) {
	moov := buildSyntheticMoov(t, 0x01010000)
	samples := []mp4.FullSample{mkSample(3000, 700, 0x01010000, 0xAA)}
	payload := samplePayload(samples)

	// Encode side: full, delta, then EncodeRaw, then full again.
	tx := NewState()
	obj1, err := EncodeCanonical(nil, makeFragment(t, 1, 90000, samples), payload, tx, moov)
	require.NoError(t, err)
	require.Equal(t, ElementTypeFullHeader, headerElementType(t, obj1))
	obj2, err := EncodeCanonical(nil, makeFragment(t, 2, 93000, samples), payload, tx, moov)
	require.NoError(t, err)
	require.Equal(t, ElementTypeDeltaHeader, headerElementType(t, obj2))
	rawObj, err := EncodeRaw(testBoxes(t), tx)
	require.NoError(t, err)
	obj3, err := EncodeCanonical(nil, makeFragment(t, 3, 96000, samples), payload, tx, moov)
	require.NoError(t, err)
	require.Equal(t, ElementTypeFullHeader, headerElementType(t, obj3),
		"the chunk after a rawBoxes Object re-anchors with a full header")

	// Decode side: full, rawBoxes, then the old delta must be rejected.
	rx := NewState()
	_, _, err = Decode(obj1, rx, moov)
	require.NoError(t, err)
	_, raw, err := Decode(rawObj, rx, moov)
	require.NoError(t, err)
	require.NotNil(t, raw)
	_, _, err = Decode(obj2, rx, moov)
	require.ErrorIs(t, err, ErrMalformed, "delta directly after rawBoxes")
	_, _, err = Decode(obj3, rx, moov)
	require.NoError(t, err, "the re-anchoring full header decodes")
}

// TestRawBoxesMalformed covers the wire-level MUST-rejects of the
// rawBoxes element.
func TestRawBoxesMalformed(t *testing.T) {
	moov := buildSyntheticMoov(t, 0x01010000)
	boxes := testBoxes(t)

	cases := []struct {
		name string
		obj  []byte
	}{
		{"empty content", []byte{0x04}},
		{"box size 0 (to-end-of-file escape)", []byte{0x04, 0, 0, 0, 0, 'f', 'r', 'e', 'e'}},
		{"box size 1 (largesize escape)", []byte{0x04, 0, 0, 0, 1, 'f', 'r', 'e', 'e'}},
		{"truncated box header", []byte{0x04, 0, 0, 0, 8}},
		{"box exceeds content", []byte{0x04, 0, 0, 0, 9, 'f', 'r', 'e', 'e'}},
		{"trailing bytes after the last box", append(append([]byte{0x04}, boxes...), 0xFF)},
		{"rawBoxes after a genBox", append(append([]byte{
			0x01, 0x04, 's', 't', 'y', 'p'}, 0x04), boxes...)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := Decode(c.obj, NewState(), moov)
			require.ErrorIs(t, err, ErrMalformed)
		})
	}
}

// TestEncodeRawRejectsBadSource: the encoder applies the same structural
// checks, classified as source errors.
func TestEncodeRawRejectsBadSource(t *testing.T) {
	for _, boxes := range [][]byte{
		nil,
		{0, 0, 0, 8},                     // truncated header
		{0, 0, 0, 0, 'f', 'r', 'e', 'e'}, // size escape 0
		{0, 0, 0, 9, 'f', 'r', 'e', 'e'}, // overrun
	} {
		_, err := EncodeRaw(boxes, NewState())
		require.ErrorIs(t, err, ErrBadSource)
	}
}

// TestFramedRoundTrip: the self-framed carriage concatenates
// length-prefixed Objects and splits back losslessly.
func TestFramedRoundTrip(t *testing.T) {
	moov := buildSyntheticMoov(t, 0x01010000)
	samples := []mp4.FullSample{mkSample(3000, 700, 0x01010000, 0xAA)}
	payload := samplePayload(samples)

	tx := NewState()
	rawObj, err := EncodeRaw(testBoxes(t), tx)
	require.NoError(t, err)
	obj1, err := EncodeCanonical(nil, makeFragment(t, 1, 90000, samples), payload, tx, moov)
	require.NoError(t, err)
	obj2, err := EncodeCanonical(nil, makeFragment(t, 2, 93000, samples), payload, tx, moov)
	require.NoError(t, err)

	var file []byte
	for _, obj := range [][]byte{rawObj, obj1, obj2} {
		file = AppendFramed(file, obj)
	}

	rx := NewState()
	var got [][]byte
	rest := file
	for len(rest) > 0 {
		var obj []byte
		obj, rest, err = NextFramed(rest)
		require.NoError(t, err)
		_, _, err = Decode(obj, rx, moov)
		require.NoError(t, err)
		got = append(got, obj)
	}
	require.Equal(t, [][]byte{rawObj, obj1, obj2}, got)

	// A truncated file surfaces as ErrMalformed, not a short read.
	_, _, err = NextFramed(file[:len(file)-1])
	require.NoError(t, err)
	_, rest, err = NextFramed(file[:5])
	require.ErrorIs(t, err, ErrMalformed)
	require.Nil(t, rest)
}
