package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/Eyevinn/locmaf/internal/vectorgen"
)

func runVectors(args []string, stdout, stderr io.Writer) int {
	if len(args) < 1 {
		fmt.Fprintln(stderr, "vectors: missing subcommand (gen or check)")
		return 2
	}
	switch args[0] {
	case cmdGen:
		fs := flag.NewFlagSet("vectors gen", flag.ContinueOnError)
		fs.SetOutput(stderr)
		out := fs.String("out", "testdata/vectors", "output directory for the corpus")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		if err := vectorgen.Generate(*out); err != nil {
			fmt.Fprintf(stderr, "vectors gen: %v\n", err)
			return 2
		}
		fmt.Fprintf(stdout, "corpus written to %s\n", *out)
		return 0

	case cmdCheck:
		fs := flag.NewFlagSet("vectors check", flag.ContinueOnError)
		fs.SetOutput(stderr)
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		dir := "testdata/vectors"
		if fs.NArg() > 0 {
			dir = fs.Arg(0)
		}
		problems, err := vectorgen.Check(dir)
		if err != nil {
			fmt.Fprintf(stderr, "vectors check: %v\n", err)
			return 2
		}
		if len(problems) > 0 {
			for _, p := range problems {
				fmt.Fprintln(stdout, p)
			}
			fmt.Fprintf(stdout, "%d problem(s)\n", len(problems))
			return 1
		}
		fmt.Fprintf(stdout, "corpus matches the codec (%s)\n", dir)
		return 0

	default:
		fmt.Fprintf(stderr, "vectors: unknown subcommand %q (want gen or check)\n", args[0])
		return 2
	}
}
