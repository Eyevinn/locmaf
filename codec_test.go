package locmaf

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/Eyevinn/locmaf/vi64"
	"github.com/Eyevinn/mp4ff/mp4"
	"github.com/stretchr/testify/require"
)

// buildSyntheticMoov builds the smallest moov sufficient for the codec:
// a single video trak with a populated trex.
func buildSyntheticMoov(t *testing.T, defaultFlags uint32) *mp4.MoovBox {
	t.Helper()
	init := mp4.CreateEmptyInit()
	trak := init.AddEmptyTrack(90000, "video", "und")
	require.NotNil(t, trak)
	init.Moov.Mvex.Trex.DefaultSampleDuration = 3000
	init.Moov.Mvex.Trex.DefaultSampleSize = 1000
	init.Moov.Mvex.Trex.DefaultSampleFlags = defaultFlags
	return init.Moov
}

// makeFragment builds a moof with the given samples and BMDT.
func makeFragment(t *testing.T, seqnum uint32, bmdt uint64, samples []mp4.FullSample) *mp4.MoofBox {
	t.Helper()
	f, err := mp4.CreateFragment(seqnum, 1)
	require.NoError(t, err)
	for _, s := range samples {
		f.AddFullSample(s)
	}
	f.Moof.Traf.Tfdt.SetBaseMediaDecodeTime(bmdt)
	return f.Moof
}

func samplePayload(samples []mp4.FullSample) []byte {
	var out []byte
	for _, s := range samples {
		out = append(out, s.Data...)
	}
	return out
}

func mkSample(dur, size, flags uint32, fill byte) mp4.FullSample {
	return mp4.FullSample{
		Sample: mp4.Sample{Dur: dur, Size: size, Flags: flags},
		Data:   bytes.Repeat([]byte{fill}, int(size)),
	}
}

// headerElementType returns the element type of the first element.
func headerElementType(t *testing.T, obj []byte) uint64 {
	t.Helper()
	v, _, err := vi64.Parse(obj)
	require.NoError(t, err)
	return v
}

// TestGoldenFullObject pins the exact canonical wire bytes of a small
// full chunk: n=2, BMDT 90000, sizes 800/700 (varying), durations both
// at the trex default, IDR first sample (first-sample-flags case with
// the remainder equal to the trex default).
func TestGoldenFullObject(t *testing.T) {
	moov := buildSyntheticMoov(t, 0x01010000)
	samples := []mp4.FullSample{
		mkSample(3000, 800, 0x02000000, 0xAA),
		mkSample(3000, 700, 0x01010000, 0xBB),
	}
	moof := makeFragment(t, 1, 90000, samples)
	payload := samplePayload(samples)

	state := NewState()
	obj, err := EncodeCanonical(nil, moof, payload, state, moov)
	require.NoError(t, err)

	wantHeader := "020f" + // element_type 2, properties_length 15
		"01" + "02" + "8320" + // field 1, 2 bytes, [vi64(800)]
		"0a" + "c15f90" + // field 10, vi64(90000)
		"0c" + "e2000000" + // field 12, vi64(0x02000000)
		"0e" + "02" // field 14, vi64(2)
	want, err := hex.DecodeString(wantHeader)
	require.NoError(t, err)
	require.Equal(t, want, obj[:len(want)], "wire header bytes\n got: %x\nwant: %x", obj[:len(want)], want)
	require.Equal(t, payload, obj[len(want):], "mdat payload follows the header")

	// Decode and pin the canonical reconstruction bytes.
	rx := NewState()
	eff, _, err := Decode(obj, rx, moov)
	require.NoError(t, err)
	require.Equal(t, 2, eff.SampleCount)
	require.Equal(t, uint64(90000), eff.BMDT)
	require.Equal(t, []uint32{800, 700}, eff.Sizes)
	require.Equal(t, []uint32{3000, 3000}, eff.Durations)
	require.Equal(t, []uint32{0x02000000, 0x01010000}, eff.Flags)

	chunk, err := ReconstructCanonical(moov, eff)
	require.NoError(t, err)
	wantMoof := "00000064" + "6d6f6f66" + // moof, 100 bytes
		"00000010" + "6d666864" + "00000000" + "00000000" + // mfhd, seq 0
		"0000004c" + "74726166" + // traf, 76 bytes
		"00000010" + "74666864" + "00020000" + "00000001" + // tfhd: base-is-moof, track 1
		"00000014" + "74666474" + "01000000" + "0000000000015f90" + // tfdt v1
		"00000020" + "7472756e" + "00000205" + "00000002" + "0000006c" + // trun: fsf+sizes, data_offset 108
		"02000000" + // first_sample_flags
		"00000320" + "000002bc" + // sizes 800, 700
		"000005e4" + "6d646174" // mdat, 8+1500
	wantChunk, err := hex.DecodeString(wantMoof)
	require.NoError(t, err)
	require.Equal(t, wantChunk, chunk[:len(wantChunk)],
		"canonical chunk bytes\n got: %x\nwant: %x", chunk[:len(wantChunk)], wantChunk)
	require.Equal(t, payload, chunk[len(wantChunk):])

	// Byte determinism: a second encode/reconstruct is identical.
	state2 := NewState()
	obj2, err := EncodeCanonical(nil, moof, payload, state2, moov)
	require.NoError(t, err)
	require.Equal(t, obj, obj2)
}

