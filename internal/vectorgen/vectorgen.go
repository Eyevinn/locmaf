package vectorgen

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/Eyevinn/locmaf"
)

var errPairDiverged = errors.New("alternate source encodes differently")

// Manifest describes one vector case directory.
type Manifest struct {
	Name                string            `json:"name"`
	Description         string            `json:"description"`
	LocmafVersion       string            `json:"locmafVersion"`
	DraftCommit         string            `json:"draftCommit"`
	Groups              int               `json:"groups"`
	Objects             int               `json:"objects"`
	RepresentationPairs int               `json:"representationPairs,omitempty"`
	Files               map[string]string `json:"files"` // path → sha256
}

// effectiveJSON is the pinned JSON encoding of a moof-carrying object's
// effective values: IVs and payloads as lowercase hex, flags as plain
// uint32 numbers, the mdat payload by length + sha256 (the bytes
// themselves live in the canonical .cmfc).
type effectiveJSON struct {
	SampleCount            int           `json:"sampleCount"`
	BMDT                   uint64        `json:"bmdt"`
	SampleDescriptionIndex uint32        `json:"sampleDescriptionIndex"`
	Durations              []uint32      `json:"durations"`
	Sizes                  []uint32      `json:"sizes"`
	Flags                  []uint32      `json:"flags"`
	CTOs                   []int32       `json:"ctos"`
	PerSampleIVSize        uint8         `json:"perSampleIVSize,omitempty"`
	IVs                    string        `json:"ivs,omitempty"`
	HasSubsamples          bool          `json:"hasSubsamples,omitempty"`
	SubsampleCounts        []uint16      `json:"subsampleCounts,omitempty"`
	ClearBytes             []uint16      `json:"clearBytes,omitempty"`
	ProtectedBytes         []uint32      `json:"protectedBytes,omitempty"`
	GenBoxes               []genBoxJSON  `json:"genBoxes,omitempty"`
	MdatLength             int           `json:"mdatLength"`
	MdatSHA256             string        `json:"mdatSha256"`
	RawBoxes               *rawBoxesJSON `json:"rawBoxes,omitempty"`
}

type genBoxJSON struct {
	Name    string `json:"name"`
	Payload string `json:"payload"` // hex
}

// rawBoxesJSON describes a rawBoxes Object; when present, all other
// fields of effectiveJSON are absent.
type rawBoxesJSON struct {
	Length int    `json:"length"`
	SHA256 string `json:"sha256"`
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func marshalEffective(eff *locmaf.EffectiveValues) []byte {
	ej := effectiveJSON{
		SampleCount:            eff.SampleCount,
		BMDT:                   eff.BMDT,
		SampleDescriptionIndex: eff.SampleDescriptionIndex,
		Durations:              eff.Durations,
		Sizes:                  eff.Sizes,
		Flags:                  eff.Flags,
		CTOs:                   eff.CTOs,
		PerSampleIVSize:        eff.PerSampleIVSize,
		HasSubsamples:          eff.HasSubsamples,
		SubsampleCounts:        eff.SubsampleCounts,
		ClearBytes:             eff.ClearBytes,
		ProtectedBytes:         eff.ProtectedBytes,
		MdatLength:             len(eff.MdatPayload),
		MdatSHA256:             sha256Hex(eff.MdatPayload),
	}
	if len(eff.IVs) > 0 {
		ej.IVs = hex.EncodeToString(eff.IVs)
	}
	for _, gb := range eff.GenBoxes {
		ej.GenBoxes = append(ej.GenBoxes, genBoxJSON{
			Name:    gb.Name,
			Payload: hex.EncodeToString(gb.Payload),
		})
	}
	return append(mustMarshalIndent(ej), '\n')
}

func marshalRawObject(raw []byte) []byte {
	ej := effectiveJSON{RawBoxes: &rawBoxesJSON{Length: len(raw), SHA256: sha256Hex(raw)}}
	return append(mustMarshalIndent(struct {
		RawBoxes *rawBoxesJSON `json:"rawBoxes"`
	}{ej.RawBoxes}), '\n')
}

func mustMarshalIndent(v any) []byte {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		panic(err) // struct marshalling cannot fail
	}
	return b
}

