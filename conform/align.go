package conform

import (
	"bytes"
	"fmt"

	"github.com/Eyevinn/locmaf"
	"github.com/Eyevinn/mp4ff/mp4"
)

// Align verifies every fragment of a loaded CMAF input by asserting that
// canonical reconstruction straight from the source equals the
// encode→decode→reconstruct round trip, byte-identically, per fragment.
//
// When collectCanon is set and no chunk diverges, it also returns the
// canonical CMAF bytes: the leading init/ftyp region unchanged, followed
// by each chunk's canonical form — a byte-for-byte canonicalization of
// the input. When wantBytes is set, each normalized chunk also gets a raw
// first-differing-byte window (offsets can mislead once box sizes change,
// so it is opt-in; Normalizations is the box/field-level explanation).
func Align(lc *LoadedCMAF, collectCanon, wantBytes bool) (*AlignReport, []byte, error) {
	raw, f, moov, starts := lc.Raw, lc.File, lc.Moov, lc.Starts

	report := &AlignReport{MediaSegments: len(f.Segments)}
	// Everything before the first chunk (inline ftyp+moov, if any) passes
	// through unchanged; only the media chunks get canonicalized.
	var canonFile []byte
	if collectCanon {
		canonFile = append(canonFile, raw[:starts[0]]...)
	}
	chunkIdx := 0
	for g, seg := range f.Segments {
		tx, rx := locmaf.NewState(), locmaf.NewState()
		for o, frag := range seg.Fragments {
			res := ChunkResult{Group: g, Object: o}
			srcStart := starts[chunkIdx]
			srcEnd := uint64(len(raw))
			if chunkIdx+1 < len(starts) {
				srcEnd = starts[chunkIdx+1]
			}
			chunkIdx++
			src := raw[srcStart:srcEnd]
			res.SourceBytes = len(src)

			canon, objLen, headerLen, srcMoofLen, err := alignFragment(seg, o, frag, tx, rx, moov)
			if err != nil {
				res.Error = err.Error()
				report.Diverged++
				report.Chunks = append(report.Chunks, res)
				continue
			}
			res.Aligned = true
			res.WireBytes = objLen
			res.WireHeaderBytes = headerLen
			res.SourceMoofBytes = srcMoofLen
			report.Aligned++
			report.SourceMoofBytes += srcMoofLen
			report.WireHeaderBytes += headerLen
			if collectCanon {
				canonFile = append(canonFile, canon...)
			}

			if bytes.Equal(src, canon) {
				res.SourceIdentical = true
			} else {
				res.Normalizations = describeNormalizations(src, canon, frag.Moof, moov)
				if wantBytes {
					if off := FirstDiff(src, canon); off >= 0 {
						res.FirstDiff = &DiffPoint{
							Offset:    off,
							SourceHex: HexWindow(src, off),
							CanonHex:  HexWindow(canon, off),
						}
					}
				}
			}
			report.Chunks = append(report.Chunks, res)
		}
	}
	if !collectCanon || report.Diverged > 0 {
		return report, nil, nil
	}
	return report, canonFile, nil
}

// alignFragment runs both paths for one fragment and asserts A == B:
// (A) effective values straight from the source moof → canonical bytes;
// (B) canonical-encode to LOCMAF → decode against the in-group state →
// canonical bytes. It returns the canonical chunk, the full LOCMAF Object
// size, the LOCMAF header-on-wire size (Object minus mdat payload), and
// the source moof box size.
func alignFragment(seg *mp4.MediaSegment, fragIdx int, frag *mp4.Fragment,
	tx, rx *locmaf.State, moov *mp4.MoovBox) ([]byte, int, int, int, error) {
	genBoxes, err := FragmentGenBoxes(seg, fragIdx, frag)
	if err != nil {
		return nil, 0, 0, 0, err
	}
	var payload []byte
	if frag.Mdat != nil {
		payload = frag.Mdat.Data
	}

	effA, err := locmaf.ExtractEffective(genBoxes, frag.Moof, payload, moov)
	if err != nil {
		return nil, 0, 0, 0, fmt.Errorf("extract: %w", err)
	}
	canonA, err := locmaf.ReconstructCanonical(moov, effA)
	if err != nil {
		return nil, 0, 0, 0, fmt.Errorf("reconstruct (direct): %w", err)
	}

	obj, err := locmaf.EncodeCanonical(genBoxes, frag.Moof, payload, tx, moov)
	if err != nil {
		return nil, 0, 0, 0, fmt.Errorf("encode: %w", err)
	}
	effB, _, err := locmaf.Decode(obj, rx, moov)
	if err != nil {
		return nil, 0, 0, 0, fmt.Errorf("decode: %w", err)
	}
	canonB, err := locmaf.ReconstructCanonical(moov, effB)
	if err != nil {
		return nil, 0, 0, 0, fmt.Errorf("reconstruct (round trip): %w", err)
	}

	if !bytes.Equal(canonA, canonB) {
		off := FirstDiff(canonA, canonB)
		return nil, 0, 0, 0, fmt.Errorf("%w at offset %d (A %d bytes, B %d bytes)",
			ErrMisaligned, off, len(canonA), len(canonB))
	}
	srcMoofLen := 0
	if b := reencodeBox(frag.Moof); b != nil {
		srcMoofLen = len(b)
	}
	headerLen := len(obj) - len(payload) // LOCMAF header on the wire
	return canonA, len(obj), headerLen, srcMoofLen, nil
}

func reencodeBox(b mp4.Box) []byte {
	if b == nil {
		return nil
	}
	var buf bytes.Buffer
	if err := b.Encode(&buf); err != nil {
		return nil
	}
	return buf.Bytes()
}
