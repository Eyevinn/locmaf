package conform

import (
	"bytes"
	"errors"
	"fmt"
	"sort"

	"github.com/Eyevinn/locmaf"
	"github.com/Eyevinn/locmaf/vi64"
	"github.com/Eyevinn/mp4ff/mp4"
)

// Sentinel errors surfaced by the conformance core.
var (
	ErrNotFragmented = errors.New("no media segments (not fragmented CMAF)")
	ErrNoInit        = errors.New("no init segment in input; pass a separate init")
	ErrNoMoov        = errors.New("no moov")
	ErrMisaligned    = errors.New("direct and round-trip canonical bytes differ")
	ErrShortBox      = errors.New("box shorter than a header")
)

// LoadedCMAF is a parsed fragmented CMAF input with its init resolved:
// the raw file bytes, the parsed file, the moov (from an inline init or a
// separate init segment), the ftyp+moov region as verbatim bytes (for
// carrying in-band as a rawBoxes Object), and the per-chunk start offsets.
type LoadedCMAF struct {
	Raw       []byte
	File      *mp4.File
	Moov      *mp4.MoovBox
	InitBytes []byte
	Starts    []uint64
}

// LoadCMAF parses a fragmented CMAF byte stream and resolves its init.
// When the input carries no inline moov, initBytes must hold a separate
// init segment (ftyp+moov); pass nil when the input is self-contained.
func LoadCMAF(raw, initBytes []byte) (*LoadedCMAF, error) {
	f, err := mp4.DecodeFile(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("parse input: %w", err)
	}
	if len(f.Segments) == 0 {
		return nil, ErrNotFragmented
	}
	starts := ChunkStarts(f, uint64(len(raw)))

	moov := f.Moov
	if f.Init != nil {
		moov = f.Init.Moov
	}
	if moov != nil {
		// Inline init: everything before the first chunk is ftyp+moov.
		return &LoadedCMAF{Raw: raw, File: f, Moov: moov, InitBytes: raw[:starts[0]], Starts: starts}, nil
	}

	if len(initBytes) == 0 {
		return nil, ErrNoInit
	}
	m, err := MoovFromBytes(initBytes)
	if err != nil {
		return nil, fmt.Errorf("parse init: %w", err)
	}
	return &LoadedCMAF{Raw: raw, File: f, Moov: m, InitBytes: initBytes, Starts: starts}, nil
}

