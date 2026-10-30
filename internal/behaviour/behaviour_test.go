package behaviour

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/modulestf/facts/internal/facts"
)

var update = flag.Bool("update", false, "rewrite the golden files")

const pr556 = "testdata/pr556"

// TestPR556 is the acceptance case: the one-line change that drops
// compact() around vpc_security_group_ids is a wrapper change for L1 and,
// with no schema or with the attribute Optional+Computed, an unknown
// propagation candidate for L1 then L3. The report is the same for a base
// tree and for every diff form and context size.
func TestPR556(t *testing.T) {
	head := filepath.Join(pr556, "head")
	inputs := map[string]Options{
		"base":       {Base: filepath.Join(pr556, "base")},
		"diff -U0":   {Diff: filepath.Join(pr556, "change-u0.diff")},
		"diff -U3":   {Diff: filepath.Join(pr556, "change-u3.diff")},
		"diff -U20":  {Diff: filepath.Join(pr556, "change-u20.diff")},
		"files list": {Diff: filepath.Join(pr556, "files.json")},
	}
	var want []byte
	for _, name := range []string{"base", "diff -U0", "diff -U3", "diff -U20", "files list"} {
		o := inputs[name]
		o.Head, o.Limits = head, facts.DefaultLimits
		r, err := Run(o)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got := mustJSON(t, r)
		if want == nil {
			want = got
			continue
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s: report differs from the base-tree report:\n%s\nwant\n%s", name, got, want)
		}
	}
	golden := filepath.Join(pr556, "expected.json")
	if *update {
		if err := os.WriteFile(golden, want, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	g, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(g, want) {
		t.Errorf("report differs from %s (go test -update rewrites it):\n%s", golden, want)
	}
	// The schema example is the golden report, so the schema checks real output.
	example := "../../schema/examples/behaviour.valid.json"
	if *update {
		if err := os.WriteFile(example, want, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if e, err := os.ReadFile(example); err != nil || !bytes.Equal(e, want) {
		t.Errorf("%s differs from the golden report (go test -update rewrites it)", example)
	}

	r, err := Run(Options{Head: head, Base: filepath.Join(pr556, "base"), Limits: facts.DefaultLimits})
	if err != nil {
		t.Fatal(err)
	}
	if r.Inert || len(r.Triggers) != 2 || r.Capped != 0 {
		t.Fatalf("want 2 triggers, not inert: %s", mustJSON(t, r))
	}
	w, u := r.Triggers[0], r.Triggers[1]
	if w.Category != CatWrapper || !slices.Equal(w.Levels, []string{"L1"}) {
		t.Errorf("T-001: %s %v, want wrapper-change [L1]", w.Category, w.Levels)
	}
	if u.Category != CatUnknownPropagation || !slices.Equal(u.Levels, []string{"L1", "L3"}) || u.Reason == nil {
		t.Errorf("T-002: %s %v reason %s, want unknown-propagation-candidate [L1 L3] with a reason", u.Category, u.Levels, str(u.Reason))
	}
	for _, tr := range r.Triggers {
		if str(tr.Address) != "aws_rds_cluster.this" || str(tr.Attribute) != "vpc_security_group_ids" ||
			tr.File != "main.tf" || tr.Line == nil || *tr.Line != 10 || !tr.Pure {
			t.Errorf("%s: %s %s %s:%v pure %v", tr.ID, str(tr.Address), str(tr.Attribute), tr.File, tr.Line, tr.Pure)
		}
		if str(tr.BaseExpr) != "compact(concat(aws_security_group.this[*].id, var.vpc_security_group_ids))" ||
			str(tr.HeadExpr) != "concat(aws_security_group.this[*].id, var.vpc_security_group_ids)" {
			t.Errorf("%s: base %s head %s", tr.ID, str(tr.BaseExpr), str(tr.HeadExpr))
		}
		if len(tr.ReferencedInputs) != 1 || tr.ReferencedInputs[0] != (Input{Name: "vpc_security_group_ids", Type: "list(string)"}) {
			t.Errorf("%s: inputs %v", tr.ID, tr.ReferencedInputs)
		}
		if !slices.Equal(tr.ContextualRefs, []string{"aws_security_group.this"}) {
			t.Errorf("%s: contextual refs %v", tr.ID, tr.ContextualRefs)
		}
	}
	if len(r.Entries) != 1 || r.Entries[0].Path != "main.tf" || r.Entries[0].ChangeKind != KindModified ||
		r.Entries[0].Inert || !slices.Equal(r.Entries[0].TriggerIDs, []string{"T-001", "T-002"}) {
		t.Errorf("entries: %s", mustJSON(t, r.Entries))
	}
}

// TestPR556Schema: Optional+Computed in the schema keeps the unknown
// propagation trigger; Optional only drops it.
func TestPR556Schema(t *testing.T) {
	for file, want := range map[string][]string{
		"schema-optional-computed.json": {CatWrapper, CatUnknownPropagation},
		"schema-optional.json":          {CatWrapper},
	} {
		r, err := Run(Options{Head: filepath.Join(pr556, "head"), Base: filepath.Join(pr556, "base"),
			Schema: filepath.Join(pr556, file), Limits: facts.DefaultLimits})
		if err != nil {
			t.Fatal(err)
		}
		if got := categories(r); !slices.Equal(got, want) {
			t.Errorf("%s: %v, want %v", file, got, want)
		}
		d := r.Triggers[0].ContextualRefDetails
		if len(d) != 1 || d[0].Text != "aws_security_group.this[*].id" || d[0].Kind != "resource" ||
			str(d[0].Type) != "string" || str(d[0].Multi) != "count" {
			t.Errorf("%s: contextual ref details %s", file, mustJSON(t, d))
		}
	}
}

// TestSchemaFailsClosed: a schema without the provider, the type or the
// attribute keeps the unknown propagation trigger.
func TestSchemaFailsClosed(t *testing.T) {
	dir := t.TempDir()
	write := func(name, doc string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	docs := map[string]string{
		"no-provider.json": `{"format_version":"1.0","provider_schemas":{"registry.terraform.io/hashicorp/random":{}}}`,
		"no-type.json":     `{"format_version":"1.0","provider_schemas":{"registry.terraform.io/hashicorp/aws":{"resource_schemas":{}}}}`,
		"no-attr.json": `{"format_version":"1.0","provider_schemas":{"registry.terraform.io/hashicorp/aws":{"resource_schemas":` +
			`{"aws_rds_cluster":{"version":0,"block":{"attributes":{}}}}}}}`,
	}
	for name, doc := range docs {
		r, err := Run(Options{Head: filepath.Join(pr556, "head"), Base: filepath.Join(pr556, "base"),
			Schema: write(name, doc), Limits: facts.DefaultLimits})
		if err != nil {
			t.Fatal(err)
		}
		if got := categories(r); !slices.Equal(got, []string{CatWrapper, CatUnknownPropagation}) {
			t.Errorf("%s: %v", name, got)
		}
	}
	if _, err := Run(Options{Head: filepath.Join(pr556, "head"), Base: filepath.Join(pr556, "base"),
		Schema: write("bad.json", "{"), Limits: facts.DefaultLimits}); err == nil {
		t.Error("a malformed schema file must be an error")
	}
}

const rds = `resource "aws_rds_cluster" "this" {
  engine = "aurora"
}
`

func TestInert(t *testing.T) {
	cases := []struct {
		name       string
		base, head map[string]string
		inert      bool
		cats       []string
	}{
		{"readme only", map[string]string{"main.tf": rds, "README.md": "a\n"},
			map[string]string{"main.tf": rds, "README.md": "b\n"}, true, nil},
		{"docs dir and github", map[string]string{"main.tf": rds, "docs/x.png": "a", ".github/workflows/ci.yml": "a"},
			map[string]string{"main.tf": rds, "docs/x.png": "b", ".github/workflows/ci.yml": "b"}, true, nil},
		{"comment only in tf", map[string]string{"main.tf": rds},
			map[string]string{"main.tf": "# a comment\n" + rds}, true, nil},
		{"block moved between files", map[string]string{"main.tf": rds, "other.tf": ""},
			map[string]string{"main.tf": "", "other.tf": rds}, true, nil},
		{"readme read by file()", map[string]string{"main.tf": rds + `output "r" { value = file("${path.module}/README.md") }` + "\n", "README.md": "a\n"},
			map[string]string{"main.tf": rds + `output "r" { value = file("${path.module}/README.md") }` + "\n", "README.md": "b\n"}, false, []string{CatNonHCL}},
		{"readme read through a cwd relative path", map[string]string{"main.tf": rds, "examples/a/main.tf": `locals { x = file("../../README.md") }` + "\n", "README.md": "a\n"},
			map[string]string{"main.tf": rds, "examples/a/main.tf": `locals { x = file("../../README.md") }` + "\n", "README.md": "b\n"}, false, []string{CatNonHCL}},
		{"docs read by fileset prefix", map[string]string{"main.tf": `locals { x = fileset("${path.module}/docs", "*.md") }` + "\n", "docs/a.md": "a"},
			map[string]string{"main.tf": `locals { x = fileset("${path.module}/docs", "*.md") }` + "\n", "docs/a.md": "b"}, false, []string{CatNonHCL}},
		{"other path read is still inert", map[string]string{"main.tf": `locals { x = file("${path.module}/policy.json") }` + "\n", "README.md": "a"},
			map[string]string{"main.tf": `locals { x = file("${path.module}/policy.json") }` + "\n", "README.md": "b"}, true, nil},
		{"dynamic read site", map[string]string{"main.tf": `locals { x = file(var.path) }` + "\n", "README.md": "a"},
			map[string]string{"main.tf": `locals { x = file(var.path) }` + "\n", "README.md": "b"}, false, []string{CatNonHCL}},
		{"templatefile with a dynamic suffix", map[string]string{"main.tf": `locals { x = templatefile("${path.module}/${var.t}", {}) }` + "\n", "README.md": "a"},
			map[string]string{"main.tf": `locals { x = templatefile("${path.module}/${var.t}", {}) }` + "\n", "README.md": "b"}, false, []string{CatNonHCL}},
		{"local-exec command", map[string]string{"main.tf": "resource \"terraform_data\" \"x\" {\n  provisioner \"local-exec\" {\n    command = \"cat README.md\"\n  }\n}\n", "README.md": "a"},
			map[string]string{"main.tf": "resource \"terraform_data\" \"x\" {\n  provisioner \"local-exec\" {\n    command = \"cat README.md\"\n  }\n}\n", "README.md": "b"}, false, []string{CatNonHCL}},
		{"archive source_dir", map[string]string{"main.tf": `data "archive_file" "z" { source_dir = "${path.module}/docs" }` + "\n", "docs/a.md": "a"},
			map[string]string{"main.tf": `data "archive_file" "z" { source_dir = "${path.module}/docs" }` + "\n", "docs/a.md": "b"}, false, []string{CatNonHCL}},
		{"parse failure elsewhere", map[string]string{"main.tf": rds, "broken.tf": "x = \n", "README.md": "a"},
			map[string]string{"main.tf": rds, "broken.tf": "x = \n", "README.md": "b"}, false, []string{CatNonHCL}},
		{"json configuration elsewhere", map[string]string{"main.tf": rds, "x.tf.json": "{}", "README.md": "a"},
			map[string]string{"main.tf": rds, "x.tf.json": "{}", "README.md": "b"}, false, []string{CatNonHCL}},
		{"lock file", map[string]string{"main.tf": rds, ".terraform.lock.hcl": "a"},
			map[string]string{"main.tf": rds, ".terraform.lock.hcl": "b"}, false, []string{CatNonHCL}},
		{"script", map[string]string{"main.tf": rds, "scripts/x.sh": "a"},
			map[string]string{"main.tf": rds, "scripts/x.sh": "b"}, false, []string{CatNonHCL}},
		{"no change", map[string]string{"main.tf": rds}, map[string]string{"main.tf": rds}, true, nil},
		{"comment in a tf file a read site hashes", map[string]string{"main.tf": rds + `locals { h = filemd5("${path.module}/main.tf") }` + "\n"},
			map[string]string{"main.tf": "# c\n" + rds + `locals { h = filemd5("${path.module}/main.tf") }` + "\n"}, false, []string{CatNonHCL}},
		{"comment in a tf file with an incomplete scan", map[string]string{"main.tf": rds, "x.tf.json": "{}"},
			map[string]string{"main.tf": "# c\n" + rds, "x.tf.json": "{}"}, false, []string{CatNonHCL}},
		{"heredoc line break", map[string]string{"main.tf": "locals {\n  s = <<EOT\na\nb\nEOT\n}\n"},
			map[string]string{"main.tf": "locals {\n  s = <<EOT\na b\nEOT\n}\n"}, false, []string{CatLiteral}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := runPair(t, c.base, c.head, "")
			if r.Inert != c.inert || !slices.Equal(categories(r), c.cats) {
				t.Errorf("inert %v %v, want %v %v:\n%s", r.Inert, categories(r), c.inert, c.cats, mustJSON(t, r))
			}
			for _, e := range r.Entries {
				if e.Inert && len(e.TriggerIDs) > 0 {
					t.Errorf("%s: inert entry with triggers", e.Path)
				}
				if !e.Inert && len(e.TriggerIDs) == 0 {
					t.Errorf("%s: non-inert entry with no trigger", e.Path)
				}
			}
		})
	}
}

func TestModeOnlyChange(t *testing.T) {
	files := map[string]string{"main.tf": rds, "README.md": "a\n"}
	base, head := writeTree(t, files), writeTree(t, files)
	if err := os.Chmod(filepath.Join(head, "README.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	r, err := Run(Options{Head: head, Base: base, Limits: facts.DefaultLimits})
	if err != nil {
		t.Fatal(err)
	}
	if r.Inert || len(r.Entries) != 1 || r.Entries[0].ChangeKind != KindMode || len(r.Triggers) != 1 ||
		r.Triggers[0].ChangeKind != KindMode || r.Triggers[0].Category != CatUnclassified {
		t.Errorf("mode-only change: %s", mustJSON(t, r))
	}
}

func TestRename(t *testing.T) {
	r := runPair(t, map[string]string{"main.tf": rds, "a.txt": "same"}, map[string]string{"main.tf": rds, "b.txt": "same"}, "")
	if len(r.Entries) != 1 || r.Entries[0].ChangeKind != KindRenamed || r.Entries[0].Path != "b.txt" ||
		str(r.Entries[0].PreviousPath) != "a.txt" {
		t.Errorf("rename: %s", mustJSON(t, r.Entries))
	}
}

// TestCategories checks one change per category.
func TestCategories(t *testing.T) {
	vars := "variable \"a\" {\n  type    = string\n  default = \"x\"\n}\nvariable \"n\" {\n  type = number\n  default = 1\n}\n"
	cases := []struct {
		name        string
		base, head  string
		cat         string
		levels      []string
		address     string
		attribute   string
		emptyReason bool
	}{
		{"wrapper added", `locals { x = var.a }`, `locals { x = trimspace(var.a) }`, CatWrapper, []string{"L1"}, "local.x", "", true},
		{"wrapper swapped", `locals { x = upper(var.a) }`, `locals { x = lower(var.a) }`, CatWrapper, []string{"L1"}, "local.x", "", true},
		{"local changed", `locals { x = var.a }`, `locals { x = "${var.a}-y" }`, CatLocalsOutputs, []string{"L1"}, "local.x", "", true},
		{"local added", ``, `locals { x = var.a }`, CatLocalsOutputs, []string{"L1"}, "local.x", "", true},
		{"output changed", `output "o" { value = var.a }`, `output "o" { value = var.n }`, CatLocalsOutputs, []string{"L1"}, "output.o", "value", true},
		{"conditional", `locals { x = var.n > 1 ? "a" : "b" }`, `locals { x = var.n > 2 ? "a" : "b" }`, CatCountForEach, []string{"L1"}, "local.x", "", true},
		{"count", "resource \"aws_s3_bucket\" \"b\" {\n  count = var.n\n}\n", "resource \"aws_s3_bucket\" \"b\" {\n  count = var.n + 1\n}\n", CatCountForEach, []string{"L1"}, "aws_s3_bucket.b", "count", true},
		{"default", "variable \"d\" {\n  default = 1\n}\n", "variable \"d\" {\n  default = 2\n}\n", CatDefault, []string{"L1"}, "var.d", "default", true},
		{"type", "variable \"d\" {\n  type = string\n}\n", "variable \"d\" {\n  type = number\n}\n", CatTypeValidation, []string{"L1"}, "var.d", "type", true},
		{"validation", "variable \"d\" {\n  type = string\n}\n", "variable \"d\" {\n  type = string\n  validation {\n    condition     = length(var.d) > 0\n    error_message = \"x\"\n  }\n}\n", CatTypeValidation, []string{"L1"}, "var.d", "validation", true},
		{"literal", "resource \"aws_s3_bucket\" \"b\" {\n  bucket = \"a\"\n}\n", "resource \"aws_s3_bucket\" \"b\" {\n  bucket = \"b\"\n}\n", CatLiteral, []string{"L4"}, "aws_s3_bucket.b", "bucket", true},
		{"resource added", ``, "resource \"aws_s3_bucket\" \"b\" {\n  bucket = \"b\"\n}\n", CatResourceAddRemove, []string{"L4"}, "aws_s3_bucket.b", "", true},
		{"module removed", "module \"m\" {\n  source = \"./m\"\n}\n", ``, CatResourceAddRemove, []string{"L4"}, "module.m", "", true},
		{"lifecycle", "resource \"aws_s3_bucket\" \"b\" {\n  lifecycle {\n    prevent_destroy = false\n  }\n}\n", "resource \"aws_s3_bucket\" \"b\" {\n  lifecycle {\n    prevent_destroy = true\n  }\n}\n", CatLifecycle, []string{"L4"}, "aws_s3_bucket.b", "lifecycle.prevent_destroy", true},
		{"dynamic", "resource \"aws_s3_bucket\" \"b\" {\n  dynamic \"x\" {\n    for_each = var.a\n    content {}\n  }\n}\n", "resource \"aws_s3_bucket\" \"b\" {\n  dynamic \"x\" {\n    for_each = var.n\n    content {}\n  }\n}\n", CatDynamic, []string{"L4"}, "aws_s3_bucket.b", "dynamic.x", true},
		{"moved", ``, "moved {\n  from = aws_s3_bucket.a\n  to   = aws_s3_bucket.b\n}\n", CatMoved, []string{}, "aws_s3_bucket.b", "", false},
		{"renamed resource", "resource \"aws_s3_bucket\" \"a\" {\n  bucket = \"x\"\n}\n", "resource \"aws_s3_bucket\" \"b\" {\n  bucket = \"x\"\n}\n", CatMoved, []string{}, "aws_s3_bucket.b", "", false},
		{"version floor", "terraform {\n  required_version = \">= 1.0\"\n}\n", "terraform {\n  required_version = \">= 1.5\"\n}\n", CatVersionFloor, []string{"compat"}, "terraform", "required_version", true},
		{"provider floor", "terraform {\n  required_providers {\n    aws = {\n      source  = \"hashicorp/aws\"\n      version = \">= 5.0\"\n    }\n  }\n}\n", "terraform {\n  required_providers {\n    aws = {\n      source  = \"hashicorp/aws\"\n      version = \">= 6.0\"\n    }\n  }\n}\n", CatVersionFloor, []string{"compat"}, "terraform", "required_providers.aws", true},
		{"provisioner", "resource \"terraform_data\" \"x\" {\n  provisioner \"local-exec\" {\n    command = \"echo a\"\n  }\n}\n", "resource \"terraform_data\" \"x\" {\n  provisioner \"local-exec\" {\n    command = \"echo b\"\n  }\n}\n", CatUnclassified, []string{}, "terraform_data.x", "provisioner.local-exec", false},
		{"general expression", "resource \"aws_s3_bucket\" \"b\" {\n  bucket = var.a\n}\n", "resource \"aws_s3_bucket\" \"b\" {\n  bucket = local.x\n}\n", CatUnclassified, []string{"L4"}, "aws_s3_bucket.b", "bucket", true},
		{"provider block", "provider \"aws\" {\n  region = \"a\"\n}\n", "provider \"aws\" {\n  region = \"b\"\n}\n", CatLiteral, []string{"L4"}, "provider.aws", "region", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := runPair(t, map[string]string{"variables.tf": vars, "main.tf": c.base + "\n"},
				map[string]string{"variables.tf": vars, "main.tf": c.head + "\n"}, "")
			// A resource attribute with no schema also yields an unknown
			// propagation trigger; this test looks at the first one only.
			if len(r.Triggers) == 0 {
				t.Fatalf("no trigger: %s", mustJSON(t, r))
			}
			tr := r.Triggers[0]
			if tr.Category != c.cat || !slices.Equal(tr.Levels, c.levels) || str(tr.Address) != c.address ||
				(c.attribute != "" && str(tr.Attribute) != c.attribute) || (tr.Reason == nil) != c.emptyReason {
				t.Errorf("got %s %v %s %s reason %s, want %s %v %s %s:\n%s", tr.Category, tr.Levels, str(tr.Address),
					str(tr.Attribute), str(tr.Reason), c.cat, c.levels, c.address, c.attribute, mustJSON(t, r))
			}
		})
	}
}

func TestImpureNotPure(t *testing.T) {
	r := runPair(t, map[string]string{"main.tf": `locals { x = var.a }` + "\n"},
		map[string]string{"main.tf": `locals { x = timestamp() }` + "\n"}, "")
	if len(r.Triggers) != 1 || r.Triggers[0].Pure {
		t.Errorf("timestamp() must not be pure: %s", mustJSON(t, r))
	}
	r = runPair(t, map[string]string{"main.tf": "resource \"x_y\" \"z\" {\n  a = self.id\n}\n"},
		map[string]string{"main.tf": "resource \"x_y\" \"z\" {\n  a = upper(self.id)\n}\n"}, "")
	if len(r.Triggers) == 0 || r.Triggers[0].Pure {
		t.Errorf("self must not be pure: %s", mustJSON(t, r))
	}
}

func TestCap(t *testing.T) {
	var base, head strings.Builder
	base.WriteString("resource \"aws_s3_bucket\" \"b\" {\n")
	head.WriteString("resource \"aws_s3_bucket\" \"b\" {\n")
	for i := range 45 {
		fmt.Fprintf(&base, "  a%02d = \"x\"\n", i)
		fmt.Fprintf(&head, "  a%02d = \"y\"\n", i)
	}
	base.WriteString("}\n")
	head.WriteString("}\n")
	r := runPair(t, map[string]string{"main.tf": base.String()}, map[string]string{"main.tf": head.String()}, "")
	if len(r.Triggers) != MaxTriggers || r.Capped != 5 {
		t.Fatalf("got %d triggers, %d capped", len(r.Triggers), r.Capped)
	}
	if r.Triggers[39].ID != "T-040" || len(r.Entries[0].TriggerIDs) != MaxTriggers {
		t.Errorf("ids: %s, entry %d", r.Triggers[39].ID, len(r.Entries[0].TriggerIDs))
	}
}

func TestDeterministic(t *testing.T) {
	base := map[string]string{"main.tf": "locals {\n  a = 1\n  b = 2\n  c = 3\n}\n", "x/main.tf": `locals { a = 1 }` + "\n", "z.sh": "a"}
	head := map[string]string{"main.tf": "locals {\n  a = 2\n  b = 3\n  c = 4\n}\n", "x/main.tf": `locals { a = 2 }` + "\n", "z.sh": "b"}
	first := mustJSON(t, runPair(t, base, head, ""))
	for range 10 {
		if got := mustJSON(t, runPair(t, base, head, "")); !bytes.Equal(got, first) {
			t.Fatalf("output differs between runs:\n%s\n%s", got, first)
		}
	}
}