// TestDeltaSequenceBMDTDerivation: full + deltas with a steady cadence;
// the receiver derives BMDT without seeing it on the wire, and ID 10
// never appears in a delta header.
func TestDeltaSequenceBMDTDerivation(t *testing.T) {
	moov := buildSyntheticMoov(t, 0x01010000)
	dur := uint32(3000)
	mkSamples := func(n int, baseSize uint32) []mp4.FullSample {
		out := make([]mp4.FullSample, n)
		for i := range out {
			out[i] = mkSample(dur, baseSize+uint32(i)*10, 0x01010000, 0x55)
		}
		return out
	}

	tx, rx := NewState(), NewState()
	bmdt := uint64(0)
	for i := 0; i < 5; i++ {
		samples := mkSamples(3, 1000+uint32(i)*100)
		moof := makeFragment(t, uint32(i+1), bmdt, samples)
		obj, err := EncodeCanonical(nil, moof, samplePayload(samples), tx, moov)
		require.NoError(t, err)

		wantType := ElementTypeDeltaHeader
		if i == 0 {
			wantType = ElementTypeFullHeader
		}
		require.Equal(t, wantType, headerElementType(t, obj), "chunk %d header type", i)

		eff, _, err := Decode(obj, rx, moov)
		require.NoError(t, err)
		require.Equal(t, bmdt, eff.BMDT, "chunk %d BMDT", i)
		require.Equal(t, 3, eff.SampleCount, "chunk %d sample count", i)
		for j := range samples {
			require.Equal(t, samples[j].Size, eff.Sizes[j], "chunk %d sample %d size", i, j)
		}
		bmdt += uint64(dur) * 3
	}
}

