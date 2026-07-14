package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Eyevinn/mp4ff/mp4"
	"github.com/stretchr/testify/require"
)

// writeTestCMAF synthesizes a small fragmented CMAF file: init inline,
// two segments (styp) of two fragments each, varying sample sizes.
func writeTestCMAF(t *testing.T, dir string) string {
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

	path := filepath.Join(dir, "test.cmaf")
	require.NoError(t, os.WriteFile(path, buf.Bytes(), 0o644))
	return path
}

func TestAlignOnSynthesizedCMAF(t *testing.T) {
	dir := t.TempDir()
	path := writeTestCMAF(t, dir)

	report, canon, err := alignFile(path, "", false)
	require.NoError(t, err)
	require.Nil(t, canon)
	require.Len(t, report.Chunks, 4)
	require.Equal(t, 4, report.Aligned)
	require.Zero(t, report.Diverged)
	for _, c := range report.Chunks {
		require.True(t, c.Aligned, "g%d o%d", c.Group, c.Object)
		require.Positive(t, c.WireBytes)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{cmdAlign, "-report", formatJSON, path}, &stdout, &stderr)
	require.Zero(t, code, "stderr: %s", stderr.String())
	require.Contains(t, stdout.String(), `"alignedChunks": 4`)

	stdout.Reset()
	code = run([]string{cmdAlign, path}, &stdout, &stderr)
	require.Zero(t, code)
	require.Contains(t, stdout.String(), "4 aligned, 0 diverged")
}

func TestAlignCanonOut(t *testing.T) {
	dir := t.TempDir()
	path := writeTestCMAF(t, dir)
	canonPath := filepath.Join(dir, "canon.cmaf")

	var stdout, stderr bytes.Buffer
	code := run([]string{cmdAlign, "-canon-out", canonPath, path}, &stdout, &stderr)
	require.Zero(t, code, "stderr: %s", stderr.String())
	require.Contains(t, stdout.String(), "4 aligned, 0 diverged")

	// The canonical file is a valid, self-contained CMAF file and it is
	// already canonical: re-aligning it needs no further normalization.
	report, _, err := alignFile(canonPath, "", false)
	require.NoError(t, err)
	require.Len(t, report.Chunks, 4)
	require.Zero(t, report.Diverged)
	for _, c := range report.Chunks {
		require.True(t, c.SourceIdentical, "g%d o%d not canonical", c.Group, c.Object)
	}

	// Feeding it back through -canon-out is a fixed point.
	canonPath2 := filepath.Join(dir, "canon2.cmaf")
	stdout.Reset()
	stderr.Reset()
	code = run([]string{cmdAlign, "-canon-out", canonPath2, canonPath}, &stdout, &stderr)
	require.Zero(t, code, "stderr: %s", stderr.String())
	first, err := os.ReadFile(canonPath)
	require.NoError(t, err)
	second, err := os.ReadFile(canonPath2)
	require.NoError(t, err)
	require.Equal(t, first, second)
}

func TestVectorsGenAndCheck(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := run([]string{cmdVectors, cmdGen, "-out", dir}, &stdout, &stderr)
	require.Zero(t, code, "stderr: %s", stderr.String())

	stdout.Reset()
	code = run([]string{cmdVectors, cmdCheck, dir}, &stdout, &stderr)
	require.Zero(t, code, "check output: %s", stdout.String())
	require.Contains(t, stdout.String(), "corpus matches")

	// Corrupt one vector: check must flag it and exit 1.
	var victim string
	require.NoError(t, filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.HasSuffix(p, ".locmafobj") && victim == "" {
			victim = p
		}
		return err
	}))
	require.NotEmpty(t, victim)
	require.NoError(t, os.WriteFile(victim, []byte{0xFF}, 0o644))

	stdout.Reset()
	code = run([]string{cmdVectors, cmdCheck, dir}, &stdout, &stderr)
	require.Equal(t, 1, code)
	require.Contains(t, stdout.String(), "differs from codec-derived bytes")
}

func TestVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	require.Zero(t, run([]string{"-version"}, &stdout, &stderr))
	require.Regexp(t, `^locmaf v.+\n$`, stdout.String())
	stdout.Reset()
	require.Zero(t, run([]string{"--version"}, &stdout, &stderr))
	require.Regexp(t, `^locmaf v`, stdout.String())
}

func TestUsageAndErrors(t *testing.T) {
	var stdout, stderr bytes.Buffer
	require.Equal(t, 2, run(nil, &stdout, &stderr))
	require.Equal(t, 2, run([]string{"bogus"}, &stdout, &stderr))
	require.Equal(t, 0, run([]string{"help"}, &stdout, &stderr))
	require.Equal(t, 2, run([]string{cmdAlign}, &stdout, &stderr))
	require.Equal(t, 2, run([]string{cmdAlign, "/nonexistent.cmaf"}, &stdout, &stderr))
	require.Equal(t, 2, run([]string{cmdVectors}, &stdout, &stderr))
}
