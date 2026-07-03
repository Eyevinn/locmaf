package locmaf

import (
	"encoding/binary"
	"fmt"

	"github.com/Eyevinn/mp4ff/mp4"
)

// tfhd tf_flags bits.
const (
	tfhdBaseDataOffsetPresent  = 0x000001
	tfhdSampleDescIndexPresent = 0x000002
	tfhdDefaultDurationPresent = 0x000008
	tfhdDefaultSizePresent     = 0x000010
	tfhdDefaultFlagsPresent    = 0x000020
	tfhdDefaultBaseIsMoof      = 0x020000
)

// trun tr_flags bits.
const (
	trunDataOffsetPresent       = 0x000001
	trunFirstSampleFlagsPresent = 0x000004
	trunSampleDurationPresent   = 0x000100
	trunSampleSizePresent       = 0x000200
	trunSampleFlagsPresent      = 0x000400
	trunSampleCTOPresent        = 0x000800
)

// canonicalLayout captures every presence decision of the canonical
// reconstruction, derived from the effective values and the trex
// defaults alone — wire presence plays no part.
type canonicalLayout struct {
	n int

	sdixPresent     bool
	defDurPresent   bool
	defDur          uint32
	defSizePresent  bool
	defSize         uint32
	defFlagsPresent bool
	defFlags        uint32

	fsfPresent   bool
	durPresent   bool
	sizePresent  bool
	flagsPresent bool
	ctoPresent   bool
	trunVersion  byte

	cenc     bool
	auxSizes []uint32 // per-sample aux_info sizes
	auxEqual bool
}

func deriveLayout(eff *EffectiveValues, trex *mp4.TrexBox) (*canonicalLayout, error) {
	n := eff.SampleCount
	l := &canonicalLayout{n: n}

	l.sdixPresent = eff.SampleDescriptionIndex != trex.DefaultSampleDescriptionIndex

	if n > 0 {
		if allEqualU32(eff.Durations) {
			if eff.Durations[0] != trex.DefaultSampleDuration {
				l.defDurPresent = true
				l.defDur = eff.Durations[0]
			}
		} else {
			l.durPresent = true
		}

		// Uniform sizes (n == 1 is trivially uniform) ride as a tfhd
		// default when they differ from trex. The wire omits a single
		// sample's size, but the canonical CMAF chunk must still carry
		// it: ISO BMFF has no rule deriving a sample size from the
		// mdat length.
		if allEqualU32(eff.Sizes) {
			if eff.Sizes[0] != trex.DefaultSampleSize {
				l.defSizePresent = true
				l.defSize = eff.Sizes[0]
			}
		} else {
			l.sizePresent = true
		}

		switch {
		case allEqualU32(eff.Flags):
			if eff.Flags[0] != trex.DefaultSampleFlags {
				l.defFlagsPresent = true
				l.defFlags = eff.Flags[0]
			}
		case equalExceptFirst(eff.Flags):
			l.fsfPresent = true
			if eff.Flags[1] != trex.DefaultSampleFlags {
				l.defFlagsPresent = true
				l.defFlags = eff.Flags[1]
			}
		default:
			l.flagsPresent = true
		}

		for _, c := range eff.CTOs {
			if c != 0 {
				l.ctoPresent = true
			}
			if c < 0 {
				l.trunVersion = 1
			}
		}
	}

	if eff.PerSampleIVSize > 0 || eff.HasSubsamples {
		l.cenc = true
		l.auxSizes = make([]uint32, n)
		l.auxEqual = true
		for i := 0; i < n; i++ {
			size := uint32(eff.PerSampleIVSize)
			if eff.HasSubsamples {
				size += 2 + 6*uint32(eff.SubsampleCounts[i])
			}
			if size > 255 {
				return nil, fmt.Errorf("aux_info size %d exceeds 8-bit saiz limit: %w", size, ErrMalformed)
			}
			l.auxSizes[i] = size
			if size != l.auxSizes[0] {
				l.auxEqual = false
			}
		}
	}

	return l, nil
}

