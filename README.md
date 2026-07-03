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

- Specification: [draft-einarsson-moq-locmaf](https://datatracker.ietf.org/doc/draft-einarsson-moq-locmaf/)
  (source: [Eyevinn/locmaf-id](https://github.com/Eyevinn/locmaf-id))
- Explainer site and slides: <https://locmaf.dev> (source in [`web/`](web/))
- Wire version: `locmafVersion "0.3"`

## Status

The Go module is being assembled: the v0.3 codec is ported here from
[Eyevinn/moqlivemock](https://github.com/Eyevinn/moqlivemock) (which
currently implements wire v0.2). Planned layout:

```
locmaf/          package locmaf — codec: encode, decode, canonical reconstruction
├── vi64/        MOQT vi64 varints + zigzag (stdlib-only)
├── cmd/locmaf/  CLI: align, vectors, verify, dump
├── testdata/    golden canonical-encoding vectors
└── web/         locmaf.dev site (separate stub module, not part of the Go module)
```

## Related

- [Eyevinn/moqlivemock](https://github.com/Eyevinn/moqlivemock) — MoQ test
  service (publisher/subscriber) using this packaging
- [Eyevinn/warp-player](https://github.com/Eyevinn/warp-player) — browser
  MoQ player with LOCMAF and EME/DRM support
- Live demo: <https://moqlivemock.demo.osaas.io>

## License

MIT — see [LICENSE](LICENSE).
