package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	fb "github.com/modulestf/facts/internal/bundle"
	"github.com/modulestf/facts/internal/facts"
)

// pinnedRegistry returns the registry copy CI fetches. It skips without one,
// except in CI, where a skip would hide the check.
func pinnedRegistry(t *testing.T) string {
	t.Helper()
	p := os.Getenv(registryEnv)
	if p == "" {
		if os.Getenv("CI") != "" {
			t.Fatalf("%s is not set", registryEnv)
		}
		t.Skipf("%s is not set", registryEnv)
	}
	return p
}

// TestRootHelp checks -h, --help and help print the same root help to
// stdout and exit 0, with the groups and commands in their declared order.
func TestRootHelp(t *testing.T) {
	var want string
	for _, args := range [][]string{{"-h"}, {"--help"}, {"help"}} {
		code, out, errOut := runCLI(args)
		if code != 0 || errOut != "" || out == "" {
			t.Fatalf("%v: exit %d, stderr %q", args, code, errOut)
		}
		if want == "" {
			want = out
		} else if out != want {
			t.Errorf("%v: help differs from -h:\n%s", args, out)
		}
	}
	last := -1
	for _, line := range []string{
		"Check a module:", "  lint ", "  ledger ", "  schema-diff ",
		"Review pipeline (run by hosts):", "  bundle ", "  merge ",
		"Other commands:", "  rules ", "  version ", "  completion ", "  help ",
	} {
		i := strings.Index(want, "\n"+line)
		if i <= last {
			t.Errorf("%q is missing or out of order:\n%s", line, want)
		}
		last = i
	}
}

// TestCommandHelp checks `help <command>` prints the command's --help to
// stdout and exits 0.
func TestCommandHelp(t *testing.T) {
	for _, path := range [][]string{{"lint"}, {"merge"}, {"merge", "check"}} {
		code, out, errOut := runCLI(append([]string{"help"}, path...))
		if code != 0 || errOut != "" || !strings.Contains(out, "Usage:\n  mtf-facts "+strings.Join(path, " ")) {
			t.Errorf("help %v: exit %d, stdout %q, stderr %q", path, code, out, errOut)
		}
		if code, flagOut, _ := runCLI(append(slices.Clone(path), "--help")); code != 0 || flagOut != out {
			t.Errorf("%v --help: exit %d, differs from help %v:\n%s", path, code, path, flagOut)
		}
	}
}

// TestBareUsage checks a bare root or merge prints its usage to stderr and
// exits 2.
func TestBareUsage(t *testing.T) {
	for _, args := range [][]string{{}, {"merge"}} {
		code, out, errOut := runCLI(args)
		if code != exitError || out != "" || !strings.Contains(errOut, "Usage:\n  mtf-facts ") {
			t.Errorf("%v: exit %d, stdout %q, stderr %q", args, code, out, errOut)
		}
	}
}

// TestUsageErrors checks each usage error exits 2 with nothing on stdout,
// the error on stderr and a hint naming the command whose help to read.
func TestUsageErrors(t *testing.T) {
	for _, tc := range []struct {
		args []string
		path string
	}{
		{[]string{"bogus"}, "mtf-facts"},
		{[]string{"merge", "bogus"}, "mtf-facts merge"},
		{[]string{"help", "bogus"}, "mtf-facts help"},
		{[]string{"help", "merge", "nosuch"}, "mtf-facts help"},
		{[]string{"lint", "--bogus", "."}, "mtf-facts lint"},
		{[]string{"lint", "--version", "."}, "mtf-facts lint"},
		{[]string{"lint", "--base", "a", "--diff", "b", "."}, "mtf-facts lint"},
		{[]string{"rules", "extra"}, "mtf-facts rules"},
		{[]string{"version", "extra"}, "mtf-facts version"},
	} {
		code, out, errOut := runCLI(tc.args)
		if code != exitError || out != "" || !strings.HasPrefix(errOut, "mtf-facts: ") ||
			!strings.HasSuffix(errOut, "\nRun '"+tc.path+" --help' for usage.\n") {
			t.Errorf("%v: exit %d, stdout %q, stderr %q", tc.args, code, out, errOut)
		}
	}
}

