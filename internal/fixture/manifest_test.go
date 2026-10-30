package fixture

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// subjectTF is a template tree: main.tf holds the base output and the
// wrapped subject, head.tf the head output whose value is the splice.
func subjectTF(expr string) map[string]string {
	return map[string]string{
		"main.tf": "output \"base\" {\n  value = 1\n}\n\nresource \"terraform_data\" \"subject\" {\n  count = 1\n  input = 1\n}\n",
		"head.tf": "output \"head\" {\n  value = " + expr + "\n}\n",
	}
}

const subjectManifest = `{"schema_version": 1, "files": {
  "main.tf": {
    "blocks": [
      {"type": "output", "labels": ["base"]},
      {"type": "resource", "labels": ["terraform_data", "subject"]}
    ],
    "single_expressions": [{"block": "resource", "labels": ["terraform_data", "subject"], "attribute": "input"}]
  },
  "head.tf": {
    "blocks": [{"type": "output", "labels": ["head"]}],
    "single_expressions": [{"block": "output", "labels": ["head"], "attribute": "value"}]
  }}}`

// notLastManifest splices the first of two blocks in head.tf.
const notLastManifest = `{"files": {
  "main.tf": {"blocks": [{"type": "output", "labels": ["base"]}, {"type": "resource", "labels": ["terraform_data", "subject"]}]},
  "head.tf": {
    "blocks": [{"type": "output", "labels": ["head"]}, {"type": "output", "labels": ["tail"]}],
    "single_expressions": [{"block": "output", "labels": ["head"], "attribute": "value"}]
  }}}`

func with(files map[string]string, name, data string) map[string]string {
	files[name] = data
	return files
}

type expectCase struct {
	name     string
	files    map[string]string
	manifest string
	rule     string
}

var expectCases = []expectCase{
	// The spliced expression closes its block and opens another allowed one.
	{name: "splice adds a block", files: subjectTF("1\n}\n\noutput \"x\" {\n  value = 2"), rule: RuleManifestBlockSet},
	{name: "splice repeats a block", files: subjectTF("1\n}\n\noutput \"base\" {\n  value = 2"), rule: RuleManifestBlockSet},
	{name: "block missing", files: with(subjectTF("1"), "main.tf", "resource \"terraform_data\" \"subject\" {\n  input = 1\n}\n"), rule: RuleManifestBlockSet},
	{name: "labels differ", files: with(subjectTF("1"), "head.tf", "output \"head2\" {\n  value = 1\n}\n"), rule: RuleManifestBlockSet},
	{name: "file not in manifest", files: with(subjectTF("1"), "extra.tf", "locals {}\n"), rule: RuleManifestBlockSet},
	{name: "file missing", files: map[string]string{"main.tf": subjectTF("1")["main.tf"]}, rule: RuleManifestBlockSet},

	// The spliced expression ends its attribute and adds another argument.
	{name: "splice adds an argument", files: subjectTF("1\n  sensitive = true"), rule: RuleManifestExpression},
	{name: "splice adds a nested block", files: subjectTF("1\n  precondition {\n    condition = true\n    error_message = \"x\"\n  }"), rule: RuleManifestExpression},
	{name: "attribute missing", files: with(subjectTF("1"), "main.tf", "output \"base\" {\n  value = 1\n}\n\nresource \"terraform_data\" \"subject\" {\n  count = 1\n}\n"), rule: RuleManifestExpression},
	// A comment in the splice could swallow template text.
	{name: "block comment in splice", files: subjectTF("1 /* x */"), rule: RuleManifestExpression},
	{name: "line comment in splice", files: subjectTF("1 # x"), rule: RuleManifestExpression},
	{name: "comment after splice", files: with(subjectTF("1"), "head.tf", "output \"head\" {\n  value = 1\n  // x\n}\n"), rule: RuleManifestExpression},
	// The spliced block must be the last block of its file.
	{name: "spliced block not last", manifest: notLastManifest, files: with(subjectTF("1"), "head.tf", "output \"head\" {\n  value = 1\n}\n\noutput \"tail\" {\n  value = 2\n}\n"), rule: RuleManifestExpression},
}

