package fixture

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// subjectTF is a template whose head output value is the spliced expression.
func subjectTF(expr string) string {
	return "output \"base\" {\n  value = 1\n}\n\nresource \"terraform_data\" \"subject\" {\n  count = 1\n  input = 1\n}\n\noutput \"head\" {\n  value = " + expr + "\n}\n"
}

const subjectManifest = `{"schema_version": 1, "files": {"main.tf": {
  "blocks": [
    {"type": "output", "labels": ["base"]},
    {"type": "output", "labels": ["head"]},
    {"type": "resource", "labels": ["terraform_data", "subject"]}
  ],
  "single_expressions": [
    {"block": "output", "labels": ["head"], "attribute": "value"},
    {"block": "resource", "labels": ["terraform_data", "subject"], "attribute": "input"}
  ]}}}`

type expectCase struct {
	name     string
	files    map[string]string
	manifest string
	rule     string
}

var expectCases = []expectCase{
	// The spliced expression closes its block and opens another allowed one.
	{name: "splice adds a block", files: l1(subjectTF("1\n}\n\noutput \"x\" {\n  value = 2")), rule: RuleManifestBlockSet},
	{name: "splice repeats a block", files: l1(subjectTF("1\n}\n\noutput \"base\" {\n  value = 2")), rule: RuleManifestBlockSet},
	{name: "block missing", files: l1(strings.Replace(subjectTF("1"), "output \"base\" {\n  value = 1\n}\n", "", 1)), rule: RuleManifestBlockSet},
	{name: "labels differ", files: l1(strings.Replace(subjectTF("1"), `output "base"`, `output "base2"`, 1)), rule: RuleManifestBlockSet},
	{name: "file not in manifest", files: map[string]string{"main.tf": subjectTF("1"), "extra.tf": "locals {}\n"}, rule: RuleManifestBlockSet},
	{name: "file missing", files: map[string]string{"other.tf": subjectTF("1")}, rule: RuleManifestBlockSet},

	// The spliced expression ends its attribute and adds another argument.
	{name: "splice adds an argument", files: l1(subjectTF("1\n  sensitive = true")), rule: RuleManifestExpression},
	{name: "splice adds a nested block", files: l1(subjectTF("1\n  precondition {\n    condition = true\n    error_message = \"x\"\n  }")), rule: RuleManifestExpression},
	{name: "attribute missing", files: l1(strings.Replace(subjectTF("1"), "  input = 1\n", "", 1)), rule: RuleManifestExpression},
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
			for _, v := range r.Violations {
				if v.Rule != tc.rule {
					t.Errorf("violation %+v, want rule %s only", v, tc.rule)
				}
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
	mustDo(t, os.WriteFile(filepath.Join(dir, "main.tf"), []byte(subjectTF("compact(concat([terraform_data.subject[0].output], []))")), 0o644))
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
