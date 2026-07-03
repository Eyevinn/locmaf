package locmaf

import (
	"encoding/binary"
	"fmt"

	"github.com/Eyevinn/locmaf/vi64"
	"github.com/Eyevinn/mp4ff/mp4"
)

// maxSampleCount bounds trunSampleCount as a defence against
// allocation attacks; no real CMAF chunk approaches it.
const maxSampleCount = 1 << 24

// Decode decodes one LOCMAF Object payload, using prev as the in-group
// reference for delta chunks. For a moof-carrying Object it returns the
// chunk's effective values and a nil raw slice; for a rawBoxes Object it
// returns nil effective values and the carried boxes verbatim (a
// subslice of payload), which are also their own canonical form. prev is
// mutated to reflect the decoded Object (a full header resets it first;
// a rawBoxes Object resets it, so the next moof-carrying Object must be
// full); it must not be nil. Callers use one State per MOQT group, in
// object order, and Reset it (or use a fresh one) at every group
// boundary.
func Decode(payload []byte, prev *State, moov *mp4.MoovBox) (*EffectiveValues, []byte, error) {
	if prev == nil {
		return nil, nil, fmt.Errorf("prev state must not be nil: %w", ErrMalformed)
	}
	if moov == nil || moov.Mvex == nil || moov.Mvex.Trex == nil {
		return nil, nil, fmt.Errorf("moov or trex not defined: %w", ErrMalformed)
	}

	genBoxes, headerType, props, mdat, raw, err := splitElements(payload)
	if err != nil {
		return nil, nil, err
	}
	if raw != nil {
		prev.Reset()
		return nil, raw, nil
	}

	var cf *chunkFields
	switch headerType {
	case ElementTypeFullHeader:
		cf, err = applyFullProperties(props)
		if err != nil {
			return nil, nil, err
		}
		prev.Reset()
	case ElementTypeDeltaHeader:
		if !prev.hasAny {
			return nil, nil, fmt.Errorf("delta header before any full header in the group: %w", ErrMalformed)
		}
		cf, err = applyDeltaProperties(props, prev, moov.Mvex.Trex.DefaultSampleDuration)
		if err != nil {
			return nil, nil, err
		}
	}

	eff, err := expandEffective(cf, genBoxes, mdat, moov)
	if err != nil {
		return nil, nil, err
	}
	prev.store(cf)
	return eff, nil, nil
}

// splitElements walks the element sequence: either genBoxes, exactly
// one full or delta header, and the untagged mdat payload, or a single
// rawBoxes element spanning the whole Object (returned via raw).
func splitElements(payload []byte) (genBoxes []GenBox, headerType uint64, props, mdat, raw []byte, err error) {
	pos := 0
	for {
		if pos >= len(payload) {
			return nil, 0, nil, nil, nil, fmt.Errorf("object ends before a header element: %w", ErrMalformed)
		}
		elementType, n, err := vi64.Parse(payload[pos:])
		if err != nil {
			return nil, 0, nil, nil, nil, fmt.Errorf("invalid element_type: %w", ErrMalformed)
		}
		pos += n

		switch elementType {
		case ElementTypeGenBox:
			boxSize, n, err := vi64.Parse(payload[pos:])
			if err != nil {
				return nil, 0, nil, nil, nil, fmt.Errorf("invalid genBox box_size: %w", ErrMalformed)
			}
			pos += n
			if boxSize < 4 || boxSize > 0xFFFFFFFB {
				return nil, 0, nil, nil, nil, fmt.Errorf("genBox box_size %d out of range: %w", boxSize, ErrMalformed)
			}
			if boxSize > uint64(len(payload)-pos) {
				return nil, 0, nil, nil, nil, fmt.Errorf("genBox exceeds object payload: %w", ErrMalformed)
			}
			genBoxes = append(genBoxes, GenBox{
				Name:    string(payload[pos : pos+4]),
				Payload: append([]byte(nil), payload[pos+4:pos+int(boxSize)]...),
			})
			pos += int(boxSize)

		case ElementTypeFullHeader, ElementTypeDeltaHeader:
			propsLen, n, err := vi64.Parse(payload[pos:])
			if err != nil {
				return nil, 0, nil, nil, nil, fmt.Errorf("invalid properties_length: %w", ErrMalformed)
			}
			pos += n
			if propsLen > uint64(len(payload)-pos) {
				return nil, 0, nil, nil, nil, fmt.Errorf("property block exceeds object payload: %w", ErrMalformed)
			}
			props = payload[pos : pos+int(propsLen)]
			mdat = payload[pos+int(propsLen):]
			return genBoxes, elementType, props, mdat, nil, nil

		case ElementTypeRawBoxes:
			if len(genBoxes) > 0 {
				return nil, 0, nil, nil, nil, fmt.Errorf("rawBoxes element after a genBox: %w", ErrMalformed)
			}
			// A rawBoxes element carries no length of its own: as the
			// sole element of its Object, the Object length delimits it,
			// like the untagged mdat payload of a moof-carrying Object.
			raw = payload[pos:]
			if err := validateRawBoxes(raw, ErrMalformed); err != nil {
				return nil, 0, nil, nil, nil, err
			}
			return nil, 0, nil, nil, raw, nil

		default:
			// Not self-delimiting: the Object cannot be skipped past.
			return nil, 0, nil, nil, nil, fmt.Errorf("unknown element_type %d: %w", elementType, ErrMalformed)
		}
	}
}

