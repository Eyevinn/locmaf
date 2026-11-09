// Package vectorgen defines the golden-vector case matrix and the
// generation/verification machinery around it. The canonical encoder is
// the oracle: Generate derives every vector from the codec, and Check
// re-derives and byte-compares so the committed corpus can never drift
// from the code.
package vectorgen

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"

	"github.com/Eyevinn/locmaf"
	"github.com/Eyevinn/mp4ff/aac"
	"github.com/Eyevinn/mp4ff/mp4"
)

// draftCommit is the locmaf-id commit the corpus was generated against
// (the draft-einarsson-moq-locmaf-01 tag). Bump it together with any
// wire- or canonical-affecting draft change.
const draftCommit = "6a2439a"

// chunkSrc is one source chunk of a group: either a moof + mdat pair
// (with optional genBoxes and decode-equivalent alternate moofs), or a
// raw-boxes payload.
type chunkSrc struct {
	genBoxes []locmaf.GenBox
	moof     *mp4.MoofBox
	mdat     []byte
	// altMoofs are decode-equivalent representations of moof; the
	// generator asserts each canonically encodes to identical bytes
	// (representation invariance as data).
	altMoofs []*mp4.MoofBox
	// raw, when set, makes this chunk a rawBoxes Object.
	raw []byte
}

// caseData is a fully built vector case: an init segment and the source
// chunks of each group.
type caseData struct {
	init   *mp4.InitSegment
	groups [][]chunkSrc
	// locmafFile requests an additional file.locmaf: the self-framed
	// concatenation of every object of every group.
	locmafFile bool
}

type vectorCase struct {
	name        string
	description string
	build       func() (*caseData, error)
}

// AVC parameter sets of the video sample entry: High profile, level 3.0,
// 640x360. The sample payloads are patterned filler, not AVC, but the
// CMAF header is a real one.
var (
	videoSPS = mustHex("6764001eacd940a02ff9610000030001000003003c8f162d96")
	videoPPS = mustHex("68ebecb22c")
)

// eventSchemeURN names the scheme of the event-only track's URIMetaSampleEntry,
// the DASH event message scheme that DASH-IF Ingest recommends for timed
// metadata tracks with emsg boxes.
const eventSchemeURN = "urn:mpeg:dash:event:2012"