// generateCase derives every file of one case from the codec.
func generateCase(c vectorCase) (map[string][]byte, error) {
	data, err := c.build()
	if err != nil {
		return nil, fmt.Errorf("case %s: %w", c.name, err)
	}
	moov := data.init.Moov
	files := map[string][]byte{}

	initBytes, err := encodeInit(data.init)
	if err != nil {
		return nil, fmt.Errorf("case %s: %w", c.name, err)
	}
	files["init.mp4"] = initBytes

	objects, pairs := 0, 0
	var framed []byte
	for g, group := range data.groups {
		tx, rx := locmaf.NewState(), locmaf.NewState()
		for o, ch := range group {
			var obj []byte
			switch {
			case ch.raw != nil:
				obj, err = locmaf.EncodeRaw(ch.raw, tx)
			default:
				obj, err = locmaf.EncodeCanonical(ch.genBoxes, ch.moof, ch.mdat, tx, moov)
			}
			if err != nil {
				return nil, fmt.Errorf("case %s g%03d o%03d encode: %w", c.name, g, o, err)
			}

			// Representation invariance: every decode-equivalent
			// alternate source must canonically encode to the same
			// bytes, given the same in-group history.
			for _, alt := range ch.altMoofs {
				tx2 := locmaf.NewState()
				for _, prev := range group[:o] {
					if prev.raw != nil {
						if _, err := locmaf.EncodeRaw(prev.raw, tx2); err != nil {
							return nil, err
						}
						continue
					}
					if _, err := locmaf.EncodeCanonical(prev.genBoxes, prev.moof, prev.mdat, tx2, moov); err != nil {
						return nil, err
					}
				}
				altObj, err := locmaf.EncodeCanonical(ch.genBoxes, alt, ch.mdat, tx2, moov)
				if err != nil {
					return nil, fmt.Errorf("case %s g%03d o%03d alternate encode: %w", c.name, g, o, err)
				}
				if !bytes.Equal(altObj, obj) {
					return nil, fmt.Errorf("case %s g%03d o%03d: %w", c.name, g, o, errPairDiverged)
				}
				pairs++
			}

			stem := fmt.Sprintf("g%03d_o%03d", g, o)
			files["objects/"+stem+".locmafobj"] = obj
			framed = locmaf.AppendFramed(framed, obj)
			objects++

			eff, raw, err := locmaf.Decode(obj, rx, moov)
			if err != nil {
				return nil, fmt.Errorf("case %s %s decode: %w", c.name, stem, err)
			}
			if raw != nil {
				files["canonical/"+stem+".cmfc"] = raw
				files["effective/"+stem+".json"] = marshalRawObject(raw)
				continue
			}
			chunk, err := locmaf.ReconstructCanonical(moov, eff)
			if err != nil {
				return nil, fmt.Errorf("case %s %s reconstruct: %w", c.name, stem, err)
			}
			files["canonical/"+stem+".cmfc"] = chunk
			files["effective/"+stem+".json"] = marshalEffective(eff)
		}
	}
	if data.locmafFile {
		files["file.locmaf"] = framed
	}

	m := Manifest{
		Name:                c.name,
		Description:         c.description,
		LocmafVersion:       locmaf.Version,
		DraftCommit:         draftCommit,
		Groups:              len(data.groups),
		Objects:             objects,
		RepresentationPairs: pairs,
		Files:               map[string]string{},
	}
	for path, content := range files {
		m.Files[path] = sha256Hex(content)
	}
	files["manifest.json"] = append(mustMarshalIndent(m), '\n')
	return files, nil
}

// Generate writes the whole corpus under dir, one directory per case,
// plus a top-level README.md derived from the same cases.
func Generate(dir string) error {
	var summaries []caseSummary
	for _, c := range cases() {
		files, err := generateCase(c)
		if err != nil {
			return err
		}
		for path, content := range files {
			full := filepath.Join(dir, c.name, filepath.FromSlash(path))
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(full, content, 0o644); err != nil {
				return err
			}
		}
		summaries = append(summaries, summarize(c, files))
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, readmeName), corpusReadme(summaries), 0o644)
}

// Check re-derives every case from the codec and byte-compares against
// the corpus on disk, so the committed vectors can never drift from the
// code. It returns one message per divergence (empty means clean).
func Check(dir string) ([]string, error) {
	var problems []string
	var summaries []caseSummary
	seen := map[string]struct{}{}
	for _, c := range cases() {
		files, err := generateCase(c)
		if err != nil {
			return nil, err
		}
		paths := make([]string, 0, len(files))
		for path := range files {
			paths = append(paths, path)
		}
		sort.Strings(paths)
		for _, path := range paths {
			rel := filepath.Join(c.name, filepath.FromSlash(path))
			seen[rel] = struct{}{}
			got, err := os.ReadFile(filepath.Join(dir, rel))
			switch {
			case os.IsNotExist(err):
				problems = append(problems, fmt.Sprintf("%s: missing from corpus (regenerate with `locmaf vectors gen`)", rel))
			case err != nil:
				return nil, err
			case !bytes.Equal(got, files[path]):
				problems = append(problems, fmt.Sprintf("%s: differs from codec-derived bytes", rel))
			}
		}
		summaries = append(summaries, summarize(c, files))
	}
	// The top-level README is derived from the same cases and pinned too.
	seen[readmeName] = struct{}{}
	switch got, err := os.ReadFile(filepath.Join(dir, readmeName)); {
	case os.IsNotExist(err):
		problems = append(problems, fmt.Sprintf("%s: missing from corpus (regenerate with `locmaf vectors gen`)", readmeName))
	case err != nil:
		return nil, err
	case !bytes.Equal(got, corpusReadme(summaries)):
		problems = append(problems, fmt.Sprintf("%s: differs from codec-derived bytes", readmeName))
	}
	// Stale files: anything under a case directory the matrix no longer
	// produces.
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		if _, ok := seen[rel]; !ok {
			problems = append(problems, fmt.Sprintf("%s: stale file not produced by the case matrix", rel))
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	sort.Strings(problems)
	return problems, nil
}