// validateRawBoxes checks that data is the concatenation of one or more
// complete ISO BMFF boxes: each box's declared size at least 8, neither
// ISO size escape (0 or 1) used, and the sizes summing to exactly
// len(data). sentinel is the error class to wrap: ErrMalformed on the
// decode side, ErrBadSource on the encode side.
func validateRawBoxes(data []byte, sentinel error) error {
	if len(data) == 0 {
		return fmt.Errorf("empty rawBoxes content: %w", sentinel)
	}
	pos := 0
	for pos < len(data) {
		if len(data)-pos < 8 {
			return fmt.Errorf("truncated box header at offset %d in rawBoxes: %w", pos, sentinel)
		}
		size := binary.BigEndian.Uint32(data[pos:])
		if size < 8 {
			return fmt.Errorf("box size %d at offset %d in rawBoxes (ISO size escapes and sub-header sizes not allowed): %w",
				size, pos, sentinel)
		}
		if uint64(size) > uint64(len(data)-pos) {
			return fmt.Errorf("box at offset %d exceeds rawBoxes content: %w", pos, sentinel)
		}
		pos += int(size)
	}
	return nil
}

// rawProperties splits a property block into raw bytes per field ID via
// the parity rule. Unknown field IDs are skipped (their framing is
// still parsed); a repeated field ID rejects the block.
func rawProperties(data []byte) (map[fieldID][]byte, error) {
	out := make(map[fieldID][]byte)
	seen := make(map[fieldID]struct{})
	pos := 0
	for pos < len(data) {
		idValue, n, err := vi64.Parse(data[pos:])
		if err != nil {
			return nil, fmt.Errorf("invalid field id at offset %d: %w", pos, ErrMalformed)
		}
		pos += n
		id := fieldID(idValue)
		if _, dup := seen[id]; dup {
			return nil, fmt.Errorf("field id %d repeated in one property block: %w", id, ErrMalformed)
		}
		seen[id] = struct{}{}

		var value []byte
		if !id.isList() {
			_, n, err := vi64.Parse(data[pos:])
			if err != nil {
				return nil, fmt.Errorf("invalid scalar value for id %d: %w", id, ErrMalformed)
			}
			value = data[pos : pos+n]
			pos += n
		} else {
			length, n, err := vi64.Parse(data[pos:])
			if err != nil {
				return nil, fmt.Errorf("invalid byte length for id %d: %w", id, ErrMalformed)
			}
			pos += n
			if length > uint64(len(data)-pos) {
				return nil, fmt.Errorf("field id %d exceeds property block: %w", id, ErrMalformed)
			}
			value = data[pos : pos+int(length)]
			pos += int(length)
		}
		if known(id) {
			out[id] = value
		}
	}
	return out, nil
}