func (l *canonicalLayout) tfhdSize() int {
	k := 0
	for _, present := range []bool{l.sdixPresent, l.defDurPresent, l.defSizePresent, l.defFlagsPresent} {
		if present {
			k++
		}
	}
	return 16 + 4*k
}

func (l *canonicalLayout) trunSize() int {
	perSample := 0
	for _, present := range []bool{l.durPresent, l.sizePresent, l.flagsPresent, l.ctoPresent} {
		if present {
			perSample += 4
		}
	}
	size := 8 + 4 + 4 + 4 // header, version+flags, sample_count, data_offset
	if l.fsfPresent {
		size += 4
	}
	return size + l.n*perSample
}

func (l *canonicalLayout) sencSize(eff *EffectiveValues) int {
	size := 8 + 4 + 4 // header, version+flags, sample_count
	size += l.n * int(eff.PerSampleIVSize)
	if eff.HasSubsamples {
		for _, c := range eff.SubsampleCounts {
			size += 2 + 6*int(c)
		}
	}
	return size
}

func (l *canonicalLayout) saizSize() int {
	size := 8 + 4 + 1 + 4
	if !l.auxEqual {
		size += l.n
	}
	return size
}

// validateEffective checks the internal consistency of caller-supplied
// effective values, so the reconstruction below cannot index past a
// vector. Values produced by Decode always pass.
func validateEffective(eff *EffectiveValues) error {
	n := eff.SampleCount
	if n < 0 {
		return fmt.Errorf("negative sample count: %w", ErrMalformed)
	}
	if len(eff.Durations) != n || len(eff.Sizes) != n || len(eff.Flags) != n || len(eff.CTOs) != n {
		return fmt.Errorf("per-sample vectors (%d, %d, %d, %d) do not match sample count %d: %w",
			len(eff.Durations), len(eff.Sizes), len(eff.Flags), len(eff.CTOs), n, ErrMalformed)
	}
	var sizeSum uint64
	for _, s := range eff.Sizes {
		sizeSum += uint64(s)
	}
	if sizeSum != uint64(len(eff.MdatPayload)) {
		return fmt.Errorf("sample sizes sum to %d but the mdat payload is %d bytes: %w",
			sizeSum, len(eff.MdatPayload), ErrMalformed)
	}
	if want := int(eff.PerSampleIVSize) * n; len(eff.IVs) != want {
		return fmt.Errorf("IV payload is %d bytes, expected %d: %w", len(eff.IVs), want, ErrMalformed)
	}
	if eff.HasSubsamples {
		if len(eff.SubsampleCounts) != n {
			return fmt.Errorf("subsample count vector has %d entries for %d samples: %w",
				len(eff.SubsampleCounts), n, ErrMalformed)
		}
		total := 0
		for _, c := range eff.SubsampleCounts {
			total += int(c)
		}
		if len(eff.ClearBytes) != total || len(eff.ProtectedBytes) != total {
			return fmt.Errorf("subsample byte vectors (%d, %d) do not match total count %d: %w",
				len(eff.ClearBytes), len(eff.ProtectedBytes), total, ErrMalformed)
		}
	} else if len(eff.SubsampleCounts) != 0 || len(eff.ClearBytes) != 0 || len(eff.ProtectedBytes) != 0 {
		return fmt.Errorf("subsample vectors present without HasSubsamples: %w", ErrMalformed)
	}
	return nil
}

