# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
Each release notes which LOCMAF wire version (`locmafVersion`) it implements.

## [Unreleased]

### Changed

- Repurposed the repository as the Go reference implementation
  (`module github.com/Eyevinn/locmaf`); the locmaf.dev site moved to
  `web/`, carved out of the Go module by a stub `web/go.mod`.
- Site and slides rewritten for wire v0.3 (element types, vi64,
  packaging framing, no IANA actions) and shortened.
