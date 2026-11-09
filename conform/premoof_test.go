package conform_test

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/Eyevinn/locmaf"
	"github.com/Eyevinn/locmaf/conform"
	"github.com/Eyevinn/mp4ff/mp4"
	"github.com/stretchr/testify/require"
)

// preMoofCMAF builds an inline init and one segment of len(pre) chunks,
// three samples each. The segment starts with a styp, and pre[i] holds
// the encoded top-level boxes written right before chunk i's moof.
func preMoofCMAF(t *testing.T, pre [][][]byte) []byte {
	t.Helper()
	init := mp4.CreateEmptyInit()
	init.AddEmptyTrack(90000, "video", "und")
	init.Moov.Mvex.Trex.DefaultSampleDuration = 3000
	init.Moov.Mvex.Trex.DefaultSampleFlags = 0x01010000

	var buf bytes.Buffer
	require.NoError(t, init.Encode(&buf))
	require.NoError(t, mp4.CreateStyp().Encode(&buf))
	for c, boxes := range pre {
		for _, b := range boxes {
			buf.Write(b)
		}
		frag, err := mp4.CreateFragment(uint32(c+1), 1)
		require.NoError(t, err)
		for i := range 3 {
			size := uint32(100 + 10*c + i)
			frag.AddFullSample(mp4.FullSample{
				Sample: mp4.Sample{Dur: 3000, Size: size, Flags: 0x01010000},
				Data:   bytes.Repeat([]byte{byte(16*c + i)}, int(size)),
			})
		}
		frag.Moof.Traf.Tfdt.SetBaseMediaDecodeTime(uint64(9000 * c))
		require.NoError(t, frag.Encode(&buf))
	}
	return buf.Bytes()
}

func encodeBox(t *testing.T, b mp4.Box) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, b.Encode(&buf))
	return buf.Bytes()
}

func emsgBox(t *testing.T, id uint32) []byte {
	t.Helper()
	return encodeBox(t, &mp4.EmsgBox{
		Version:          1,
		TimeScale:        90000,
		PresentationTime: uint64(id) * 9000,
		EventDuration:    3000,
		ID:               id,
		SchemeIDURI:      "urn:example:event",
		Value:            "1",
		MessageData:      []byte{byte(id), 0xee},
	})
}

func prftBox(t *testing.T, mediaTime uint64) []byte {
	t.Helper()
	return encodeBox(t, mp4.CreatePrftBox(1, 0, 1, mp4.NTP64(0xe8c1a2b3c4d5e6f7), mediaTime))
}

// rawBox builds a box with a 32-bit size header around payload.
func rawBox(name string, payload []byte) []byte {
	out := binary.BigEndian.AppendUint32(nil, uint32(8+len(payload)))
	out = append(out, name...)
	return append(out, payload...)
}

func uuidBox(id byte) []byte {
	usertype := bytes.Repeat([]byte{0xa5}, 16)
	usertype[15] = id
	return rawBox("uuid", append(usertype, 'x', id))
}

type topBox struct {
	name string
	data []byte
}

// topLevelBoxes splits a byte string into its top-level boxes.
func topLevelBoxes(t *testing.T, data []byte) []topBox {
	t.Helper()
	var out []topBox
	for pos := 0; pos < len(data); {
		require.LessOrEqual(t, pos+8, len(data), "truncated box header at %d", pos)
		size := int(binary.BigEndian.Uint32(data[pos:]))
		require.GreaterOrEqual(t, size, 8, "box at %d", pos)
		require.LessOrEqual(t, pos+size, len(data), "box at %d overruns", pos)
		out = append(out, topBox{name: string(data[pos+4 : pos+8]), data: data[pos : pos+size]})
		pos += size
	}
	return out
}

// requireSameTopLevel asserts that got has the top-level box sequence of
// want, and that every box other than moof and mdat, which the canonical
// form may normalize, is byte-identical.
func requireSameTopLevel(t *testing.T, want, got []byte) {
	t.Helper()
	wb, gb := topLevelBoxes(t, want), topLevelBoxes(t, got)
	names := func(bs []topBox) []string {
		var n []string
		for _, b := range bs {
			n = append(n, b.name)
		}
		return n
	}
	require.Equal(t, names(wb), names(gb))
	for i := range wb {
		if wb[i].name != "moof" && wb[i].name != "mdat" {
			require.Equal(t, wb[i].data, gb[i].data, "box %d (%s)", i, wb[i].name)
		}
	}
}

// TestPreMoofBoxes checks that the top-level boxes before any chunk's
// moof, not only the first one's, become genBoxes of that chunk's Object
// and survive align's canonical output and the pack → decode →
// reconstruct round trip in place (issue #11).
func TestPreMoofBoxes(t *testing.T) {
	styp := encodeBox(t, mp4.CreateStyp())
	cases := []struct {
		name string
		pre  [][][]byte
	}{
		{
			name: "emsg before a later chunk only",
			pre:  [][][]byte{nil, {emsgBox(t, 2)}, nil},
		},
		{
			name: "emsg before every chunk",
			pre:  [][][]byte{{emsgBox(t, 1)}, {emsgBox(t, 2)}, {emsgBox(t, 3)}},
		},
		{
			name: "prft, uuid and free before later chunks",
			pre: [][][]byte{
				{prftBox(t, 0)},
				{prftBox(t, 9000), uuidBox(2)},
				{uuidBox(3), rawBox("free", []byte{0, 0, 0}), emsgBox(t, 3)},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := preMoofCMAF(t, tc.pre)
			lc, err := conform.LoadCMAF(raw, nil)
			require.NoError(t, err)
			require.Len(t, lc.File.Segments, 1)
			seg := lc.File.Segments[0]
			require.Len(t, seg.Fragments, len(tc.pre))

			// Each chunk's genBoxes are exactly the boxes before its moof,
			// after the styp on the first chunk.
			for c, frag := range seg.Fragments {
				want := tc.pre[c]
				if c == 0 {
					want = append([][]byte{styp}, want...)
				}
				gbs, err := conform.FragmentGenBoxes(seg, c, frag)
				require.NoError(t, err)
				require.Len(t, gbs, len(want), "chunk %d", c)
				for i, gb := range gbs {
					require.Equal(t, string(want[i][4:8]), gb.Name, "chunk %d genBox %d", c, i)
					require.Equal(t, want[i][8:], gb.Payload, "chunk %d genBox %d", c, i)
				}
			}

			// align's canonical output keeps every box in its place.
			rep, canon, err := conform.Align(lc, true, false)
			require.NoError(t, err)
			require.Zero(t, rep.Diverged)
			require.NotNil(t, canon)
			requireSameTopLevel(t, raw, canon)

			// So does pack → decode → reconstruct, which equals align's
			// canonical output and verifies as canonical.
			stream := canonicalStream(t, raw)
			objs, err := conform.SplitFramed(stream)
			require.NoError(t, err)
			require.Len(t, objs, 1+len(tc.pre))
			got := append([]byte(nil), lc.InitBytes...)
			st := locmaf.NewState()
			for _, obj := range objs[1:] {
				eff, rawBoxes, err := locmaf.Decode(obj, st, lc.Moov)
				require.NoError(t, err)
				require.Nil(t, rawBoxes)
				chunk, err := locmaf.ReconstructCanonical(lc.Moov, eff)
				require.NoError(t, err)
				got = append(got, chunk...)
			}
			require.Equal(t, canon, got)
			vrep, err := conform.Verify(stream, nil, true)
			require.NoError(t, err)
			require.Zero(t, vrep.NonConformant)
		})
	}
}
