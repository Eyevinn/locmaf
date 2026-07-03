package locmaf

import "errors"

// Version is the LOCMAF wire-format version implemented by this package,
// announced in the CMSF catalog Track entry as locmafVersion.
const Version = "0.3"

// Element types. Each element of a LOCMAF Object payload begins with an
// element_type vi64; the mdat payload that follows the header element is
// untagged. A rawBoxes element is the sole element of its Object. An
// unknown element type is not self-delimiting and rejects the whole
// Object.
const (
	ElementTypeGenBox      uint64 = 1
	ElementTypeFullHeader  uint64 = 2
	ElementTypeDeltaHeader uint64 = 3
	ElementTypeRawBoxes    uint64 = 4
)

// Sentinel errors. Wire-level violations that a receiver MUST reject
// wrap ErrMalformed; source content that cannot be expressed in LOCMAF
// wraps ErrBadSource.
var (
	ErrMalformed = errors.New("locmaf: malformed object")
	ErrBadSource = errors.New("locmaf: source not expressible in LOCMAF")
)

// fieldID enumerates the (field_id, value) tuple identifiers carried
// inside a full or delta locmafHeader. Parity carries the framing: even
// IDs are single vi64 scalars, odd IDs are length-prefixed bytes.
type fieldID uint64

const (
	// Scalar fields (even IDs).
	idTfhdSampleDescriptionIndex fieldID = 2
	idTfhdDefaultSampleDuration  fieldID = 4
	idTfhdDefaultSampleSize      fieldID = 6
	idTfhdDefaultSampleFlags     fieldID = 8
	idTfdtBaseMediaDecodeTime    fieldID = 10 // full-header-only
	idTrunFirstSampleFlags       fieldID = 12
	idTrunSampleCount            fieldID = 14
	idSencPerSampleIVSize        fieldID = 16

	// List / raw fields (odd IDs).
	idTrunSampleSizes                  fieldID = 1
	idTrunSampleDurations              fieldID = 3
	idTrunSampleCompositionTimeOffsets fieldID = 5 // signed: zigzag in both contexts
	idTrunSampleFlags                  fieldID = 7
	idSencInitializationVector         fieldID = 9 // raw bytes in both contexts
	idSencSubsampleCount               fieldID = 11
	idSencBytesOfClearData             fieldID = 13
	idSencBytesOfProtectedData         fieldID = 15

	// Delta-only control field: plain unsigned vi64 list.
	idDeltaDeletedLocmafIDs fieldID = 27
)

// isList reports whether the wire framing for this ID uses the
// odd-parity length-prefixed form.
func (id fieldID) isList() bool { return uint64(id)%2 == 1 }

// isKnownScalar reports whether id is a defined even-parity field.
func isKnownScalar(id fieldID) bool {
	switch id {
	case idTfhdSampleDescriptionIndex, idTfhdDefaultSampleDuration,
		idTfhdDefaultSampleSize, idTfhdDefaultSampleFlags,
		idTfdtBaseMediaDecodeTime, idTrunFirstSampleFlags,
		idTrunSampleCount, idSencPerSampleIVSize:
		return true
	}
	return false
}

// isKnownVarintList reports whether id is a defined unsigned-vi64-list
// field (odd parity, excluding the signed list 5, the raw-bytes field 9,
// and the control field 27).
func isKnownVarintList(id fieldID) bool {
	switch id {
	case idTrunSampleSizes, idTrunSampleDurations, idTrunSampleFlags,
		idSencSubsampleCount, idSencBytesOfClearData, idSencBytesOfProtectedData:
		return true
	}
	return false
}

// GenBox is one generic pre-moof box carried verbatim: the ISO box type
// FourCC and the box contents without the 8-byte ISO box header. For a
// uuid box the 16-byte usertype is the first 16 bytes of Payload.
type GenBox struct {
	Name    string
	Payload []byte
}

// EffectiveValues is a decoded chunk's meaning: the per-sample vectors
// after applying deltas, deletions, and the sample-size and BMDT
// derivations. It is the only chunk-derived input to
// ReconstructCanonical; the remaining inputs come from the CMAF Header.
type EffectiveValues struct {
	SampleCount            int
	BMDT                   uint64
	SampleDescriptionIndex uint32

	// Per-sample vectors, each SampleCount long.
	Durations []uint32
	Sizes     []uint32
	Flags     []uint32
	CTOs      []int32

	// CENC per-sample auxiliary information. IVs is the concatenation
	// of per-sample initialization vectors, PerSampleIVSize bytes each.
	// HasSubsamples reports whether the effective subsample map is
	// present; when true, SubsampleCounts has SampleCount entries and
	// ClearBytes/ProtectedBytes are flattened in chunk order.
	PerSampleIVSize uint8
	IVs             []byte
	HasSubsamples   bool
	SubsampleCounts []uint16
	ClearBytes      []uint16
	ProtectedBytes  []uint32

	// GenBoxes render before the moof, in order. MdatPayload is the raw
	// sample data (a subslice of the decoded object payload).
	GenBoxes    []GenBox
	MdatPayload []byte
}