func known(id fieldID) bool {
	return isKnownScalar(id) || isKnownVarintList(id) ||
		id == idTrunSampleCompositionTimeOffsets ||
		id == idSencInitializationVector ||
		id == idDeltaDeletedLocmafIDs
}

func parseScalar(raw []byte, id fieldID) (uint64, error) {
	v, _, err := vi64.Parse(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid scalar id %d: %w", id, ErrMalformed)
	}
	return v, nil
}

func parseUnsignedList(raw []byte, id fieldID) ([]uint64, error) {
	var out []uint64
	pos := 0
	for pos < len(raw) {
		v, n, err := vi64.Parse(raw[pos:])
		if err != nil {
			return nil, fmt.Errorf("invalid vi64 list for id %d: %w", id, ErrMalformed)
		}
		out = append(out, v)
		pos += n
	}
	return out, nil
}

func parseZigzagList(raw []byte, id fieldID) ([]int64, error) {
	var out []int64
	pos := 0
	for pos < len(raw) {
		v, n, err := vi64.ParseZigzag(raw[pos:])
		if err != nil {
			return nil, fmt.Errorf("invalid zigzag list for id %d: %w", id, ErrMalformed)
		}
		out = append(out, v)
		pos += n
	}
	return out, nil
}

// applyFullProperties interprets a full header's property block: every
// value absolute.
func applyFullProperties(props []byte) (*chunkFields, error) {
	raw, err := rawProperties(props)
	if err != nil {
		return nil, err
	}
	if _, ok := raw[idDeltaDeletedLocmafIDs]; ok {
		return nil, fmt.Errorf("field 27 in a full header: %w", ErrMalformed)
	}
	cf := newChunkFields()
	for id, bytes := range raw {
		switch {
		case isKnownScalar(id):
			v, err := parseScalar(bytes, id)
			if err != nil {
				return nil, err
			}
			cf.scalars[id] = v
		case id == idTrunSampleCompositionTimeOffsets:
			list, err := parseZigzagList(bytes, id)
			if err != nil {
				return nil, err
			}
			cf.signedLists[id] = list
		case id == idSencInitializationVector:
			cf.rawBlobs[id] = append([]byte(nil), bytes...)
		case isKnownVarintList(id):
			list, err := parseUnsignedList(bytes, id)
			if err != nil {
				return nil, err
			}
			cf.lists[id] = list
		}
	}
	if _, ok := cf.scalars[idTrunSampleCount]; !ok {
		return nil, fmt.Errorf("full header lacks trunSampleCount: %w", ErrMalformed)
	}
	if _, ok := cf.scalars[idTfdtBaseMediaDecodeTime]; !ok {
		return nil, fmt.Errorf("full header lacks tfdtBaseMediaDecodeTime: %w", ErrMalformed)
	}
	return cf, nil
}

