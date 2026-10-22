package facts

import (
	"slices"
	"strconv"
	"testing"
)

// runProfileTool runs the profile tool rules on a head and a base tree, with
// the test profile.
func runProfileTool(t *testing.T, base, head map[string]string) *Report {
	t.Helper()
	r, err := run(Options{Head: writeTree(t, head), Base: writeTree(t, base), ProfileDir: testProfile, Limits: DefaultLimits}, profileToolRules)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func findingSpots(r *Report, rule string) []string {
	var out []string
	for _, f := range r.Findings {
		if f.RuleID == rule {
			s := f.File
			if f.Line != nil {
				s += ":" + strconv.Itoa(*f.Line)
			}
			out = append(out, s)
		}
	}
	slices.Sort(out)
	return out
}

func TestTofuOnlyLines(t *testing.T) {
	for name, c := range map[string]struct {
		src  string
		want []int
	}{
		"encryption":          {"terraform {\n  encryption {\n  }\n}\n", []int{2}},
		"provider for_each":   {"provider \"aws\" {\n  alias = \"x\"\n  for_each = toset([])\n}\n", []int{3}},
		"one-line provider":   {"provider \"aws\" { for_each = toset([]) }\n", []int{1}},
		"unterminated file":   {"terraform {\n  encryption {\n", []int{2}},
		"resource for_each":   {"resource \"a\" \"b\" {\n  for_each = x\n}\n", nil},
		"nested encryption":   {"resource \"a\" \"b\" {\n  encryption {\n  }\n}\n", nil},
		"deeper in terraform": {"terraform {\n  backend \"s3\" {\n    encryption {\n    }\n  }\n}\n", nil},
		"heredoc and comment": {"locals {\n  x = <<EOT\nprovider \"aws\" {\n  for_each = y\n}\nEOT\n}\n# terraform {\n#   encryption {\n", nil},
		"object key":          {"provider \"aws\" {\n  default_tags {\n    tags = { for_each = \"x\" }\n  }\n}\n", nil},
		"encryption argument": {"terraform {\n  encryption = {}\n}\n", nil},
	} {
		if got := tofuOnlyLines([]byte(c.src)); !slices.Equal(got, c.want) {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
	}
}

func TestNonstandardLinks(t *testing.T) {
	const id = "profile.nonstandard-config-file"
	base := map[string]string{"main.tf": "", "a.tf": "", "old.tf": "->a.tf"}
	head := map[string]string{"main.tf": "", "a.tf": "", "old.tf": "->a.tf"}
	if s := statusOf(runProfileTool(t, base, head).Manifest, id, "."); s != "missing" {
		t.Errorf("unchanged tree: %s", s)
	}
	head["main.tf"] = "# edited\n"
	if s := statusOf(runProfileTool(t, base, head).Manifest, id, "."); s != StatusClean {
		t.Errorf("untouched link: %s", s)
	}
	head["new.tf"] = "->a.tf"
	if s := statusOf(runProfileTool(t, base, head).Manifest, id, "."); s != "not_evaluated: new.tf is a symbolic link, so its blocks are not read" {
		t.Errorf("added link: %s", s)
	}
}

// TestNewModule runs an empty base: a new module is held to the whole file
// set, and a new root is judged whole for the creation gate.
func TestNewModule(t *testing.T) {
	vars := "variable \"create\" {\n  type = bool\n}\n"
	gate := "variable \"putin_khuylo\" {\n  type = bool\n}\n"
	head := map[string]string{
		"main.tf":           "resource \"a\" \"b\" {\n  count = var.create ? 1 : 0\n}\n",
		"variables.tf":      vars,
		"modules/s/main.tf": "", "examples/basic/main.tf": "",
	}
	r := runProfileTool(t, map[string]string{}, head)
	want := []string{"LICENSE", "README.md", "examples/complete", "outputs.tf", "versions.tf",
		"examples/basic/README.md", "examples/basic/outputs.tf", "examples/basic/versions.tf",
		"modules/s/README.md", "modules/s/outputs.tf", "modules/s/variables.tf", "modules/s/versions.tf"}
	want = append(want, []string{".editorconfig", ".gitignore", ".releaserc.json", ".pre-commit-config.yaml",
		".github/workflows/pre-commit.yml", ".github/workflows/release.yml", ".github/workflows/lock.yml",
		".github/workflows/pr-title.yml", ".github/workflows/stale-actions.yaml"}...)
	slices.Sort(want)
	if got := findingSpots(r, "profile.required-file-missing"); !slices.Equal(got, want) {
		t.Errorf("required files:\n%q\nwant\n%q", got, want)
	}
	if got := findingSpots(r, "profile.putin-khuylo-missing"); !slices.Equal(got, []string{"variables.tf:1"}) {
		t.Errorf("no gate input: %q", got)
	}
	head["variables.tf"] = vars + gate
	if got := findingSpots(runProfileTool(t, map[string]string{}, head), "profile.putin-khuylo-missing"); !slices.Equal(got, []string{"variables.tf:1"}) {
		t.Errorf("ungated block: %q", got)
	}
	head["main.tf"] = "locals {\n  create = var.create && var.putin_khuylo\n}\n\nresource \"a\" \"b\" {\n  count = local.create ? 1 : 0\n}\n"
	if s := statusOf(runProfileTool(t, map[string]string{}, head).Manifest, "profile.putin-khuylo-missing", "."); s != StatusClean {
		t.Errorf("gated new root: %s", s)
	}
}

// TestPutinCreationInputs checks a create_<x> input a local combines with
// the gate is a creation input, and a block that reads it past the gate is
// a finding.
func TestPutinCreationInputs(t *testing.T) {
	base := map[string]string{"main.tf": "locals {\n  create_bucket = var.create_bucket && var.putin_khuylo\n}\n"}
	head := map[string]string{"main.tf": base["main.tf"] +
		"\nresource \"a\" \"gated\" {\n  count = local.create_bucket ? 1 : 0\n}\n" +
		"\nresource \"a\" \"bypass\" {\n  count = var.create_bucket ? 1 : 0\n}\n" +
		"\nresource \"a\" \"other\" {\n  count = var.create ? 1 : 0\n}\n"}
	if got := findingSpots(runProfileTool(t, base, head), "profile.putin-khuylo-missing"); !slices.Equal(got, []string{"main.tf:10"}) {
		t.Errorf("findings %q", got)
	}
}