// State carries the per-group in-group reference that both encoder and
// decoder consult for delta chunks. Callers keep one State per MOQT
// group and Reset it (or use a fresh one) when a new group begins. A
// full header resets it implicitly on both sides.
type State struct {
	scalars     map[fieldID]uint64
	lists       map[fieldID][]uint64
	signedLists map[fieldID][]int64
	rawBlobs    map[fieldID][]byte
	hasAny      bool
}

// NewState returns an empty State.
func NewState() *State {
	s := &State{}
	s.Reset()
	return s
}

// Reset empties the State so the next chunk is encoded as (or must be)
// a full header, e.g. at the start of a new MOQT group.
func (s *State) Reset() {
	s.scalars = make(map[fieldID]uint64)
	s.lists = make(map[fieldID][]uint64)
	s.signedLists = make(map[fieldID][]int64)
	s.rawBlobs = make(map[fieldID][]byte)
	s.hasAny = false
}

// chunkFields is the per-field represented content of one chunk: the
// same shape the State stores. Both encoder and decoder produce it —
// the encoder from the emission rules, the decoder from the wire — so
// the two sides keep identical in-group references.
type chunkFields struct {
	scalars     map[fieldID]uint64
	lists       map[fieldID][]uint64
	signedLists map[fieldID][]int64
	rawBlobs    map[fieldID][]byte
}

func newChunkFields() *chunkFields {
	return &chunkFields{
		scalars:     make(map[fieldID]uint64),
		lists:       make(map[fieldID][]uint64),
		signedLists: make(map[fieldID][]int64),
		rawBlobs:    make(map[fieldID][]byte),
	}
}

// store replaces the State content with cf.
func (s *State) store(cf *chunkFields) {
	s.scalars = make(map[fieldID]uint64, len(cf.scalars))
	for k, v := range cf.scalars {
		s.scalars[k] = v
	}
	s.lists = make(map[fieldID][]uint64, len(cf.lists))
	for k, v := range cf.lists {
		s.lists[k] = append([]uint64(nil), v...)
	}
	s.signedLists = make(map[fieldID][]int64, len(cf.signedLists))
	for k, v := range cf.signedLists {
		s.signedLists[k] = append([]int64(nil), v...)
	}
	s.rawBlobs = make(map[fieldID][]byte, len(cf.rawBlobs))
	for k, v := range cf.rawBlobs {
		s.rawBlobs[k] = append([]byte(nil), v...)
	}
	s.hasAny = true
}

// snapshot returns a chunkFields copy of the State — the starting point
// for applying a delta chunk.
func (s *State) snapshot() *chunkFields {
	cf := newChunkFields()
	for k, v := range s.scalars {
		cf.scalars[k] = v
	}
	for k, v := range s.lists {
		cf.lists[k] = append([]uint64(nil), v...)
	}
	for k, v := range s.signedLists {
		cf.signedLists[k] = append([]int64(nil), v...)
	}
	for k, v := range s.rawBlobs {
		cf.rawBlobs[k] = append([]byte(nil), v...)
	}
	return cf
}

// deriveNextBMDT computes the BMDT a delta chunk would have: the
// previous chunk's BMDT plus the sum of its effective sample durations.
// The second return is false when the state lacks what the derivation
// needs (no previous chunk, or no duration information).
func (s *State) deriveNextBMDT(trexDefaultSampleDuration uint32) (uint64, bool) {
	bmdt, ok := s.scalars[idTfdtBaseMediaDecodeTime]
	if !ok {
		return 0, false
	}
	n, ok := s.scalars[idTrunSampleCount]
	if !ok {
		return 0, false
	}
	var total uint64
	if durs, ok := s.lists[idTrunSampleDurations]; ok {
		if uint64(len(durs)) != n {
			return 0, false
		}
		for _, d := range durs {
			total += d
		}
	} else {
		def, ok := s.scalars[idTfhdDefaultSampleDuration]
		if !ok {
			def = uint64(trexDefaultSampleDuration)
		}
		total = def * n
	}
	if bmdt > ^uint64(0)-total {
		return 0, false
	}
	return bmdt + total, true
}
