package conform

import (
	"bytes"
	"fmt"

	"github.com/Eyevinn/locmaf"
	"github.com/Eyevinn/mp4ff/mp4"
)

// Verify walks the Objects of a .locmaf stream and checks each against
// the conformance ladder: it decodes (rung 1), reconstructs a canonical
// CMAF chunk (rung 2), and — when strict — re-encodes that chunk and
// requires the result to be byte-identical to the wire Object (rung 3,
// the canonical-encoding conformance). A rawBoxes Object is carried
// verbatim, so it is canonical by construction once it decodes.
//
// initBytes resolves the moov for a bare-media stream; pass nil when the
// stream leads with an in-band init rawBoxes Object.
//
// Two in-group states run in parallel: rx follows the wire (deltas as
// received), tx follows the canonical re-encode. For a canonical stream
// they stay in lockstep. Because delta headers chain, a later Object's
// verdict can be affected by an earlier non-canonical one — the first
// failure is the reliable signal.
func Verify(data, initBytes []byte, strict bool) (*VerifyReport, error) {
	objs, err := SplitFramed(data)
	if err != nil {
		return nil, err
	}

	var moov *mp4.MoovBox
	if len(initBytes) > 0 {
		if moov, err = MoovFromBytes(initBytes); err != nil {
			return nil, fmt.Errorf("parse init: %w", err)
		}
	}

	report := &VerifyReport{Strict: strict, NumObjects: len(objs)}
	rx, tx := locmaf.NewState(), locmaf.NewState()
	for i, obj := range objs {
		rec := VerifyObject{Index: i, WireBytes: len(obj)}
		kind, kerr := HeaderKind(obj)
		if kerr != nil {
			rec.Error = kerr.Error()
			report.NonConformant++
			report.Objects = append(report.Objects, rec)
			continue
		}
		rec.Kind = kind

		if moov == nil {
			// Resolve the moov from a leading in-band init rawBoxes.
			if kind != KindRawBoxes {
				return nil, fmt.Errorf("object %d is a %s header but the stream carries no in-band init: %w",
					i, kind, ErrNoInit)
			}
			content, cerr := RawBoxesContent(obj)
			if cerr != nil {
				rec.Error = cerr.Error()
				report.NonConformant++
				report.Objects = append(report.Objects, rec)
				continue
			}
			m, merr := MoovFromBytes(content)
			if merr != nil {
				rec.Error = fmt.Sprintf("leading rawBoxes is not a valid init: %v", merr)
				report.NonConformant++
				report.Objects = append(report.Objects, rec)
				continue
			}
			moov = m
			rec.Conformant = true // rawBoxes: carried verbatim, canonical by construction
			report.Conformant++
			report.Objects = append(report.Objects, rec)
			continue
		}

		conf, canonObj, verr := verifyObjectAt(obj, rx, tx, moov, strict)
		switch {
		case verr != nil:
			rec.Error = verr.Error()
			report.NonConformant++
		case conf:
			rec.Conformant = true
			report.Conformant++
		default:
			rec.CanonBytes = len(canonObj)
			if off := FirstDiff(obj, canonObj); off >= 0 {
				rec.FirstDiff = &DiffPoint{
					Offset:    off,
					SourceHex: HexWindow(obj, off),
					CanonHex:  HexWindow(canonObj, off),
				}
			}
			report.NonConformant++
		}
		report.Objects = append(report.Objects, rec)
	}
	return report, nil
}

// verifyObjectAt runs one Object through the ladder, advancing the decode
// state rx and (in strict mode) the encode state tx. It returns whether
// the Object is conformant, the canonical re-encode (only when it differs
// from the wire bytes), and any rung 1/2 error.
func verifyObjectAt(obj []byte, rx, tx *locmaf.State, moov *mp4.MoovBox, strict bool) (bool, []byte, error) {
	eff, raw, err := locmaf.Decode(obj, rx, moov)
	if err != nil {
		return false, nil, err
	}
	if raw != nil {
		// A rawBoxes Object resets the in-group chain on both sides.
		tx.Reset()
		return true, nil, nil
	}
	chunk, err := locmaf.ReconstructCanonical(moov, eff)
	if err != nil {
		return false, nil, err
	}
	if !strict {
		return true, nil, nil
	}
	// Re-encode from the decoded content. The genBoxes and mdat payload
	// come straight from the decode (eff); only the moof needs to be
	// recovered by parsing the canonical chunk.
	moof, err := ParseMoof(chunk)
	if err != nil {
		return false, nil, fmt.Errorf("re-parse canonical chunk: %w", err)
	}
	canonObj, err := locmaf.EncodeCanonical(eff.GenBoxes, moof, eff.MdatPayload, tx, moov)
	if err != nil {
		return false, nil, fmt.Errorf("canonical re-encode: %w", err)
	}
	if bytes.Equal(canonObj, obj) {
		return true, nil, nil
	}
	return false, canonObj, nil
}
