package facts

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	mergeRoot       = "../../testdata/merge"
	schemaIDPrefix  = "https://github.com/modulestf/facts/schema/"
	confirmSchemaID = schemaIDPrefix + "confirm-output.schema.json"
)

// keywordMessages maps a schema keyword an invalid example names to the text
// the validator's message for that keyword holds. An invalid example naming a
// keyword missing here fails, so a new one cannot pass on any other error.
var keywordMessages = map[string]string{
	"enum":                 "not in enum",
	"required":             ": missing ",
	"additionalProperties": "unexpected property",
	"type":                 ": type ",
}

// TestMergeFixtureSchemas checks every merge fixture against the schemas:
// the bundles, the replies and the valid confirm outputs pass, and every
// confirm/*.invalid.<keyword>.json fails on that keyword. The merged findings
// are checked by TestMergedFindingsSchema.
func TestMergeFixtureSchemas(t *testing.T) {
	t.Parallel()
	if bad := checkMergeFixtures(t, loadSchemas(t), mergeRoot); len(bad) > 0 {
		t.Errorf("merge fixtures:\n%s", strings.Join(bad, "\n"))
	}
}

// TestMergeFixtureSchemasCatchBreakage runs the check on a broken copy of a
// fixture: each break must be reported.
func TestMergeFixtureSchemasCatchBreakage(t *testing.T) {
	t.Parallel()
	set := loadSchemas(t)
	src := filepath.Join(mergeRoot, "confirm-outcomes")
	breaks := map[string]func(dir string){
		"bundle":         func(dir string) { write(t, filepath.Join(dir, "bundle.json"), `{}`) },
		"before confirm": func(dir string) { write(t, filepath.Join(dir, "bundle.before-confirm.json"), `[]`) },
		"model output":   func(dir string) { write(t, filepath.Join(dir, "model-output.repair.json"), `{"findings":[]}`) },
		"confirm":        func(dir string) { write(t, filepath.Join(dir, "confirm", "C-001.json"), `{"decision":"reject"}`) },
		"invalid valid": func(dir string) {
			write(t, filepath.Join(dir, "confirm", "C-004.invalid.enum.json"), `{"decision":"confirm"}`)
		},
		"wrong keyword": func(dir string) { write(t, filepath.Join(dir, "confirm", "C-004.invalid.enum.json"), `{}`) },
		"no keyword":    func(dir string) { rename(t, dir, "C-004.invalid.enum.json", "C-004.invalid.json") },
		"note":          func(dir string) { write(t, filepath.Join(dir, "note.txt"), "a\nb\n") },
		"no expected":   func(dir string) { remove(t, filepath.Join(dir, "expected-findings.json")) },
		"host":          func(dir string) { write(t, filepath.Join(dir, "host.json"), `{`) },
	}
	for name, mutate := range breaks {
		root := t.TempDir()
		dir := filepath.Join(root, "f")
		if err := os.CopyFS(dir, os.DirFS(src)); err != nil {
			t.Fatal(err)
		}
		if bad := checkMergeFixtures(t, set, root); len(bad) > 0 {
			t.Fatalf("unbroken copy fails: %v", bad)
		}
		mutate(dir)
		if bad := checkMergeFixtures(t, set, root); len(bad) == 0 {
			t.Errorf("%s: a broken fixture passes", name)
		}
	}
}

// checkMergeFixtures returns every problem of the merge fixtures under root.
func checkMergeFixtures(t *testing.T, set schemaSet, root string) []string {
	t.Helper()
	names, err := fixtureDirs(root)
	if err != nil || len(names) == 0 {
		t.Fatalf("no merge fixtures under %s: %v", root, err)
	}
	var bad []string
	add := func(f string, a ...any) { bad = append(bad, fmt.Sprintf(f, a...)) }
	validate := func(rel, schema string) []string {
		data, err := os.ReadFile(filepath.Join(root, rel)) //nolint:gosec // G304: a test file of this repository
		if err != nil {
			return []string{err.Error()}
		}
		return set.validate(schemaIDPrefix+schema, decodeJSON(t, data))
	}
	for _, name := range names {
		dir := filepath.Join(root, name)
		if note, err := os.ReadFile(filepath.Join(dir, "note.txt")); err != nil || strings.Count(string(note), "\n") != 1 || !strings.HasSuffix(string(note), "\n") {
			add("%s/note.txt is not one line", name)
		}
		if _, err := os.Stat(filepath.Join(dir, "expected-findings.json")); err != nil {
			add("%s has no expected-findings.json", name)
		}
		for pattern, schema := range map[string]string{
			"bundle*.json":       "facts-bundle.schema.json",
			"model-output*.json": "model-output.schema.json",
		} {
			files, _ := filepath.Glob(filepath.Join(dir, pattern))
			for _, f := range files {
				rel := filepath.Join(name, filepath.Base(f))
				for _, e := range validate(rel, schema) {
					add("%s: %s", rel, e)
				}
			}
		}
		confirm, _ := filepath.Glob(filepath.Join(dir, "confirm", "*.json"))
		for _, f := range confirm {
			rel := filepath.Join(name, "confirm", filepath.Base(f))
			errs := validate(rel, "confirm-output.schema.json")
			base := strings.TrimSuffix(filepath.Base(f), ".json")
			_, after, invalid := strings.Cut(base, ".invalid")
			switch {
			case !invalid:
				for _, e := range errs {
					add("%s: %s", rel, e)
				}
			case !strings.HasPrefix(after, "."):
				add("%s: an invalid example must name its keyword, as <id>.invalid.<keyword>.json", rel)
			case len(errs) == 0:
				add("%s: valid, want invalid", rel)
			default:
				kw, _, _ := strings.Cut(after[1:], ".")
				msg, ok := keywordMessages[kw]
				if !ok {
					add("%s: no message text listed for keyword %q", rel, kw)
				} else if !strings.Contains(strings.Join(errs, "\n"), msg) {
					add("%s: fails with %v, want a %s error", rel, errs, kw)
				}
			}
		}
		hosts, _ := filepath.Glob(filepath.Join(dir, "host.json"))
		for _, f := range hosts {
			if data, err := os.ReadFile(f); err != nil || !json.Valid(data) { //nolint:gosec // G304: a test file of this repository
				add("%s/host.json is not JSON", name)
			}
		}
	}
	if data, err := os.ReadFile(filepath.Join(mergeRoot, "registry.json")); err != nil || !json.Valid(data) {
		add("registry.json is not JSON")
	}
	return bad
}

func write(t *testing.T, path, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func remove(t *testing.T, path string) {
	t.Helper()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
}

func rename(t *testing.T, dir, from, to string) {
	t.Helper()
	if err := os.Rename(filepath.Join(dir, "confirm", from), filepath.Join(dir, "confirm", to)); err != nil {
		t.Fatal(err)
	}
}
