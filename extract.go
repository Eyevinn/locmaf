package locmaf

import (
	"fmt"

	"github.com/Eyevinn/mp4ff/mp4"
)

// sourceValues holds the effective values derived from a source CMAF
// moof: the per-sample trun value when present, else the tfhd default
// when present, else the trex default (composition-time offsets default
// to 0). Emission works from these, so two decode-equivalent source
// moofs yield the same emitted fields.
type sourceValues struct {
	n     int
	bmdt  uint64
	sdix  uint32
	durs  []uint32
	sizes []uint32
	flags []uint32
	ctos  []int32

	sencPresent     bool
	perSampleIVSize uint8
	tencIVSize      uint8
	ivs             []byte
	hasSubsamples   bool
	subCounts       []uint64
	clearBytes      []uint64
	protBytes       []uint64
}

// extractSourceValues resolves a source moof against the CMAF Header's
// trex and tenc defaults into effective per-sample vectors.
func extractSourceValues(moof *mp4.MoofBox, moov *mp4.MoovBox) (*sourceValues, error) {
	if moof == nil || moof.Traf == nil {
		return nil, fmt.Errorf("moof or traf not defined: %w", ErrBadSource)
	}
	if moov == nil || moov.Mvex == nil || moov.Mvex.Trex == nil {
		return nil, fmt.Errorf("moov or trex not defined: %w", ErrBadSource)
	}
	traf := moof.Traf
	trun, tfhd, tfdt := traf.Trun, traf.Tfhd, traf.Tfdt
	if trun == nil || tfhd == nil || tfdt == nil {
		return nil, fmt.Errorf("traf lacks trun, tfhd, or tfdt: %w", ErrBadSource)
	}
	trex := moov.Mvex.Trex

	sv := &sourceValues{
		n:    len(trun.Samples),
		bmdt: tfdt.BaseMediaDecodeTime(),
	}

	sv.sdix = trex.DefaultSampleDescriptionIndex
	if tfhd.HasSampleDescriptionIndex() {
		sv.sdix = tfhd.SampleDescriptionIndex
	}

	defaultDur := trex.DefaultSampleDuration
	if tfhd.HasDefaultSampleDuration() {
		defaultDur = tfhd.DefaultSampleDuration
	}
	defaultSize := trex.DefaultSampleSize
	if tfhd.HasDefaultSampleSize() {
		defaultSize = tfhd.DefaultSampleSize
	}
	defaultFlags := trex.DefaultSampleFlags
	if tfhd.HasDefaultSampleFlags() {
		defaultFlags = tfhd.DefaultSampleFlags
	}
	firstFlags, hasFirstFlags := trun.FirstSampleFlags()

	sv.durs = make([]uint32, sv.n)
	sv.sizes = make([]uint32, sv.n)
	sv.flags = make([]uint32, sv.n)
	sv.ctos = make([]int32, sv.n)
	for i, s := range trun.Samples {
		if trun.HasSampleDuration() {
			sv.durs[i] = s.Dur
		} else {
			sv.durs[i] = defaultDur
		}
		if trun.HasSampleSize() {
			sv.sizes[i] = s.Size
		} else {
			sv.sizes[i] = defaultSize
		}
		switch {
		case trun.HasSampleFlags():
			sv.flags[i] = s.Flags
		case i == 0 && hasFirstFlags:
			sv.flags[i] = firstFlags
		default:
			sv.flags[i] = defaultFlags
		}
		if trun.HasSampleCompositionTimeOffset() {
			sv.ctos[i] = s.CompositionTimeOffset
		}
	}

	senc, ivSize, err := parseSenc(moof, moov)
	if err != nil {
		return nil, err
	}
	sv.tencIVSize = defaultPerSampleIVSize(moov, tfhd.TrackID)
	if senc != nil {
		sv.sencPresent = true
		sv.perSampleIVSize = ivSize
		if ivSize > 0 && len(senc.IVs) > 0 {
			if len(senc.IVs) != sv.n {
				return nil, fmt.Errorf("senc has %d IVs for %d samples: %w", len(senc.IVs), sv.n, ErrBadSource)
			}
			sv.ivs = make([]byte, 0, int(ivSize)*sv.n)
			for _, iv := range senc.IVs {
				if len(iv) != int(ivSize) {
					return nil, fmt.Errorf("senc IV length %d != per_sample_IV_size %d: %w", len(iv), ivSize, ErrBadSource)
				}
				sv.ivs = append(sv.ivs, iv...)
			}
		}
		if len(senc.SubSamples) > 0 {
			if len(senc.SubSamples) != sv.n {
				return nil, fmt.Errorf("senc has %d subsample entries for %d samples: %w",
					len(senc.SubSamples), sv.n, ErrBadSource)
			}
			sv.hasSubsamples = true
			for _, subs := range senc.SubSamples {
				sv.subCounts = append(sv.subCounts, uint64(len(subs)))
				for _, ss := range subs {
					sv.clearBytes = append(sv.clearBytes, uint64(ss.BytesOfClearData))
					sv.protBytes = append(sv.protBytes, uint64(ss.BytesOfProtectedData))
				}
			}
		}
	}

	return sv, nil
}