func TestVersionFlag(t *testing.T) {
	code, flagOut, _ := runCLI([]string{"--version"})
	cmdCode, cmdOut, _ := runCLI([]string{"version"})
	if code != 0 || cmdCode != 0 || flagOut == "" || flagOut != cmdOut {
		t.Errorf("--version exit %d %q, version exit %d %q", code, flagOut, cmdCode, cmdOut)
	}
}

// TestLintParsing runs lint on one tree with the flag forms hosts and users
// write, and the forms it refuses.
func TestLintParsing(t *testing.T) {
	head := "../../testdata/versions-missing-positive/head"
	_, human, _ := runCLI([]string{"lint", head})
	dir := t.TempDir()
	comma, plain := filepath.Join(dir, "a,b.json"), filepath.Join(dir, "c.json")
	for _, p := range []string{comma, plain} {
		if err := os.WriteFile(p, []byte(`{"format_version": "1.0", "provider_schemas": {}}`), 0o600); err != nil { //nolint:gosec // G703: a file in the test temp directory
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name string
		args []string
		code int
		out  string // "json", "human" or "" for none
	}{
		{"flags after the positional", []string{head, "--json"}, exitFindings, "json"},
		{"flag=value", []string{"--timeout=30s", head, "--json"}, exitFindings, "json"},
		{"bool set false", []string{head, "--json=false"}, exitFindings, "human"},
		{"repeated schema files", []string{head, "--json", "--schema-files", comma, "--schema-files", plain}, exitFindings, "json"},
		{"single-dash long flag", []string{head, "-json"}, exitError, ""},
		{"argument after --", []string{"--", head, "--json"}, exitError, ""},
		{"two positionals", []string{head, head}, exitError, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, out, errOut := runCLI(append([]string{"lint"}, tc.args...))
			if code != tc.code {
				t.Fatalf("exit %d, stderr %q", code, errOut)
			}
			var r facts.Report
			switch tc.out {
			case "":
				if out != "" {
					t.Errorf("stdout %q", out)
				}
			case "human":
				if out != human {
					t.Errorf("stdout differs from lint without flags:\n%s", out)
				}
			case "json":
				if err := json.Unmarshal([]byte(out), &r); err != nil {
					t.Fatal(err)
				}
			}
			if slices.Contains(tc.args, "--schema-files") {
				var got []string
				if s := r.Envelope.SchemaInputs; s != nil {
					for _, f := range s.SchemaFiles {
						got = append(got, f.Path)
					}
				}
				if !slices.Equal(got, []string{"a,b.json", "c.json"}) {
					t.Errorf("schema files %v", got)
				}
			}
		})
	}
}

// TestLintDiff runs lint with the change as a diff: the JSON report records
// the diff and the scope, the text report prints the scope and the callers,
// and --full-tree checks every area.
func TestLintDiff(t *testing.T) {
	const dir = "../../testdata/diff-base"
	args := []string{"lint", dir + "/head", "--diff", dir + "/files.json"}
	code, out, errOut := runCLI(append(args, "--json"))
	if code != exitNotEvaluated {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	var r facts.Report
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatal(err)
	}
	if r.Envelope.Inputs.Diff == nil || r.Envelope.Inputs.Diff.Format != facts.DiffFormatGitHubFiles || r.Envelope.Inputs.Base == nil {
		t.Errorf("inputs %+v", r.Envelope.Inputs)
	}
	if r.Scope == nil || r.Scope.Change != facts.ChangeDiff || len(r.Scope.AreasLeftOut) != 1 || r.Callers == nil || len(*r.Callers) != 1 {
		t.Errorf("scope %+v, callers %+v", r.Scope, r.Callers)
	}
	_, human, _ := runCLI(args)
	for _, want := range []string{
		"scope: change from diff; 3 areas checked, 3 calling a changed module, 1 left out: modules/other (--full-tree checks every area)\n",
		"changed module modules/log (inputs, versions): required_providers.aws >= 5.81 -> >= 6.25\n",
		"  called from examples/a/main.tf:2: required_providers.aws >= 5.81, candidates C-001\n",
		"  called from examples/b/main.tf:2: required_providers.aws >= 6.30\n",
	} {
		if !strings.Contains(human, want) {
			t.Errorf("text report lacks %q:\n%s", want, human)
		}
	}
	_, full, _ := runCLI(append(args, "--full-tree"))
	if !strings.Contains(full, "scope: change from diff, full tree; 7 areas checked\n") {
		t.Errorf("full tree:\n%s", full)
	}
	if _, plain, _ := runCLI([]string{"lint", dir + "/head"}); !strings.Contains(plain, "scope: no change; 6 areas checked\n") {
		t.Errorf("no change:\n%s", plain)
	}
}

