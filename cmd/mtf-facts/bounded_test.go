package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modulestf/facts/internal/facts"
	"github.com/spf13/cobra"
)

// boundedCase is one run of a command that succeeds on its fixture, and the
// exit code it succeeds with.
type boundedCase struct {
	name string
	args []string
	code int
}

// boundedCases are a successful run of every command runBounded holds.
func boundedCases(t *testing.T) []boundedCase {
	t.Helper()
	tmp := t.TempDir()
	const ex = "../../schema/examples"
	sd, ftd, mfx := "../../testdata/schema-diff", "../../testdata/fixture-check", filepath.Join(mergeFixtures, "answered-once-duplicate")
	bundleOut := filepath.Join(tmp, "bundle")
	if err := os.Mkdir(bundleOut, 0o755); err != nil {
		t.Fatal(err)
	}
	cases := []boundedCase{
		{"lint", []string{"lint", "../../testdata/variable-untyped-base/head", "--json"}, exitFindings},
		{"lint text", []string{"lint", "../../testdata/variable-untyped-base/head"}, exitFindings},
		{"affected", []string{"affected", "../../testdata/diff-base/head", "--base", "../../testdata/diff-base/base", "--json"}, exitClean},
		{"depth", []string{"depth", "--head", "../../testdata/diff-base/head", "--base", "../../testdata/diff-base/base", "--files", "../../testdata/diff-base/files.json"}, exitClean},
		{"behaviour", []string{"behaviour", pr556 + "/head", "--base", pr556 + "/base", "--json"}, exitClean},
		{"claims-eval", []string{"claims-eval", "--behaviour", ex + "/behaviour.valid.json", "--ladder", ex + "/ladder-record.valid.json", "--json",
			"--head-sha", strings.Repeat("0", 40), "--base-sha", strings.Repeat("1", 40)}, exitClean},
		{"ledger", []string{"ledger", ledgerModule, "--schema-files", ledgerSchema}, exitClean},
		{"schema-diff", []string{"schema-diff", "--from", filepath.Join(sd, "from.json"), "--to", filepath.Join(sd, "to.json"), "--types-from", filepath.Join(sd, "module")}, exitClean},
		{"fixture-check", []string{"fixture-check", filepath.Join(ftd, "l1"), "--json"}, exitClean},
		{"profile-check", []string{"profile-check", "--profile-dir", "../../testdata/profile"}, exitClean},
		{"bundle", bundleArgsFor(writeReport(t, tmp), "../../testdata/bundle/skills", bundleOut), exitClean},
		{"merge check", []string{"merge", "check", regionFile(t, "answered-once-duplicate"), filepath.Join(mfx, "model-output.repair.json")}, exitReplyValid},
		{"merge counts", []string{"merge", "counts", regionFile(t, "answered-once-duplicate"), filepath.Join(mfx, "model-output.repair.json")}, exitClean},
	}
	// merge final needs the pinned registry, which CI always sets.
	if reg := os.Getenv(registryEnv); reg != "" || os.Getenv("CI") != "" {
		cases = append(cases, boundedCase{"merge final", []string{"merge", "final", "--registry", reg,
			regionFile(t, "answered-once-duplicate"), filepath.Join(mfx, "model-output.repair.json")}, exitClean})
	}
	return cases
}

// TestBoundedCasesSucceed checks each case's fixture: the timeout and cap
// tests below mean nothing for a run that fails anyway.
func TestBoundedCasesSucceed(t *testing.T) {
	for _, c := range boundedCases(t) {
		if code, out, errOut := runCLI(c.args); code != c.code || out == "" {
			t.Errorf("%s: exit %d, stdout %d bytes, stderr %q", c.name, code, len(out), errOut)
		}
	}
}

// TestEveryCommandTimesOut holds each command's worker past --timeout. Every
// command but depth exits 2 with the timeout message and no stdout; depth
// fails closed with the deep-unwalked report and exit 0.
func TestEveryCommandTimesOut(t *testing.T) {
	testHookWork = func() { select {} } // the held worker leaks until the test binary exits
	t.Cleanup(func() { testHookWork = nil })
	for _, c := range boundedCases(t) {
		code, out, errOut := runCLI(append(c.args, "--timeout", "20ms"))
		if c.name == "depth" {
			var rep struct {
				Depth   string   `json:"depth"`
				Reasons []string `json:"reasons"`
			}
			if err := json.Unmarshal([]byte(out), &rep); err != nil || code != exitClean || rep.Depth != "deep" || !strings.Contains(strings.Join(rep.Reasons, ","), "unwalked") {
				t.Errorf("depth: exit %d, stdout %q, stderr %q", code, out, errOut)
			}
			continue
		}
		if code != exitError || out != "" || errOut != "mtf-facts: timeout\n" {
			t.Errorf("%s: exit %d, stdout %q, stderr %q", c.name, code, out, errOut)
		}
	}
}

// TestEveryCommandRefusesNonPositiveTimeout checks the usage error.
func TestEveryCommandRefusesNonPositiveTimeout(t *testing.T) {
	for _, c := range boundedCases(t) {
		code, out, errOut := runCLI(append(c.args, "--timeout", "0s"))
		if code != exitError || out != "" || !strings.Contains(errOut, timeoutNotPositive) {
			t.Errorf("%s: exit %d, stdout %q, stderr %q", c.name, code, out, errOut)
		}
	}
}

// TestEveryCommandCapsOutput lowers the output cap below each command's
// output: the run exits 2 with no stdout, and bundle, whose files the cap
// holds too, writes no file.
func TestEveryCommandCapsOutput(t *testing.T) {
	orig := facts.DefaultLimits.MaxOutputBytes
	facts.DefaultLimits.MaxOutputBytes = 1
	t.Cleanup(func() { facts.DefaultLimits.MaxOutputBytes = orig })
	for _, c := range boundedCases(t) {
		code, out, errOut := runCLI(c.args)
		want := "mtf-facts: output over 1 bytes\n"
		if c.name == "bundle" { // its files are refused before its stdout
			want = "mtf-facts: bundle: a file over 1 bytes\n"
		}
		if code != exitError || out != "" || errOut != want {
			t.Errorf("%s: exit %d, stdout %q, stderr %q", c.name, code, out, errOut)
		}
		if c.name == "bundle" {
			if _, err := os.Lstat(c.args[slices.Index(c.args, "--out")+1]); !os.IsNotExist(err) {
				t.Errorf("bundle wrote its file past the cap: %v", err)
			}
		}
	}
}

// TestRunBoundedSetsDeadline checks runBounded gives the engine a deadline
// of now plus the timeout, and that the engine's own deadline error reads as
// the timeout: exit 2, the timeout message, no stdout.
func TestRunBoundedSetsDeadline(t *testing.T) {
	const timeout = time.Hour
	var lim facts.Limits
	before := time.Now()
	var seen time.Time
	var cmd cobra.Command
	var out, errb bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errb)
	code := runBounded(&cmd, timeout, &lim, func() workResult {
		seen = lim.Deadline
		return workResult{err: fmt.Errorf("read: %w", facts.ErrDeadline), out: []byte("late")}
	}, nil)
	after := time.Now()
	if seen.Before(before.Add(timeout)) || seen.After(after.Add(timeout)) {
		t.Errorf("deadline %v, want within [%v, %v]", seen, before.Add(timeout), after.Add(timeout))
	}
	if code != exitError || out.Len() != 0 || errb.String() != "mtf-facts: timeout\n" {
		t.Errorf("exit %d, stdout %q, stderr %q", code, out.String(), errb.String())
	}
}