func allEqualU32(v []uint32) bool {
	for i := 1; i < len(v); i++ {
		if v[i] != v[0] {
			return false
		}
	}
	return true
}

// equalExceptFirst reports whether v[1:] are all equal to each other
// and v[0] differs from them. Requires len(v) > 1.
func equalExceptFirst(v []uint32) bool {
	if len(v) < 2 {
		return false
	}
	for i := 2; i < len(v); i++ {
		if v[i] != v[1] {
			return false
		}
	}
	return v[0] != v[1]
}

// emitFields applies the full-chunk emission rules to the source's
// effective values, producing the represented per-field content that
// goes on the wire (absolutely in a full header, or as deltas against
// the in-group reference in a delta header) and into the State.
func emitFields(sv *sourceValues, moov *mp4.MoovBox) (*chunkFields, error) {
	trex := moov.Mvex.Trex
	cf := newChunkFields()

	cf.scalars[idTrunSampleCount] = uint64(sv.n)
	cf.scalars[idTfdtBaseMediaDecodeTime] = sv.bmdt

	if sv.sdix != trex.DefaultSampleDescriptionIndex {
		cf.scalars[idTfhdSampleDescriptionIndex] = uint64(sv.sdix)
	}

	// Durations: per-sample list when varying, else a default when it
	// differs from trex.
	if sv.n > 0 {
		if !allEqualU32(sv.durs) {
			list := make([]uint64, sv.n)
			for i, d := range sv.durs {
				list[i] = uint64(d)
			}
			cf.lists[idTrunSampleDurations] = list
		} else if sv.durs[0] != trex.DefaultSampleDuration {
			cf.scalars[idTfhdDefaultSampleDuration] = uint64(sv.durs[0])
		}
	}

	// Sizes per the sample-size derivation rules: nothing when n == 1;
	// a default for uniform sizes (when != trex); n-1 listed sizes when
	// varying.
	if sv.n > 1 {
		if allEqualU32(sv.sizes) {
			if sv.sizes[0] != trex.DefaultSampleSize {
				cf.scalars[idTfhdDefaultSampleSize] = uint64(sv.sizes[0])
			}
		} else {
			list := make([]uint64, sv.n-1)
			for i := 0; i < sv.n-1; i++ {
				list[i] = uint64(sv.sizes[i])
			}
			cf.lists[idTrunSampleSizes] = list
		}
	}

	// Flags: all equal -> default (when != trex); equal except the
	// first (n > 1) -> firstSampleFlags plus a default for the rest
	// (when != trex); otherwise the full per-sample list.
	if sv.n > 0 {
		switch {
		case allEqualU32(sv.flags):
			if sv.flags[0] != trex.DefaultSampleFlags {
				cf.scalars[idTfhdDefaultSampleFlags] = uint64(sv.flags[0])
			}
		case equalExceptFirst(sv.flags):
			cf.scalars[idTrunFirstSampleFlags] = uint64(sv.flags[0])
			if sv.flags[1] != trex.DefaultSampleFlags {
				cf.scalars[idTfhdDefaultSampleFlags] = uint64(sv.flags[1])
			}
		default:
			list := make([]uint64, sv.n)
			for i, f := range sv.flags {
				list[i] = uint64(f)
			}
			cf.lists[idTrunSampleFlags] = list
		}
	}

	// Composition-time offsets: emitted iff any is non-zero.
	anyCTO := false
	for _, c := range sv.ctos {
		if c != 0 {
			anyCTO = true
			break
		}
	}
	if anyCTO {
		list := make([]int64, sv.n)
		for i, c := range sv.ctos {
			list[i] = int64(c)
		}
		cf.signedLists[idTrunSampleCompositionTimeOffsets] = list
	}

	// CENC fields.
	if sv.sencPresent {
		if sv.perSampleIVSize != sv.tencIVSize {
			cf.scalars[idSencPerSampleIVSize] = uint64(sv.perSampleIVSize)
		}
		if sv.perSampleIVSize > 0 {
			if len(sv.ivs) != int(sv.perSampleIVSize)*sv.n {
				return nil, fmt.Errorf("IV payload %d bytes for %d samples of %d: %w",
					len(sv.ivs), sv.n, sv.perSampleIVSize, ErrBadSource)
			}
			cf.rawBlobs[idSencInitializationVector] = append([]byte(nil), sv.ivs...)
		}
		if sv.hasSubsamples {
			cf.lists[idSencSubsampleCount] = append([]uint64(nil), sv.subCounts...)
			cf.lists[idSencBytesOfClearData] = append([]uint64(nil), sv.clearBytes...)
			cf.lists[idSencBytesOfProtectedData] = append([]uint64(nil), sv.protBytes...)
		}
	}

	return cf, nil
}
