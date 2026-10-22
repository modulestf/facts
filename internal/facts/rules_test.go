package facts

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
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
		"examples/d/main.tf":     "",
		"examples/d/versions.tf": "->missing.tf",
		"examples/e/main.tf":     "",
		"examples/e/versions.tf": "->../../../../outside-the-tree/versions.tf",
	})
	// A link at versions.tf is present and never followed: the path rule is
	// clean whether the target is in the tree, missing, or outside the tree.
	for _, area := range []string{"examples/b", "examples/d", "examples/e"} {
		if s := statusOf(r.Manifest, "examples.versions-missing", area); s != "clean" {
			t.Errorf("%s: %s", area, s)
		}
	}
	if s := statusOf(r.Manifest, "examples.versions-missing", "examples/c"); s != "not_evaluated: examples/c is a symbolic link, not a directory" {
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

// The structure rules fire in root and submodule areas only, as the
// registry's area_kinds say; in an example area they are not_applicable.
func TestStructureRulesAreaScope(t *testing.T) {
	const untyped = "variable \"x\" {}\n" // no type and no description
	r := runHead(t, map[string]string{
		"variables.tf":            untyped,
		"modules/m/variables.tf":  untyped,
		"examples/e/variables.tf": untyped,
		"examples/e/versions.tf":  "terraform {}\n",
	})
	for _, tt := range []struct {
		area, description, untyped string
	}{
		{area: ".", description: "finding", untyped: "candidate"},
		{area: "modules/m", description: "finding", untyped: "candidate"},
		{area: "examples/e", description: "not_applicable", untyped: "not_applicable"},
	} {
		if s := statusOf(r.Manifest, "structure.missing-description", tt.area); s != tt.description {
			t.Errorf("missing-description %s: %s", tt.area, s)
		}
		if s := statusOf(r.Manifest, "structure.variable-untyped", tt.area); s != tt.untyped {
			t.Errorf("variable-untyped %s: %s", tt.area, s)
		}
	}
	for _, f := range r.Findings {
		if strings.HasPrefix(f.File, "examples/") {
			t.Errorf("finding in an example area: %+v", f)
		}
	}
	for _, c := range r.Candidates {
		if strings.HasPrefix(c.File, "examples/") {
			t.Errorf("candidate in an example area: %+v", c)
		}
	}
	if len(r.Findings) != 2 || len(r.Candidates) != 2 {
		t.Errorf("want 2 findings and 2 candidates, got %d and %d", len(r.Findings), len(r.Candidates))
	}
}

// Every rule has a positive case that yields its finding or candidate and a
// negative case that yields neither. A case is the golden case named after
// the rule under testdata/, which must then yield or not yield it, or an
// oracle fixture: one that yields the rule is positive, one that lists the
// rule clean somewhere and yields it nowhere is negative.
func TestEveryRuleHasGoldenCases(t *testing.T) {
	yields := func(v goldenView, id string) bool {
		return slices.ContainsFunc(v.Findings, func(f Finding) bool { return f.RuleID == id }) ||
			slices.ContainsFunc(v.Candidates, func(c Candidate) bool { return c.RuleID == id })
	}
	var oracles []goldenView
	for _, dir := range oracleFixtures(t) {
		oracles = append(oracles, readOracle(t, dir))
	}
	for _, r := range Rules {
		if r.Tier != "tool" && r.Tier != "candidate" {
			t.Errorf("%s: tier %q", r.ID, r.Tier)
		}
		slug := r.ID[strings.Index(r.ID, ".")+1:]
		has := map[bool]bool{}
		for kind, want := range map[string]bool{"positive": true, "negative": false} {
			data, err := os.ReadFile(filepath.Join("../../testdata", slug+"-"+kind, "expected.json"))
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			var g goldenView
			if err := json.Unmarshal(data, &g); err != nil {
				t.Fatal(err)
			}
			if got := yields(g, r.ID); got != want {
				t.Errorf("%s: %s case yields the rule: %v", r.ID, kind, got)
				continue
			}
			has[want] = true
		}
		for _, o := range oracles {
			switch {
			case yields(o, r.ID):
				has[true] = true
			case slices.ContainsFunc(o.Manifest, func(e ManifestEntry) bool { return e.RuleID == r.ID && e.Status == StatusClean }):
				has[false] = true
			}
		}
		if !has[true] {
			t.Errorf("%s: no positive golden or oracle case", r.ID)
		}
		if !has[false] {
			t.Errorf("%s: no negative golden or oracle case", r.ID)
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
	if ev := c.Evidence; c.EvidenceComplete || len(*ev.Head) != maxEvidenceText || ev.Base != nil {
		t.Errorf("cap did not bite: complete=%v len=%d base=%v", c.EvidenceComplete, len(*ev.Head), ev.Base)
	}
}

func TestRulesSortedWithScope(t *testing.T) {
	scopes := []string{ScopeTouchedBlock, ScopeTouchedExpression, ScopeNewlyIntroduced, ScopeAnyFileInArea}
	for i, r := range Rules {
		if i > 0 && Rules[i-1].ID >= r.ID {
			t.Errorf("Rules not sorted by id at %s", r.ID)
		}
		if !slices.Contains(scopes, r.Scope) {
			t.Errorf("%s: scope %q", r.ID, r.Scope)
		}
	}
}

func TestJoinRulesRefusesDuplicateID(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("no panic on a duplicate id")
		}
	}()
	joinRules(p1Rules, p1Rules[:1])
}

func TestVariableEvidenceSplitsBlockComment(t *testing.T) {
	r := runHead(t, map[string]string{"variables.tf": "variable \"v\" {\n  /* One,\r\n     two. */\n  type = any\n}\n"})
	if len(r.Candidates) != 1 {
		t.Fatalf("candidates %+v", r.Candidates)
	}
	want := []SourceLine{
		{Revision: "head", File: "variables.tf", Line: 2, Text: "/* One,"},
		{Revision: "head", File: "variables.tf", Line: 3, Text: "     two. */"},
	}
	if got := r.Candidates[0].Evidence.SourceLines; !reflect.DeepEqual(got, want) {
		t.Errorf("source lines\n got %+v\nwant %+v", got, want)
	}
}

func TestVariableEvidenceCapsCommentLines(t *testing.T) {
	r := runHead(t, map[string]string{"variables.tf": "variable \"v\" {\n  /*" + strings.Repeat("\n x", 20) + " */\n  type = any\n}\n"})
	c := r.Candidates[0]
	if c.EvidenceComplete || len(c.Evidence.SourceLines) != maxEvidenceComments {
		t.Errorf("complete=%v lines=%d", c.EvidenceComplete, len(c.Evidence.SourceLines))
	}
}