// TestMidGroupReanchor: a BMDT discontinuity forces a full header
// mid-group, which resets the receiver's in-group reference.
func TestMidGroupReanchor(t *testing.T) {
	moov := buildSyntheticMoov(t, 0x01010000)
	samples := []mp4.FullSample{mkSample(3000, 900, 0x01010000, 0x11), mkSample(3000, 800, 0x01010000, 0x22)}

	tx, rx := NewState(), NewState()
	obj1, err := EncodeCanonical(nil, makeFragment(t, 1, 0, samples), samplePayload(samples), tx, moov)
	require.NoError(t, err)
	require.Equal(t, ElementTypeFullHeader, headerElementType(t, obj1))
	_, _, err = Decode(obj1, rx, moov)
	require.NoError(t, err)

	// Contiguous -> delta.
	obj2, err := EncodeCanonical(nil, makeFragment(t, 2, 6000, samples), samplePayload(samples), tx, moov)
	require.NoError(t, err)
	require.Equal(t, ElementTypeDeltaHeader, headerElementType(t, obj2))
	eff2, _, err := Decode(obj2, rx, moov)
	require.NoError(t, err)
	require.Equal(t, uint64(6000), eff2.BMDT)

	// Splice: BMDT jumps -> the encoder must re-anchor with a full header.
	obj3, err := EncodeCanonical(nil, makeFragment(t, 3, 500000, samples), samplePayload(samples), tx, moov)
	require.NoError(t, err)
	require.Equal(t, ElementTypeFullHeader, headerElementType(t, obj3))
	eff3, _, err := Decode(obj3, rx, moov)
	require.NoError(t, err)
	require.Equal(t, uint64(500000), eff3.BMDT)

	// And the re-anchored reference carries the group forward.
	obj4, err := EncodeCanonical(nil, makeFragment(t, 4, 506000, samples), samplePayload(samples), tx, moov)
	require.NoError(t, err)
	require.Equal(t, ElementTypeDeltaHeader, headerElementType(t, obj4))
	eff4, _, err := Decode(obj4, rx, moov)
	require.NoError(t, err)
	require.Equal(t, uint64(506000), eff4.BMDT)
}

// TestListLengthChangesGrowAndShrink: sample count varies across
// deltas; per-sample lists resize per the length rules.
func TestListLengthChangesGrowAndShrink(t *testing.T) {
	moov := buildSyntheticMoov(t, 0x01010000)
	dur := uint32(3000)
	mkSamples := func(n int) []mp4.FullSample {
		out := make([]mp4.FullSample, n)
		for i := range out {
			out[i] = mkSample(dur, 1000+uint32(i)*7, 0x01010000, 0x33)
		}
		return out
	}

	tx, rx := NewState(), NewState()
	var bmdt uint64
	for i, n := range []int{2, 4, 1, 3} {
		samples := mkSamples(n)
		moof := makeFragment(t, uint32(i+1), bmdt, samples)
		obj, err := EncodeCanonical(nil, moof, samplePayload(samples), tx, moov)
		require.NoError(t, err)

		eff, _, err := Decode(obj, rx, moov)
		require.NoError(t, err)
		require.Equal(t, n, eff.SampleCount, "chunk %d", i)
		for j := range samples {
			require.Equal(t, samples[j].Size, eff.Sizes[j], "chunk %d sample %d size", i, j)
			require.Equal(t, samples[j].Flags, eff.Flags[j], "chunk %d sample %d flags", i, j)
			require.Equal(t, samples[j].Dur, eff.Durations[j], "chunk %d sample %d dur", i, j)
		}
		bmdt += uint64(dur) * uint64(n)
	}
}

