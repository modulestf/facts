// Command mtf-facts reports deterministic facts about Terraform module trees.
// It reads files only: no network, no Terraform, no other program.
package main

import (
	"fmt"
	"io"
	"os"
	"runtime/debug"

	"github.com/modulestf/facts/internal/facts"
)

const usage = `usage:
  mtf-facts rules                 list the implemented rules and their tier
  mtf-facts version               print the binary version
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "rules":
		for _, r := range facts.Rules {
			fmt.Fprintf(stdout, "%s\t%s\n", r.ID, r.Tier)
		}
		return 0
	case "version":
		fmt.Fprintln(stdout, version())
		return 0
	}
	fmt.Fprint(stderr, usage)
	return 2
}

func version() string {
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" {
		return bi.Main.Version
	}
	return "(devel)"
}
