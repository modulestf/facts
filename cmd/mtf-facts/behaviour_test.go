package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modulestf/facts/internal/behaviour"
)

const pr556 = "../../internal/behaviour/testdata/pr556"

// TestBehaviourCLI runs behaviour on the PR 556 fixture with every change
// form; the output is the golden report byte for byte.
func TestBehaviourCLI(t *testing.T) {
	want, err := os.ReadFile(filepath.Join(pr556, "expected.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range [][]string{
		{"--base", pr556 + "/base"},
		{"--diff", pr556 + "/change-u0.diff"},
		{"--diff", pr556 + "/files.json"},
	} {
		code, out, errOut := runCLI(append([]string{"behaviour", pr556 + "/head", "--json"}, change...))
		if code != exitClean || out != string(want) {
			t.Errorf("%v: exit %d, stderr %q, stdout:\n%s", change, code, errOut, out)
		}
	}
	for _, args := range [][]string{
		{"behaviour", pr556 + "/head", "--json"},
		{"behaviour", pr556 + "/head", "--base", pr556 + "/base"},
		{"behaviour", pr556 + "/head", "--json", "--base", pr556 + "/base", "--diff", pr556 + "/files.json"},
		{"behaviour", pr556 + "/head", "--json", "--base", pr556 + "/base", "--timeout", "0s"},
		{"behaviour", pr556 + "/nosuch", "--json", "--base", pr556 + "/base"},
		{"behaviour", pr556 + "/head", "--json", "--base", pr556 + "/base", "--schema", pr556 + "/nosuch.json"},
	} {
		if code, out, errOut := runCLI(args); code != exitError || out != "" || !strings.HasPrefix(errOut, "mtf-facts: ") {
			t.Errorf("%v: exit %d, stdout %q, stderr %q", args, code, out, errOut)
		}
	}
}

func TestClaimsEvalCLI(t *testing.T) {
	const ex = "../../schema/examples"
	shas := []string{"--head-sha", strings.Repeat("0", 40), "--base-sha", strings.Repeat("1", 40)}
	code, out, errOut := runCLI(append([]string{"claims-eval", "--behaviour", ex + "/behaviour.valid.json",
		"--ladder", ex + "/ladder-record.valid.l1.json", "--claims", ex + "/claims.valid.json", "--json"}, shas...))
	want, err := os.ReadFile(ex + "/claims-eval.valid.json")
	if err != nil {
		t.Fatal(err)
	}
	if code != exitClean || out != string(want) {
		t.Errorf("exit %d, stderr %q, stdout:\n%s", code, errOut, out)
	}
	code, out, _ = runCLI([]string{"claims-eval", "--behaviour", ex + "/behaviour.valid.json", "--ladder", ex + "/ladder-record.valid.json", "--json",
		"--head-sha", strings.Repeat("0", 40), "--base-sha", strings.Repeat("1", 40)})
	var r behaviour.EvalReport
	if err := json.Unmarshal([]byte(out), &r); err != nil || code != exitClean || len(r.Triggers) != 1 || len(r.Claims) != 0 {
		t.Errorf("no claims: exit %d, %v, %s", code, err, out)
	}
	for _, s := range r.Triggers {
		if s.State != behaviour.StateUnverifiable {
			t.Errorf("no ladder run: %s is %s", s.ID, s.State)
		}
	}
	for _, args := range [][]string{
		{"claims-eval", "--behaviour", ex + "/behaviour.valid.json", "--ladder", ex + "/ladder-record.valid.json"},
		{"claims-eval", "--ladder", ex + "/ladder-record.valid.json", "--json"},
		{"claims-eval", "--behaviour", ex + "/behaviour.valid.json", "--ladder", ex + "/ladder-record.invalid.enum.control.json", "--json"},
		{"claims-eval", "--behaviour", ex + "/behaviour.valid.json", "--ladder", ex + "/ladder-record.valid.json", "--claims", ex + "/claims.invalid.const.schema-version.json", "--json"},
		{"claims-eval", "--behaviour", ex + "/claims.valid.json", "--ladder", ex + "/ladder-record.valid.json", "--json"},
		{"claims-eval", "--behaviour", ex + "/behaviour.valid.json", "--ladder", ex + "/ladder-record.valid.l1.json", "--json"},
		{"claims-eval", "--behaviour", ex + "/behaviour.valid.json", "--ladder", ex + "/ladder-record.valid.l1.json", "--json", "--head-sha", strings.Repeat("0", 40)},
		{"claims-eval", "--behaviour", ex + "/behaviour.valid.json", "--ladder", ex + "/ladder-record.valid.json", "--json", "--head-sha", strings.Repeat("2", 40)},
		{"claims-eval", "--behaviour", ex + "/behaviour.valid.json", "--ladder", ex + "/ladder-record.valid.json", "--json", "--base-sha", strings.Repeat("0", 40)},
	} {
		if code, out, errOut := runCLI(args); code != exitError || out != "" || !strings.HasPrefix(errOut, "mtf-facts: ") {
			t.Errorf("%v: exit %d, stdout %q, stderr %q", args, code, out, errOut)
		}
	}
}