// TestFirstSampleFlagsDeletion: a SAP chunk emits ID 12; the next chunk
// deletes it via ID 27 so the first sample falls back to the default.
func TestFirstSampleFlagsDeletion(t *testing.T) {
	moov := buildSyntheticMoov(t, 0x01010000)
	idr, p := uint32(0x02000000), uint32(0x01010000)

	samples1 := []mp4.FullSample{mkSample(3000, 900, idr, 0xAA), mkSample(3000, 900, p, 0xBB), mkSample(3000, 900, p, 0xCC)}
	samples2 := []mp4.FullSample{mkSample(3000, 900, p, 0xAA), mkSample(3000, 900, p, 0xBB), mkSample(3000, 900, p, 0xCC)}

	tx, rx := NewState(), NewState()
	obj1, err := EncodeCanonical(nil, makeFragment(t, 1, 0, samples1), samplePayload(samples1), tx, moov)
	require.NoError(t, err)

	// The full header carries ID 12 (first-differs case).
	_, hdrType, props, _, _, err := splitElements(obj1)
	require.NoError(t, err)
	require.Equal(t, ElementTypeFullHeader, hdrType)
	raw, err := rawProperties(props)
	require.NoError(t, err)
	require.Contains(t, raw, idTrunFirstSampleFlags)

	obj2, err := EncodeCanonical(nil, makeFragment(t, 2, 9000, samples2), samplePayload(samples2), tx, moov)
	require.NoError(t, err)
	_, hdrType, props, _, _, err = splitElements(obj2)
	require.NoError(t, err)
	require.Equal(t, ElementTypeDeltaHeader, hdrType)
	raw, err = rawProperties(props)
	require.NoError(t, err)
	delBytes, ok := raw[idDeltaDeletedLocmafIDs]
	require.True(t, ok, "delta must carry the deletion marker")
	deleted, err := parseUnsignedList(delBytes, idDeltaDeletedLocmafIDs)
	require.NoError(t, err)
	require.Contains(t, deleted, uint64(idTrunFirstSampleFlags))

	_, _, err = Decode(obj1, rx, moov)
	require.NoError(t, err)
	eff2, _, err := Decode(obj2, rx, moov)
	require.NoError(t, err)
	require.Equal(t, []uint32{p, p, p}, eff2.Flags, "first sample falls back to the default flags")
}

// TestUnknownElementTypeRejects: v0.3 has no skip for unknown top-level
// elements — the Object is malformed.
func TestUnknownElementTypeRejects(t *testing.T) {
	moov := buildSyntheticMoov(t, 0)
	obj := vi64.Append(nil, 99)
	obj = vi64.Append(obj, 0)
	_, _, err := Decode(obj, NewState(), moov)
	require.ErrorIs(t, err, ErrMalformed)
}

func TestDeltaWithoutFullRejects(t *testing.T) {
	moov := buildSyntheticMoov(t, 0)
	obj := vi64.Append(nil, ElementTypeDeltaHeader)
	obj = vi64.Append(obj, 0)
	_, _, err := Decode(obj, NewState(), moov)
	require.ErrorIs(t, err, ErrMalformed)
}

// TestField10InDeltaRejects: BMDT is full-header-only in v0.3.
func TestField10InDeltaRejects(t *testing.T) {
	moov := buildSyntheticMoov(t, 0x01010000)
	samples := []mp4.FullSample{mkSample(3000, 1000, 0x01010000, 0x77)}
	tx, rx := NewState(), NewState()
	obj1, err := EncodeCanonical(nil, makeFragment(t, 1, 0, samples), samplePayload(samples), tx, moov)
	require.NoError(t, err)
	_, _, err = Decode(obj1, rx, moov)
	require.NoError(t, err)

	var props []byte
	props = vi64.Append(props, uint64(idTfdtBaseMediaDecodeTime))
	props = vi64.AppendZigzag(props, 3000)
	obj := vi64.Append(nil, ElementTypeDeltaHeader)
	obj = vi64.Append(obj, uint64(len(props)))
	obj = append(obj, props...)
	obj = append(obj, samplePayload(samples)...)
	_, _, err = Decode(obj, rx, moov)
	require.ErrorIs(t, err, ErrMalformed)
}

// TestField27InFullRejects: the deletion marker is delta-only.
func TestField27InFullRejects(t *testing.T) {
	moov := buildSyntheticMoov(t, 0)
	var props []byte
	props = vi64.Append(props, uint64(idTfdtBaseMediaDecodeTime))
	props = vi64.Append(props, 0)
	props = vi64.Append(props, uint64(idTrunSampleCount))
	props = vi64.Append(props, 0)
	props = vi64.Append(props, uint64(idDeltaDeletedLocmafIDs))
	props = vi64.Append(props, 1)
	props = vi64.Append(props, 12)
	obj := vi64.Append(nil, ElementTypeFullHeader)
	obj = vi64.Append(obj, uint64(len(props)))
	obj = append(obj, props...)
	_, _, err := Decode(obj, NewState(), moov)
	require.ErrorIs(t, err, ErrMalformed)
}

