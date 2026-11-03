package locmaf

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/Eyevinn/locmaf/vi64"
	"github.com/Eyevinn/mp4ff/mp4"
	"github.com/stretchr/testify/require"
)

// headerObject frames a header element of the given type around props,
// followed by the mdat payload.
func headerObject(elementType uint64, props, payload []byte) []byte {
	obj := vi64.Append(nil, elementType)
	obj = vi64.Append(obj, uint64(len(props)))
	obj = append(obj, props...)
	return append(obj, payload...)
}

// seqSample is one sample at the trex default duration and size, so a
// chunk of it needs no sample fields beyond the count and BMDT.
var seqSample = []mp4.FullSample{mkSample(3000, 1000, 0x01010000, 0x5A)}

// TestSequenceNumberRoundTrip encodes a group per pattern, decodes it,
// and checks the effective and reconstructed sequence number of every
// chunk, plus the delta property blocks that carry it.
func TestSequenceNumberRoundTrip(t *testing.T) {
	cases := []struct {
		name  string
		seqs  []uint32
		props []string // hex property block of each delta header
	}{
		{"zero", []uint32{0, 0, 0}, []string{"", ""}},
		{"constant", []uint32{880000000, 880000000, 880000000}, []string{"", ""}},
		{"increment-wrap", []uint32{0xFFFFFFFE, 0xFFFFFFFF, 0, 1}, []string{"1402", "", ""}},
		{"jump-decrease", []uint32{5, 6, 100, 101, 50, 50}, []string{"1402", "1480ba", "1480b9", "1467", "1466"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			moov := buildSyntheticMoov(t, 0x01010000)
			tx, rx := NewState(), NewState()
			for i, seq := range tc.seqs {
				moof := makeFragment(t, seq, uint64(3000*i), seqSample)
				obj, err := EncodeCanonical(nil, moof, samplePayload(seqSample), tx, moov)
				require.NoError(t, err)
				if i > 0 {
					require.Equal(t, ElementTypeDeltaHeader, headerElementType(t, obj))
					props, _, err := splitHeaderProps(obj)
					require.NoError(t, err)
					require.Equal(t, tc.props[i-1], hex.EncodeToString(props), "chunk %d delta properties", i)
				}
				eff, _, err := Decode(obj, rx, moov)
				require.NoError(t, err)
				require.Equal(t, seq, eff.SequenceNumber, "chunk %d", i)

				chunk, err := ReconstructCanonical(moov, eff)
				require.NoError(t, err)
				box, err := mp4.DecodeBox(0, bytes.NewReader(chunk))
				require.NoError(t, err)
				require.Equal(t, seq, box.(*mp4.MoofBox).Mfhd.SequenceNumber, "chunk %d mfhd", i)
			}
		})
	}
}

// TestSequenceNumberZeroOmitted: a full header carries no ID 18 for
// sequence number 0, and ID 18 otherwise.
func TestSequenceNumberZeroOmitted(t *testing.T) {
	moov := buildSyntheticMoov(t, 0x01010000)
	for _, seq := range []uint32{0, 7} {
		obj, err := EncodeCanonical(nil, makeFragment(t, seq, 0, seqSample), samplePayload(seqSample), NewState(), moov)
		require.NoError(t, err)
		props, _, err := splitHeaderProps(obj)
		require.NoError(t, err)
		want := "0a000e01" // BMDT 0, sample count 1
		if seq != 0 {
			want += "1207" // ID 18, sequence number 7
		}
		require.Equal(t, want, hex.EncodeToString(props), "seq %d", seq)
	}
}

// TestSequenceNumberStepDeletion: listing ID 20 in the deletion marker
// sets the step to 0, so the number repeats.
func TestSequenceNumberStepDeletion(t *testing.T) {
	moov := buildSyntheticMoov(t, 0x01010000)
	tx, rx := NewState(), NewState()
	payload := samplePayload(seqSample)
	for i, seq := range []uint32{10, 11} {
		obj, err := EncodeCanonical(nil, makeFragment(t, seq, uint64(3000*i), seqSample), payload, tx, moov)
		require.NoError(t, err)
		_, _, err = Decode(obj, rx, moov)
		require.NoError(t, err)
	}
	var props []byte
	props = vi64.Append(props, uint64(idDeltaDeletedLocmafIDs))
	props = vi64.Append(props, 1)
	props = vi64.Append(props, uint64(idMfhdSequenceNumberStep))
	eff, _, err := Decode(headerObject(ElementTypeDeltaHeader, props, payload), rx, moov)
	require.NoError(t, err)
	require.Equal(t, uint32(11), eff.SequenceNumber)
}

