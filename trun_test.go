package locmaf

import (
	"bytes"
	"testing"

	"github.com/Eyevinn/mp4ff/mp4"
	"github.com/stretchr/testify/require"
)

// trunMoov builds a single-track moov of the given media type, with an
// offset edit list when editList is set. When decoded is set, the moov
// is encoded and decoded again, so that mp4ff fills EdtsBox.Elst as it
// does for a received CMAF Header.
func trunMoov(t *testing.T, mediaType string, editList, decoded bool) *mp4.MoovBox {
	t.Helper()
	init := mp4.CreateEmptyInit()
	trak := init.AddEmptyTrack(90000, mediaType, "und")
	init.Moov.Mvex.Trex.DefaultSampleDuration = 3000
	if editList {
		edts := &mp4.EdtsBox{}
		edts.AddChild(&mp4.ElstBox{Entries: []mp4.ElstEntry{{MediaTime: 3000, MediaRateInteger: 1}}})
		trak.AddChild(edts)
	}
	if !decoded {
		return init.Moov
	}
	var buf bytes.Buffer
	require.NoError(t, init.Encode(&buf))
	f, err := mp4.DecodeFile(&buf)
	require.NoError(t, err)
	require.NotNil(t, f.Init)
	if editList {
		require.Len(t, f.Init.Moov.Trak.Edts.Elst, 1)
	}
	return f.Init.Moov
}

// TestCanonicalTrunVersion checks the canonical trun version rule: a
// video track without an edit list gets version 1 in every chunk, and
// any other chunk gets version 1 only when it has a negative
// composition-time offset. The offsets survive in either version.
func TestCanonicalTrunVersion(t *testing.T) {
	const video = "video"
	nonNegative := []int32{0, 6000, 0}
	negative := []int32{3000, 6000, -3000}
	zero := []int32{0, 0, 0}
	cases := []struct {
		name      string
		mediaType string
		editList  bool
		decoded   bool
		ctos      []int32
		want      byte
	}{
		{"video without edit list, zero offsets", video, false, false, zero, 1},
		{"video without edit list, non-negative offsets", video, false, false, nonNegative, 1},
		{"video without edit list, negative offset", video, false, false, negative, 1},
		{"video with edit list, zero offsets", video, true, false, zero, 0},
		{"video with edit list, non-negative offsets", video, true, false, nonNegative, 0},
		{"video with decoded edit list, non-negative offsets", video, true, true, nonNegative, 0},
		{"video with edit list, negative offset", video, true, false, negative, 1},
		{"audio, zero offsets", "audio", false, false, zero, 0},
		{"audio, negative offset", "audio", false, false, negative, 1},
		{"subtitles, non-negative offsets", "subtitle", false, false, nonNegative, 0},
		{"metadata, zero offsets", "meta", false, false, zero, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			moov := trunMoov(t, tc.mediaType, tc.editList, tc.decoded)
			samples := make([]mp4.FullSample, len(tc.ctos))
			for i, cto := range tc.ctos {
				samples[i] = mkSample(3000, uint32(100+i), 0x01010000, byte(i+1))
				samples[i].CompositionTimeOffset = cto
			}
			moof := makeFragment(t, 1, 90000, samples)
			eff, err := ExtractEffective(nil, moof, samplePayload(samples), moov)
			require.NoError(t, err)
			chunk, err := ReconstructCanonical(moov, eff)
			require.NoError(t, err)

			box, err := mp4.DecodeBox(0, bytes.NewReader(chunk))
			require.NoError(t, err)
			trun := box.(*mp4.MoofBox).Traf.Trun
			require.Equal(t, tc.want, trun.Version)
			for i, s := range trun.Samples {
				require.Equal(t, tc.ctos[i], s.CompositionTimeOffset, "sample %d", i)
			}
		})
	}
}
