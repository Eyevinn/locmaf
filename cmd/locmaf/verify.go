package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/Eyevinn/locmaf/conform"
)

func runVerify(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	initPath := fs.String("init", "", "separate init segment (ftyp+moov) when the file carries none in-band")
	format := fs.String("report", formatText, "report format: text or json")
	decodable := fs.Bool("decodable", false, "check only that each Object decodes and reconstructs; skip the canonical-bytes check")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "verify: exactly one input file expected")
		return 2
	}
	if *format != formatText && *format != formatJSON {
		fmt.Fprintf(stderr, "verify: unknown report format %q\n", *format)
		return 2
	}

	report, err := verifyFile(fs.Arg(0), *initPath, !*decodable)
	if err != nil {
		fmt.Fprintf(stderr, "verify: %v\n", err)
		return 2
	}

	if *format == formatJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			fmt.Fprintf(stderr, "verify: %v\n", err)
			return 2
		}
	} else {
		printVerifyReport(stdout, report)
	}
	if report.NonConformant > 0 {
		return 1
	}
	return 0
}

// verifyFile reads a .locmaf file (and an optional separate init) and runs
// the shared conformance check over its bytes.
func verifyFile(inputPath, initPath string, strict bool) (*conform.VerifyReport, error) {
	data, err := os.ReadFile(inputPath)
	if err != nil {
		return nil, err
	}
	var initBytes []byte
	if initPath != "" {
		if initBytes, err = os.ReadFile(initPath); err != nil {
			return nil, err
		}
	}
	report, err := conform.Verify(data, initBytes, strict)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", inputPath, err)
	}
	report.Input = inputPath
	return report, nil
}

func printVerifyReport(w io.Writer, r *conform.VerifyReport) {
	mode := "strict: canonical"
	if !r.Strict {
		mode = "decodable"
	}
	fmt.Fprintf(w, "%s: %d object(s), %d conformant, %d non-conformant [%s]\n",
		r.Input, r.NumObjects, r.Conformant, r.NonConformant, mode)
	for _, o := range r.Objects {
		switch {
		case o.Error != "":
			fmt.Fprintf(w, "  #%03d %-8s %d bytes: NON-CONFORMANT — %s\n", o.Index, o.Kind, o.WireBytes, o.Error)
		case o.Conformant:
			fmt.Fprintf(w, "  #%03d %-8s %d bytes: ok\n", o.Index, o.Kind, o.WireBytes)
		default:
			fmt.Fprintf(w, "  #%03d %-8s %d bytes: NON-CONFORMANT — not canonical (%d wire vs %d canonical bytes)\n",
				o.Index, o.Kind, o.WireBytes, o.WireBytes, o.CanonBytes)
			if o.FirstDiff != nil {
				fmt.Fprintf(w, "    first diff at offset %d\n      wire   %s\n      canon  %s\n",
					o.FirstDiff.Offset, o.FirstDiff.SourceHex, o.FirstDiff.CanonHex)
			}
		}
	}
}
