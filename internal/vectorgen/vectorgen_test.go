package vectorgen

import (
	"testing"

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
			require.Contains(t, files, "objects/g000_o000.locobj")
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
