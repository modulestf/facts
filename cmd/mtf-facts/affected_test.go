package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/modulestf/facts/internal/facts"
)

// TestAffected runs affected with the change as a files list, a unified diff
// and a base tree: each lists the touched example, then its callers.
func TestAffected(t *testing.T) {
	t.Parallel()
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

// TestAffectedModuleRoot runs affected on a repository root with a module
// root: the report is repository-relative, names the module root, and an
// unresolved repository-root example says so in both forms. A module root
// that is not a directory of the tree is an error.
func TestAffectedModuleRoot(t *testing.T) {
	dir := t.TempDir()
	for p, body := range map[string]string{
		"head/modules/a/main.tf":              "variable \"v\" {}\n",
		"head/modules/a/examples/own/main.tf": "module \"a\" {\n  source = \"../../\"\n}\n",
		"head/examples/calls/main.tf":         "module \"a\" {\n  source = \"../../modules/a\"\n}\n",
		"head/examples/odd/main.tf":           "variable \"s\" {}\nmodule \"x\" {\n  source = var.s\n}\n",
		"head/examples/other/main.tf":         "locals {}\n",
		"base/modules/a/main.tf":              "variable \"u\" {}\n",
		"base/modules/a/examples/own/main.tf": "module \"a\" {\n  source = \"../../\"\n}\n",
		"base/examples/calls/main.tf":         "module \"a\" {\n  source = \"../../modules/a\"\n}\n",
		"base/examples/odd/main.tf":           "variable \"s\" {}\nmodule \"x\" {\n  source = var.s\n}\n",
		"base/examples/other/main.tf":         "locals {}\n",
	} {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	head, base := filepath.Join(dir, "head"), filepath.Join(dir, "base")
	code, out, errOut := runCLI([]string{"affected", head, "--base", base, "--module-root", "modules/a", "--json"})
	if code != exitClean {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	var r facts.AffectedReport
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatal(err)
	}
	want := []facts.AffectedExample{{Dir: "examples/calls", Reason: "caller"}, {Dir: "modules/a/examples/own", Reason: "caller"},
		{Dir: "examples/odd", Reason: "all", Unresolved: true}}
	if r.ModuleRoot != "modules/a" || r.Inert || r.Fallback != nil || !slices.Equal(r.Examples, want) || !slices.Equal(r.Changed, []string{"modules/a/main.tf"}) {
		t.Errorf("report %+v", r)
	}
	code, human, _ := runCLI([]string{"affected", head, "--base", base, "--module-root", "modules/a"})
	if code != exitClean || !strings.HasPrefix(human, "caller examples/calls\ncaller modules/a/examples/own\nall examples/odd unresolved\n") {
		t.Errorf("exit %d, text report:\n%s", code, human)
	}
	for _, m := range []string{"modules/none", "../head/modules/a", "modules/a/"} {
		if code, out, errOut := runCLI([]string{"affected", head, "--base", base, "--module-root", m, "--json"}); code != exitError || out != "" || !strings.Contains(errOut, "module root") {
			t.Errorf("%q: exit %d, stdout %q, stderr %q", m, code, out, errOut)
		}
	}
}