// TestDuplicateFieldRejects: a field ID must not repeat in one block.
func TestDuplicateFieldRejects(t *testing.T) {
	moov := buildSyntheticMoov(t, 0)
	var props []byte
	for i := 0; i < 2; i++ {
		props = vi64.Append(props, uint64(idTrunSampleCount))
		props = vi64.Append(props, 0)
	}
	obj := vi64.Append(nil, ElementTypeFullHeader)
	obj = vi64.Append(obj, uint64(len(props)))
	obj = append(obj, props...)
	_, _, err := Decode(obj, NewState(), moov)
	require.ErrorIs(t, err, ErrMalformed)
}

// TestUnknownFieldIDSkipped: unknown field IDs are skipped by parity,
// known fields around them still decode.
func TestUnknownFieldIDSkipped(t *testing.T) {
	moov := buildSyntheticMoov(t, 0x01010000)
	var props []byte
	props = vi64.Append(props, uint64(idTfdtBaseMediaDecodeTime))
	props = vi64.Append(props, 90000)
	props = vi64.Append(props, uint64(idTrunSampleCount))
	props = vi64.Append(props, 1)
	props = vi64.Append(props, 98) // unknown scalar
	props = vi64.Append(props, 12345)
	props = vi64.Append(props, 99) // unknown list
	props = vi64.Append(props, 3)
	props = append(props, 0xDE, 0xAD, 0xBF)
	obj := vi64.Append(nil, ElementTypeFullHeader)
	obj = vi64.Append(obj, uint64(len(props)))
	obj = append(obj, props...)
	obj = append(obj, bytes.Repeat([]byte{0x42}, 500)...)

	eff, _, err := Decode(obj, NewState(), moov)
	require.NoError(t, err)
	require.Equal(t, 1, eff.SampleCount)
	require.Equal(t, uint64(90000), eff.BMDT)
	require.Equal(t, []uint32{500}, eff.Sizes, "single-sample size derived from mdat length")
}

// TestGenBoxRoundTrip: genBoxes ride ahead of the header and are
// wrapped byte-for-byte on reconstruction.
func TestGenBoxRoundTrip(t *testing.T) {
	moov := buildSyntheticMoov(t, 0x01010000)
	samples := []mp4.FullSample{mkSample(3000, 600, 0x01010000, 0x99)}
	prft := GenBox{Name: "prft", Payload: []byte{0, 0, 0, 0, 0, 0, 0, 1, 0x12, 0x34, 0x56, 0x78, 0, 0, 0, 0, 0, 0, 0, 2}}
	emsg := GenBox{Name: "emsg", Payload: append([]byte{1, 0, 0, 0}, []byte("scheme\x00value\x00")...)}

	tx, rx := NewState(), NewState()
	obj, err := EncodeCanonical([]GenBox{prft, emsg}, makeFragment(t, 1, 0, samples),
		samplePayload(samples), tx, moov)
	require.NoError(t, err)

	eff, _, err := Decode(obj, rx, moov)
	require.NoError(t, err)
	require.Len(t, eff.GenBoxes, 2)
	require.Equal(t, prft, eff.GenBoxes[0])
	require.Equal(t, emsg, eff.GenBoxes[1])

	chunk, err := ReconstructCanonical(moov, eff)
	require.NoError(t, err)
	// The chunk starts with the wrapped prft box.
	wantPrft := appendU32(nil, uint32(8+len(prft.Payload)))
	wantPrft = append(wantPrft, "prft"...)
	wantPrft = append(wantPrft, prft.Payload...)
	require.Equal(t, wantPrft, chunk[:len(wantPrft)])
	// Then the wrapped emsg, then the moof.
	rest := chunk[len(wantPrft):]
	wantEmsgHdr := appendU32(nil, uint32(8+len(emsg.Payload)))
	wantEmsgHdr = append(wantEmsgHdr, "emsg"...)
	require.Equal(t, wantEmsgHdr, rest[:8])
	require.Equal(t, "moof", string(rest[8+len(emsg.Payload)+4:8+len(emsg.Payload)+8]))
}

