package facts

import (
	"os"
	"path/filepath"
	"testing"
)

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
		if errs := set.validate("schema-diff.schema.json", decodeJSON(t, data)); len(errs) > 0 {
			t.Errorf("%s: %v", filepath.Base(f), errs)
		}
	}
}
