package facts

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func statusOf(m []ManifestEntry, rule, area string) string {
	for _, e := range m {
		if e.RuleID == rule && e.Area == area {
			if e.Reason != "" {
				return e.Status + ": " + e.Reason
			}
			return e.Status
		}
	}
	return "missing"
}

func runHead(t *testing.T, files map[string]string) *Report {
	t.Helper()
	r, err := Run(Options{Head: writeTree(t, files), Limits: DefaultLimits})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// Links are built here rather than committed under testdata/.
func TestVersionsMissingLink(t *testing.T) {
	r := runHead(t, map[string]string{
		"examples/a/versions.tf": "terraform {}\n",
		"examples/b/main.tf":     "",
		"examples/b/versions.tf": "->../a/versions.tf",
		"examples/c":             "->a",
	})
	if s := statusOf(r.Manifest, "examples.versions-missing", "examples/b"); s != "not_evaluated: versions.tf is a symbolic link" {
		t.Error(s)
	}
	if s := statusOf(r.Manifest, "examples.versions-missing", "examples/c"); s != "not_evaluated: examples/c is a symbolic link, not a directory" {
		t.Error(s)
	}
	if s := statusOf(r.Manifest, "structure.variable-untyped", "examples/b"); s != "not_evaluated: versions.tf is a symbolic link" {
		t.Error(s)
	}
	if s := statusOf(r.Manifest, "examples.versions-missing", "examples/a"); s != "clean" {
		t.Error(s)
	}
}

func TestDeepNestingStaysInItsArea(t *testing.T) {
	// A file over the nesting limit affects only its own area.
	r := runHead(t, map[string]string{
		"modules/deep/main.tf": "locals {\n  x = " + strings.Repeat("(", 200) + "1" + strings.Repeat(")", 200) + "\n}\n",
		"modules/ok/main.tf":   "variable \"x\" {}\n",
	})
	if s := statusOf(r.Manifest, "structure.variable-untyped", "modules/deep"); !strings.HasPrefix(s, "not_evaluated: main.tf: nesting depth 201 ") {
		t.Error(s)
	}
	if s := statusOf(r.Manifest, "structure.variable-untyped", "modules/ok"); s != "candidate" {
		t.Error(s)
	}
}

func TestEveryRuleHasGoldenCases(t *testing.T) {
	for _, r := range Rules {
		if r.Tier != "tool" && r.Tier != "candidate" {
			t.Errorf("%s: tier %q", r.ID, r.Tier)
		}
		slug := r.ID[strings.Index(r.ID, ".")+1:]
		for _, kind := range []string{"positive", "negative"} {
			if _, err := os.Stat(filepath.Join("../../testdata", slug+"-"+kind, "expected.json")); err != nil {
				t.Errorf("%s: no %s golden case", r.ID, kind)
			}
		}
	}
	if !sort.SliceIsSorted(Rules, func(i, j int) bool { return Rules[i].ID < Rules[j].ID }) {
		t.Error("Rules are not sorted by id")
	}
}

func TestVariableEvidenceCap(t *testing.T) {
	long := "object({" + strings.Repeat(" a = any,", 200) + " })"
	r := runHead(t, map[string]string{"variables.tf": "variable \"big\" {\n  type = " + long + "\n}\n"})
	if len(r.Candidates) != 1 {
		t.Fatalf("candidates %+v", r.Candidates)
	}
	c := r.Candidates[0]
	if ev := c.Evidence.(VariableEvidence); c.EvidenceComplete || len(*ev.Type) != maxEvidenceText {
		t.Errorf("cap did not bite: complete=%v len=%d", c.EvidenceComplete, len(*ev.Type))
	}
}
