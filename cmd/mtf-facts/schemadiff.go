package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"

	"github.com/modulestf/facts/internal/facts"
	"github.com/modulestf/facts/internal/schemadiff"
)

const schemaDiffUsage = "usage: mtf-facts schema-diff --from <file> --to <file> --types-from <dir>\n"

// schemaDiff compares two provider schema files for the types a module tree
// uses and writes the report as JSON. It exits 0 with the report, changes or
// not, and 2 on bad input or a limit.
func schemaDiff(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("schema-diff", flag.ContinueOnError)
	fs.SetOutput(stderr)
	o := schemadiff.Options{Limits: facts.DefaultLimits}
	fs.StringVar(&o.From, "from", "", "terraform providers schema -json file of the current provider version")
	fs.StringVar(&o.To, "to", "", "terraform providers schema -json file of the target provider version")
	fs.StringVar(&o.TypesFrom, "types-from", "", "module tree whose resource and data source types are compared")
	if err := fs.Parse(args); err != nil {
		return exitError
	}
	if fs.NArg() != 0 || o.From == "" || o.To == "" || o.TypesFrom == "" {
		fmt.Fprint(stderr, schemaDiffUsage)
		return exitError
	}
	sum, err := binarySHA256()
	if err != nil {
		fmt.Fprintln(stderr, "mtf-facts:", err)
		return exitError
	}
	o.Tool = schemadiff.Tool{Name: "mtf-facts", Version: version(), BinarySHA256: sum}
	r, err := schemadiff.Run(o)
	if err != nil {
		fmt.Fprintln(stderr, "mtf-facts: schema-diff:", err)
		return exitError
	}
	out, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		fmt.Fprintln(stderr, "mtf-facts:", err)
		return exitError
	}
	if len(out) > o.Limits.MaxOutputBytes {
		fmt.Fprintf(stderr, "mtf-facts: output over %d bytes\n", o.Limits.MaxOutputBytes)
		return exitError
	}
	if _, err := stdout.Write(append(out, '\n')); err != nil {
		return exitError
	}
	return 0
}