// applyDeltaProperties applies a delta header to the in-group
// reference: deletions first, then per-field deltas; the BMDT is always
// derived.
func applyDeltaProperties(props []byte, prev *State, trexDefaultDur uint32) (*chunkFields, error) {
	raw, err := rawProperties(props)
	if err != nil {
		return nil, err
	}
	if _, ok := raw[idTfdtBaseMediaDecodeTime]; ok {
		return nil, fmt.Errorf("field 10 in a delta header: %w", ErrMalformed)
	}

	cf := prev.snapshot()

	// Deletions apply before deltas.
	if delBytes, ok := raw[idDeltaDeletedLocmafIDs]; ok {
		ids, err := parseUnsignedList(delBytes, idDeltaDeletedLocmafIDs)
		if err != nil {
			return nil, err
		}
		for _, idv := range ids {
			id := fieldID(idv)
			delete(cf.scalars, id)
			delete(cf.lists, id)
			delete(cf.signedLists, id)
			delete(cf.rawBlobs, id)
		}
		delete(raw, idDeltaDeletedLocmafIDs)
	}

	// Scalars, with sample count first so list lengths are known.
	applyScalarDelta := func(id fieldID) error {
		bytes, ok := raw[id]
		if !ok {
			return nil
		}
		delta, _, err := vi64.ParseZigzag(bytes)
		if err != nil {
			return fmt.Errorf("invalid delta scalar id %d: %w", id, ErrMalformed)
		}
		newV := int64(cf.scalars[id]) + delta // absent previous value counts as 0
		if newV < 0 {
			return fmt.Errorf("negative value for id %d after delta: %w", id, ErrMalformed)
		}
		cf.scalars[id] = uint64(newV)
		delete(raw, id)
		return nil
	}
	if err := applyScalarDelta(idTrunSampleCount); err != nil {
		return nil, err
	}
	n := cf.scalars[idTrunSampleCount]
	if n > maxSampleCount {
		return nil, fmt.Errorf("sample count %d exceeds implementation limit: %w", n, ErrMalformed)
	}
	for _, id := range []fieldID{idTfhdSampleDescriptionIndex, idTfhdDefaultSampleDuration,
		idTfhdDefaultSampleSize, idTfhdDefaultSampleFlags, idTrunFirstSampleFlags,
		idSencPerSampleIVSize} {
		if err := applyScalarDelta(id); err != nil {
			return nil, err
		}
	}

	// Unsigned lists; subsample counts before the per-subsample lists
	// so their expected total is known.
	applyListDelta := func(id fieldID, want int) error {
		bytes, ok := raw[id]
		if !ok {
			// Inherited: resize to the current expected length.
			list, has := cf.lists[id]
			if !has {
				return nil
			}
			if len(list) > want {
				cf.lists[id] = list[:want]
			} else if len(list) < want {
				return fmt.Errorf("inherited list id %d has %d elements, need %d: %w",
					id, len(list), want, ErrMalformed)
			}
			return nil
		}
		deltas, err := parseZigzagList(bytes, id)
		if err != nil {
			return err
		}
		if len(deltas) != want {
			return fmt.Errorf("list id %d carries %d elements, expected %d: %w",
				id, len(deltas), want, ErrMalformed)
		}
		prevList := cf.lists[id]
		out := make([]uint64, len(deltas))
		for i, d := range deltas {
			var p int64
			if i < len(prevList) {
				p = int64(prevList[i])
			}
			v := p + d
			if v < 0 {
				return fmt.Errorf("negative element in list id %d: %w", id, ErrMalformed)
			}
			out[i] = uint64(v)
		}
		cf.lists[id] = out
		delete(raw, id)
		return nil
	}

	if err := applyListDelta(idTrunSampleDurations, int(n)); err != nil {
		return nil, err
	}
	if err := applyListDelta(idTrunSampleFlags, int(n)); err != nil {
		return nil, err
	}
	sizesWant := int(n) - 1
	if sizesWant < 0 {
		sizesWant = 0
	}
	if err := applyListDelta(idTrunSampleSizes, sizesWant); err != nil {
		return nil, err
	}
	if err := applyListDelta(idSencSubsampleCount, int(n)); err != nil {
		return nil, err
	}
	totalSubs := 0
	for _, c := range cf.lists[idSencSubsampleCount] {
		totalSubs += int(c)
		if totalSubs > maxSampleCount {
			return nil, fmt.Errorf("subsample total exceeds implementation limit: %w", ErrMalformed)
		}
	}
	if err := applyListDelta(idSencBytesOfClearData, totalSubs); err != nil {
		return nil, err
	}
	if err := applyListDelta(idSencBytesOfProtectedData, totalSubs); err != nil {
		return nil, err
	}

	// Signed list (composition-time offsets).
	if bytes, ok := raw[idTrunSampleCompositionTimeOffsets]; ok {
		deltas, err := parseZigzagList(bytes, idTrunSampleCompositionTimeOffsets)
		if err != nil {
			return nil, err
		}
		if len(deltas) != int(n) {
			return nil, fmt.Errorf("cto list carries %d elements, expected %d: %w",
				len(deltas), n, ErrMalformed)
		}
		prevList := cf.signedLists[idTrunSampleCompositionTimeOffsets]
		out := make([]int64, len(deltas))
		for i, d := range deltas {
			var p int64
			if i < len(prevList) {
				p = prevList[i]
			}
			out[i] = p + d
		}
		cf.signedLists[idTrunSampleCompositionTimeOffsets] = out
		delete(raw, idTrunSampleCompositionTimeOffsets)
	} else if list, has := cf.signedLists[idTrunSampleCompositionTimeOffsets]; has {
		if len(list) > int(n) {
			cf.signedLists[idTrunSampleCompositionTimeOffsets] = list[:n]
		} else if len(list) < int(n) {
			return nil, fmt.Errorf("inherited cto list has %d elements, need %d: %w",
				len(list), n, ErrMalformed)
		}
	}

	// Raw bytes (IVs): overwrite.
	if bytes, ok := raw[idSencInitializationVector]; ok {
		cf.rawBlobs[idSencInitializationVector] = append([]byte(nil), bytes...)
	}

	// Derived BMDT becomes the chunk's BMDT and the next reference.
	derived, ok := prev.deriveNextBMDT(trexDefaultDur)
	if !ok {
		return nil, fmt.Errorf("cannot derive BMDT for delta chunk: %w", ErrMalformed)
	}
	cf.scalars[idTfdtBaseMediaDecodeTime] = derived

	return cf, nil
}

