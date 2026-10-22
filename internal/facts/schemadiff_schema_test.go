package facts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// schemaDiffID is the $id of schema/schema-diff.schema.json, the key
// loadSchemas stores it under.
const schemaDiffID = "schema-diff.schema.json"

// The schema-diff report is written by internal/schemadiff, which imports
// this package, so its test checks that this example is its real output,
// and this test validates the example with the same validator as lint's.
func TestSchemaDiffExampleValidates(t *testing.T) {
	t.Parallel()
	set := loadSchemas(t)
	files, err := filepath.Glob("../../schema/examples/schema-diff.valid*.json")
	if err != nil || len(files) == 0 {
		t.Fatal("no schema-diff examples", err)
	}
	for _, f := range files {
		data, err := os.ReadFile(f) //nolint:gosec // G304: an example of this repository
		if err != nil {
			t.Fatal(err)
		}
		if errs := set.validate(schemaDiffID, decodeJSON(t, data)); len(errs) > 0 {
			t.Errorf("%s: %v", filepath.Base(f), errs)
		}
	}
}

// TestSchemaDiffSchemaRejects proves the check above can fail: hand-made
// violations of the valid example are each rejected for the named reason.
func TestSchemaDiffSchemaRejects(t *testing.T) {
	t.Parallel()
	set := loadSchemas(t)
	data, err := os.ReadFile("../../schema/examples/schema-diff.valid.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, want string
		mutate     func(row map[string]any)
	}{
		{"unknown change kind", "not in enum", func(row map[string]any) { row["change"] = "renamed" }},
		{"missing from", `missing "from"`, func(row map[string]any) { delete(row, "from") }},
		{"missing to", `missing "to"`, func(row map[string]any) { delete(row, "to") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var doc map[string]any
			if err := json.Unmarshal(data, &doc); err != nil {
				t.Fatal(err)
			}
			tc.mutate(doc["changes"].([]any)[0].(map[string]any))
			bad, err := json.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			errs := set.validate(schemaDiffID, decodeJSON(t, bad))
			if !strings.Contains(strings.Join(errs, "\n"), tc.want) {
				t.Errorf("errors %v, want one with %q", errs, tc.want)
			}
		})
	}
}

func TestValidateUnknownSchema(t *testing.T) {
	t.Parallel()
	errs := loadSchemas(t).validate("no-such.schema.json", map[string]any{})
	if len(errs) != 1 || !strings.Contains(errs[0], "no schema with $id") {
		t.Errorf("errors %v", errs)
	}
}