func TestCheckExpect(t *testing.T) {
	t.Parallel()
	for _, tc := range expectCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			for name, data := range tc.files {
				mustDo(t, os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644))
			}
			m := cmpOr(tc.manifest, subjectManifest)
			mf := filepath.Join(t.TempDir(), "expect.json")
			mustDo(t, os.WriteFile(mf, []byte(m), 0o644))
			exp, err := ReadManifest(mf)
			if err != nil {
				t.Fatal(err)
			}
			r, err := Check(Options{Dir: dir, Expect: exp})
			if err != nil {
				t.Fatal(err)
			}
			if r.OK {
				t.Fatalf("passes: %+v", r)
			}
			// A splice that adds a block also leaves its block not last:
			// the case's rule must be among the violations, all manifest rules.
			found := false
			for _, v := range r.Violations {
				found = found || v.Rule == tc.rule
				if !strings.HasPrefix(v.Rule, "manifest.") {
					t.Errorf("violation %+v, want manifest rules only", v)
				}
			}
			if !found {
				t.Errorf("no %s in %+v", tc.rule, r.Violations)
			}
		})
	}
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func TestCheckExpectPasses(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for name, data := range subjectTF("compact(concat([terraform_data.subject[0].output], []))") {
		mustDo(t, os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644))
	}
	mf := filepath.Join(t.TempDir(), "expect.json")
	mustDo(t, os.WriteFile(mf, []byte(subjectManifest), 0o644))
	exp, err := ReadManifest(mf)
	if err != nil {
		t.Fatal(err)
	}
	if r, err := Check(Options{Dir: dir, Expect: exp}); err != nil || !r.OK {
		t.Fatalf("%v %+v", err, r)
	}
	// The testdata manifest matches the L1 fixture.
	exp, err = ReadManifest(filepath.Join(td, "l1.expect.json"))
	if err != nil {
		t.Fatal(err)
	}
	if r, err := Check(Options{Dir: filepath.Join(td, "l1"), Expect: exp}); err != nil || !r.OK {
		t.Fatalf("l1: %v %+v", err, r)
	}
}

// Every manifest rule has a case.
func TestEveryManifestRuleTested(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for _, tc := range expectCases {
		seen[tc.rule] = true
	}
	for _, r := range []string{RuleManifestBlockSet, RuleManifestExpression} {
		if !seen[r] {
			t.Errorf("rule %s has no case", r)
		}
	}
}

func TestReadManifestErrors(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for name, data := range map[string]string{
		"syntax":           `{"files":`,
		"unknown key":      `{"files":{},"x":1}`,
		"no files":         `{"schema_version":1}`,
		"version":          `{"schema_version":2,"files":{"main.tf":{"blocks":[]}}}`,
		"file name":        `{"files":{"../main.tf":{"blocks":[]}}}`,
		"json file":        `{"files":{"main.tf.json":{"blocks":[]}}}`,
		"empty type":       `{"files":{"main.tf":{"blocks":[{"type":"","labels":[]}]}}}`,
		"no labels":        `{"files":{"main.tf":{"blocks":[{"type":"output"}]}}}`,
		"attribute":        `{"files":{"main.tf":{"blocks":[{"type":"output","labels":["a"]}],"single_expressions":[{"block":"output","labels":["a"],"attribute":"a b"}]}}}`,
		"two splices":      `{"files":{"main.tf":{"blocks":[{"type":"output","labels":["a"]},{"type":"output","labels":["b"]}],"single_expressions":[{"block":"output","labels":["a"],"attribute":"value"},{"block":"output","labels":["b"],"attribute":"value"}]}}}`,
		"block not listed": `{"files":{"main.tf":{"blocks":[],"single_expressions":[{"block":"output","labels":["a"],"attribute":"value"}]}}}`,
		"unknown inner":    `{"files":{"main.tf":{"blocks":[{"type":"output","labels":["a"],"x":1}]}}}`,
		"trailing":         `{"files":{}} {}`,
	} {
		p := filepath.Join(dir, strings.ReplaceAll(name, " ", "-")+".json")
		mustDo(t, os.WriteFile(p, []byte(data), 0o644))
		if _, err := ReadManifest(p); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}
