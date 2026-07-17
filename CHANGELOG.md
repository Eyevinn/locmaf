# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
Each release notes which LOCMAF packaging version (`locmafVersion`) it implements.

## [Unreleased]

_Nothing yet._

## [0.2.0] - 2026-07-17

Still implements LOCMAF packaging version `"0.3"` — a Go-module feature
release, not a packaging-version bump.

### Added

- In-browser **conformance checker** at
  [locmaf.dev/tools/](https://locmaf.dev/tools/): drop a `.locmaf` file to
  verify it, or a fragmented CMAF file to round-trip (align) it — entirely
  client-side via a WebAssembly build (`cmd/locmaf-wasm`), nothing uploaded.
- `conform` package (`github.com/Eyevinn/locmaf/conform`): the
  verify/dump/align conformance core as an importable, I/O-free library
  (operates on byte slices and parsed boxes). The `locmaf` CLI and the
  browser build are thin layers over it, so both give identical verdicts.
- `locmaf pack` and `locmaf dump`: encode a fragmented CMAF file into
  the self-framed `.locmaf` file format, and inspect a `.locmaf` file
  Object by Object.
- `locmaf verify`: check that a `.locmaf` file is a conformant LOCMAF
  stream — every Object decodes, reconstructs, and is canonically
  encoded.
- `locmaf align -canon-out`: also emit the canonical CMAF bytes, so
  `align` can generate canonical reference files.

### Changed

- `locmaf align` now explains normalizations at the box/field level
  (e.g. `moof/traf/tfdt: version 0 → 1 [TFDT_WIDENED]`) instead of a
  byte offset; the raw hex diff moved behind `-bytes`.
- Updated the `mp4ff` dependency to v0.54.0.

### Security

- Added a `govulncheck` CI workflow: reachability-based vulnerability
  scanning (dependencies and the Go stdlib) on push/PR and a weekday
  schedule, in place of Dependabot alerts.

See the [README](README.md#command-line-tool) for the CLI reference.

## [0.1.1] - 2026-07-05

Still implements LOCMAF packaging version `"0.3"` — this is an
editorial retarget, not a packaging-version bump.

### Changed

- Retargeted the golden-vector corpus to the published Internet-Draft
  `draft-einarsson-moq-locmaf-01` (`draftCommit 6a2439a`). The revision
  is editorial only: no wire or canonical-encoding change, so every
  reconstructed chunk and effective-value file is byte-identical to
  0.1.0 and the manifests differ only in the recorded draft commit.
- Normalized all prose to US English spelling (`serialize`, `defense`,
  `signaling`) across Go comments and the web site/slides, mirroring
  the draft's own spelling normalization.
- Renamed the bare-object vector files `.locobj` → `.locmafobj`.
- Expanded the README to reflect the repository's scope: the reference
  codec together with the conformance vectors, golden files, and worked
  examples.

### Added

- A generated `testdata/vectors/README.md`, emitted by `vectors gen`
  and pinned by `vectors check`, documenting every case in the corpus.

## [0.1.0] - 2026-07-04

Implements LOCMAF packaging version `"0.3"`.

### Added

- `vi64` subpackage: the MOQT (draft-18 §1.4.1) variable-length
  integer encoding plus the zigzag signed variant, stdlib-only.
- The v0.3 codec: `EncodeCanonical` (canonical full/delta object
  encoding with automatic full-header re-anchoring on BMDT
  discontinuities), `Decode` (element sequence, parity-rule property
  parsing, delta and deletion application, effective-value
  expansion), `ExtractEffective` (effective values straight from a
  source moof, no wire round trip), and `ReconstructCanonical`
  (byte-exact canonical CMAF chunk rebuild, including CENC
  senc/saiz/saio regeneration and the omit rule). Ported from
  moqlivemock's internal v0.2 codec and reworked for v0.3 (element
  types, genBox, full 32-bit sample_flags, derived-only delta BMDT,
  vi64).
- The `rawBoxes` element (type 4): verbatim carriage of complete ISO
  BMFF boxes as a whole Object — `EncodeRaw` on the encode side, a
  separate raw return from `Decode`, and an in-group state reset on
  both sides. `AppendFramed`/`NextFramed` implement the draft's
  self-framed carriage (length-prefixed Objects), which together
  with an in-band rawBoxes init makes the self-contained `.locmaf`
  file format.
- The `locmaf` CLI (`cmd/locmaf`, `go install`-able):
  - `locmaf align [-init init.mp4] [-report text|json] input.cmaf` —
    the CMAF round-trip conformance tool. Per fragment it asserts
    that canonical reconstruction straight from the source moof
    equals the encode→decode→reconstruct round trip
    byte-identically, and reports how the canonical form normalized
    the source bytes (box-level diff plus a hex window at the first
    differing byte). Exit codes: 0 aligned, 1 diverged, 2 usage or
    I/O error.
  - `locmaf vectors gen [-out dir]` / `locmaf vectors check [dir]` —
    generate and verify the golden-vector corpus in
    `testdata/vectors`: 14 cases (uniform, varying sizes,
    single-sample, negative CTOs, first-sample-flags with ID 27
    deletion, per-sample flags, list grow/shrink, BMDT re-anchor,
    cenc subsamples, cbcs omit, genBoxes, a strict-cmf2
    representation-invariance pair, a rawBoxes `.locmaf` file, and
    event-only). Each case carries the wire objects, effective-value
    JSON, byte-exact canonical chunks, and a sha256 manifest. The
    corpus is derived from the codec by `internal/vectorgen` and
    re-derived in CI so it cannot drift; other implementations
    consume it as a three-rung conformance ladder (decode →
    effective values → canonical bytes).
  - `locmaf -version` — tool version and commit date, injected by
    the Makefile via ldflags.

### Changed

- Repurposed the repository as the Go reference implementation
  (`module github.com/Eyevinn/locmaf`); the locmaf.dev site moved to
  `web/`, carved out of the Go module by a stub `web/go.mod`.
- Site and slides rewritten for wire v0.3 (element types, vi64,
  packaging framing, no IANA actions) and shortened.

[0.2.0]: https://github.com/Eyevinn/locmaf/releases/tag/v0.2.0
[0.1.1]: https://github.com/Eyevinn/locmaf/releases/tag/v0.1.1
[0.1.0]: https://github.com/Eyevinn/locmaf/releases/tag/v0.1.0