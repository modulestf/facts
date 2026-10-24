package facts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// runCoverageSchema runs head and base trees with the schemas of the
// coverage-schema-positive handoff, its tree hashes replaced by theirs so it
// binds. headSHA is the head commit the run claims.
func runCoverageSchema(t *testing.T, head, base map[string]string, headSHA string) *Report {
	t.Helper()
	o := Options{Head: writeTree(t, head), Base: writeTree(t, base), HeadSHA: headSHA, MergeBaseSHA: oracleBaseSHA, Limits: DefaultLimits}
	o.SchemaHandoff = boundCoverageHandoff(t, o)
	r, err := run(o, coverageSchemaRules)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// boundCoverageHandoff writes the coverage-schema-positive handoff with the
// tree hashes of o's head and base, and returns its path.
func boundCoverageHandoff(t *testing.T, o Options) string {
	t.Helper()
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
	p := filepath.Join(t.TempDir(), "schema-handoff.json")
	if err := os.WriteFile(p, mustJSON(t, h), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
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

// rawWidgetSchema is a `terraform providers schema -json` file with one
// aws_demo_widget type under source, whose extra attribute is set too.
func rawWidgetSchema(t *testing.T, source, extra string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "schema.json")
	attrs := `"name": {"type": "string", "required": true}, "legacy_mode": {"type": "bool", "optional": true, "deprecated": true}`
	if extra != "" {
		attrs += `, "` + extra + `": {"type": "string", "optional": true}`
	}
	doc := `{"format_version": "1.0", "provider_schemas": {"` + source + `": {"resource_schemas": {"aws_demo_widget": {"version": 0, "block": {"attributes": {` + attrs + `}}}}}}}`
	if err := os.WriteFile(p, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

const rawWidgetVersions = "terraform {\n  required_providers {\n    aws = {\n      source = \"example/aws\"\n    }\n  }\n}\n"

func manifestByRule(r *Report) map[string]ManifestEntry {
	m := map[string]ManifestEntry{}
	for _, e := range r.Manifest {
		m[e.RuleID] = e
	}
	return m
}

// With raw schema files and no handoff, the provider source comes from
// required_providers and the files are read as the latest schema. They
// record no declared minimum, so the rule that needs one stays
// not_evaluated; the others evaluate.
func TestCoverageSchemaRawFilesOnly(t *testing.T) {
	raw := rawWidgetSchema(t, "registry.terraform.io/example/aws", "")
	head := writeTree(t, map[string]string{
		"versions.tf": rawWidgetVersions,
		"main.tf":     "resource \"aws_demo_widget\" \"this\" {\n  name        = \"a\"\n  bogus       = 1\n  legacy_mode = true\n}\n",
	})
	r, err := run(Options{Head: head, SchemaFiles: []string{raw}, Limits: DefaultLimits}, append(slices.Clone(coverageSchemaRules), coverageReachRules...))
	if err != nil {
		t.Fatal(err)
	}
	m := manifestByRule(r)
	if e := m["coverage.argument-above-min-version"]; e.Status != StatusNotEvaluated || e.ReasonCode != ReasonSchemaUnavailable || !strings.Contains(e.Reason, "no declared minimum") {
		t.Errorf("above-min-version: %+v", e)
	}
	for _, id := range []string{"coverage.unknown-argument", "coverage.deprecated-argument", "coverage.block-attribute-mismatch", "coverage.required-argument-unreachable"} {
		if e := m[id]; e.Status == StatusNotEvaluated {
			t.Errorf("%s not evaluated: %s", id, e.Reason)
		}
	}
	got := map[string]string{}
	for _, c := range r.Candidates {
		if len(c.Evidence.SchemaFacts) != 1 {
			t.Errorf("%s: facts %+v", c.RuleID, c.Evidence.SchemaFacts)
			continue
		}
		f := c.Evidence.SchemaFacts[0]
		if f.ProviderSource != "registry.terraform.io/example/aws" || f.Version != rawSchemaVersion {
			t.Errorf("%s: fact %+v", c.RuleID, f)
		}
		got[c.RuleID] = c.Subject
	}
	if got["coverage.unknown-argument"] != ".:aws_demo_widget.this.bogus" || got["coverage.deprecated-argument"] != ".:aws_demo_widget.this.legacy_mode" || len(got) != 2 {
		t.Errorf("candidates %v", got)
	}
}

// A provider the raw files do not have keeps every rule on a touched block
// not_evaluated with schema_unavailable, even when another file has the type
// under the implied hashicorp source.
func TestCoverageSchemaRawFilesMissingProvider(t *testing.T) {
	raw := rawWidgetSchema(t, "registry.terraform.io/hashicorp/aws", "")
	head := writeTree(t, map[string]string{
		"versions.tf": rawWidgetVersions,
		"main.tf":     "resource \"aws_demo_widget\" \"this\" {\n  bogus = 1\n}\n",
	})
	r, err := run(Options{Head: head, SchemaFiles: []string{raw}, Limits: DefaultLimits}, append(slices.Clone(coverageSchemaRules), coverageReachRules...))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range r.Manifest {
		if e.Status != StatusNotEvaluated || e.ReasonCode != ReasonSchemaUnavailable || !strings.Contains(e.Reason, "registry.terraform.io/example/aws is in no schema file") {
			t.Errorf("%s: %s %s %q", e.RuleID, e.Status, e.ReasonCode, e.Reason)
		}
	}
}

// In raw mode a data block reads the file's data source schemas, a
// provider argument with an alias resolves the source by its local name,
// and a type the file lacks says so, with no version in the reason.
func TestCoverageSchemaRawFilesDataAliasMissingType(t *testing.T) {
	raw := filepath.Join(t.TempDir(), "schema.json")
	doc := `{"format_version": "1.0", "provider_schemas": {"registry.terraform.io/example/aws": {` +
		`"resource_schemas": {"aws_demo_widget": {"version": 0, "block": {"attributes": {"name": {"type": "string", "optional": true}}}}},` +
		`"data_source_schemas": {"aws_demo_lookup": {"version": 0, "block": {"attributes": {"name": {"type": "string", "optional": true}}}}}}}}`
	if err := os.WriteFile(raw, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	versions := "terraform {\n  required_providers {\n    demo = {\n      source = \"example/aws\"\n    }\n  }\n}\n"
	for _, c := range []struct{ name, main, subject, reason string }{
		{"data block", "data \"aws_demo_lookup\" \"x\" {\n  provider = demo\n  bogus    = 1\n}\n", ".:data.aws_demo_lookup.x.bogus", ""},
		{"provider alias", "resource \"aws_demo_widget\" \"x\" {\n  provider = demo.west\n  bogus    = 1\n}\n", ".:aws_demo_widget.x.bogus", ""},
		{"missing type", "data \"aws_demo_widget\" \"x\" {\n  provider = demo\n}\n", "", "data.aws_demo_widget.x: type absent from the schema file for registry.terraform.io/example/aws"},
	} {
		head := writeTree(t, map[string]string{"versions.tf": versions, "main.tf": c.main})
		r, err := run(Options{Head: head, SchemaFiles: []string{raw}, Limits: DefaultLimits}, coverageSchemaRules)
		if err != nil {
			t.Fatal(err)
		}
		e := manifestByRule(r)["coverage.unknown-argument"]
		if c.reason != "" {
			if e.Status != StatusNotEvaluated || e.ReasonCode != ReasonSchemaUnavailable || e.Reason != c.reason {
				t.Errorf("%s: %s %s %q", c.name, e.Status, e.ReasonCode, e.Reason)
			}
			continue
		}
		if len(r.Candidates) != 1 || r.Candidates[0].Subject != c.subject || r.Candidates[0].Evidence.SchemaFacts[0].Version != rawSchemaVersion {
			t.Errorf("%s: %s %q, candidates %+v", c.name, e.Status, e.Reason, r.Candidates)
		}
	}
}

// With both inputs the handoff alone is read, bound to the run's commits:
// the raw file, which has bogus, does not hide the handoff's unknown
// argument, and an unbound handoff does not fall back to the raw file.
func TestCoverageSchemaHandoffWinsOverRawFiles(t *testing.T) {
	head := map[string]string{"main.tf": "resource \"aws_demo_widget\" \"this\" {\n  name  = \"a\"\n  bogus = 1\n}\n"}
	for _, c := range []struct {
		headSHA string
		bound   bool
	}{{oracleHeadSHA, true}, {strings.Repeat("3", 40), false}} {
		o := Options{Head: writeTree(t, head), Base: writeTree(t, map[string]string{}), HeadSHA: c.headSHA, MergeBaseSHA: oracleBaseSHA, Limits: DefaultLimits}
		o.SchemaHandoff = boundCoverageHandoff(t, o)
		o.SchemaFiles = []string{rawWidgetSchema(t, "registry.terraform.io/hashicorp/aws", "bogus")}
		r, err := run(o, coverageSchemaRules)
		if err != nil {
			t.Fatal(err)
		}
		e := manifestByRule(r)["coverage.unknown-argument"]
		if !c.bound {
			if e.Status != StatusNotEvaluated || e.ReasonCode != ReasonSchemaUnavailable {
				t.Errorf("unbound: %+v", e)
			}
			continue
		}
		if len(r.Candidates) != 1 || r.Candidates[0].RuleID != "coverage.unknown-argument" || r.Candidates[0].Evidence.SchemaFacts[0].Version != "6.2.0" {
			t.Errorf("bound: candidates %+v", r.Candidates)
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

// A deprecated argument the change adds to a block the base already has is a
// candidate: the rule's scope is touched-expression, so an edited block keeps
// the arguments it newly sets.
func TestCoverageSchemaDeprecatedInEditedBlock(t *testing.T) {
	raw := rawWidgetSchema(t, "registry.terraform.io/example/aws", "")
	base := map[string]string{
		"versions.tf": rawWidgetVersions,
		"main.tf":     "resource \"aws_demo_widget\" \"this\" {\n  name = \"a\"\n}\n",
	}
	head := map[string]string{
		"versions.tf": rawWidgetVersions,
		"main.tf":     "resource \"aws_demo_widget\" \"this\" {\n  name        = \"a\"\n  legacy_mode = true\n}\n",
	}
	r, err := run(Options{Head: writeTree(t, head), Base: writeTree(t, base), SchemaFiles: []string{raw}, Limits: DefaultLimits}, coverageSchemaRules)
	if err != nil {
		t.Fatal(err)
	}
	cs := candidatesOf(r, "coverage.deprecated-argument")
	if len(cs) != 1 || cs[0].Subject != ".:aws_demo_widget.this.legacy_mode" || cs[0].Line == nil || *cs[0].Line != 3 {
		t.Fatalf("candidates %+v", cs)
	}
}