// TestCENCSubsampleRoundTrip drives the CENC fields through the wire
// and pins the canonical saiz/saio/senc layout.
func TestCENCSubsampleRoundTrip(t *testing.T) {
	moov := buildSyntheticMoov(t, 0x01010000)
	// Hand-build a full header: n=2, uniform size 800 via field 6,
	// IV size 8 via field 16, one subsample per sample.
	ivs := bytes.Repeat([]byte{0x0F}, 16)
	var props []byte
	props = vi64.Append(props, uint64(idTfhdDefaultSampleSize)) // 6
	props = vi64.Append(props, 800)
	var ivField []byte
	ivField = vi64.Append(ivField, uint64(idSencInitializationVector)) // 9
	ivField = vi64.Append(ivField, uint64(len(ivs)))
	ivField = append(ivField, ivs...)
	props = append(props, ivField...)
	props = vi64.Append(props, uint64(idTfdtBaseMediaDecodeTime)) // 10
	props = vi64.Append(props, 0)
	props = vi64.Append(props, uint64(idSencSubsampleCount)) // 11
	props = vi64.Append(props, 2)
	props = vi64.Append(props, 1)
	props = vi64.Append(props, 1)
	props = vi64.Append(props, uint64(idSencBytesOfClearData)) // 13
	props = vi64.Append(props, 2)
	props = vi64.Append(props, 7)
	props = vi64.Append(props, 7)
	props = vi64.Append(props, uint64(idTrunSampleCount)) // 14
	props = vi64.Append(props, 2)
	var protPayload []byte
	protPayload = vi64.Append(protPayload, 793)
	protPayload = vi64.Append(protPayload, 693)
	props = vi64.Append(props, uint64(idSencBytesOfProtectedData)) // 15
	props = vi64.Append(props, uint64(len(protPayload)))
	props = append(props, protPayload...)
	props = vi64.Append(props, uint64(idSencPerSampleIVSize)) // 16
	props = vi64.Append(props, 8)

	obj := vi64.Append(nil, ElementTypeFullHeader)
	obj = vi64.Append(obj, uint64(len(props)))
	obj = append(obj, props...)
	obj = append(obj, bytes.Repeat([]byte{0xEE}, 1600)...)

	eff, _, err := Decode(obj, NewState(), moov)
	require.NoError(t, err)
	require.Equal(t, uint8(8), eff.PerSampleIVSize)
	require.Equal(t, ivs, eff.IVs)
	require.True(t, eff.HasSubsamples)
	require.Equal(t, []uint16{1, 1}, eff.SubsampleCounts)
	require.Equal(t, []uint16{7, 7}, eff.ClearBytes)
	require.Equal(t, []uint32{793, 693}, eff.ProtectedBytes)

	chunk, err := ReconstructCanonical(moov, eff)
	require.NoError(t, err)

	// Box order inside traf: tfhd, tfdt, trun, saiz, saio, senc.
	s := string(chunk)
	order := []string{"tfhd", "tfdt", "trun", "saiz", "saio", "senc"}
	last := -1
	for _, name := range order {
		idx := strings.Index(s, name)
		require.Greater(t, idx, last, "box %s out of order", name)
		last = idx
	}

	// saio's single offset points at the first IV inside senc,
	// moof-relative.
	saioPos := strings.Index(s, "saio") - 4
	offset := uint32(chunk[saioPos+16])<<24 | uint32(chunk[saioPos+17])<<16 |
		uint32(chunk[saioPos+18])<<8 | uint32(chunk[saioPos+19])
	sencPos := strings.Index(s, "senc") - 4
	require.Equal(t, uint32(sencPos+16), offset, "saio offset = senc offset in moof + 16")
	require.Equal(t, ivs[:8], chunk[offset:offset+8], "offset lands on the first IV")

	// saiz: uniform aux size 8 + 2 + 6*1 = 16 -> default_sample_info_size.
	saizPos := strings.Index(s, "saiz") - 4
	require.Equal(t, byte(16), chunk[saizPos+12], "default_sample_info_size")

	// senc: version 0, subsample flag set.
	require.Equal(t, []byte{0, 0, 0, 2}, chunk[sencPos+8:sencPos+12], "senc flags")
}

