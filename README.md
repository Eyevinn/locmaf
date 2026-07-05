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
- Packaging version: `locmafVersion "0.3"`

## Layout

```
locmaf/          package locmaf — codec: EncodeCanonical, Decode, ReconstructCanonical
├── vi64/        MOQT vi64 varints + zigzag (stdlib-only)
├── cmd/locmaf/  CLI: align, vectors (verify, dump planned)
├── testdata/    golden canonical-encoding vectors (see testdata/vectors/README.md)
└── web/         locmaf.dev site (separate stub module, not part of the Go module)
```

The codec implements packaging version 0.3: the element sequence (genBox / full /
delta headers), vi64 integers, full 32-bit sample_flags, derived-only
delta BMDT, scheme-agnostic CENC carriage, and the canonical
reconstruction (byte-exact moof rebuild including senc/saiz/saio).

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

## Related

- [Eyevinn/moqlivemock](https://github.com/Eyevinn/moqlivemock) — MoQ test
  service (publisher/subscriber) using this packaging
- [Eyevinn/warp-player](https://github.com/Eyevinn/warp-player) — browser
  MoQ player with LOCMAF and EME/DRM support
- Live demo: <https://moqlivemock.demo.osaas.io>

## License

MIT — see [LICENSE](LICENSE).
