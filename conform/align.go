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
	add := func(res ChunkResult, canon []byte) {
		report.Chunks = append(report.Chunks, res)
		if !res.Aligned {
			report.Diverged++
			return
		}
		report.Aligned++
		report.SourceMoofBytes += res.SourceMoofBytes
		report.WireHeaderBytes += res.WireHeaderBytes
		if collectCanon {
			canonFile = append(canonFile, canon...)
		}
	}
	chunkIdx := 0
	for g, seg := range f.Segments {
		tx, rx := locmaf.NewState(), locmaf.NewState()
		obj := 0 // Object index in the group, counting trailing rawBoxes Objects
		for o, frag := range seg.Fragments {
			srcStart := starts[chunkIdx]
			srcEnd := uint64(len(raw))
			if chunkIdx+1 < len(starts) {
				srcEnd = starts[chunkIdx+1]
			}
			chunkIdx++
			// Boxes after the mdat are not part of the chunk, but a
			// trailing rawBoxes Object after it.
			trailing := FragmentTrailingBoxes(frag)
			chunkEnd := srcEnd
			if len(trailing) > 0 && frag.Mdat != nil {
				if end := frag.Mdat.StartPos + frag.Mdat.Size(); end > srcStart && end <= srcEnd {
					chunkEnd = end
				}
			}
			src := raw[srcStart:chunkEnd]
			res := ChunkResult{Group: g, Object: obj, SourceBytes: len(src)}
			obj++

			canon, objLen, headerLen, srcMoofLen, err := alignFragment(seg, o, frag, tx, rx, moov)
			if err != nil {
				res.Error = err.Error()
			} else {
				res.Aligned = true
				res.WireBytes = objLen
				res.WireHeaderBytes = headerLen
				res.SourceMoofBytes = srcMoofLen
				if bytes.Equal(src, canon) {
					res.SourceIdentical = true
				} else {
					res.Normalizations = describeNormalizations(src, canon, frag.Moof, moov)
					if wantBytes {
						res.FirstDiff = firstDiffPoint(src, canon)
					}
				}
			}
			add(res, canon)

			if len(trailing) > 0 {
				res, canon := alignTrailing(trailing, raw[chunkEnd:srcEnd], tx, rx, moov, wantBytes)
				res.Group, res.Object = g, obj
				obj++
				add(res, canon)
			}
		}
	}
	if !collectCanon || report.Diverged > 0 {
		return report, nil, nil
	}
	return report, canonFile, nil
}

// alignTrailing runs both paths for the boxes after a segment's last
// mdat, which the draft carries as a trailing rawBoxes Object, and
// asserts A == B: (A) the boxes re-encoded back to back; (B) a rawBoxes
// Object encoded and decoded against the in-group states, which it
// resets. src is the source bytes after the mdat. It returns the result,
// without its group and Object index, and the canonical bytes.
func alignTrailing(boxes []mp4.Box, src []byte, tx, rx *locmaf.State,
	moov *mp4.MoovBox, wantBytes bool) (ChunkResult, []byte) {
	res := ChunkResult{SourceBytes: len(src)}
	for _, b := range boxes {
		res.TrailingBoxes = append(res.TrailingBoxes, b.Type())
	}
	canon, err := EncodeBoxes(boxes)
	if err != nil {
		res.Error = err.Error()
		return res, nil
	}
	obj, err := locmaf.EncodeRaw(canon, tx)
	if err != nil {
		res.Error = fmt.Sprintf("encode: %v", err)
		return res, nil
	}
	_, content, err := locmaf.Decode(obj, rx, moov)
	if err != nil {
		res.Error = fmt.Sprintf("decode: %v", err)
		return res, nil
	}
	if !bytes.Equal(canon, content) {
		res.Error = fmt.Errorf("%w at offset %d (A %d bytes, B %d bytes)",
			ErrMisaligned, FirstDiff(canon, content), len(canon), len(content)).Error()
		return res, nil
	}
	res.Aligned = true
	res.WireBytes = len(obj)
	if bytes.Equal(src, canon) {
		res.SourceIdentical = true
	} else {
		res.Normalizations = boxDiff(src, canon)
		if wantBytes {
			res.FirstDiff = firstDiffPoint(src, canon)
		}
	}
	return res, canon
}

// firstDiffPoint is the first-differing-byte window of src and canon, or
// nil when they are equal.
func firstDiffPoint(src, canon []byte) *DiffPoint {
	off := FirstDiff(src, canon)
	if off < 0 {
		return nil
	}
	return &DiffPoint{Offset: off, SourceHex: HexWindow(src, off), CanonHex: HexWindow(canon, off)}
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
