package conform_test

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/Eyevinn/locmaf"
	"github.com/Eyevinn/locmaf/conform"
	"github.com/Eyevinn/mp4ff/mp4"
	"github.com/stretchr/testify/require"
)

// synthCMAF builds a small fragmented CMAF file in memory: an inline init
// and two segments (styp) of two fragments each, three samples per
// fragment with varying sizes and a distinct first-sample flag.
func synthCMAF(t *testing.T) []byte {
	t.Helper()
	init := mp4.CreateEmptyInit()
	init.AddEmptyTrack(90000, "video", "und")
	init.Moov.Mvex.Trex.DefaultSampleDuration = 3000
	init.Moov.Mvex.Trex.DefaultSampleFlags = 0x01010000

	var buf bytes.Buffer
	require.NoError(t, init.Encode(&buf))

	seq := uint32(1)
	bmdt := uint64(0)
	for s := 0; s < 2; s++ {
		seg := mp4.NewMediaSegment()
		for f := 0; f < 2; f++ {
			frag, err := mp4.CreateFragment(seq, 1)
			require.NoError(t, err)
			for i := 0; i < 3; i++ {
				size := uint32(200 + 50*int(seq) + 10*i)
				flags := uint32(0x01010000)
				if f == 0 && i == 0 {
					flags = 0x02000000
				}
				frag.AddFullSample(mp4.FullSample{
					Sample: mp4.Sample{Dur: 3000, Size: size, Flags: flags},
					Data:   bytes.Repeat([]byte{byte(seq*16 + uint32(i))}, int(size)),
				})
				bmdt += 3000
			}
			frag.Moof.Traf.Tfdt.SetBaseMediaDecodeTime(bmdt - 9000)
			seg.AddFragment(frag)
			seq++
		}
		require.NoError(t, seg.Encode(&buf))
	}
	return buf.Bytes()
}

// canonicalStream encodes the whole CMAF file as a self-framed .locmaf
// stream: a leading init rawBoxes Object, then one canonical Object per
// fragment across both segments on a single running state (one group).
func canonicalStream(t *testing.T, raw []byte) []byte {
	t.Helper()
	lc, err := conform.LoadCMAF(raw, nil)
	require.NoError(t, err)
	st := locmaf.NewState()
	initObj, err := locmaf.EncodeRaw(lc.InitBytes, st)
	require.NoError(t, err)
	stream := locmaf.AppendFramed(nil, initObj)
	for _, seg := range lc.File.Segments {
		for o, frag := range seg.Fragments {
			gb, err := conform.FragmentGenBoxes(seg, o, frag)
			require.NoError(t, err)
			var payload []byte
			if frag.Mdat != nil {
				payload = frag.Mdat.Data
			}
			obj, err := locmaf.EncodeCanonical(gb, frag.Moof, payload, st, lc.Moov)
			require.NoError(t, err)
			stream = locmaf.AppendFramed(stream, obj)
		}
	}
	return stream
}

// nonCanonicalStream packs the first segment but forces its second chunk
// to a full header (a fresh encode state) where the canonical form is a
// delta — decodable, but not canonically encoded.
func nonCanonicalStream(t *testing.T, raw []byte) []byte {
	t.Helper()
	lc, err := conform.LoadCMAF(raw, nil)
	require.NoError(t, err)
	st := locmaf.NewState()
	initObj, err := locmaf.EncodeRaw(lc.InitBytes, st)
	require.NoError(t, err)
	stream := locmaf.AppendFramed(nil, initObj)

	seg := lc.File.Segments[0]
	require.GreaterOrEqual(t, len(seg.Fragments), 2)
	st.Reset()

	gb0, err := conform.FragmentGenBoxes(seg, 0, seg.Fragments[0])
	require.NoError(t, err)
	o0, err := locmaf.EncodeCanonical(gb0, seg.Fragments[0].Moof, seg.Fragments[0].Mdat.Data, st, lc.Moov)
	require.NoError(t, err)
	stream = locmaf.AppendFramed(stream, o0)

	// A fresh state forces a full header instead of the canonical delta.
	gb1, err := conform.FragmentGenBoxes(seg, 1, seg.Fragments[1])
	require.NoError(t, err)
	o1, err := locmaf.EncodeCanonical(gb1, seg.Fragments[1].Moof, seg.Fragments[1].Mdat.Data, locmaf.NewState(), lc.Moov)
	require.NoError(t, err)
	stream = locmaf.AppendFramed(stream, o1)
	return stream
}