// TestBundleFullModeWritesNoFile builds the bundle of a report whose tool
// findings alone are over the cap: it prints full and creates no file.
func TestBundleFullModeWritesNoFile(t *testing.T) {
	tmp := t.TempDir()
	p := writeReport(t, tmp)
	data, err := os.ReadFile(p) //nolint:gosec // G703: a file in the test temp directory
	if err != nil {
		t.Fatal(err)
	}
	var r facts.Report
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatal(err)
	}
	f := r.Findings[0]
	f.Summary = strings.Repeat("No description. ", 20)
	for i := range 300 {
		f.ID = fmt.Sprintf("F-%03d", i+2)
		r.Findings = append(r.Findings, f)
	}
	if data, err = json.Marshal(r); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o600); err != nil { //nolint:gosec // G703: a file in the test temp directory
		t.Fatal(err)
	}
	code, out, errOut := runCLI(bundleArgsFor(p, "../../testdata/bundle/skills", tmp))
	if code != 0 || out != "full\n" || !strings.Contains(errOut, "full variant") {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	for _, name := range []string{"bundle.txt", "injection.md"} {
		if _, err := os.Lstat(filepath.Join(tmp, name)); err == nil {
			t.Errorf("full mode created %s", name)
		}
	}
}

// TestHostReplay runs the argument lists hosts run, flags after the
// positionals, and checks each exit code is one the host allows and each
// JSON stdout parses.
func TestHostReplay(t *testing.T) {
	reg := pinnedRegistry(t)
	const profile = "../../testdata/profile"
	ws := "../../testdata/coverage-schema-positive"
	handoff := filepath.Join(ws, "schema-handoff.json")
	tmp := t.TempDir()
	emptySchema := filepath.Join(tmp, "empty.json")
	if err := os.WriteFile(emptySchema, []byte(`{"format_version": "1.0", "provider_schemas": {}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// replay runs args and returns stdout; the exit code must be one of codes
	// and stdout must parse as JSON unless asJSON is false.
	replay := func(t *testing.T, args []string, asJSON bool, codes ...int) string {
		t.Helper()
		code, out, errOut := runCLI(args)
		if !slices.Contains(codes, code) {
			t.Fatalf("%v: exit %d, stderr %q", args, code, errOut)
		}
		if asJSON && !json.Valid([]byte(out)) {
			t.Fatalf("%v: stdout is not JSON: %q", args, out)
		}
		return out
	}

	report := filepath.Join(tmp, "lint.json")
	out := replay(t, []string{"lint", filepath.Join(ws, "head"), "--base", filepath.Join(ws, "base"), "--json", "--timeout", "90s",
		"--registry", reg, "--profile-dir", profile, "--schema-handoff", handoff, "--head-sha", testHead, "--merge-base-sha", testBase},
		true, exitClean, exitFindings, exitNotEvaluated)
	if err := os.WriteFile(report, []byte(out), 0o600); err != nil {
		t.Fatal(err)
	}

	binary, err := binarySHA256()
	if err != nil {
		t.Fatal(err)
	}
	bundleFile, injection := filepath.Join(tmp, "bundle.txt"), filepath.Join(tmp, "injection.md")
	if out := replay(t, []string{"bundle", "--report", report, "--skills", writeSkills(t, reg), "--out", bundleFile, "--injection", injection,
		"--schema-handoff", handoff, "--profile-dir", profile, "--head-sha", testHead, "--merge-base-sha", testBase, "--binary-sha256", binary},
		false, 0); out != "judgment\n" {
		t.Fatalf("bundle mode %q", out)
	}
	f, err := os.Open(bundleFile) //nolint:gosec // G703: a file in the test temp directory
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	b, err := fb.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(injection); err != nil {
		t.Fatal(err)
	}

	var ds []string
	for _, c := range b.Candidates {
		ds = append(ds, fmt.Sprintf(`{"candidate_id":%q,"decision":"reject","reason":"r"}`, c.CandidateID))
	}
	reply, repair := filepath.Join(tmp, "reply.json"), filepath.Join(tmp, "repair.json")
	if err := os.WriteFile(reply, []byte(`{"candidate_decisions":[`+strings.Join(ds, ",")+`],"findings":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(repair, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	replay(t, []string{"merge", "check", bundleFile, reply}, true, exitReplyValid, exitReplyRepair)
	replay(t, []string{"merge", "final", "--registry", reg, "--report", report, bundleFile, reply}, true, 0)
	replay(t, []string{"merge", "final", "--registry", reg, "--report", report, bundleFile, reply, repair}, true, 0)
	replay(t, []string{"merge", "counts", bundleFile, reply}, true, 0)
	replay(t, []string{"merge", "counts", bundleFile, reply, repair}, true, 0)

	replay(t, []string{"lint", "../../testdata/variable-untyped-base/head", "--base", "../../testdata/variable-untyped-base/base",
		"--json", "--timeout", "60s", "--registry", reg, "--profile-dir", profile, "--schema-files", emptySchema,
		"--head-sha", testHead, "--merge-base-sha", testBase}, true, exitClean, exitFindings, exitNotEvaluated)
	replay(t, []string{"ledger", ledgerModule, "--schema-files", ledgerSchema, "--schema-files", emptySchema,
		"--check", "../../testdata/ledger/valid.json", "--registry", reg}, true, exitClean, exitFindings)
	td := "../../testdata/schema-diff"
	replay(t, []string{"schema-diff", "--from", filepath.Join(td, "from.json"), "--to", filepath.Join(td, "to.json"),
		"--types-from", filepath.Join(td, "module")}, true, 0)
	replay(t, []string{"rules"}, false, 0)
}

// rootHelp is the root help the design fixes.
const rootHelp = `mtf-facts reports deterministic facts about Terraform module trees.
It reads files only: no network, no Terraform, no other program.

Usage:
  mtf-facts <command> [flags]

Check a module:
  lint         Check a module tree, or only the areas a change touched
  ledger       List the provider fields of each type, or check a coverage ledger
  schema-diff  Compare two provider schemas for the types a module uses

Review pipeline (run by hosts):
  bundle       Build the facts bundle a model reviewer reads
  merge        Check a model reply and merge it into the final findings

Other commands:
  rules        List the implemented rules and their tier
  version      Print the binary version
  completion   Generate a shell completion script
  help         Help about any command

Flags:
  -h, --help      help for mtf-facts
      --version   print the binary version

Examples:
  mtf-facts lint ./my-module
  mtf-facts lint ./my-module --base ../base-tree --json > report.json
  mtf-facts ledger ./my-module --schema-files schema.json --check ledger.json

Run 'mtf-facts <command> --help' for its flags and exit codes.
`

// TestRootHelpGolden pins the root help byte for byte.
func TestRootHelpGolden(t *testing.T) {
	if code, out, _ := runCLI([]string{"--help"}); code != 0 || out != rootHelp {
		t.Errorf("exit %d, root help:\n%s", code, out)
	}
}

// TestCompletion checks a shell script goes to stdout with exit 0, and a
// bare or unknown completion command exits 2 with nothing on stdout.
func TestCompletion(t *testing.T) {
	if code, out, _ := runCLI([]string{"completion", "bash"}); code != 0 || !strings.HasPrefix(out, "# bash completion") {
		t.Errorf("completion bash: exit %d, stdout %.40q", code, out)
	}
	for _, args := range [][]string{{"completion"}, {"completion", "bogus"}, {"completion", "bash", "extra"}} {
		if code, out, errOut := runCLI(args); code != 2 || out != "" || errOut == "" {
			t.Errorf("%v: exit %d, stdout %q, stderr %q", args, code, out, errOut)
		}
	}
}

// TestCommandTreeSilencedWithArgs checks every command leaves its errors and
// usage to run and validates its arguments.
func TestCommandTreeSilencedWithArgs(t *testing.T) {
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		if !c.SilenceErrors || !c.SilenceUsage {
			t.Errorf("%s: not silenced", c.CommandPath())
		}
		if c.Runnable() && c.Args == nil {
			t.Errorf("%s: no argument validator", c.CommandPath())
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(newRootCmd(io.Discard, io.Discard))
}