func mustHex(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

// buildInit builds a single-track CMAF header with the given trex
// defaults and a sample entry for the media type: avc1 for video, AAC-LC
// mp4a for audio, and urim for meta. The ftyp is set explicitly, so that
// the corpus does not change with the ftyp that mp4ff writes by default.
func buildInit(timescale uint32, mediaType string, defDur, defSize, defFlags uint32) (*mp4.InitSegment, error) {
	init := mp4.CreateEmptyInit()
	init.SetFtyp(mp4.NewFtyp("cmfc", 0, []string{"cmfc", "iso6"}))
	trak := init.AddEmptyTrack(timescale, mediaType, "und")
	switch mediaType {
	case "video":
		if err := trak.SetAVCDescriptor("avc1", [][]byte{videoSPS}, [][]byte{videoPPS}, true); err != nil {
			return nil, fmt.Errorf("avc1 sample entry: %w", err)
		}
	case "audio":
		if err := trak.SetAACDescriptor(aac.AAClc, int(timescale)); err != nil {
			return nil, fmt.Errorf("mp4a sample entry: %w", err)
		}
	case "meta":
		trak.Mdia.Minf.Stbl.Stsd.AddChild(urimSampleEntry(eventSchemeURN))
	default:
		return nil, fmt.Errorf("%w %q", errUnknownMediaType, mediaType)
	}
	init.Moov.Mvex.Trex.DefaultSampleDuration = defDur
	init.Moov.Mvex.Trex.DefaultSampleSize = defSize
	init.Moov.Mvex.Trex.DefaultSampleFlags = defFlags
	return init, nil
}

// urimSampleEntry builds a URIMetaSampleEntry (urim) whose URIBox (uri)
// holds uri. mp4ff has no type for it, so it is written as raw bytes:
// the sample entry header with data_reference_index 1, then the URIBox,
// a FullBox with a null-terminated string.
func urimSampleEntry(uri string) mp4.Box {
	uriPayload := append([]byte{0, 0, 0, 0}, append([]byte(uri), 0)...)
	payload := []byte{0, 0, 0, 0, 0, 0, 0, 1}
	payload = binary.BigEndian.AppendUint32(payload, uint32(8+len(uriPayload)))
	payload = append(payload, "uri "...)
	payload = append(payload, uriPayload...)
	return mp4.CreateUnknownBox("urim", uint64(8+len(payload)), payload)
}

// protect marks the init's single track as protected with mp4ff's
// InitProtect: the sample entry becomes encv or enca, with a sinf of frma,
// schm and schi/tenc. For cenc, iv sets the per-sample IV size; for cbcs,
// it is the constant IV. The KID is 01 02 ... 10.
func protect(init *mp4.InitSegment, scheme string, iv []byte) error {
	kid := make(mp4.UUID, 16)
	for i := range kid {
		kid[i] = byte(i + 1)
	}
	if _, err := mp4.InitProtect(init, make([]byte, 16), iv, scheme, kid, nil); err != nil {
		return fmt.Errorf("protect init: %w", err)
	}
	return nil
}

// sampleSpec is one sample: duration, size, flags, cto, and a fill byte
// for the deterministic patterned payload.
type sampleSpec struct {
	dur   uint32
	size  uint32
	flags uint32
	cto   int32
	fill  byte
}

// buildChunk builds a moof + patterned mdat from sample specs.
func buildChunk(seq uint32, bmdt uint64, samples []sampleSpec) (chunkSrc, error) {
	frag, err := mp4.CreateFragment(seq, 1)
	if err != nil {
		return chunkSrc{}, err
	}
	var mdat []byte
	for _, s := range samples {
		data := bytes.Repeat([]byte{s.fill}, int(s.size))
		frag.AddFullSample(mp4.FullSample{
			Sample: mp4.Sample{Dur: s.dur, Size: s.size, Flags: s.flags, CompositionTimeOffset: s.cto},
			Data:   data,
		})
		mdat = append(mdat, data...)
	}
	frag.Moof.Traf.Tfdt.SetBaseMediaDecodeTime(bmdt)
	return chunkSrc{moof: frag.Moof, mdat: mdat}, nil
}

// videoSamples produces size-varying video-like sample specs with a
// deterministic fill derived from the chunk index.
func videoSamples(chunkIdx int, n int, sync bool) []sampleSpec {
	out := make([]sampleSpec, n)
	for i := range out {
		flags := uint32(0x01010000)
		if sync && i == 0 {
			flags = 0x02000000
		}
		out[i] = sampleSpec{
			dur:   3000,
			size:  uint32(400 + 100*chunkIdx + 10*i),
			flags: flags,
			fill:  byte(0x10*chunkIdx + i + 1),
		}
	}
	return out
}

// addSenc attaches a hand-built senc box to the chunk's traf: one IV of
// ivSize bytes per sample (nil for cbcs-style no-IV) and one subsample
// per sample covering the sample exactly with clearLen clear bytes.
func addSenc(ch *chunkSrc, ivSize int, clearLen uint16, chunkIdx int) error {
	traf := ch.moof.Traf
	n := len(traf.Trun.Samples)
	senc := &mp4.SencBox{SampleCount: uint32(n)}
	if ivSize > 0 {
		// SencBox derives PerSampleIVSize from AddSample or an explicit
		// set; assigning IVs directly would leave it 0 and drop the IVs
		// from the encoding.
		senc.SetPerSampleIVSize(byte(ivSize))
	}
	for i := 0; i < n; i++ {
		if ivSize > 0 {
			iv := make([]byte, ivSize)
			for j := range iv {
				iv[j] = byte(0xA0 + 0x10*chunkIdx + i + j)
			}
			senc.IVs = append(senc.IVs, iv)
		}
		size := traf.Trun.Samples[i].Size
		senc.SubSamples = append(senc.SubSamples, []mp4.SubSamplePattern{{
			BytesOfClearData:     clearLen,
			BytesOfProtectedData: size - uint32(clearLen),
		}})
	}
	return traf.AddChild(senc)
}

// cases is the golden-vector matrix. Each case is a complete group
// sequence exercising one wire feature; names are stable — they become
// directory names in the corpus.
func cases() []vectorCase {
	return []vectorCase{
		{
			name:        "uniform",
			description: "audio-like uniform durations and sizes: ID 6 default size, empty steady-state deltas",
			build: func() (*caseData, error) {
				init, err := buildInit(48000, "audio", 1024, 0, 0x02000000)
				if err != nil {
					return nil, err
				}
				var group []chunkSrc
				for c := 0; c < 3; c++ {
					samples := make([]sampleSpec, 4)
					for i := range samples {
						samples[i] = sampleSpec{dur: 1024, size: 200, flags: 0x02000000, fill: byte(0x30*c + i + 1)}
					}
					ch, err := buildChunk(uint32(c+1), uint64(c)*4096, samples)
					if err != nil {
						return nil, err
					}
					group = append(group, ch)
				}
				return &caseData{init: init, groups: [][]chunkSrc{group}}, nil
			},
		},
		{
			name:        "varying-sizes",
			description: "video-like varying sizes: ID 1 carries n−1 entries, the last derives from P",
			build: func() (*caseData, error) {
				init, err := buildInit(90000, "video", 3000, 0, 0x01010000)
				if err != nil {
					return nil, err
				}
				var group []chunkSrc
				for c := 0; c < 3; c++ {
					ch, err := buildChunk(uint32(c+1), uint64(c)*9000, videoSamples(c, 3, c == 0))
					if err != nil {
						return nil, err
					}
					group = append(group, ch)
				}
				return &caseData{init: init, groups: [][]chunkSrc{group}}, nil
			},
		},
		{
			name:        "single-sample",
			description: "one sample per chunk: sizes never on the wire (P authoritative); ID 8 sync flags deleted via ID 27",
			build: func() (*caseData, error) {
				init, err := buildInit(90000, "video", 3000, 0, 0x01010000)
				if err != nil {
					return nil, err
				}
				var group []chunkSrc
				for c := 0; c < 4; c++ {
					flags := uint32(0x01010000)
					if c == 0 {
						flags = 0x02000000
					}
					ch, err := buildChunk(uint32(c+1), uint64(c)*3000, []sampleSpec{{
						dur: 3000, size: uint32(500 + 70*c), flags: flags, fill: byte(0x40 + c),
					}})
					if err != nil {
						return nil, err
					}
					group = append(group, ch)
				}
				return &caseData{init: init, groups: [][]chunkSrc{group}}, nil
			},
		},
		{
			name:        "negative-ctos",
			description: "B-frame composition offsets: negative CTOs force trun v1; ID 5 zigzag in both contexts",
			build: func() (*caseData, error) {
				init, err := buildInit(90000, "video", 3000, 0, 0x01010000)
				if err != nil {
					return nil, err
				}
				ctos := [][]int32{{0, 6000, -3000}, {0, 3000, -3000}, {0, 6000, 3000}}
				var group []chunkSrc
				for c := 0; c < 3; c++ {
					samples := videoSamples(c, 3, c == 0)
					for i := range samples {
						samples[i].cto = ctos[c][i]
					}
					ch, err := buildChunk(uint32(c+1), uint64(c)*9000, samples)
					if err != nil {
						return nil, err
					}
					group = append(group, ch)
				}
				return &caseData{init: init, groups: [][]chunkSrc{group}}, nil
			},
		},
		{
			name:        "first-sample-flags",
			description: "sync-led chunks: ID 12 emitted, then deleted via ID 27, then re-introduced by delta",
			build: func() (*caseData, error) {
				init, err := buildInit(90000, "video", 3000, 0, 0x01010000)
				if err != nil {
					return nil, err
				}
				sync := []bool{true, false, true}
				var group []chunkSrc
				for c := 0; c < 3; c++ {
					ch, err := buildChunk(uint32(c+1), uint64(c)*9000, videoSamples(c, 3, sync[c]))
					if err != nil {
						return nil, err
					}
					group = append(group, ch)
				}
				return &caseData{init: init, groups: [][]chunkSrc{group}}, nil
			},
		},
		{
			name:        "per-sample-flags",
			description: "mixed flags neither uniform nor equal-except-first: ID 7 full 32-bit list",
			build: func() (*caseData, error) {
				init, err := buildInit(90000, "video", 3000, 0, 0x01010000)
				if err != nil {
					return nil, err
				}
				flagSets := [][]uint32{
					{0x02000000, 0x01010000, 0x02010000},
					{0x02000000, 0x02010000, 0x01010000},
				}
				var group []chunkSrc
				for c := 0; c < 2; c++ {
					samples := videoSamples(c, 3, false)
					for i := range samples {
						samples[i].flags = flagSets[c][i]
					}
					ch, err := buildChunk(uint32(c+1), uint64(c)*9000, samples)
					if err != nil {
						return nil, err
					}
					group = append(group, ch)
				}
				return &caseData{init: init, groups: [][]chunkSrc{group}}, nil
			},
		},
		{
			name:        "list-grow-shrink",
			description: "sample count 2 → 4 → 1 across deltas: list grow (absolute-as-zigzag) and shrink (truncate)",
			build: func() (*caseData, error) {
				init, err := buildInit(90000, "video", 3000, 0, 0x01010000)
				if err != nil {
					return nil, err
				}
				counts := []int{2, 4, 1}
				bmdt := uint64(0)
				var group []chunkSrc
				for c, n := range counts {
					ch, err := buildChunk(uint32(c+1), bmdt, videoSamples(c, n, c == 0))
					if err != nil {
						return nil, err
					}
					group = append(group, ch)
					bmdt += uint64(3000 * n)
				}
				return &caseData{init: init, groups: [][]chunkSrc{group}}, nil
			},
		},
		{
			name:        "bmdt-reanchor",
			description: "a BMDT discontinuity mid-group re-anchors with a full header",
			build: func() (*caseData, error) {
				init, err := buildInit(90000, "video", 3000, 0, 0x01010000)
				if err != nil {
					return nil, err
				}
				bmdts := []uint64{0, 9000, 900000, 909000} // jump at chunk 2
				var group []chunkSrc
				for c, b := range bmdts {
					ch, err := buildChunk(uint32(c+1), b, videoSamples(c, 3, c == 0))
					if err != nil {
						return nil, err
					}
					group = append(group, ch)
				}
				return &caseData{init: init, groups: [][]chunkSrc{group}}, nil
			},
		},
		{
			name:        "cenc-subsamples",
			description: "cenc-protected chunks: per-sample IVs (ID 9 overwrite) and subsample maps (IDs 11/13/15)",
			build: func() (*caseData, error) {
				init, err := buildInit(90000, "video", 3000, 0, 0x01010000)
				if err != nil {
					return nil, err
				}
				if err := protect(init, "cenc", make([]byte, 8)); err != nil {
					return nil, err
				}
				var group []chunkSrc
				for c := 0; c < 3; c++ {
					samples := make([]sampleSpec, 2)
					for i := range samples {
						samples[i] = sampleSpec{dur: 3000, size: 200, flags: 0x01010000, fill: byte(0x60*c + i + 1)}
					}
					if c == 0 {
						samples[0].flags = 0x02000000
					}
					ch, err := buildChunk(uint32(c+1), uint64(c)*6000, samples)
					if err != nil {
						return nil, err
					}
					if err := addSenc(&ch, 8, 7, c); err != nil {
						return nil, err
					}
					group = append(group, ch)
				}
				return &caseData{init: init, groups: [][]chunkSrc{group}}, nil
			},
		},
		{
			name:        "cbcs-omit",
			description: "cbcs constant-IV audio with no per-sample aux info: senc/saiz/saio all omitted",
			build: func() (*caseData, error) {
				// trex carries the uniform size, so no size field is on
				// the wire at all — the trex-default derivation path.
				init, err := buildInit(48000, "audio", 1024, 160, 0x02000000)
				if err != nil {
					return nil, err
				}
				iv := make([]byte, 16)
				for i := range iv {
					iv[i] = byte(0xC0 + i)
				}
				if err := protect(init, "cbcs", iv); err != nil {
					return nil, err
				}
				var group []chunkSrc
				for c := 0; c < 2; c++ {
					samples := make([]sampleSpec, 4)
					for i := range samples {
						samples[i] = sampleSpec{dur: 1024, size: 160, flags: 0x02000000, fill: byte(0x70*c + i + 1)}
					}
					ch, err := buildChunk(uint32(c+1), uint64(c)*4096, samples)
					if err != nil {
						return nil, err
					}
					group = append(group, ch)
				}
				return &caseData{init: init, groups: [][]chunkSrc{group}}, nil
			},
		},
		{
			name:        "genboxes",
			description: "prft and emsg boxes ride verbatim as genBox elements ahead of full and delta headers",
			build: func() (*caseData, error) {
				init, err := buildInit(90000, "video", 3000, 0, 0x01010000)
				if err != nil {
					return nil, err
				}
				// prft payload: FullBox v0 flags 0, reference_track_ID,
				// 64-bit NTP, 32-bit media time. Content is opaque to
				// LOCMAF; bytes are deterministic.
				prft := locmaf.GenBox{Name: "prft", Payload: []byte{
					0, 0, 0, 0, // version 0, flags 0
					0, 0, 0, 1, // reference_track_ID
					0xE8, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, // ntp_timestamp
					0, 0, 0x23, 0x28, // media_time
				}}
				emsg := locmaf.GenBox{Name: "emsg", Payload: append([]byte{
					1, 0, 0, 0, // version 1, flags 0
					0, 1, 0x5F, 0x90, // timescale 90000
					0, 0, 0, 0, 0, 0, 0x23, 0x28, // presentation_time
					0, 0, 0x0B, 0xB8, // event_duration
					0, 0, 0, 7, // id
				}, []byte("urn:x\x00v\x00")...)}
				var group []chunkSrc
				for c := 0; c < 2; c++ {
					ch, err := buildChunk(uint32(c+1), uint64(c)*6000, videoSamples(c, 2, c == 0))
					if err != nil {
						return nil, err
					}
					if c == 0 {
						ch.genBoxes = []locmaf.GenBox{prft}
					} else {
						ch.genBoxes = []locmaf.GenBox{prft, emsg}
					}
					group = append(group, ch)
				}
				return &caseData{init: init, groups: [][]chunkSrc{group}}, nil
			},
		},
		{
			name:        "strict-cmf2-pair",
			description: "two decode-equivalent source encodings (per-sample values vs tfhd defaults) yield one canonical object",
			build: func() (*caseData, error) {
				init, err := buildInit(90000, "video", 3000, 0, 0x01010000)
				if err != nil {
					return nil, err
				}
				samples := make([]sampleSpec, 3)
				for i := range samples {
					samples[i] = sampleSpec{dur: 2500, size: 300, flags: 0x01010000, fill: byte(0x0A + i)}
				}
				samples[0].flags = 0x02000000
				ch, err := buildChunk(1, 0, samples)
				if err != nil {
					return nil, err
				}
				// Alternate representation: durations and sizes ride as
				// tfhd defaults instead of per-sample trun entries.
				alt, err := buildChunk(1, 0, samples)
				if err != nil {
					return nil, err
				}
				trun, tfhd := alt.moof.Traf.Trun, alt.moof.Traf.Tfhd
				trun.Flags &^= 0x000100 | 0x000200 // duration/size not per-sample
				tfhd.Flags |= 0x000008 | 0x000010
				tfhd.DefaultSampleDuration = 2500
				tfhd.DefaultSampleSize = 300
				ch.altMoofs = []*mp4.MoofBox{alt.moof}
				return &caseData{init: init, groups: [][]chunkSrc{{ch}}}, nil
			},
		},
		{
			name:        "rawboxes-file",
			description: "the .locmaf file format: a leading rawBoxes Object carries the init in-band, then normal chunks",
			build: func() (*caseData, error) {
				init, err := buildInit(90000, "video", 3000, 0, 0x01010000)
				if err != nil {
					return nil, err
				}
				var initBuf bytes.Buffer
				if err := init.Encode(&initBuf); err != nil {
					return nil, err
				}
				group := []chunkSrc{{raw: initBuf.Bytes()}}
				for c := 0; c < 2; c++ {
					ch, err := buildChunk(uint32(c+1), uint64(c)*6000, videoSamples(c, 2, c == 0))
					if err != nil {
						return nil, err
					}
					group = append(group, ch)
				}
				return &caseData{init: init, groups: [][]chunkSrc{group}, locmafFile: true}, nil
			},
		},
		{
			name:        "event-only",
			description: "zero-sample event chunks: emsg genBoxes, full header per chunk, empty mdat",
			build: func() (*caseData, error) {
				init, err := buildInit(90000, "meta", 0, 0, 0)
				if err != nil {
					return nil, err
				}
				var group []chunkSrc
				for c := 0; c < 2; c++ {
					ch, err := buildChunk(uint32(c+1), uint64(90000*c), nil)
					if err != nil {
						return nil, err
					}
					ch.genBoxes = []locmaf.GenBox{{Name: "emsg", Payload: append([]byte{
						1, 0, 0, 0,
						0, 1, 0x5F, 0x90,
						0, 0, 0, 0, 0, byte(c), 0x5F, 0x90,
						0, 0, 0x0B, 0xB8,
						0, 0, 0, byte(c + 1),
					}, []byte("urn:y\x00e\x00")...)}}
					group = append(group, ch)
				}
				return &caseData{init: init, groups: [][]chunkSrc{group}}, nil
			},
		},
	}
}

// encodeInit serializes the init segment.
func encodeInit(init *mp4.InitSegment) ([]byte, error) {
	var buf bytes.Buffer
	if err := init.Encode(&buf); err != nil {
		return nil, fmt.Errorf("encode init: %w", err)
	}
	return buf.Bytes(), nil
}