func TestLoadCMAF(t *testing.T) {
	raw := synthCMAF(t)
	lc, err := conform.LoadCMAF(raw, nil)
	require.NoError(t, err)
	require.NotNil(t, lc.Moov)
	require.Len(t, lc.File.Segments, 2)
	require.Len(t, lc.Starts, 4)
	require.Equal(t, raw[:lc.Starts[0]], lc.InitBytes)
}

func TestAlign(t *testing.T) {
	raw := synthCMAF(t)
	lc, err := conform.LoadCMAF(raw, nil)
	require.NoError(t, err)

	rep, canon, err := conform.Align(lc, false, false)
	require.NoError(t, err)
	require.Nil(t, canon) // only returned with collectCanon
	require.Len(t, rep.Chunks, 4)
	require.Equal(t, 4, rep.Aligned)
	require.Zero(t, rep.Diverged)
	for _, c := range rep.Chunks {
		require.True(t, c.Aligned, "g%d o%d", c.Group, c.Object)
		require.Positive(t, c.WireBytes)
	}

	// Field-level normalization explanation for a non-canonical source chunk.
	var norms []string
	for _, c := range rep.Chunks {
		if c.Aligned && !c.SourceIdentical {
			norms = c.Normalizations
			require.Nil(t, c.FirstDiff, "raw hex diff is opt-in (wantBytes)")
			break
		}
	}
	require.NotEmpty(t, norms, "expected a chunk with normalizations")
	joined := strings.Join(norms, "\n")
	require.Contains(t, joined, "moof/mfhd: sequence_number")
	require.Contains(t, joined, "moof/traf/tfdt: version 0 → 1")
	require.Contains(t, joined, "data_offset")
	require.Contains(t, joined, "mdat: payload identical")

	// wantBytes adds the raw first-diff window.
	wb, _, err := conform.Align(lc, false, true)
	require.NoError(t, err)
	var sawFirstDiff bool
	for _, c := range wb.Chunks {
		if c.Aligned && !c.SourceIdentical {
			require.NotNil(t, c.FirstDiff)
			sawFirstDiff = true
			break
		}
	}
	require.True(t, sawFirstDiff)
}

func TestAlignCanonicalIsFixedPoint(t *testing.T) {
	raw := synthCMAF(t)
	lc, err := conform.LoadCMAF(raw, nil)
	require.NoError(t, err)

	rep, canon, err := conform.Align(lc, true, false)
	require.NoError(t, err)
	require.Zero(t, rep.Diverged)
	require.NotNil(t, canon)

	// The canonical bytes are a valid CMAF file that is already canonical:
	// aligning them needs no further normalization, and re-canonicalizing is
	// a byte-for-byte fixed point.
	lc2, err := conform.LoadCMAF(canon, nil)
	require.NoError(t, err)
	rep2, canon2, err := conform.Align(lc2, true, false)
	require.NoError(t, err)
	require.Len(t, rep2.Chunks, 4)
	for _, c := range rep2.Chunks {
		require.True(t, c.SourceIdentical, "g%d o%d not canonical", c.Group, c.Object)
	}
	require.Equal(t, canon, canon2)
}

