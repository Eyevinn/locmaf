package locmaf

import (
	"fmt"
	"sort"

	"github.com/Eyevinn/locmaf/vi64"
	"github.com/Eyevinn/mp4ff/mp4"
)

// EncodeCanonical encodes one CMAF chunk as a canonical LOCMAF Object:
// the genBox elements, one full or delta header, and the mdat payload.
//
// The header type follows the canonical rule: a full header when prev
// holds no in-group reference (the first chunk of a group) or when the
// source BMDT diverges from the delta derivation (a timeline
// discontinuity re-anchors with a full header); a delta header
// otherwise. prev is mutated to reflect the just-emitted chunk; it must
// not be nil.
func EncodeCanonical(genBoxes []GenBox, moof *mp4.MoofBox, mdatPayload []byte,
	prev *State, moov *mp4.MoovBox) ([]byte, error) {
	if prev == nil {
		return nil, fmt.Errorf("prev state must not be nil: %w", ErrBadSource)
	}
	sv, err := extractSourceValues(moof, moov)
	if err != nil {
		return nil, err
	}
	cf, err := emitFields(sv, moov)
	if err != nil {
		return nil, err
	}

	full := !prev.hasAny
	if !full {
		derived, ok := prev.deriveNextBMDT(moov.Mvex.Trex.DefaultSampleDuration)
		if !ok || derived != sv.bmdt {
			full = true // timeline discontinuity: re-anchor with a full header
		}
	}

	var elementType uint64
	var props []byte
	if full {
		elementType = ElementTypeFullHeader
		props = encodeFullProperties(cf)
	} else {
		elementType = ElementTypeDeltaHeader
		props = encodeDeltaProperties(cf, prev)
	}

	out := make([]byte, 0, len(props)+len(mdatPayload)+64)
	for _, gb := range genBoxes {
		if len(gb.Name) != 4 {
			return nil, fmt.Errorf("genBox name %q is not a FourCC: %w", gb.Name, ErrBadSource)
		}
		boxSize := uint64(4 + len(gb.Payload))
		if boxSize > 0xFFFFFFFB {
			return nil, fmt.Errorf("genBox %q payload too large: %w", gb.Name, ErrBadSource)
		}
		out = vi64.Append(out, ElementTypeGenBox)
		out = vi64.Append(out, boxSize)
		out = append(out, gb.Name...)
		out = append(out, gb.Payload...)
	}
	out = vi64.Append(out, elementType)
	out = vi64.Append(out, uint64(len(props)))
	out = append(out, props...)
	out = append(out, mdatPayload...)

	prev.store(cf)
	return out, nil
}

// propertyEntry is one (field_id, payload) tuple ready for framing.
type propertyEntry struct {
	id      fieldID
	payload []byte
}

// frameProperties serialises entries in ascending field-ID order with
// the parity-rule framing (scalars bare, odd IDs length-prefixed).
func frameProperties(entries []propertyEntry) []byte {
	sort.Slice(entries, func(i, j int) bool { return entries[i].id < entries[j].id })
	var out []byte
	for _, e := range entries {
		out = vi64.Append(out, uint64(e.id))
		if e.id.isList() {
			out = vi64.Append(out, uint64(len(e.payload)))
		}
		out = append(out, e.payload...)
	}
	return out
}

// encodeFullProperties emits every represented field with its absolute
// value encoding.
func encodeFullProperties(cf *chunkFields) []byte {
	var entries []propertyEntry
	for id, v := range cf.scalars {
		entries = append(entries, propertyEntry{id, vi64.Append(nil, v)})
	}
	for id, list := range cf.lists {
		var payload []byte
		for _, v := range list {
			payload = vi64.Append(payload, v)
		}
		entries = append(entries, propertyEntry{id, payload})
	}
	for id, list := range cf.signedLists {
		var payload []byte
		for _, v := range list {
			payload = vi64.AppendZigzag(payload, v)
		}
		entries = append(entries, propertyEntry{id, payload})
	}
	for id, blob := range cf.rawBlobs {
		entries = append(entries, propertyEntry{id, append([]byte(nil), blob...)})
	}
	return frameProperties(entries)
}

// encodeDeltaProperties emits exactly the fields whose represented
// values changed from the in-group reference, plus the deletion marker
// for fields that left it. Scalar and list elements are zigzag deltas
// (missing previous entries count as 0); raw bytes overwrite.
func encodeDeltaProperties(cf *chunkFields, prev *State) []byte {
	var entries []propertyEntry

	for id, v := range cf.scalars {
		prevV, prevHas := prev.scalars[id]
		if prevHas && prevV == v {
			continue
		}
		if id == idTfdtBaseMediaDecodeTime {
			// Full-header-only: a delta chunk's BMDT is always derived.
			// EncodeCanonical only reaches here when the derivation
			// matches, so there is never a delta to emit.
			continue
		}
		entries = append(entries, propertyEntry{id, vi64.AppendZigzag(nil, int64(v)-int64(prevV))})
	}

	for id, list := range cf.lists {
		prevList := prev.lists[id]
		if equalU64(list, prevList) {
			continue
		}
		var payload []byte
		for i, v := range list {
			var p uint64
			if i < len(prevList) {
				p = prevList[i]
			}
			payload = vi64.AppendZigzag(payload, int64(v)-int64(p))
		}
		entries = append(entries, propertyEntry{id, payload})
	}
	for id, list := range cf.signedLists {
		prevList := prev.signedLists[id]
		if equalI64(list, prevList) {
			continue
		}
		var payload []byte
		for i, v := range list {
			var p int64
			if i < len(prevList) {
				p = prevList[i]
			}
			payload = vi64.AppendZigzag(payload, v-p)
		}
		entries = append(entries, propertyEntry{id, payload})
	}
	for id, blob := range cf.rawBlobs {
		if equalByteSlices(blob, prev.rawBlobs[id]) {
			continue
		}
		entries = append(entries, propertyEntry{id, append([]byte(nil), blob...)})
	}

	// Deletion marker: fields present in prev but absent now, as plain
	// unsigned vi64 values in ascending order.
	var deleted []uint64
	for id := range prev.scalars {
		if _, ok := cf.scalars[id]; !ok {
			deleted = append(deleted, uint64(id))
		}
	}
	for id := range prev.lists {
		if _, ok := cf.lists[id]; !ok {
			deleted = append(deleted, uint64(id))
		}
	}
	for id := range prev.signedLists {
		if _, ok := cf.signedLists[id]; !ok {
			deleted = append(deleted, uint64(id))
		}
	}
	for id := range prev.rawBlobs {
		if _, ok := cf.rawBlobs[id]; !ok {
			deleted = append(deleted, uint64(id))
		}
	}
	if len(deleted) > 0 {
		sort.Slice(deleted, func(i, j int) bool { return deleted[i] < deleted[j] })
		var payload []byte
		for _, id := range deleted {
			payload = vi64.Append(payload, id)
		}
		entries = append(entries, propertyEntry{idDeltaDeletedLocmafIDs, payload})
	}

	return frameProperties(entries)
}

func equalU64(a, b []uint64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalI64(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalByteSlices(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
