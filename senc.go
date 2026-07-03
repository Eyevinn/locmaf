package locmaf

import (
	"fmt"

	"github.com/Eyevinn/mp4ff/mp4"
)

// parseSenc returns the senc box of the moof's traf (parsing it on
// demand if it is still raw) plus its per-sample IV size.
func parseSenc(moof *mp4.MoofBox, moov *mp4.MoovBox) (*mp4.SencBox, uint8, error) {
	if moof == nil || moof.Traf == nil {
		return nil, 0, fmt.Errorf("moof or traf not defined: %w", ErrBadSource)
	}
	traf := moof.Traf
	ok, parsed := traf.ContainsSencBox()
	if !ok {
		return nil, 0, nil
	}
	defaultIVSize := defaultPerSampleIVSize(moov, traf.Tfhd.TrackID)
	if !parsed {
		if err := traf.ParseReadSenc(defaultIVSize, moof.StartPos); err != nil {
			return nil, 0, fmt.Errorf("parse senc: %w", err)
		}
	}
	senc := traf.Senc
	if senc == nil && traf.UUIDSenc != nil {
		senc = traf.UUIDSenc.Senc
	}
	if senc == nil {
		return nil, 0, nil
	}
	return senc, senc.PerSampleIVSize(), nil
}

// safeGetSinf finds the sinf box of the given track without panicking
// on a moov with no stsd children (synthetic test moovs).
func safeGetSinf(moov *mp4.MoovBox, trackID uint32) *mp4.SinfBox {
	if moov == nil {
		return nil
	}
	for _, trak := range moov.Traks {
		if trak == nil || trak.Tkhd == nil || trak.Mdia == nil ||
			trak.Mdia.Minf == nil || trak.Mdia.Minf.Stbl == nil ||
			trak.Mdia.Minf.Stbl.Stsd == nil {
			continue
		}
		stsd := trak.Mdia.Minf.Stbl.Stsd
		if len(stsd.Children) == 0 {
			continue
		}
		if trak.Tkhd.TrackID != trackID {
			continue
		}
		switch sd := stsd.Children[0].(type) {
		case *mp4.VisualSampleEntryBox:
			return sd.Sinf
		case *mp4.AudioSampleEntryBox:
			return sd.Sinf
		}
	}
	return nil
}

// defaultPerSampleIVSize returns tenc.default_Per_Sample_IV_Size for
// the given track, or 0 when the moov carries no encryption metadata.
func defaultPerSampleIVSize(moov *mp4.MoovBox, trackID uint32) uint8 {
	tenc := getTenc(moov, trackID)
	if tenc == nil {
		return 0
	}
	return tenc.DefaultPerSampleIVSize
}

// getTenc returns the track's tenc box, or nil.
func getTenc(moov *mp4.MoovBox, trackID uint32) *mp4.TencBox {
	sinf := safeGetSinf(moov, trackID)
	if sinf == nil || sinf.Schi == nil {
		return nil
	}
	return sinf.Schi.Tenc
}