// TestCbcsOmitRule: a protected chunk with no per-sample aux info
// (constant-IV cbcs) reconstructs with no senc, saiz, or saio at all.
func TestCbcsOmitRule(t *testing.T) {
	moov := buildSyntheticMoov(t, 0x01010000)
	samples := []mp4.FullSample{mkSample(3000, 800, 0x01010000, 0x10), mkSample(3000, 800, 0x01010000, 0x20)}
	tx, rx := NewState(), NewState()
	obj, err := EncodeCanonical(nil, makeFragment(t, 1, 0, samples), samplePayload(samples), tx, moov)
	require.NoError(t, err)
	eff, _, err := Decode(obj, rx, moov)
	require.NoError(t, err)
	require.Equal(t, uint8(0), eff.PerSampleIVSize)
	require.False(t, eff.HasSubsamples)

	chunk, err := ReconstructCanonical(moov, eff)
	require.NoError(t, err)
	for _, name := range []string{"senc", "saiz", "saio"} {
		require.NotContains(t, string(chunk), name)
	}
}

// TestEventOnly: a zero-sample chunk with an emsg genBox and an empty
// mdat payload.
func TestEventOnly(t *testing.T) {
	moov := buildSyntheticMoov(t, 0)
	emsg := GenBox{Name: "emsg", Payload: append([]byte{1, 0, 0, 0}, []byte("id3\x00")...)}
	moof := makeFragment(t, 1, 123456, nil)

	tx, rx := NewState(), NewState()
	obj, err := EncodeCanonical([]GenBox{emsg}, moof, nil, tx, moov)
	require.NoError(t, err)
	require.Equal(t, ElementTypeGenBox, headerElementType(t, obj))

	eff, _, err := Decode(obj, rx, moov)
	require.NoError(t, err)
	require.Equal(t, 0, eff.SampleCount)
	require.Equal(t, uint64(123456), eff.BMDT)
	require.Len(t, eff.GenBoxes, 1)
	require.Empty(t, eff.MdatPayload)

	chunk, err := ReconstructCanonical(moov, eff)
	require.NoError(t, err)
	// The chunk ends with an 8-byte-only mdat box.
	require.Equal(t, []byte{0, 0, 0, 8, 'm', 'd', 'a', 't'}, chunk[len(chunk)-8:])
}

// TestZeroSamplesWithPayloadRejects: n == 0 with a non-empty mdat
// payload is malformed.
func TestZeroSamplesWithPayloadRejects(t *testing.T) {
	moov := buildSyntheticMoov(t, 0)
	var props []byte
	props = vi64.Append(props, uint64(idTfdtBaseMediaDecodeTime))
	props = vi64.Append(props, 0)
	props = vi64.Append(props, uint64(idTrunSampleCount))
	props = vi64.Append(props, 0)
	obj := vi64.Append(nil, ElementTypeFullHeader)
	obj = vi64.Append(obj, uint64(len(props)))
	obj = append(obj, props...)
	obj = append(obj, 0x01)
	_, _, err := Decode(obj, NewState(), moov)
	require.ErrorIs(t, err, ErrMalformed)
}

