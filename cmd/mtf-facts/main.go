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
  mtf-facts ledger <dir> --schema-files <file>... [--check <ledger>]
                   [--registry <rule-facts.md>]
                                  list the provider fields of each type the
                                  module uses, nested fields included; with
                                  --check, exit 1 unless the ledger gives each
                                  leaf field exactly one disposition; the
                                  registry hash is null without --registry
  mtf-facts rules                 list the implemented rules and their tier
  mtf-facts version               print the binary version
  mtf-facts schema-diff --from <file> --to <file> --types-from <dir>
                                  compare two provider schema files for the
                                  types a module tree uses
  mtf-facts merge check <bundle> <reply>
  mtf-facts merge final --registry <rule-facts.md> [--report <lint.json>]
                        [--templates <file>] <bundle> <reply> [<repair-reply>]
                                  check a model reply, or merge it with the
                                  bundle into the final findings
  mtf-facts bundle --report <lint.json> --skills <dir> --out <file>
                   --injection <file> ...
                                  build the facts bundle; see mtf-facts bundle -h
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
		return ledgerCmd(args[1:], stdout, stderr)
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
