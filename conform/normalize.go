package conform

import (
	"bytes"
	"fmt"

	"github.com/Eyevinn/mp4ff/mp4"
)

// describeNormalizations explains, box and field at a time, how the
// canonical chunk differs from the source chunk. Align only reaches this
// for an aligned chunk, so both sides decode to the same effective values
// and every line is an expected LOCMAF normalization tagged with its
// reason. A byte diff would be misleading here: the canonical moof is
// smaller, so a positional compare flags everything after it (including
// the byte-identical mdat) as different. If either side cannot be parsed
// as a moof, it falls back to the coarse byte-level box diff.
func describeNormalizations(src, canon []byte, srcMoof *mp4.MoofBox, moov *mp4.MoovBox) []string {
	canonMoof, err := ParseMoof(canon)
	if err != nil || srcMoof == nil || canonMoof == nil {
		return boxDiff(src, canon)
	}
	var out []string
	if len(src) != len(canon) {
		out = append(out, fmt.Sprintf("chunk: %d → %d bytes (%+d)", len(src), len(canon), len(canon)-len(src)))
	}
	out = append(out, topLevelNorms(src, canon)...)
	out = append(out, moofNorms(srcMoof, canonMoof, moov)...)
	out = append(out, mdatNorm(src, canon)...)
	if len(out) == 0 {
		// Parsed clean but nothing named the difference; show something.
		return boxDiff(src, canon)
	}
	return out
}

// boxDiff walks the top-level boxes of both byte strings and reports
// per-box differences.
func boxDiff(src, canon []byte) []string {
	srcBoxes := walkBoxes(src)
	canonBoxes := walkBoxes(canon)
	var out []string
	i, j := 0, 0
	for i < len(srcBoxes) || j < len(canonBoxes) {
		switch {
		case i >= len(srcBoxes):
			b := canonBoxes[j]
			out = append(out, fmt.Sprintf("%s: only in canonical (%d bytes)", b.name, len(b.body)))
			j++
		case j >= len(canonBoxes):
			b := srcBoxes[i]
			out = append(out, fmt.Sprintf("%s: only in source (%d bytes)", b.name, len(b.body)))
			i++
		case srcBoxes[i].name != canonBoxes[j].name:
			out = append(out, fmt.Sprintf("%s (source) vs %s (canonical): box order or presence differs",
				srcBoxes[i].name, canonBoxes[j].name))
			i++
			j++
		case !bytes.Equal(srcBoxes[i].body, canonBoxes[j].body):
			out = append(out, fmt.Sprintf("%s: normalized (%d → %d bytes)",
				srcBoxes[i].name, len(srcBoxes[i].body), len(canonBoxes[j].body)))
			i++
			j++
		default:
			i++
			j++
		}
	}
	return out
}

func moofNorms(s, c *mp4.MoofBox, moov *mp4.MoovBox) []string {
	var out []string
	if s.Mfhd != nil && c.Mfhd != nil && s.Mfhd.SequenceNumber != c.Mfhd.SequenceNumber {
		out = append(out, fmt.Sprintf("moof/mfhd: sequence_number %d → %d [SEQUENCE_ZEROED]",
			s.Mfhd.SequenceNumber, c.Mfhd.SequenceNumber))
	}
	st, ct := s.Traf, c.Traf
	if st == nil || ct == nil {
		return out
	}
	if so, co := childTypes(st.Children), childTypes(ct.Children); !equalStrs(so, co) {
		out = append(out, fmt.Sprintf("moof/traf: box order %v → %v [REORDERED]", so, co))
	}
	out = append(out, tfhdNorms(st.Tfhd, ct.Tfhd, effectiveTrex(moov))...)
	out = append(out, tfdtNorms(st.Tfdt, ct.Tfdt)...)
	out = append(out, trunNorms(st.Trun, ct.Trun)...)
	out = append(out, cencPresenceNorms(st, ct)...)
	return out
}

