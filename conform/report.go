// Package conform is the shared conformance core behind the locmaf CLI
// and its browser (js/wasm) build. It operates purely on byte slices and
// parsed mp4 boxes — no file or network I/O — so the same code path
// backs `locmaf verify/dump/align` on the command line and the
// client-side checker at locmaf.dev.
//
// The three entry points mirror the CLI subcommands:
//
//	Verify — is a .locmaf stream conformant (decodes, reconstructs, and,
//	         in strict mode, is itself canonical)?
//	Dump   — what does each Object of a .locmaf stream carry?
//	Align  — does a fragmented CMAF file round-trip through LOCMAF
//	         byte-identically, and what canonical normalizations apply?
package conform

// Object kinds, matching the LOCMAF header element types plus the
// rawBoxes Object.
const (
	KindRawBoxes = "rawBoxes"
	KindFull     = "full"
	KindDelta    = "delta"
)

// DiffPoint is the first-differing-byte window between two byte strings.
type DiffPoint struct {
	Offset    int    `json:"offset"`
	SourceHex string `json:"sourceHex"`
	CanonHex  string `json:"canonHex"`
}

// VerifyObject is the conformance outcome for one Object of a .locmaf file.
type VerifyObject struct {
	Index      int    `json:"index"`
	WireBytes  int    `json:"wireBytes"`
	Kind       string `json:"kind"`
	Conformant bool   `json:"conformant"`
	// Error is a rung 1/2 failure: the Object did not decode, or its
	// effective values did not reconstruct a canonical chunk.
	Error string `json:"error,omitempty"`
	// CanonBytes and FirstDiff describe a rung 3 failure: the Object
	// decodes and reconstructs but is not itself canonical — the
	// canonical re-encode differs from the wire bytes.
	CanonBytes int        `json:"canonBytes,omitempty"`
	FirstDiff  *DiffPoint `json:"firstDiff,omitempty"`
}

// VerifyReport is the result of Verify over a whole .locmaf stream.
type VerifyReport struct {
	Input         string         `json:"input"`
	Strict        bool           `json:"strict"`
	NumObjects    int            `json:"numObjects"`
	Conformant    int            `json:"conformantObjects"`
	NonConformant int            `json:"nonConformantObjects"`
	Objects       []VerifyObject `json:"objects"`
}

// RawInfo describes a rawBoxes Object: the top-level ISO boxes it carries
// and whether it is a CMAF Header (has a moov).
type RawInfo struct {
	Boxes  []string `json:"boxes"`
	IsInit bool     `json:"isInit"`
}

// MoofInfo summarizes a moof-carrying Object's decoded effective values.
type MoofInfo struct {
	GenBoxes     []string `json:"genBoxes,omitempty"`
	SampleCount  int      `json:"sampleCount"`
	BMDT         uint64   `json:"bmdt"`
	PayloadBytes int      `json:"payloadBytes"`
}

// DumpObject is one decoded LOCMAF Object in a .locmaf file.
type DumpObject struct {
	Index     int    `json:"index"`
	WireBytes int    `json:"wireBytes"`
	Kind      string `json:"kind"`
	// Exactly one of Raw / Moof is set (unless Error is).
	Raw   *RawInfo  `json:"rawBoxes,omitempty"`
	Moof  *MoofInfo `json:"moof,omitempty"`
	Error string    `json:"error,omitempty"`
}

// DumpReport is the result of Dump over a whole .locmaf stream.
type DumpReport struct {
	Input      string       `json:"input"`
	NumObjects int          `json:"numObjects"`
	Objects    []DumpObject `json:"objects"`
}

// ChunkResult is the align outcome for one CMAF fragment (one LOCMAF
// Object).
type ChunkResult struct {
	Group  int `json:"group"`
	Object int `json:"object"`
	// Aligned: canonical bytes from the source (A) equal the canonical
	// bytes from the encode→decode round trip (B). This is the
	// conformance assertion.
	Aligned bool `json:"aligned"`
	// SourceIdentical: the source chunk bytes already equal the canonical
	// form (no normalization was needed).
	SourceIdentical bool `json:"sourceIdentical"`
	// Normalizations explains, box and field at a time, how the canonical
	// form differs from the source bytes (mfhd sequence zeroed, tfdt
	// widened, defaults folded, box reorder, data_offset recomputed, …).
	Normalizations []string `json:"normalizations,omitempty"`
	// FirstDiff is the raw first-differing-byte window, populated only on
	// request (the CLI's -bytes flag). Offsets can mislead once a box
	// size changes, so it is opt-in.
	FirstDiff *DiffPoint `json:"firstDiff,omitempty"`
	Error     string     `json:"error,omitempty"`
	// WireBytes is the full LOCMAF Object; SourceBytes is the full source
	// CMAF chunk (both include the mdat payload). SourceMoofBytes is the
	// source moof box alone and WireHeaderBytes is the Object minus its
	// mdat payload — the like-for-like moof-overhead comparison.
	WireBytes       int `json:"wireBytes"`
	SourceBytes     int `json:"sourceBytes"`
	SourceMoofBytes int `json:"sourceMoofBytes"`
	WireHeaderBytes int `json:"wireHeaderBytes"`
}

// AlignReport is the result of Align over a whole fragmented CMAF file.
type AlignReport struct {
	Input           string        `json:"input"`
	Chunks          []ChunkResult `json:"chunks"`
	Aligned         int           `json:"alignedChunks"`
	Diverged        int           `json:"divergedChunks"`
	SourceMoofBytes int           `json:"sourceMoofBytes"`
	WireHeaderBytes int           `json:"wireHeaderBytes"`
	MediaSegments   int           `json:"mediaSegments"`
}