// TestNegativeCTOs: B-frame composition offsets ride field 5 as zigzag
// in both contexts and flip trun to version 1.
func TestNegativeCTOs(t *testing.T) {
	moov := buildSyntheticMoov(t, 0x01010000)
	samples := []mp4.FullSample{
		{Sample: mp4.Sample{Dur: 3000, Size: 500, Flags: 0x01010000, CompositionTimeOffset: 0},
			Data: bytes.Repeat([]byte{1}, 500)},
		{Sample: mp4.Sample{Dur: 3000, Size: 500, Flags: 0x01010000, CompositionTimeOffset: 6000},
			Data: bytes.Repeat([]byte{2}, 500)},
		{Sample: mp4.Sample{Dur: 3000, Size: 500, Flags: 0x01010000, CompositionTimeOffset: -3000},
			Data: bytes.Repeat([]byte{3}, 500)},
	}
	tx, rx := NewState(), NewState()
	obj, err := EncodeCanonical(nil, makeFragment(t, 1, 0, samples), samplePayload(samples), tx, moov)
	require.NoError(t, err)
	eff, _, err := Decode(obj, rx, moov)
	require.NoError(t, err)
	require.Equal(t, []int32{0, 6000, -3000}, eff.CTOs)

	chunk, err := ReconstructCanonical(moov, eff)
	require.NoError(t, err)
	trunPos := strings.Index(string(chunk), "trun") - 4
	require.Equal(t, byte(1), chunk[trunPos+8], "trun version 1 for negative CTOs")
}

// TestReconstructionParsesWithMp4ff: the canonical bytes are valid ISO
// BMFF that mp4ff can parse back to the same values.
func TestReconstructionParsesWithMp4ff(t *testing.T) {
	moov := buildSyntheticMoov(t, 0x01010000)
	samples := []mp4.FullSample{
		mkSample(3000, 800, 0x02000000, 0xAA),
		mkSample(2990, 700, 0x01010000, 0xBB),
		mkSample(3010, 600, 0x01010000, 0xCC),
	}
	tx, rx := NewState(), NewState()
	obj, err := EncodeCanonical(nil, makeFragment(t, 1, 90000, samples), samplePayload(samples), tx, moov)
	require.NoError(t, err)
	eff, _, err := Decode(obj, rx, moov)
	require.NoError(t, err)
	chunk, err := ReconstructCanonical(moov, eff)
	require.NoError(t, err)

	sr := bytes.NewReader(chunk)
	box, err := mp4.DecodeBox(0, sr)
	require.NoError(t, err)
	moofOut, ok := box.(*mp4.MoofBox)
	require.True(t, ok, "first box is the moof")
	require.Equal(t, uint32(0), moofOut.Mfhd.SequenceNumber)
	require.Equal(t, uint64(90000), moofOut.Traf.Tfdt.BaseMediaDecodeTime())
	require.Equal(t, byte(1), moofOut.Traf.Tfdt.Version)
	require.Len(t, moofOut.Traf.Trun.Samples, 3)
	for i := range samples {
		require.Equal(t, samples[i].Dur, moofOut.Traf.Trun.Samples[i].Dur, "sample %d dur", i)
		require.Equal(t, samples[i].Size, moofOut.Traf.Trun.Samples[i].Size, "sample %d size", i)
	}
	// Flags ride first_sample_flags + the tfhd default in the
	// first-differs case, not per-sample trun entries.
	fsf, hasFsf := moofOut.Traf.Trun.FirstSampleFlags()
	require.True(t, hasFsf)
	require.Equal(t, samples[0].Flags, fsf)
	require.False(t, moofOut.Traf.Trun.HasSampleFlags())
	require.Equal(t, int32(len(chunk)-len(eff.MdatPayload)), moofOut.Traf.Trun.DataOffset,
		"data_offset points just past the mdat header")
}
