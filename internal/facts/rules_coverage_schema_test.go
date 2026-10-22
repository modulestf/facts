package facts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runCoverageSchema runs head and base trees with the schemas of the
// coverage-schema-positive handoff, its tree hashes replaced by theirs so it
// binds. headSHA is the head commit the run claims.
func runCoverageSchema(t *testing.T, head, base map[string]string, headSHA string) *Report {
	t.Helper()
	o := Options{Head: writeTree(t, head), Base: writeTree(t, base), HeadSHA: headSHA, MergeBaseSHA: oracleBaseSHA, Limits: DefaultLimits}
	data, err := os.ReadFile("../../testdata/coverage-schema-positive/schema-handoff.json")
	if err != nil {
		t.Fatal(err)
	}
	var h map[string]any
	if err := json.Unmarshal(data, &h); err != nil {
		t.Fatal(err)
	}
	in := h["inputs"].(map[string]any)
	in["head"].(map[string]any)["tree_hash"] = tfTreeHash(mustTree(t, o.Head))
	in["merge_base"].(map[string]any)["tree_hash"] = tfTreeHash(mustTree(t, o.Base))
	o.SchemaHandoff = filepath.Join(t.TempDir(), "schema-handoff.json")
	if err := os.WriteFile(o.SchemaHandoff, mustJSON(t, h), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := run(o, coverageSchemaRules)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestCoverageSchemaEvidenceCap(t *testing.T) {
	long := `"` + strings.Repeat("x", 2*maxEvidenceText) + `"`
	r := runCoverageSchema(t, map[string]string{
		"main.tf": "resource \"aws_demo_widget\" \"this\" {\n  name  = \"a\"\n  bogus = " + long + "\n}\n",
	}, map[string]string{}, oracleHeadSHA)
	if len(r.Candidates) != 1 {
		t.Fatalf("candidates %+v", r.Candidates)
	}
	c := r.Candidates[0]
	if c.RuleID != "coverage.unknown-argument" || c.EvidenceComplete || len(*c.Evidence.Head) != maxEvidenceText ||
		len(c.Evidence.SourceLines[0].Text) != maxEvidenceText {
		t.Errorf("cap did not bite: %s complete=%v", c.RuleID, c.EvidenceComplete)
	}
}

// A handoff for another head commit does not bind, so a touched block makes
// every rule not_evaluated, and an untouched one leaves them clean.
func TestCoverageSchemaUnboundHandoff(t *testing.T) {
	block := "resource \"aws_demo_widget\" \"this\" {\n  bogus = 1\n}\n"
	for _, c := range []struct {
		base, want string
	}{
		{"", StatusNotEvaluated},
		{block, StatusClean},
	} {
		r := runCoverageSchema(t, map[string]string{"main.tf": block, "variables.tf": ""}, map[string]string{"main.tf": c.base}, strings.Repeat("3", 40))
		if len(r.Manifest) != len(coverageSchemaRules) {
			t.Fatalf("manifest %+v", r.Manifest)
		}
		for _, e := range r.Manifest {
			if e.Status != c.want || (c.want == StatusNotEvaluated) != (e.ReasonCode == ReasonSchemaUnavailable) {
				t.Errorf("base %q: %s is %s %s", c.base, e.RuleID, e.Status, e.ReasonCode)
			}
		}
	}
}

// Raw schema files alone carry no versions, so a touched block makes every
// rule not_evaluated, and the reason says the rule needs the handoff.
func TestCoverageSchemaRawFilesOnly(t *testing.T) {
	raw := filepath.Join(t.TempDir(), "aws.json")
	if err := os.WriteFile(raw, []byte(`{"format_version": "1.0", "provider_schemas": {}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	head := writeTree(t, map[string]string{"main.tf": "resource \"aws_demo_widget\" \"this\" {\n  bogus = 1\n}\n"})
	r, err := run(Options{Head: head, SchemaFiles: []string{raw}, Limits: DefaultLimits}, coverageSchemaRules)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range r.Manifest {
		if e.Status != StatusNotEvaluated || e.ReasonCode != ReasonSchemaUnavailable || !strings.Contains(e.Reason, "needs the schema handoff") {
			t.Errorf("%s: %s %s %q", e.RuleID, e.Status, e.ReasonCode, e.Reason)
		}
	}
}

// The reports of the coverage-schema golden cases, flag facts included,
// validate against the lint schema, whose candidate evidence is the bundle's.
func TestCoverageSchemaReportsMatchLintSchema(t *testing.T) {
	set := loadSchemas(t)
	dirs, err := filepath.Glob("../../testdata/coverage-schema-*")
	if err != nil || len(dirs) == 0 {
		t.Fatalf("no coverage-schema cases: %v", err)
	}
	for _, dir := range dirs {
		o := Options{Head: filepath.Join(dir, "head"), Base: filepath.Join(dir, "base"), HeadSHA: oracleHeadSHA, MergeBaseSHA: oracleBaseSHA,
			SchemaHandoff: filepath.Join(dir, "schema-handoff.json"), Limits: DefaultLimits,
			Version: "test", BinarySHA256: strings.Repeat("0", 64)}
		r, err := Run(o)
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Candidates) == 0 && filepath.Base(dir) != "coverage-schema-negative" {
			t.Errorf("%s: no candidates", dir)
		}
		if errs := set.validate("lint.schema.json", decodeJSON(t, mustJSON(t, r))); len(errs) > 0 {
			t.Errorf("%s: lint schema: %v", dir, errs)
		}
	}
}
