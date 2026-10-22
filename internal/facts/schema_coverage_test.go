package facts

import (
	"path/filepath"
	"slices"
	"testing"
)

// coverageRun runs the coverage-schema-positive case with its handoff and
// the given head SHA.
func coverageRun(t *testing.T, headSHA string, handoff bool) *Report {
	t.Helper()
	dir := "../../testdata/coverage-schema-positive"
	o := Options{Head: filepath.Join(dir, "head"), Base: filepath.Join(dir, "base"), HeadSHA: headSHA, MergeBaseSHA: oracleBaseSHA, Limits: DefaultLimits}
	if handoff {
		o.SchemaHandoff = filepath.Join(dir, "schema-handoff.json")
	}
	r, err := Run(o)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// TestSchemaCoverageFromBoundHandoff checks the coverage of an edited block
// lists only the paths the change adds, with their state at both versions,
// and that an added block lists every path it sets.
func TestSchemaCoverageFromBoundHandoff(t *testing.T) {
	cov := *coverageRun(t, oracleHeadSHA, true).SchemaCoverage
	var addrs []string
	for _, c := range cov {
		addrs = append(addrs, c.Address)
		if c.File != "main.tf" || c.Outcome != "present" || c.ProviderSource != "registry.terraform.io/hashicorp/aws" {
			t.Errorf("%s: %+v", c.Address, c)
		}
	}
	if want := []string{"aws_demo_widget.this", "aws_demo_widget.nested", "data.aws_demo_lookup.this"}; !slices.Equal(addrs, want) {
		t.Fatalf("blocks %v, want %v", addrs, want)
	}
	// The edited block: base already set name, typo_arg and setting.
	want := []CoverageArgument{
		{"fast_path", "added", "present", "absent"},
		{"new_typo", "added", "absent", "absent"},
		{"rule", "added", "present", "present"},
	}
	if !slices.Equal(cov[0].Arguments, want) {
		t.Errorf("edited block %+v, want %+v", cov[0].Arguments, want)
	}
	for _, w := range []CoverageArgument{
		{"old_rule", "added", "deprecated", "deprecated"},
		{"setting.mode", "added", "present", "absent"},
		{"setting.bogus", "added", "absent", "absent"},
		{"rule.color", "added", "absent", "absent"},
	} {
		if !slices.Contains(cov[1].Arguments, w) {
			t.Errorf("added block lacks %+v: %+v", w, cov[1].Arguments)
		}
	}
}

// TestSchemaCoverageRemovedPath lists a path the base block set and the head
// block no longer does as removed.
func TestSchemaCoverageRemovedPath(t *testing.T) {
	head := map[string]string{"main.tf": "resource \"aws_demo_widget\" \"this\" {\n  name = \"a\"\n}\n"}
	base := map[string]string{"main.tf": "resource \"aws_demo_widget\" \"this\" {\n  name = \"a\"\n  fast_path = true\n}\n"}
	r := runCoverageSchema(t, head, base, oracleHeadSHA)
	if cov := *r.SchemaCoverage; len(cov) != 1 || !slices.Equal(cov[0].Arguments, []CoverageArgument{{"fast_path", "removed", "present", "absent"}}) {
		t.Errorf("coverage %+v", cov)
	}
	// A bound handoff and no touched block: an empty section, not none.
	edited := map[string]string{"main.tf": head["main.tf"], "outputs.tf": "output \"x\" {\n  value = 1\n}\n"}
	r = runCoverageSchema(t, edited, head, oracleHeadSHA)
	if r.SchemaCoverage == nil || len(*r.SchemaCoverage) != 0 {
		t.Errorf("untouched block: %+v", r.SchemaCoverage)
	}
}

// TestSchemaCoverageNeedsBoundHandoff leaves the section out with no
// handoff and with one bound to another commit.
func TestSchemaCoverageNeedsBoundHandoff(t *testing.T) {
	if cov := coverageRun(t, oracleHeadSHA, false).SchemaCoverage; cov != nil {
		t.Errorf("no handoff: %+v", cov)
	}
	if cov := coverageRun(t, oracleBaseSHA, true).SchemaCoverage; cov != nil {
		t.Errorf("unbound handoff: %+v", cov)
	}
}