// ReconstructCanonical builds the canonical CMAF chunk bytes — each
// genBox wrapped as an ISO box, the moof, and the mdat — from the
// chunk's effective values and the CMAF Header's moov.
func ReconstructCanonical(moov *mp4.MoovBox, eff *EffectiveValues) ([]byte, error) {
	if moov == nil || moov.Mvex == nil || moov.Mvex.Trex == nil {
		return nil, fmt.Errorf("moov or trex not defined: %w", ErrMalformed)
	}
	if err := validateEffective(eff); err != nil {
		return nil, err
	}
	if uint64(len(eff.MdatPayload)) > 0xFFFFFFF7 {
		return nil, fmt.Errorf("mdat payload exceeds 32-bit box size: %w", ErrMalformed)
	}
	trex := moov.Mvex.Trex
	trackID := uint32(1)
	if moov.Trak != nil && moov.Trak.Tkhd != nil && moov.Trak.Tkhd.TrackID != 0 {
		trackID = moov.Trak.Tkhd.TrackID
	}

	l, err := deriveLayout(eff, trex)
	if err != nil {
		return nil, err
	}

	// Box sizes, bottom-up, so data_offset and saio.offset are known
	// before any byte is written.
	const mfhdSize = 16
	const tfdtSize = 20
	tfhdSize := l.tfhdSize()
	trunSize := l.trunSize()
	trafSize := 8 + tfhdSize + tfdtSize + trunSize
	sencOffsetInMoof := 0
	if l.cenc {
		saizSize := l.saizSize()
		const saioSize = 20
		sencOffsetInMoof = 8 + mfhdSize + 8 + tfhdSize + tfdtSize + trunSize + saizSize + saioSize
		trafSize += saizSize + saioSize + l.sencSize(eff)
	}
	moofSize := 8 + mfhdSize + trafSize
	// data_offset is a signed 32-bit field; a moof anywhere near that
	// bound is far outside any real chunk.
	if uint64(moofSize)+8 > 0x7FFFFFFF {
		return nil, fmt.Errorf("moof size %d overflows trun.data_offset: %w", moofSize, ErrMalformed)
	}

	total := moofSize + 8 + len(eff.MdatPayload)
	for _, gb := range eff.GenBoxes {
		total += 8 + len(gb.Payload)
	}
	out := make([]byte, 0, total)

	// genBoxes, wrapped byte-for-byte.
	for _, gb := range eff.GenBoxes {
		if len(gb.Name) != 4 {
			return nil, fmt.Errorf("genBox name %q is not a FourCC: %w", gb.Name, ErrMalformed)
		}
		boxLen := uint64(8 + len(gb.Payload))
		if boxLen > 0xFFFFFFFF {
			return nil, fmt.Errorf("genBox %q exceeds 32-bit box size: %w", gb.Name, ErrMalformed)
		}
		out = appendBoxHeader(out, uint32(boxLen), gb.Name)
		out = append(out, gb.Payload...)
	}

	// moof
	out = appendBoxHeader(out, uint32(moofSize), "moof")

	// mfhd: version 0, flags 0, sequence_number 0.
	out = appendBoxHeader(out, mfhdSize, "mfhd")
	out = appendU32(out, 0)
	out = appendU32(out, 0)

	// traf
	out = appendBoxHeader(out, uint32(trafSize), "traf")

	// tfhd
	tfFlags := uint32(tfhdDefaultBaseIsMoof)
	if l.sdixPresent {
		tfFlags |= tfhdSampleDescIndexPresent
	}
	if l.defDurPresent {
		tfFlags |= tfhdDefaultDurationPresent
	}
	if l.defSizePresent {
		tfFlags |= tfhdDefaultSizePresent
	}
	if l.defFlagsPresent {
		tfFlags |= tfhdDefaultFlagsPresent
	}
	out = appendBoxHeader(out, uint32(tfhdSize), "tfhd")
	out = appendU32(out, tfFlags) // version 0 in the top byte
	out = appendU32(out, trackID)
	if l.sdixPresent {
		out = appendU32(out, eff.SampleDescriptionIndex)
	}
	if l.defDurPresent {
		out = appendU32(out, l.defDur)
	}
	if l.defSizePresent {
		out = appendU32(out, l.defSize)
	}
	if l.defFlagsPresent {
		out = appendU32(out, l.defFlags)
	}

	// tfdt: version 1, 64-bit BMDT.
	out = appendBoxHeader(out, tfdtSize, "tfdt")
	out = appendU32(out, 1<<24)
	out = appendU64(out, eff.BMDT)

	// trun
	trFlags := uint32(trunDataOffsetPresent)
	if l.fsfPresent {
		trFlags |= trunFirstSampleFlagsPresent
	}
	if l.durPresent {
		trFlags |= trunSampleDurationPresent
	}
	if l.sizePresent {
		trFlags |= trunSampleSizePresent
	}
	if l.flagsPresent {
		trFlags |= trunSampleFlagsPresent
	}
	if l.ctoPresent {
		trFlags |= trunSampleCTOPresent
	}
	out = appendBoxHeader(out, uint32(trunSize), "trun")
	out = appendU32(out, uint32(l.trunVersion)<<24|trFlags)
	out = appendU32(out, uint32(l.n))
	out = appendU32(out, uint32(moofSize+8)) // data_offset
	if l.fsfPresent {
		out = appendU32(out, eff.Flags[0])
	}
	for i := 0; i < l.n; i++ {
		if l.durPresent {
			out = appendU32(out, eff.Durations[i])
		}
		if l.sizePresent {
			out = appendU32(out, eff.Sizes[i])
		}
		if l.flagsPresent {
			out = appendU32(out, eff.Flags[i])
		}
		if l.ctoPresent {
			out = appendU32(out, uint32(eff.CTOs[i]))
		}
	}

	if l.cenc {
		// saiz: default aux size or per-sample array.
		out = appendBoxHeader(out, uint32(l.saizSize()), "saiz")
		out = appendU32(out, 0)
		if l.auxEqual {
			var def uint32
			if l.n > 0 {
				def = l.auxSizes[0]
			}
			out = append(out, byte(def))
			out = appendU32(out, uint32(l.n))
		} else {
			out = append(out, 0)
			out = appendU32(out, uint32(l.n))
			for _, s := range l.auxSizes {
				out = append(out, byte(s))
			}
		}

		// saio: one offset to the first byte of aux info in senc.
		out = appendBoxHeader(out, 20, "saio")
		out = appendU32(out, 0)
		out = appendU32(out, 1)
		out = appendU32(out, uint32(sencOffsetInMoof+16))

		// senc
		var sencFlags uint32
		if eff.HasSubsamples {
			sencFlags = 0x000002
		}
		out = appendBoxHeader(out, uint32(l.sencSize(eff)), "senc")
		out = appendU32(out, sencFlags)
		out = appendU32(out, uint32(l.n))
		ivSize := int(eff.PerSampleIVSize)
		subIdx := 0
		for i := 0; i < l.n; i++ {
			if ivSize > 0 {
				out = append(out, eff.IVs[i*ivSize:(i+1)*ivSize]...)
			}
			if eff.HasSubsamples {
				cnt := int(eff.SubsampleCounts[i])
				out = appendU16(out, uint16(cnt))
				for j := 0; j < cnt; j++ {
					out = appendU16(out, eff.ClearBytes[subIdx])
					out = appendU32(out, eff.ProtectedBytes[subIdx])
					subIdx++
				}
			}
		}
	}

	// mdat
	out = appendBoxHeader(out, uint32(8+len(eff.MdatPayload)), "mdat")
	out = append(out, eff.MdatPayload...)

	return out, nil
}

func appendBoxHeader(b []byte, size uint32, name string) []byte {
	b = appendU32(b, size)
	return append(b, name...)
}

func appendU16(b []byte, v uint16) []byte {
	return binary.BigEndian.AppendUint16(b, v)
}

func appendU32(b []byte, v uint32) []byte {
	return binary.BigEndian.AppendUint32(b, v)
}

func appendU64(b []byte, v uint64) []byte {
	return binary.BigEndian.AppendUint64(b, v)
}
