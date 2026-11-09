package vectorgen

import (
	"bytes"
	"sort"
	"strings"
	"testing"

	"github.com/Eyevinn/mp4ff/mp4"
	"github.com/stretchr/testify/require"
)

// TestGenerateAllCases exercises the whole matrix in memory: every case
// builds, encodes, decodes, and reconstructs without error, and every
// representation pair encodes identically.
func TestGenerateAllCases(t *testing.T) {
	for _, c := range cases() {
		t.Run(c.name, func(t *testing.T) {
			files, err := generateCase(c)
			require.NoError(t, err)
			require.Contains(t, files, "manifest.json")
			require.Contains(t, files, "init.mp4")
			require.Contains(t, files, "objects/g000_o000.locmafobj")
		})
	}
}

// TestVectorsMatchDisk pins the committed corpus to the codec: the
// on-disk vectors must be byte-identical to freshly derived ones.
func TestVectorsMatchDisk(t *testing.T) {
	problems, err := Check("../../testdata/vectors")
	require.NoError(t, err)
	require.Empty(t, problems)
}

// TestGenerationIsDeterministic: two derivations yield identical bytes
// for every file of every case.
func TestGenerationIsDeterministic(t *testing.T) {
	for _, c := range cases() {
		a, err := generateCase(c)
		require.NoError(t, err)
		b, err := generateCase(c)
		require.NoError(t, err)
		require.Equal(t, a, b, "case %s", c.name)
	}
}

// TestCasesAreCMAF checks the CMAF structure of every case, so that CMAF
// and ISOBMFF checkers such as dashcheck and MP4Box accept the corpus:
// the init has one sample entry, mp4ff finds no brand issue in the init
// with its canonical chunks, every video group starts with a sync sample,
// and each chunk's decode time continues from the chunk before, except in
// the cases that depart from CMAF on purpose. Audio such as AAC and Opus
// has only sync samples, so its group starts need no check.
func TestCasesAreCMAF(t *testing.T) {
	gapsAllowed := map[string]bool{"bmdt-reanchor": true, "event-only": true}
	for _, c := range cases() {
		t.Run(c.name, func(t *testing.T) {
			files, err := generateCase(c)
			require.NoError(t, err)
			initFile, err := mp4.DecodeFile(bytes.NewReader(files["init.mp4"]))
			require.NoError(t, err)
			require.NotNil(t, initFile.Init)
			require.Len(t, initFile.Init.Moov.Trak.Mdia.Minf.Stbl.Stsd.Children, 1, "sample entries")
			trex := initFile.Init.Moov.Mvex.Trex
			video := initFile.Init.Moov.Trak.Mdia.Hdlr.HandlerType == "vide"

			var paths []string
			for p := range files {
				if strings.HasPrefix(p, "canonical/") {
					paths = append(paths, p)
				}
			}
			sort.Strings(paths)
			track := bytes.Clone(files["init.mp4"])
			var group string
			var nextDecodeTime uint64
			for i, p := range paths {
				chunk := files[p]
				if bytes.Equal(chunk[4:8], []byte("ftyp")) {
					continue // the in-band init of rawboxes-file
				}
				track = append(track, chunk...)
				f, err := mp4.DecodeFile(bytes.NewReader(chunk))
				require.NoError(t, err, p)
				require.Len(t, f.Segments, 1, p)
				require.Len(t, f.Segments[0].Fragments, 1, p)
				frag := f.Segments[0].Fragments[0]
				samples, err := frag.GetFullSamples(trex)
				require.NoError(t, err, p)
				decodeTime := frag.Moof.Traf.Tfdt.BaseMediaDecodeTime()
				g, _, _ := strings.Cut(strings.TrimPrefix(p, "canonical/"), "_")
				if video && g != group && len(samples) > 0 {
					require.True(t, mp4.IsSyncSampleFlags(samples[0].Flags), "%s starts a group without a sync sample", p)
				}
				if i > 0 && !gapsAllowed[c.name] {
					require.Equal(t, nextDecodeTime, decodeTime, "%s: decode time", p)
				}
				group = g
				nextDecodeTime = decodeTime
				for _, s := range samples {
					nextDecodeTime += uint64(s.Dur)
				}
			}
			f, err := mp4.DecodeFile(bytes.NewReader(track))
			require.NoError(t, err)
			require.Empty(t, f.CheckBrands())
		})
	}
}
