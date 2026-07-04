# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
Each release notes which LOCMAF packaging version (`locmafVersion`) it implements.

## [Unreleased]

Implements LOCMAF packaging version `"0.3"`.

### Added

- `vi64` subpackage: the MOQT (draft-18 §1.4.1) variable-length
  integer encoding plus the zigzag signed variant, stdlib-only.
- The v0.3 codec: `EncodeCanonical` (canonical full/delta object
  encoding with automatic full-header re-anchoring on BMDT
  discontinuities), `Decode` (element sequence, parity-rule property
  parsing, delta and deletion application, effective-value
  expansion), and `ReconstructCanonical` (byte-exact canonical CMAF
  chunk rebuild, including CENC senc/saiz/saio regeneration and the
  omit rule). Ported from moqlivemock's internal v0.2 codec and
  reworked for v0.3 (element types, genBox, full 32-bit
  sample_flags, derived-only delta BMDT, vi64).

### Changed

- Repurposed the repository as the Go reference implementation
  (`module github.com/Eyevinn/locmaf`); the locmaf.dev site moved to
  `web/`, carved out of the Go module by a stub `web/go.mod`.
- Site and slides rewritten for wire v0.3 (element types, vi64,
  packaging framing, no IANA actions) and shortened.
