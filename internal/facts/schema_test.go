package facts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modulestf/facts/internal/schematest"
)

// schemaSet holds the schemas under schema/ by $id; see package schematest.
type schemaSet = schematest.Set

func loadSchemas(t *testing.T) schemaSet {
	t.Helper()
	return schematest.Load(t, "../../schema")
}

func decodeJSON(t *testing.T, data []byte) any {
	t.Helper()
	return schematest.DecodeJSON(t, data)
}

func TestSchemaValidatorRejects(t *testing.T) {
	set := loadSchemas(t)
	bad := map[string]string{
		"missing field": `[{"rule_id":"structure.variable-untyped","area":"."}]`,
		"bad status":    `[{"rule_id":"structure.variable-untyped","area":".","status":"ok"}]`,
		"extra field":   `[{"rule_id":"structure.variable-untyped","area":".","status":"clean","x":1}]`,
		"bad rule id":   `[{"rule_id":"Structure","area":".","status":"clean"}]`,
		"empty reason":  `[{"rule_id":"a.b","area":".","status":"not_evaluated","reason":"","reason_code":"parse_error"}]`,
		"no code":       `[{"rule_id":"a.b","area":".","status":"not_evaluated","reason":"x"}]`,
		"open code":     `[{"rule_id":"a.b","area":".","status":"not_evaluated","reason":"x","reason_code":"other"}]`,
		"code on clean": `[{"rule_id":"a.b","area":".","status":"clean","reason_code":"parse_error"}]`,
		"not an array":  `{}`,
	}
	for name, doc := range bad {
		if errs := set.Validate("manifest.schema.json", decodeJSON(t, []byte(doc))); len(errs) == 0 {
			t.Errorf("%s: accepted", name)
		}
	}
	good := `[{"rule_id":"a.b-c","area":"modules/x","status":"not_evaluated","reason":"main.tf: parse error","reason_code":"parse_error"}]`
	if errs := set.Validate("manifest.schema.json", decodeJSON(t, []byte(good))); len(errs) > 0 {
		t.Error(errs)
	}
}

func TestReportMatchesSchemas(t *testing.T) {
	t.Parallel()
	set := loadSchemas(t)
	base := writeTree(t, map[string]string{"main.tf": "", "modules/a/main.tf": ""})
	head := writeTree(t, map[string]string{"main.tf": "", "modules/a/main.tf": "# Free-form.\nvariable \"x\" { type = any }\n", "modules/b/main.tf": "variable {", "examples/e/x.tf": "->../../main.tf"})
	reg := filepath.Join(t.TempDir(), "rule-facts.md")
	if err := os.WriteFile(reg, []byte("# registry\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, o := range []Options{
		{Head: head},
		{Head: head, Base: base, HeadSHA: strings.Repeat("a", 40), MergeBaseSHA: strings.Repeat("b", 64), RegistryFile: reg},
	} {
		o.Version, o.BinarySHA256, o.Limits = "test", strings.Repeat("0", 64), DefaultLimits
		r, err := Run(o)
		if err != nil {
			t.Fatal(err)
		}
		checkReport(t, set, r)
		// The keys later inputs and builders fill, filled by hand. The second
		// run has a base and both commits, so InterfaceDiff is filled already.
		sum, head, base := strings.Repeat("c", 64), strings.Repeat("a", 40), strings.Repeat("b", 40)
		r.Envelope.ProfileSHA256 = &sum
		r.Envelope.SchemaInputs = &SchemaInputs{HandoffSHA256: &sum, HeadSHA: &head, MergeBaseSHA: &base,
			TreeHashes: &TreeHashes{Head: sum, MergeBase: sum}, SchemaFiles: []FileHash{{Path: "aws.json", SHA256: sum}}}
		r.Index = []IndexEntry{{File: "modules/a/main.tf", StartLine: 2, EndLine: 2, Subject: "modules/a:var.x"}}
		checkReport(t, set, r)
	}
}

// checkReport validates a report and its AI form against the lint,
// envelope and manifest schemas, and requires one not_evaluated entry with a
// reason code in each.
func checkReport(t *testing.T, set schemaSet, r *Report) {
	t.Helper()
	for _, form := range []any{r, r.AIReport()} {
		data, err := json.Marshal(form)
		if err != nil {
			t.Fatal(err)
		}
		v := decodeJSON(t, data)
		if errs := set.Validate("lint.schema.json", v); len(errs) > 0 {
			t.Errorf("lint schema: %v\n%s", errs, data)
		}
		m := v.(map[string]any)
		if errs := set.Validate("envelope.schema.json", m["envelope"]); len(errs) > 0 {
			t.Errorf("envelope schema: %v", errs)
		}
		if errs := set.Validate("manifest.schema.json", m["manifest"]); len(errs) > 0 {
			t.Errorf("manifest schema: %v", errs)
		}
		if !strings.Contains(string(data), `"reason_code":"parse_error"`) {
			t.Errorf("no not_evaluated entry with a reason code: %s", data)
		}
	}
}

// TestAISchemaRejectsMixedForms refuses a report that mixes the full
// and AI forms: a manifest summary with file lists, or file counts
// without a manifest summary.
func TestAISchemaRejectsMixedForms(t *testing.T) {
	set := loadSchemas(t)
	head := writeTree(t, map[string]string{"main.tf": "variable {"})
	r, err := Run(Options{Head: head, Version: "test", BinarySHA256: strings.Repeat("0", 64), Limits: DefaultLimits})
	if err != nil {
		t.Fatal(err)
	}
	full, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	ai, err := json.Marshal(r.AIReport())
	if err != nil {
		t.Fatal(err)
	}
	f, c := decodeJSON(t, full).(map[string]any), decodeJSON(t, ai).(map[string]any)
	f["manifest_summary"] = c["manifest_summary"]
	if errs := set.Validate("lint.schema.json", f); len(errs) == 0 {
		t.Error("a manifest summary with file lists is valid")
	}
	delete(c, "manifest_summary")
	if errs := set.Validate("lint.schema.json", c); len(errs) == 0 {
		t.Error("file counts without a manifest summary are valid")
	}
}

func TestRunRejectsBadInputs(t *testing.T) {
	head := writeTree(t, map[string]string{"main.tf": ""})
	for name, o := range map[string]Options{
		"short sha":    {Head: head, HeadSHA: "abc"},
		"upper sha":    {Head: head, MergeBaseSHA: strings.Repeat("A", 40)},
		"no registry":  {Head: head, RegistryFile: filepath.Join(head, "missing.md")},
		"dir registry": {Head: head, RegistryFile: head},
		"no head":      {Head: filepath.Join(head, "missing")},
	} {
		o.Limits = DefaultLimits
		if _, err := Run(o); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestRegistryHash(t *testing.T) {
	head := writeTree(t, map[string]string{"main.tf": "", "rule-facts.md": "abc"})
	r, err := Run(Options{Head: head, RegistryFile: filepath.Join(head, "rule-facts.md"), Limits: DefaultLimits})
	if err != nil {
		t.Fatal(err)
	}
	const want = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" // sha256("abc")
	if r.Envelope.RegistrySHA256 == nil || *r.Envelope.RegistrySHA256 != want {
		t.Errorf("registry hash %v", r.Envelope.RegistrySHA256)
	}
}