// expandEffective turns the represented fields plus the CMAF Header
// defaults and the mdat payload into the chunk's effective values,
// applying the sample-size derivation and its MUST-reject rules.
func expandEffective(cf *chunkFields, genBoxes []GenBox, mdat []byte, moov *mp4.MoovBox) (*EffectiveValues, error) {
	trex := moov.Mvex.Trex
	trackID := uint32(1)
	if moov.Trak != nil && moov.Trak.Tkhd != nil && moov.Trak.Tkhd.TrackID != 0 {
		trackID = moov.Trak.Tkhd.TrackID
	}

	n64, ok := cf.scalars[idTrunSampleCount]
	if !ok {
		return nil, fmt.Errorf("no trunSampleCount in chunk state: %w", ErrMalformed)
	}
	if n64 > maxSampleCount {
		return nil, fmt.Errorf("sample count %d exceeds implementation limit: %w", n64, ErrMalformed)
	}
	n := int(n64)
	bmdt, ok := cf.scalars[idTfdtBaseMediaDecodeTime]
	if !ok {
		return nil, fmt.Errorf("no BMDT in chunk state: %w", ErrMalformed)
	}
	P := uint64(len(mdat))
	if n == 0 && P != 0 {
		return nil, fmt.Errorf("zero samples with non-empty mdat payload: %w", ErrMalformed)
	}

	eff := &EffectiveValues{
		SampleCount: n,
		BMDT:        bmdt,
		GenBoxes:    genBoxes,
		MdatPayload: mdat,
	}

	eff.SampleDescriptionIndex = trex.DefaultSampleDescriptionIndex
	if v, ok := cf.scalars[idTfhdSampleDescriptionIndex]; ok {
		if v > 0xFFFFFFFF {
			return nil, fmt.Errorf("sample description index overflows 32 bits: %w", ErrMalformed)
		}
		eff.SampleDescriptionIndex = uint32(v)
	}

	scalar32 := func(id fieldID, def uint32) (uint32, error) {
		v, ok := cf.scalars[id]
		if !ok {
			return def, nil
		}
		if v > 0xFFFFFFFF {
			return 0, fmt.Errorf("scalar id %d overflows 32 bits: %w", id, ErrMalformed)
		}
		return uint32(v), nil
	}

	// Durations.
	defDur, err := scalar32(idTfhdDefaultSampleDuration, trex.DefaultSampleDuration)
	if err != nil {
		return nil, err
	}
	eff.Durations = make([]uint32, n)
	if durs, ok := cf.lists[idTrunSampleDurations]; ok {
		if len(durs) != n {
			return nil, fmt.Errorf("duration list has %d elements for %d samples: %w", len(durs), n, ErrMalformed)
		}
		for i, d := range durs {
			if d > 0xFFFFFFFF {
				return nil, fmt.Errorf("sample duration overflows 32 bits: %w", ErrMalformed)
			}
			eff.Durations[i] = uint32(d)
		}
	} else {
		for i := range eff.Durations {
			eff.Durations[i] = defDur
		}
	}

	// Sizes per the sample-size derivation.
	eff.Sizes = make([]uint32, n)
	switch {
	case cf.lists[idTrunSampleSizes] != nil:
		listed := cf.lists[idTrunSampleSizes]
		if len(listed) != n-1 {
			return nil, fmt.Errorf("size list has %d elements, expected %d: %w", len(listed), n-1, ErrMalformed)
		}
		var sum uint64
		for i, s := range listed {
			if s > 0xFFFFFFFF {
				return nil, fmt.Errorf("sample size overflows 32 bits: %w", ErrMalformed)
			}
			eff.Sizes[i] = uint32(s)
			sum += s
		}
		if sum > P {
			return nil, fmt.Errorf("listed sample sizes exceed mdat payload: %w", ErrMalformed)
		}
		last := P - sum
		if last > 0xFFFFFFFF {
			return nil, fmt.Errorf("derived last sample size overflows 32 bits: %w", ErrMalformed)
		}
		eff.Sizes[n-1] = uint32(last)
	default:
		// Derivation order: an explicit tfhd default wins; then the
		// n == 1 rule (the encoder MUST omit all size information for a
		// single sample, so its size is always P — checked before the
		// trex fallback, which could otherwise contradict P); then a
		// non-zero trex default.
		size, hasDefault := cf.scalars[idTfhdDefaultSampleSize]
		if !hasDefault && n != 1 && trex.DefaultSampleSize != 0 {
			size, hasDefault = uint64(trex.DefaultSampleSize), true
		}
		switch {
		case hasDefault:
			if size > 0xFFFFFFFF {
				return nil, fmt.Errorf("default sample size overflows 32 bits: %w", ErrMalformed)
			}
			if uint64(n)*size != P {
				return nil, fmt.Errorf("%d samples of size %d do not match mdat payload of %d bytes: %w",
					n, size, P, ErrMalformed)
			}
			for i := range eff.Sizes {
				eff.Sizes[i] = uint32(size)
			}
		case n == 1:
			if P > 0xFFFFFFFF {
				return nil, fmt.Errorf("single sample size overflows 32 bits: %w", ErrMalformed)
			}
			eff.Sizes[0] = uint32(P)
		case n > 1:
			return nil, fmt.Errorf("no sample size information for %d samples: %w", n, ErrMalformed)
		}
	}

	// Flags.
	defFlags, err := scalar32(idTfhdDefaultSampleFlags, trex.DefaultSampleFlags)
	if err != nil {
		return nil, err
	}
	firstFlags, hasFirstFlags := cf.scalars[idTrunFirstSampleFlags]
	if hasFirstFlags && firstFlags > 0xFFFFFFFF {
		return nil, fmt.Errorf("first sample flags overflow 32 bits: %w", ErrMalformed)
	}
	eff.Flags = make([]uint32, n)
	if flags, ok := cf.lists[idTrunSampleFlags]; ok {
		if len(flags) != n {
			return nil, fmt.Errorf("flags list has %d elements for %d samples: %w", len(flags), n, ErrMalformed)
		}
		for i, f := range flags {
			if f > 0xFFFFFFFF {
				return nil, fmt.Errorf("sample flags overflow 32 bits: %w", ErrMalformed)
			}
			eff.Flags[i] = uint32(f)
		}
	} else {
		for i := range eff.Flags {
			if i == 0 && hasFirstFlags {
				eff.Flags[i] = uint32(firstFlags)
			} else {
				eff.Flags[i] = defFlags
			}
		}
	}

	// Composition-time offsets.
	eff.CTOs = make([]int32, n)
	if ctos, ok := cf.signedLists[idTrunSampleCompositionTimeOffsets]; ok {
		if len(ctos) != n {
			return nil, fmt.Errorf("cto list has %d elements for %d samples: %w", len(ctos), n, ErrMalformed)
		}
		for i, c := range ctos {
			if c < -(1<<31) || c > (1<<31)-1 {
				return nil, fmt.Errorf("composition-time offset outside 32-bit range: %w", ErrMalformed)
			}
			eff.CTOs[i] = int32(c)
		}
	}

	// CENC auxiliary information.
	tenc := getTenc(moov, trackID)
	ivSize := uint64(0)
	if tenc != nil {
		ivSize = uint64(tenc.DefaultPerSampleIVSize)
	}
	if v, ok := cf.scalars[idSencPerSampleIVSize]; ok {
		ivSize = v
	}
	if ivSize > 255 {
		return nil, fmt.Errorf("per-sample IV size %d out of range: %w", ivSize, ErrMalformed)
	}
	eff.PerSampleIVSize = uint8(ivSize)
	if ivs, ok := cf.rawBlobs[idSencInitializationVector]; ok && len(ivs) > 0 {
		if ivSize == 0 {
			return nil, fmt.Errorf("IVs present with per-sample IV size 0: %w", ErrMalformed)
		}
		if len(ivs) != int(ivSize)*n {
			return nil, fmt.Errorf("IV payload is %d bytes for %d samples of %d: %w",
				len(ivs), n, ivSize, ErrMalformed)
		}
		eff.IVs = ivs
	} else if ivSize > 0 && n > 0 {
		return nil, fmt.Errorf("per-sample IV size %d but no IVs: %w", ivSize, ErrMalformed)
	}

	if counts, ok := cf.lists[idSencSubsampleCount]; ok {
		if len(counts) != n {
			return nil, fmt.Errorf("subsample count list has %d elements for %d samples: %w",
				len(counts), n, ErrMalformed)
		}
		eff.HasSubsamples = true
		eff.SubsampleCounts = make([]uint16, n)
		total := 0
		for i, c := range counts {
			if c > 0xFFFF {
				return nil, fmt.Errorf("subsample count overflows 16 bits: %w", ErrMalformed)
			}
			eff.SubsampleCounts[i] = uint16(c)
			total += int(c)
		}
		clear := cf.lists[idSencBytesOfClearData]
		prot := cf.lists[idSencBytesOfProtectedData]
		if len(clear) != total || len(prot) != total {
			return nil, fmt.Errorf("subsample byte lists (%d, %d) do not match total count %d: %w",
				len(clear), len(prot), total, ErrMalformed)
		}
		eff.ClearBytes = make([]uint16, total)
		eff.ProtectedBytes = make([]uint32, total)
		for i := range clear {
			if clear[i] > 0xFFFF {
				return nil, fmt.Errorf("bytes of clear data overflow 16 bits: %w", ErrMalformed)
			}
			if prot[i] > 0xFFFFFFFF {
				return nil, fmt.Errorf("bytes of protected data overflow 32 bits: %w", ErrMalformed)
			}
			eff.ClearBytes[i] = uint16(clear[i])
			eff.ProtectedBytes[i] = uint32(prot[i])
		}
	} else if cf.lists[idSencBytesOfClearData] != nil || cf.lists[idSencBytesOfProtectedData] != nil {
		return nil, fmt.Errorf("subsample byte lists without subsample counts: %w", ErrMalformed)
	}

	return eff, nil
}
