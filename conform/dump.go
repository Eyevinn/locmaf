package conform

import (
	"fmt"

	"github.com/Eyevinn/locmaf"
	"github.com/Eyevinn/mp4ff/mp4"
)

// Dump walks the self-framed Objects of a .locmaf stream, decoding each
// against a single running in-group State (a full header or rawBoxes
// Object re-anchors it, so group boundaries need no explicit marker).
//
// initBytes resolves the moov for a bare-media stream; pass nil when the
// stream leads with an in-band init rawBoxes Object.
func Dump(data, initBytes []byte) (*DumpReport, error) {
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

	report := &DumpReport{NumObjects: len(objs)}
	state := locmaf.NewState()
	for i, obj := range objs {
		rec := DumpObject{Index: i, WireBytes: len(obj)}
		kind, err := HeaderKind(obj)
		if err != nil {
			rec.Error = err.Error()
			report.Objects = append(report.Objects, rec)
			continue
		}
		rec.Kind = kind

		if moov == nil {
			// A self-contained stream leads with a rawBoxes Object carrying
			// the init in-band; use it to resolve the moov before any
			// moof-carrying Object can be decoded.
			if kind != KindRawBoxes {
				return nil, fmt.Errorf("object %d is a %s header but the stream carries no in-band init: %w",
					i, kind, ErrNoInit)
			}
			content, err := RawBoxesContent(obj)
			if err != nil {
				rec.Error = err.Error()
				report.Objects = append(report.Objects, rec)
				continue
			}
			rec.Raw = &RawInfo{Boxes: BoxNames(content)}
			if m, mErr := MoovFromBytes(content); mErr == nil {
				moov = m
				rec.Raw.IsInit = true
			}
			report.Objects = append(report.Objects, rec)
			continue
		}

		eff, raw, err := locmaf.Decode(obj, state, moov)
		if err != nil {
			rec.Error = err.Error()
			report.Objects = append(report.Objects, rec)
			continue
		}
		if raw != nil {
			rec.Raw = &RawInfo{Boxes: BoxNames(raw)}
			if _, mErr := MoovFromBytes(raw); mErr == nil {
				rec.Raw.IsInit = true
			}
			report.Objects = append(report.Objects, rec)
			continue
		}
		mi := &MoofInfo{SampleCount: eff.SampleCount, BMDT: eff.BMDT, PayloadBytes: len(eff.MdatPayload)}
		for _, gb := range eff.GenBoxes {
			mi.GenBoxes = append(mi.GenBoxes, gb.Name)
		}
		rec.Moof = mi
		report.Objects = append(report.Objects, rec)
	}
	return report, nil
}
