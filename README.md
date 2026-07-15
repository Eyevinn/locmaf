<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="web/assets/logo-dark.svg">
    <source media="(prefers-color-scheme: light)" srcset="web/assets/logo-light.svg">
    <img alt="LOCMAF — Low Overhead CMAF for MOQ" src="web/assets/logo-light.svg" width="640">
  </picture>
</p>

# LOCMAF

Reference implementation of **LOCMAF** (Low Overhead CMAF for MOQ) —
a compact CMAF packaging for MoQ Transport, including DRM-protected
content. Sample-level objects with a delta moof as small as 2 bytes,
reconstructed into functionally lossless CMAF chunks at the receiver.

This repository is both the reference codec and its conformance
suite: a stdlib-only Go implementation, a byte-pinned corpus of
[golden conformance vectors](testdata/vectors/), and worked examples
covering the packaging's edge cases — negative CTOs, CENC/cbcs
carriage, BMDT re-anchoring, event-only chunks, and more.

- Specification: [draft-einarsson-moq-locmaf](https://datatracker.ietf.org/doc/draft-einarsson-moq-locmaf/)
  (source: [Eyevinn/locmaf-id](https://github.com/Eyevinn/locmaf-id))
- Explainer site and slides: <https://locmaf.dev> (source in [`web/`](web/))
- In-browser conformance checker: <https://locmaf.dev/tools/> — client-side, nothing uploaded
- Packaging version: `locmafVersion "0.3"`

## Layout

```
locmaf/              package locmaf — codec: EncodeCanonical, Decode, ReconstructCanonical
├── vi64/            MOQT vi64 varints + zigzag (stdlib-only)
├── conform/         shared verify/dump/align conformance core (I/O-free; used by the CLI and the wasm build)
├── cmd/locmaf/      CLI: align, pack, dump, verify, vectors
├── cmd/locmaf-wasm/ browser (js/wasm) build of the conformance checker
├── testdata/        golden canonical-encoding vectors (see testdata/vectors/README.md)
└── web/             locmaf.dev site + /tools/ checker (separate stub module, not part of the Go module)
```

The codec implements packaging version 0.3: the element sequence (genBox / full /
delta headers), vi64 integers, full 32-bit sample_flags, derived-only
delta BMDT, scheme-agnostic CENC carriage, and the canonical
reconstruction (byte-exact moof rebuild including senc/saiz/saio).

## Command-line tool

`cmd/locmaf` is a `go install`-able CLI. Exit codes: `0` success,
`1` findings (misalignment, corpus drift, or a malformed object),
`2` usage or I/O error.

### `align` — CMAF/LOCMAF round-trip conformance

```sh
locmaf align [-init init.mp4] [-report text|json] [-canon-out path] [-bytes] input.cmaf
```

Per fragment, asserts that canonical reconstruction straight from the
source moof equals the encode→decode→reconstruct round trip,
byte-identically. For a normalized fragment it explains, box and field
at a time, how the canonical form differs from the source — e.g.
`moof/mfhd: sequence_number 3 → 0 [SEQUENCE_ZEROED]`,
`moof/traf/tfdt: version 0 → 1 [TFDT_WIDENED]`,
`moof/traf/trun: per-sample sample_size list dropped [REDUNDANT_DROPPED]` —
each tagged with its reason. (A raw byte diff would mislead here: the
smaller canonical moof shifts everything after it, so `-bytes` adds the
first-differing-byte hex window only on request.) With `-canon-out <path>`
(`"-"` for stdout) it also writes the
canonical CMAF — the input's init region unchanged followed by each
chunk's canonical form — so `align` can generate canonical reference
files, not just report differences. Those bytes are written only when
every chunk aligns; when they go to stdout the report is routed to
stderr so the two streams do not interleave.

### `pack` — CMAF → `.locmaf`

```sh
locmaf pack [-init init.mp4] [-no-init] [-o out.locmaf] input.cmaf
```

Encodes a fragmented CMAF file into the self-framed `.locmaf` file
format: a leading in-band rawBoxes init Object followed by one
length-prefixed LOCMAF Object per chunk. The file is a single group —
one delta chain, with a full header at the start and only re-anchoring
on a timeline (BMDT) discontinuity; CMAF segment structure rides in the
`styp` genBoxes. `-no-init` omits the init Object for bare media output
(decoding then needs a separate init); output defaults to stdout.

### `dump` — inspect a `.locmaf`

```sh
locmaf dump [-init init.mp4] [-report text|json] input.locmaf
```

Walks a `.locmaf` file and reports each Object — rawBoxes, full, or
delta header — with its genBoxes, sample count, BMDT, and payload size.

### `verify` — conformance-check a `.locmaf`

```sh
locmaf verify [-init init.mp4] [-report text|json] [-decodable] input.locmaf
```

Checks that a `.locmaf` file is a conformant LOCMAF stream — the
wire-format analog of `align`. Per Object it runs the conformance
ladder: it decodes, reconstructs a canonical CMAF chunk, and (unless
`-decodable`) re-encodes that chunk and requires the result to be
byte-identical to the wire Object, i.e. the Object is itself canonically
encoded. `-decodable` relaxes the check to "decodes and reconstructs"
for streams you do not require to be canonical. Exit `1` on any
non-conformant Object.

`vectors gen` / `vectors check` manage the conformance corpus; see
below.

## Conformance vectors and golden files

[`testdata/vectors/`](testdata/vectors/) holds a codec-derived
conformance corpus — one directory per case, each a worked example
with the source init, the bare LOCMAF Objects (`*.locmafobj`), the
reconstructed canonical CMAF chunks (`*.cmfc`), and the decoded
effective values. Selected cases also carry a self-framed,
self-contained `file.locmaf`. The corpus (including its own
[README](testdata/vectors/README.md), which describes every case) is
regenerated from the codec and byte-pinned in CI:

```sh
locmaf vectors gen     # rewrite the corpus from the codec
locmaf vectors check   # re-derive and byte-compare against disk
```

## Browser conformance checker

<https://locmaf.dev/tools/> runs the same conformance core in the browser
via WebAssembly (`cmd/locmaf-wasm`, a shim over the `conform` package): drop
a `.locmaf` file to verify it, or a fragmented CMAF file to align it.
Everything runs client-side — the file never leaves your machine. The page
is built by `web/`'s `npm run build` (which compiles the wasm first) and
served as static files; see [`web/`](web/).

## Related

- [Eyevinn/moqlivemock](https://github.com/Eyevinn/moqlivemock) — MoQ test
  service (publisher/subscriber) using this packaging
- [Eyevinn/warp-player](https://github.com/Eyevinn/warp-player) — browser
  MoQ player with LOCMAF and EME/DRM support
- Live demo: <https://moqlivemock.demo.osaas.io>

## License

MIT — see [LICENSE](LICENSE).