// TestSequenceNumberRejects covers the receiver MUST-reject rules: ID 18
// in a delta header, ID 20 in a full header, an ID 18 value above 32
// bits, and a step outside the signed 32-bit range.
func TestSequenceNumberRejects(t *testing.T) {
	moov := buildSyntheticMoov(t, 0x01010000)
	payload := samplePayload(seqSample)
	fullProps := func(extra ...uint64) []byte {
		var p []byte
		p = vi64.Append(p, uint64(idTfdtBaseMediaDecodeTime))
		p = vi64.Append(p, 0)
		p = vi64.Append(p, uint64(idTrunSampleCount))
		p = vi64.Append(p, 1)
		for _, v := range extra {
			p = vi64.Append(p, v)
		}
		return p
	}
	// startedState returns a receiver state anchored by a full chunk
	// with the given sequence number.
	startedState := func(seq uint32) *State {
		rx := NewState()
		obj, err := EncodeCanonical(nil, makeFragment(t, seq, 0, seqSample), payload, NewState(), moov)
		require.NoError(t, err)
		_, _, err = Decode(obj, rx, moov)
		require.NoError(t, err)
		return rx
	}
	zigzagStep := func(d int64) []byte {
		p := vi64.Append(nil, uint64(idMfhdSequenceNumberStep))
		return vi64.AppendZigzag(p, d)
	}

	t.Run("field 20 in a full header", func(t *testing.T) {
		obj := headerObject(ElementTypeFullHeader, fullProps(uint64(idMfhdSequenceNumberStep), 2), payload)
		_, _, err := Decode(obj, NewState(), moov)
		require.ErrorIs(t, err, ErrMalformed)
	})
	t.Run("field 18 above 32 bits", func(t *testing.T) {
		obj := headerObject(ElementTypeFullHeader, fullProps(uint64(idMfhdSequenceNumber), 1<<32), payload)
		_, _, err := Decode(obj, NewState(), moov)
		require.ErrorIs(t, err, ErrMalformed)
	})
	t.Run("field 18 in a delta header", func(t *testing.T) {
		props := vi64.Append(nil, uint64(idMfhdSequenceNumber))
		props = vi64.Append(props, 5)
		_, _, err := Decode(headerObject(ElementTypeDeltaHeader, props, payload), startedState(5), moov)
		require.ErrorIs(t, err, ErrMalformed)
	})
	t.Run("step above the signed 32-bit range", func(t *testing.T) {
		_, _, err := Decode(headerObject(ElementTypeDeltaHeader, zigzagStep(1<<31), payload), startedState(5), moov)
		require.ErrorIs(t, err, ErrMalformed)
	})
	t.Run("step below the signed 32-bit range", func(t *testing.T) {
		_, _, err := Decode(headerObject(ElementTypeDeltaHeader, zigzagStep(-(1<<31)-1), payload), startedState(5), moov)
		require.ErrorIs(t, err, ErrMalformed)
	})
	t.Run("step at the range limits", func(t *testing.T) {
		for _, d := range []int64{(1 << 31) - 1, -(1 << 31)} {
			eff, _, err := Decode(headerObject(ElementTypeDeltaHeader, zigzagStep(d), payload), startedState(5), moov)
			require.NoError(t, err)
			require.Equal(t, uint32(5)+uint32(d), eff.SequenceNumber, "step %d", d)
		}
	})
	t.Run("step delta far outside", func(t *testing.T) {
		_, _, err := Decode(headerObject(ElementTypeDeltaHeader, zigzagStep(1<<40), payload), startedState(5), moov)
		require.ErrorIs(t, err, ErrMalformed)
	})
}

// TestMissingMfhdRejects: the encoder needs the mfhd to read the
// sequence number.
func TestMissingMfhdRejects(t *testing.T) {
	moov := buildSyntheticMoov(t, 0x01010000)
	moof := makeFragment(t, 1, 0, seqSample)
	moof.Mfhd = nil
	_, err := EncodeCanonical(nil, moof, samplePayload(seqSample), NewState(), moov)
	require.ErrorIs(t, err, ErrBadSource)
}

// splitHeaderProps returns the property block and mdat payload of a
// moof-carrying Object.
func splitHeaderProps(obj []byte) (props, mdat []byte, err error) {
	_, _, props, mdat, _, err = splitElements(obj)
	return props, mdat, err
}
