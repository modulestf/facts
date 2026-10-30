package main

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/modulestf/facts/internal/facts"
)

// TestAffected runs affected with the change as a files list, a unified diff
// and a base tree: each lists the touched example, then its callers.
func TestAffected(t *testing.T) {
	const dir = "../../testdata/diff-base"
	want := []facts.AffectedExample{{Dir: "examples/c", Reason: "touched"}, {Dir: "examples/a", Reason: "caller"}, {Dir: "examples/b", Reason: "caller"}}
	for _, change := range [][]string{
		{"--diff", dir + "/files.json"},
		{"--diff", dir + "/change.diff"},
		{"--base", dir + "/base"},
	} {
		code, out, errOut := runCLI(append([]string{"affected", dir + "/head", "--json"}, change...))
		if code != exitClean {
			t.Fatalf("%v: exit %d, stderr %q", change, code, errOut)
		}
		var r facts.AffectedReport
		if err := json.Unmarshal([]byte(out), &r); err != nil {
			t.Fatal(err)
		}
		if r.SchemaVersion != 1 || r.Inert || r.Fallback != nil || !slices.Equal(r.Examples, want) || !slices.Contains(r.Changed, "examples/old/main.tf") {
			t.Errorf("%v: report %+v", change, r)
		}
	}
	code, human, _ := runCLI([]string{"affected", dir + "/head", "--base", dir + "/base"})
	if code != exitClean || !strings.Contains(human, "touched examples/c\ncaller examples/a\ncaller examples/b\n") ||
		!strings.Contains(human, "inert: false, fallback: none, 10 changed paths\n") {
		t.Errorf("exit %d, text report:\n%s", code, human)
	}
}

func TestAffectedFallbackAndErrors(t *testing.T) {
	const dir = "../../testdata/diff-base"
	code, out, _ := runCLI([]string{"affected", dir + "/head", "--json"})
	var r facts.AffectedReport
	if err := json.Unmarshal([]byte(out), &r); err != nil || code != exitClean || r.Fallback == nil || *r.Fallback != facts.FallbackNoChangeInput || len(r.Examples) != 3 {
		t.Errorf("no change input: exit %d, %+v, %v", code, r, err)
	}
	for _, args := range [][]string{
		{"affected", dir + "/head", "--base", dir + "/base", "--diff", dir + "/files.json"},
		{"affected", dir + "/head", "--timeout", "0s"},
		{"affected"},
	} {
		if code, out, errOut := runCLI(args); code != exitError || out != "" || !strings.HasPrefix(errOut, "mtf-facts: ") {
			t.Errorf("%v: exit %d, stdout %q, stderr %q", args, code, out, errOut)
		}
	}
	if code, out, errOut := runCLI([]string{"affected", dir + "/nosuch", "--json"}); code != exitError || out != "" || errOut == "" {
		t.Errorf("missing tree: exit %d, stdout %q, stderr %q", code, out, errOut)
	}
}
