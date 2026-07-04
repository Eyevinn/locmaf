# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this repo is

The reference implementation of **LOCMAF** (Low Overhead CMAF for MOQ), a compact CMAF packaging for MoQ Transport:

- **Go module** `github.com/Eyevinn/locmaf` at the repo root — the codec (`package locmaf`) and the `vi64/` varint subpackage (stdlib-only). The `cmd/locmaf` CLI provides `align` (CMAF round-trip conformance) and `vectors gen`/`vectors check` (the `testdata/vectors` golden corpus, derived from the codec by `internal/vectorgen` and pinned by CI — regenerate with `vectors gen` after any wire or canonical change). `verify` and `dump` subcommands are planned.
- **`web/`** — the locmaf.dev explainer site and MARP slide deck. It is a separate stub Go module (`web/go.mod`) so Go tooling and the module zip ignore it. See `web/CLAUDE.md` for site guidance (build, theme, diagram color conventions).

## Spec source of truth

The Internet-Draft `draft-einarsson-moq-locmaf` — source in `Eyevinn/locmaf-id`, published via the datatracker. **One packaging version at a time**: this module implements exactly one `locmafVersion` (currently `"0.3"`); older packaging versions are reachable via old module tags, not runtime switches. Cite the version-independent draft URL, never a pinned revision.

## Commands

```sh
go build ./...        # from the repo root (or the moq-workspace root via go.work)
go test ./...
make lint             # golangci-lint run
cd web && npm run build   # build the site; deploy with ./update_site.sh from the root
```

This repo is normally checked out inside the moq-workspace Go workspace (`../go.work` has `use ./locmaf`), so moqlivemock picks up local changes instantly. Releases are plain semver tags `vX.Y.Z` at the root, reserved for the Go module; `CHANGELOG.md` records which `locmafVersion` each release implements.

## On a packaging-version bump

Four places move together: the `locmaf.Version` constant, the moqlivemock catalog gate, the msf-catalog-validator CUE pin, and the locmaf.dev version strings (see `web/`).