func tfhdNorms(s, c *mp4.TfhdBox, trex *mp4.TrexBox) []string {
	if s == nil || c == nil {
		return nil
	}
	const p = "moof/traf/tfhd"
	dec := func(v uint64) string { return fmt.Sprintf("%d", v) }
	hex := func(v uint64) string { return fmt.Sprintf("0x%08x", v) }
	var out []string
	if s.DefaultBaseIfMoof() != c.DefaultBaseIfMoof() {
		out = append(out, fmt.Sprintf("%s: default_base_is_moof %t → %t [REPACKED]", p, s.DefaultBaseIfMoof(), c.DefaultBaseIfMoof()))
	}
	switch {
	case s.HasBaseDataOffset() && !c.HasBaseDataOffset():
		out = append(out, fmt.Sprintf("%s/base_data_offset=%d dropped in canonical [REPACKED]", p, s.BaseDataOffset))
	case !s.HasBaseDataOffset() && c.HasBaseDataOffset():
		out = append(out, fmt.Sprintf("%s/base_data_offset=%d added in canonical [REPACKED]", p, c.BaseDataOffset))
	}
	out = tfhdField(out, "sample_description_index", dec,
		s.HasSampleDescriptionIndex(), uint64(s.SampleDescriptionIndex),
		c.HasSampleDescriptionIndex(), uint64(c.SampleDescriptionIndex), uint64(trex.DefaultSampleDescriptionIndex))
	out = tfhdField(out, "default_sample_duration", dec,
		s.HasDefaultSampleDuration(), uint64(s.DefaultSampleDuration),
		c.HasDefaultSampleDuration(), uint64(c.DefaultSampleDuration), uint64(trex.DefaultSampleDuration))
	out = tfhdField(out, "default_sample_size", dec,
		s.HasDefaultSampleSize(), uint64(s.DefaultSampleSize),
		c.HasDefaultSampleSize(), uint64(c.DefaultSampleSize), uint64(trex.DefaultSampleSize))
	out = tfhdField(out, "default_sample_flags", hex,
		s.HasDefaultSampleFlags(), uint64(s.DefaultSampleFlags),
		c.HasDefaultSampleFlags(), uint64(c.DefaultSampleFlags), uint64(trex.DefaultSampleFlags))
	return out
}

// tfhdField reports a normalization for one optional tfhd scalar field
// that is present on only one side (or differs). A field dropped by the
// canonical form that equals the trex default is DEFAULT_FOLDED;
// otherwise REPACKED.
func tfhdField(out []string, name string, render func(uint64) string,
	sHas bool, sVal uint64, cHas bool, cVal uint64, def uint64) []string {
	const p = "moof/traf/tfhd"
	switch {
	case sHas && !cHas:
		reason := "REPACKED"
		if sVal == def {
			reason = "DEFAULT_FOLDED"
		}
		out = append(out, fmt.Sprintf("%s/%s=%s dropped in canonical (trex default %s) [%s]",
			p, name, render(sVal), render(def), reason))
	case !sHas && cHas:
		out = append(out, fmt.Sprintf("%s/%s=%s added in canonical [REPACKED]", p, name, render(cVal)))
	case sHas && cHas && sVal != cVal:
		out = append(out, fmt.Sprintf("%s/%s %s → %s [REPACKED]", p, name, render(sVal), render(cVal)))
	}
	return out
}

func tfdtNorms(s, c *mp4.TfdtBox) []string {
	if s == nil || c == nil {
		return nil
	}
	var out []string
	if s.Version != c.Version {
		out = append(out, fmt.Sprintf("moof/traf/tfdt: version %d → %d (base_media_decode_time %d, widened to 64-bit) [TFDT_WIDENED]",
			s.Version, c.Version, c.BaseMediaDecodeTime()))
	}
	if s.BaseMediaDecodeTime() != c.BaseMediaDecodeTime() {
		out = append(out, fmt.Sprintf("moof/traf/tfdt: base_media_decode_time %d → %d [!! VALUE CHANGED]",
			s.BaseMediaDecodeTime(), c.BaseMediaDecodeTime()))
	}
	return out
}

