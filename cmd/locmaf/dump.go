package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Eyevinn/locmaf/conform"
)

func runDump(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("dump", flag.ContinueOnError)
	fs.SetOutput(stderr)
	initPath := fs.String("init", "", "separate init segment (ftyp+moov) when the file carries none in-band")
	format := fs.String("report", formatText, "report format: text or json")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "dump: exactly one input file expected")
		return 2
	}
	if *format != formatText && *format != formatJSON {
		fmt.Fprintf(stderr, "dump: unknown report format %q\n", *format)
		return 2
	}

	report, err := dumpFile(fs.Arg(0), *initPath)
	if err != nil {
		fmt.Fprintf(stderr, "dump: %v\n", err)
		return 2
	}

	if *format == formatJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			fmt.Fprintf(stderr, "dump: %v\n", err)
			return 2
		}
	} else {
		printDumpReport(stdout, report)
	}
	for _, o := range report.Objects {
		if o.Error != "" {
			return 1
		}
	}
	return 0
}

// dumpFile reads a .locmaf file (and an optional separate init) and walks
// its Objects via the shared conform core.
func dumpFile(inputPath, initPath string) (*conform.DumpReport, error) {
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
	report, err := conform.Dump(data, initBytes)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", inputPath, err)
	}
	report.Input = inputPath
	return report, nil
}

func printDumpReport(w io.Writer, r *conform.DumpReport) {
	fmt.Fprintf(w, "%s: %d object(s)\n", r.Input, r.NumObjects)
	for _, o := range r.Objects {
		switch {
		case o.Error != "":
			fmt.Fprintf(w, "  #%03d %-8s %d bytes: ERROR — %s\n", o.Index, o.Kind, o.WireBytes, o.Error)
		case o.Raw != nil:
			init := ""
			if o.Raw.IsInit {
				init = " (init)"
			}
			fmt.Fprintf(w, "  #%03d %-8s %d bytes: %s%s\n",
				o.Index, o.Kind, o.WireBytes, strings.Join(o.Raw.Boxes, ", "), init)
		case o.Moof != nil:
			gb := ""
			if len(o.Moof.GenBoxes) > 0 {
				gb = "; genBoxes: " + strings.Join(o.Moof.GenBoxes, ", ")
			}
			fmt.Fprintf(w, "  #%03d %-8s %d bytes: %d sample(s), bmdt=%d, %d payload bytes%s\n",
				o.Index, o.Kind, o.WireBytes, o.Moof.SampleCount, o.Moof.BMDT, o.Moof.PayloadBytes, gb)
		}
	}
}
