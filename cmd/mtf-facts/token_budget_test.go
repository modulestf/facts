package main

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/modulestf/facts/internal/facts"
)

// budgetCase is a command whose fixture parses HCL, and the refusal it must
// give when the run's token budget is spent.
type budgetCase struct {
	name    string
	args    []string
	refused func(code int, out, errOut string) bool
}

// budgetCases are the commands that parse a module tree. Each refuses a
// spent budget the way it refuses any file over a parser limit: lint as
// not_evaluated limit_exceeded, behaviour as an unclassified change, depth
// as deep for a file that does not parse, affected as the unparsed fallback, changes as a limit file and
// schema-diff as an error.
func budgetCases() []budgetCase {
	has := func(s string) func(int, string, string) bool {
		return func(_ int, out, _ string) bool { return strings.Contains(out, s) }
	}
	return []budgetCase{
		{"lint", []string{"lint", "../../testdata/variable-untyped-base/head", "--json"},
			has(`"reason": "limit_exceeded"`)},
		{"behaviour", []string{"behaviour", pr556 + "/head", "--base", pr556 + "/base", "--json"},
			has(`"category": "unclassified-change"`)},
		{"depth", []string{"depth", "--head", "../../testdata/diff-base/head", "--base", "../../testdata/diff-base/base", "--files", "../../testdata/diff-base/files.json"},
			func(code int, out, _ string) bool {
				var rep struct {
					Depth   string   `json:"depth"`
					Reasons []string `json:"reasons"`
				}
				return code == exitClean && json.Unmarshal([]byte(out), &rep) == nil && rep.Depth == "deep" &&
					slices.Contains(rep.Reasons, "deep-parse-failure")
			}},
		{"affected", []string{"affected", "../../testdata/diff-base/head", "--base", "../../testdata/diff-base/base", "--json"},
			has(`"fallback": "unparsed"`)},
		{"changes", []string{"changes", "../../testdata/changes/heredoc/head", "--base", "../../testdata/changes/heredoc/base", "--json"},
			has(`"reason": "limit"`)},
	}
}

// withRunTokens sets the CLI's run token budget for one test.
func withRunTokens(t *testing.T, n int64) {
	t.Helper()
	orig := facts.DefaultLimits.MaxRunTokens
	facts.DefaultLimits.MaxRunTokens = n
	t.Cleanup(func() { facts.DefaultLimits.MaxRunTokens = orig })
}

// TestTokenBudgetRefusal spends the budget at the first file: every command
// that parses refuses as it refuses any parser limit, never with a silent
// skip.
func TestTokenBudgetRefusal(t *testing.T) {
	withRunTokens(t, 1)
	for _, c := range budgetCases() {
		code, out, errOut := runCLI(c.args)
		if !c.refused(code, out, errOut) {
			t.Errorf("%s: exit %d, stderr %q, stdout %.600s", c.name, code, errOut, out)
		}
	}
}

// TestTokenBudgetNotHit checks a run within the default budget writes the
// same bytes and exit code as a run with no budget.
func TestTokenBudgetNotHit(t *testing.T) {
	type result struct {
		code     int
		out, err string
	}
	results := map[string]result{}
	for _, c := range budgetCases() {
		code, out, errOut := runCLI(c.args)
		if c.refused(code, out, errOut) {
			t.Fatalf("%s: refused within the default budget: %.600s", c.name, out)
		}
		results[c.name] = result{code, out, errOut}
	}
	withRunTokens(t, 0)
	for _, c := range budgetCases() {
		code, out, errOut := runCLI(c.args)
		if r := results[c.name]; r != (result{code, out, errOut}) {
			t.Errorf("%s: exit %d and %d, stdout differs: %v", c.name, r.code, code, r.out != out)
		}
	}
}