func trunNorms(s, c *mp4.TrunBox) []string {
	if s == nil || c == nil {
		return nil
	}
	const p = "moof/traf/trun"
	var out []string
	if s.DataOffset != c.DataOffset {
		out = append(out, fmt.Sprintf("%s: data_offset %d → %d [OFFSET_RECOMPUTED]", p, s.DataOffset, c.DataOffset))
	}
	sf, sfp := s.FirstSampleFlags()
	cf, cfp := c.FirstSampleFlags()
	switch {
	case sfp && !cfp:
		out = append(out, fmt.Sprintf("%s: first_sample_flags=0x%08x dropped in canonical [REPACKED]", p, sf))
	case !sfp && cfp:
		out = append(out, fmt.Sprintf("%s: first_sample_flags=0x%08x added in canonical [REPACKED]", p, cf))
	case sfp && cfp && sf != cf:
		out = append(out, fmt.Sprintf("%s: first_sample_flags 0x%08x → 0x%08x [REPACKED]", p, sf, cf))
	}
	n := len(s.Samples)
	list := func(name string, sHas, cHas bool) {
		switch {
		case sHas && !cHas:
			out = append(out, fmt.Sprintf("%s: per-sample %s list (%d entries) dropped in canonical [REDUNDANT_DROPPED]", p, name, n))
		case !sHas && cHas:
			out = append(out, fmt.Sprintf("%s: per-sample %s list added in canonical [REPACKED]", p, name))
		}
	}
	list("sample_duration", s.HasSampleDuration(), c.HasSampleDuration())
	list("sample_size", s.HasSampleSize(), c.HasSampleSize())
	list("sample_flags", s.HasSampleFlags(), c.HasSampleFlags())
	list("sample_composition_time_offset", s.HasSampleCompositionTimeOffset(), c.HasSampleCompositionTimeOffset())
	return out
}

func cencPresenceNorms(s, c *mp4.TrafBox) []string {
	var out []string
	check := func(name string, sHas, cHas bool) {
		switch {
		case sHas && !cHas:
			out = append(out, fmt.Sprintf("moof/traf/%s present in source, dropped in canonical [REPACKED]", name))
		case !sHas && cHas:
			out = append(out, fmt.Sprintf("moof/traf/%s added in canonical [REPACKED]", name))
		}
	}
	check("saiz", s.Saiz != nil, c.Saiz != nil)
	check("saio", s.Saio != nil, c.Saio != nil)
	check("senc", s.Senc != nil || s.UUIDSenc != nil, c.Senc != nil || c.UUIDSenc != nil)
	return out
}

// mdatNorm confirms the sample payload is carried through unchanged (it
// only moves, because the moof shrank). A byte difference here would be a
// real defect, not a normalization.
func mdatNorm(src, canon []byte) []string {
	sm := findBoxBody(src, "mdat")
	cm := findBoxBody(canon, "mdat")
	if len(sm) < 8 || len(cm) < 8 {
		return nil
	}
	if bytes.Equal(sm[8:], cm[8:]) {
		return []string{fmt.Sprintf("mdat: payload identical (%d bytes), relocated by the moof size change", len(sm)-8)}
	}
	return []string{"mdat: PAYLOAD DIFFERS [!! unexpected — not a normalization]"}
}

// topLevelNorms reports a change in the top-level box sequence (a genBox
// added, dropped, or reordered). genBoxes are carried verbatim, so for an
// aligned chunk this is normally empty.
func topLevelNorms(src, canon []byte) []string {
	so, co := BoxNames(src), BoxNames(canon)
	if equalStrs(so, co) {
		return nil
	}
	return []string{fmt.Sprintf("top-level boxes %v → %v [PRESENCE/REORDERED]", so, co)}
}

func effectiveTrex(moov *mp4.MoovBox) *mp4.TrexBox {
	if moov != nil && moov.Mvex != nil && moov.Mvex.Trex != nil {
		return moov.Mvex.Trex
	}
	return &mp4.TrexBox{}
}

func childTypes(boxes []mp4.Box) []string {
	types := make([]string, 0, len(boxes))
	for _, b := range boxes {
		types = append(types, b.Type())
	}
	return types
}

func equalStrs(a, b []string) bool {
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
