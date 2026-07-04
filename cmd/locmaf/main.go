// Command locmaf is the LOCMAF reference tooling: a CMAF/LOCMAF
// round-trip aligner and the golden-vector corpus generator/checker.
//
// Usage:
//
//	locmaf align [-init init.mp4] [-report text|json] input.cmaf
//	locmaf vectors gen [-out dir]
//	locmaf vectors check [dir]
//	locmaf -version
//
// Exit codes: 0 success, 1 findings (misalignment or corpus drift),
// 2 usage or I/O error.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/Eyevinn/locmaf/internal"
)

// Subcommand and format names, shared with the tests.
const (
	cmdAlign   = "align"
	cmdVectors = "vectors"
	cmdCheck   = "check"
	cmdGen     = "gen"

	formatText = "text"
	formatJSON = "json"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) < 1 {
		usage(stderr)
		return 2
	}
	switch args[0] {
	case cmdAlign:
		return runAlign(args[1:], stdout, stderr)
	case cmdVectors:
		return runVectors(args[1:], stdout, stderr)
	case "-version", "--version", "version":
		fmt.Fprintf(stdout, "locmaf %s\n", internal.GetVersion())
		return 0
	case "-h", "--help", "help":
		usage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown subcommand %q\n", args[0])
		usage(stderr)
		return 2
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `locmaf — LOCMAF reference tooling

Subcommands:
  align [-init init.mp4] [-report text|json] input.cmaf
        Verify that canonical reconstruction straight from the source
        CMAF equals the encode→decode→reconstruct round trip,
        byte-identically, per fragment. Reports how the canonical form
        differs from the source bytes (expected normalizations).
  vectors gen [-out dir]
        Derive the golden-vector corpus from the codec (default
        testdata/vectors).
  vectors check [dir]
        Re-derive the corpus and byte-compare against disk.

Exit codes: 0 success, 1 findings, 2 usage or I/O error.

Options:
  -version
        Display version information and exit.
`)
}