// MoovFromBytes parses an init segment (ftyp+moov, or a bare moov) from a
// byte slice and returns its moov.
func MoovFromBytes(b []byte) (*mp4.MoovBox, error) {
	f, err := mp4.DecodeFile(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	if f.Init != nil && f.Init.Moov != nil {
		return f.Init.Moov, nil
	}
	if f.Moov != nil {
		return f.Moov, nil
	}
	return nil, ErrNoMoov
}

// SplitFramed splits a .locmaf byte stream into its length-prefixed
// Objects.
func SplitFramed(data []byte) ([][]byte, error) {
	var objs [][]byte
	rest := data
	for len(rest) > 0 {
		obj, next, err := locmaf.NextFramed(rest)
		if err != nil {
			return nil, err
		}
		objs = append(objs, obj)
		rest = next
	}
	if len(objs) == 0 {
		return nil, fmt.Errorf("no framed objects: %w", locmaf.ErrMalformed)
	}
	return objs, nil
}

// HeaderKind classifies an Object by peeking its element sequence: the
// header element type (full or delta), or rawBoxes, without decoding it.
func HeaderKind(obj []byte) (string, error) {
	pos := 0
	for pos < len(obj) {
		et, n, err := vi64.Parse(obj[pos:])
		if err != nil {
			return "", fmt.Errorf("invalid element_type: %w", locmaf.ErrMalformed)
		}
		pos += n
		switch et {
		case locmaf.ElementTypeGenBox:
			boxSize, n, err := vi64.Parse(obj[pos:])
			if err != nil {
				return "", fmt.Errorf("invalid genBox box_size: %w", locmaf.ErrMalformed)
			}
			pos += n + int(boxSize)
		case locmaf.ElementTypeFullHeader:
			return KindFull, nil
		case locmaf.ElementTypeDeltaHeader:
			return KindDelta, nil
		case locmaf.ElementTypeRawBoxes:
			return KindRawBoxes, nil
		default:
			return "", fmt.Errorf("unknown element_type %d: %w", et, locmaf.ErrMalformed)
		}
	}
	return "", fmt.Errorf("object has no header element: %w", locmaf.ErrMalformed)
}

// RawBoxesContent returns the boxes carried by a rawBoxes Object (its
// payload after the leading element_type).
func RawBoxesContent(obj []byte) ([]byte, error) {
	et, n, err := vi64.Parse(obj)
	if err != nil {
		return nil, fmt.Errorf("invalid element_type: %w", locmaf.ErrMalformed)
	}
	if et != locmaf.ElementTypeRawBoxes {
		return nil, fmt.Errorf("not a rawBoxes object: %w", locmaf.ErrMalformed)
	}
	return obj[n:], nil
}

// ParseMoof recovers the moof box from a reconstructed canonical CMAF
// chunk (which may carry leading genBoxes and a trailing mdat).
func ParseMoof(chunk []byte) (*mp4.MoofBox, error) {
	f, err := mp4.DecodeFile(bytes.NewReader(chunk))
	if err != nil {
		return nil, err
	}
	if len(f.Segments) == 0 || len(f.Segments[0].Fragments) == 0 {
		return nil, fmt.Errorf("reconstructed chunk has no fragment: %w", locmaf.ErrMalformed)
	}
	return f.Segments[0].Fragments[0].Moof, nil
}

// ChunkStarts returns the file offset of every chunk in order: the
// segment start for each first fragment (so a leading styp belongs to
// that chunk), the fragment start otherwise.
func ChunkStarts(f *mp4.File, fileSize uint64) []uint64 {
	var starts []uint64
	for _, seg := range f.Segments {
		for i, frag := range seg.Fragments {
			if i == 0 && seg.Styp != nil {
				starts = append(starts, seg.StartPos)
			} else {
				starts = append(starts, frag.StartPos)
			}
		}
	}
	sort.Slice(starts, func(i, j int) bool { return starts[i] < starts[j] })
	if len(starts) == 0 {
		starts = append(starts, fileSize)
	}
	return starts
}

// FragmentGenBoxes collects the chunk's pre-moof boxes as genBoxes: the
// segment styp on the first fragment, then every fragment-level box
// before the moof (emsg, prft, ...).
func FragmentGenBoxes(seg *mp4.MediaSegment, fragIdx int, frag *mp4.Fragment) ([]locmaf.GenBox, error) {
	var gbs []locmaf.GenBox
	if fragIdx == 0 && seg.Styp != nil {
		gb, err := ToGenBox(seg.Styp)
		if err != nil {
			return nil, err
		}
		gbs = append(gbs, gb)
	}
	for _, child := range frag.Children {
		switch child.Type() {
		case "moof", "mdat":
			return gbs, nil
		default:
			gb, err := ToGenBox(child)
			if err != nil {
				return nil, err
			}
			gbs = append(gbs, gb)
		}
	}
	return gbs, nil
}

// ToGenBox re-serializes a parsed box and strips the 8-byte header,
// yielding the genBox payload (for uuid boxes the usertype leads the
// payload, as the draft requires).
func ToGenBox(b mp4.Box) (locmaf.GenBox, error) {
	var buf bytes.Buffer
	if err := b.Encode(&buf); err != nil {
		return locmaf.GenBox{}, fmt.Errorf("re-encode %s: %w", b.Type(), err)
	}
	bb := buf.Bytes()
	if len(bb) < 8 {
		return locmaf.GenBox{}, fmt.Errorf("box %s: %w", b.Type(), ErrShortBox)
	}
	return locmaf.GenBox{Name: b.Type(), Payload: bb[8:]}, nil
}

// --- byte-level box walking and diffing ---

type boxSpan struct {
	name string
	body []byte
}

// walkBoxes splits a byte string into its top-level ISO boxes; a
// malformed tail becomes a pseudo-box named "?".
func walkBoxes(data []byte) []boxSpan {
	var out []boxSpan
	pos := 0
	for pos+8 <= len(data) {
		size := int(uint32(data[pos])<<24 | uint32(data[pos+1])<<16 | uint32(data[pos+2])<<8 | uint32(data[pos+3]))
		if size < 8 || pos+size > len(data) {
			break
		}
		out = append(out, boxSpan{name: string(data[pos+4 : pos+8]), body: data[pos : pos+size]})
		pos += size
	}
	if pos < len(data) {
		out = append(out, boxSpan{name: "?", body: data[pos:]})
	}
	return out
}

// BoxNames lists the FourCC of every top-level ISO box in data.
func BoxNames(data []byte) []string {
	var names []string
	for _, b := range walkBoxes(data) {
		names = append(names, b.name)
	}
	return names
}

func findBoxBody(data []byte, name string) []byte {
	for _, b := range walkBoxes(data) {
		if b.name == name {
			return b.body
		}
	}
	return nil
}

// FirstDiff returns the first index at which a and b differ, or -1 if
// they are byte-identical.
func FirstDiff(a, b []byte) int {
	n := min(len(a), len(b))
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	if len(a) != len(b) {
		return n
	}
	return -1
}

// HexWindow renders up to 16 bytes starting 8 bytes before off.
func HexWindow(data []byte, off int) string {
	start := max(off-8, 0)
	end := min(start+16, len(data))
	return fmt.Sprintf("@%d: % x", start, data[start:end])
}