func TestVerify(t *testing.T) {
	raw := synthCMAF(t)

	// A canonical stream verifies clean under the strict (canonical) check.
	rep, err := conform.Verify(canonicalStream(t, raw), nil, true)
	require.NoError(t, err)
	require.True(t, rep.Strict)
	require.Equal(t, 5, rep.NumObjects)
	require.Equal(t, 5, rep.Conformant)
	require.Zero(t, rep.NonConformant)

	// A decodable-but-non-canonical stream is flagged under strict...
	bad := nonCanonicalStream(t, raw)
	strict, err := conform.Verify(bad, nil, true)
	require.NoError(t, err)
	require.Equal(t, 3, strict.NumObjects)
	require.Equal(t, 1, strict.NonConformant)
	require.True(t, strict.Objects[0].Conformant)  // init rawBoxes
	require.True(t, strict.Objects[1].Conformant)  // canonical full
	require.False(t, strict.Objects[2].Conformant) // forced full where a delta is canonical
	require.NotNil(t, strict.Objects[2].FirstDiff)

	// ...but conformant under the decodable check (rungs 1-2 only).
	dec, err := conform.Verify(bad, nil, false)
	require.NoError(t, err)
	require.Zero(t, dec.NonConformant)
}

func TestDump(t *testing.T) {
	raw := synthCMAF(t)
	rep, err := conform.Dump(canonicalStream(t, raw), nil)
	require.NoError(t, err)
	require.Equal(t, 5, rep.NumObjects)
	require.Len(t, rep.Objects, 5)

	require.Equal(t, conform.KindRawBoxes, rep.Objects[0].Kind)
	require.NotNil(t, rep.Objects[0].Raw)
	require.True(t, rep.Objects[0].Raw.IsInit)
	require.Equal(t, []string{"ftyp", "moov"}, rep.Objects[0].Raw.Boxes)

	// One group across the whole file: full header first, then all delta.
	wantKinds := []string{conform.KindRawBoxes, conform.KindFull, conform.KindDelta, conform.KindDelta, conform.KindDelta}
	var bmdts []uint64
	for i, o := range rep.Objects {
		require.Equal(t, wantKinds[i], o.Kind, "object %d", i)
		if o.Moof != nil {
			require.Equal(t, 3, o.Moof.SampleCount)
			bmdts = append(bmdts, o.Moof.BMDT)
		}
	}
	require.Equal(t, []uint64{0, 9000, 18000, 27000}, bmdts)
}

// TestVerifyCorpus frames each golden-vector case (in-band init + its
// objects) into a .locmaf stream and checks every Object verifies as
// canonical. The corpus is the reference codec's own output, so this
// guards that Verify's canonical check accepts exactly what the encoder
// produces.
func TestVerifyCorpus(t *testing.T) {
	root := filepath.Join("..", "testdata", "vectors")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Skipf("corpus not present: %v", err)
	}
	ran := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		caseDir := filepath.Join(root, e.Name())
		initBytes, err := os.ReadFile(filepath.Join(caseDir, "init.mp4"))
		if err != nil {
			continue // not a case directory
		}
		ran++
		t.Run(e.Name(), func(t *testing.T) {
			st := locmaf.NewState()
			initObj, err := locmaf.EncodeRaw(initBytes, st)
			require.NoError(t, err)
			stream := locmaf.AppendFramed(nil, initObj)

			objPaths, err := filepath.Glob(filepath.Join(caseDir, "objects", "*.locmafobj"))
			require.NoError(t, err)
			require.NotEmpty(t, objPaths)
			sort.Strings(objPaths)
			for _, p := range objPaths {
				b, err := os.ReadFile(p)
				require.NoError(t, err)
				stream = locmaf.AppendFramed(stream, b)
			}
			rep, err := conform.Verify(stream, nil, true)
			require.NoError(t, err)
			require.Zero(t, rep.NonConformant, "%s: non-conformant objects", e.Name())
			require.Equal(t, len(objPaths)+1, rep.Conformant)
		})
	}
	require.Positive(t, ran, "no corpus cases found under %s", root)
}
