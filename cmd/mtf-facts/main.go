// Command mtf-facts reports deterministic facts about Terraform module trees.
// It reads files only: no network, no Terraform, no other program.
package main

import (
	"fmt"
	"io"
	"os"
	"runtime/debug"
)

const usage = `usage:
  mtf-facts lint <dir> [--base <dir>] [--json] [--registry <rule-facts.md>]
                 [--head-sha <sha>] [--merge-base-sha <sha>]
                 [--schema-handoff <file>] [--schema-files <file>]...
                 [--profile-dir <dir>]
                                  check a module tree; with --base, only the
                                  areas the change touched
  mtf-facts rules                 list the implemented rules and their tier
  mtf-facts version               print the binary version
  mtf-facts schema-diff --from <file> --to <file> --types-from <dir>
                                  compare two provider schema files for the
                                  types a module tree uses
  mtf-facts bundle | merge | ledger
                                  not implemented yet; exit 2
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return exitError
	}
	switch args[0] {
	case "rules":
		return rules(stdout)
	case "version":
		fmt.Fprintln(stdout, version())
		return 0
	case "lint":
		return lint(args[1:], stdout, stderr)
	case "bundle":
		return bundle(args[1:], stdout, stderr)
	case "merge":
		return merge(args[1:], stdout, stderr)
	case "ledger":
		return ledger(args[1:], stdout, stderr)
	case "schema-diff":
		return schemaDiff(args[1:], stdout, stderr)
	}
	fmt.Fprint(stderr, usage)
	return exitError
}

func version() string {
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" {
		return bi.Main.Version
	}
	return "(devel)"
}

// notImplemented is the result of a subcommand that has a name but no code yet.
func notImplemented(name string, stderr io.Writer) int {
	fmt.Fprintf(stderr, "mtf-facts: %s is not implemented yet\n", name)
	return exitError
}
