package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/Eyevinn/locmaf/conform"
)

func runAlign(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("align", flag.ContinueOnError)
	fs.SetOutput(stderr)
	initPath := fs.String("init", "", "separate init segment (ftyp+moov) when the input carries none")
	format := fs.String("report", formatText, "report format: text or json")
	canonOut := fs.String("canon-out", "", "write the canonical CMAF bytes to this path (\"-\" for stdout); only when every chunk aligns")
	bytesFlag := fs.Bool("bytes", false, "add the raw first-differing-byte hex window (offsets can mislead once box sizes change)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "align: exactly one input file expected")
		return 2
	}
	if *format != formatText && *format != formatJSON {
		fmt.Fprintf(stderr, "align: unknown report format %q\n", *format)
		return 2
	}

	report, canon, err := alignFile(fs.Arg(0), *initPath, *canonOut != "", *bytesFlag)
	if err != nil {
		fmt.Fprintf(stderr, "align: %v\n", err)
		return 2
	}

	// When the canonical bytes go to stdout, keep the report off stdout so
	// the binary and text streams do not interleave.
	reportOut := stdout
	if *canonOut == "-" {
		reportOut = stderr
	}
	if *format == formatJSON {
		enc := json.NewEncoder(reportOut)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			fmt.Fprintf(stderr, "align: %v\n", err)
			return 2
		}
	} else {
		printTextReport(reportOut, report)
	}

	if *canonOut != "" {
		switch {
		case report.Diverged > 0:
			fmt.Fprintf(stderr, "align: not writing canonical output; %d chunk(s) diverged\n", report.Diverged)
		case *canonOut == "-":
			if _, err := stdout.Write(canon); err != nil {
				fmt.Fprintf(stderr, "align: %v\n", err)
				return 2
			}
		default:
			if err := os.WriteFile(*canonOut, canon, 0o644); err != nil {
				fmt.Fprintf(stderr, "align: %v\n", err)
				return 2
			}
		}
	}

	if report.Diverged > 0 {
		return 1
	}
	return 0
}

// alignFile loads a fragmented CMAF file (with an optional separate init)
// and runs the shared align check over it.
func alignFile(inputPath, initPath string, collectCanon, wantBytes bool) (*conform.AlignReport, []byte, error) {
	lc, err := loadCMAF(inputPath, initPath)
	if err != nil {
		return nil, nil, err
	}
	report, canon, err := conform.Align(lc, collectCanon, wantBytes)
	if err != nil {
		return nil, nil, err
	}
	report.Input = inputPath
	return report, canon, nil
}

func printTextReport(w io.Writer, r *conform.AlignReport) {
	fmt.Fprintf(w, "%s: %d chunk(s), %d aligned, %d diverged\n", r.Input, len(r.Chunks), r.Aligned, r.Diverged)
	for _, c := range r.Chunks {
		switch {
		case c.Error != "":
			fmt.Fprintf(w, "  g%03d o%03d: DIVERGED — %s\n", c.Group, c.Object, c.Error)
		case c.SourceIdentical:
			fmt.Fprintf(w, "  g%03d o%03d: aligned; source already canonical (%d bytes, %d on the wire)\n",
				c.Group, c.Object, c.SourceBytes, c.WireBytes)
		default:
			fmt.Fprintf(w, "  g%03d o%03d: aligned; %d source bytes → %d wire bytes; normalizations:\n",
				c.Group, c.Object, c.SourceBytes, c.WireBytes)
			for _, n := range c.Normalizations {
				fmt.Fprintf(w, "    - %s\n", n)
			}
			if c.FirstDiff != nil {
				fmt.Fprintf(w, "    first diff at offset %d\n      source %s\n      canon  %s\n",
					c.FirstDiff.Offset, c.FirstDiff.SourceHex, c.FirstDiff.CanonHex)
			}
		}
	}
}
