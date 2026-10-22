package facts

import (
	"os"
	"path/filepath"
	"testing"
)

// The merged findings are written by internal/merge, which imports this
// package, so its golden test pins its real output from hostile replies
// under testdata/merge-golden, and this test validates those files, and the
// merge fixtures' expected findings, with the same validator as lint's.
func TestMergedFindingsSchema(t *testing.T) {
	t.Parallel()
	set := loadSchemas(t)
	const id = "https://github.com/modulestf/facts/schema/merged-findings.schema.json"
	if set[id] == nil {
		t.Fatalf("no schema with $id %s", id)
	}
	golden, err := filepath.Glob("../../testdata/merge-golden/*.json")
	if err != nil || len(golden) == 0 {
		t.Fatal("no merged findings under testdata/merge-golden", err)
	}
	expected, err := filepath.Glob("../../testdata/merge/*/expected-findings.json")
	if err != nil || len(expected) == 0 {
		t.Fatal("no merge fixtures", err)
	}
	for _, f := range append(golden, expected...) {
		data, err := os.ReadFile(f) //nolint:gosec // G304: a test file of this repository
		if err != nil {
			t.Fatal(err)
		}
		if errs := set.validate(id, decodeJSON(t, data)); len(errs) > 0 {
			t.Errorf("%s: %v", f, errs)
		}
	}
}
