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
		"encryption":           {"terraform {\n  encryption {\n  }\n}\n", []int{2}},
		"provider for_each":    {"provider \"aws\" {\n  alias = \"x\"\n  for_each = toset([])\n}\n", []int{3}},
		"one-line provider":    {"provider \"aws\" { for_each = toset([]) }\n", []int{1}},
		"unterminated file":    {"terraform {\n  encryption {\n", []int{2}},
		"resource for_each":    {"resource \"a\" \"b\" {\n  for_each = x\n}\n", nil},
		"nested encryption":    {"resource \"a\" \"b\" {\n  encryption {\n  }\n}\n", nil},
		"deeper in terraform":  {"terraform {\n  backend \"s3\" {\n    encryption {\n    }\n  }\n}\n", nil},
		"heredoc and comment":  {"locals {\n  x = <<EOT\nprovider \"aws\" {\n  for_each = y\n}\nEOT\n}\n# terraform {\n#   encryption {\n", nil},
		"object key":           {"provider \"aws\" {\n  default_tags {\n    tags = { for_each = \"x\" }\n  }\n}\n", nil},
		"encryption argument":  {"terraform {\n  encryption = {}\n}\n", nil},
		"comment before file":  {"# top\nterraform {\n encryption {\n }\n}\n", []int{3}},
		"comment in terraform": {"terraform {\n # c\n encryption {\n }\n}\n", []int{3}},
		"hash after alias":     {"provider \"aws\" {\n  alias = \"x\" # c\n  for_each = toset([])\n}\n", []int{3}},
		"slashes after alias":  {"provider \"aws\" {\n  alias = \"x\" // c\n  for_each = toset([])\n}\n", []int{3}},
		"slashes before file":  {"// top\nterraform {\n // c\n encryption {\n }\n}\n", []int{4}},
		"block comment inline": {"terraform { /* c */\n  encryption {\n  }\n}\n", []int{2}},
	} {
		got, ok := tofuOnlyLines([]byte(c.src))
		if !ok || !slices.Equal(got, c.want) {
			t.Errorf("%s: %v %v, want %v", name, got, ok, c.want)
		}
	}
	if _, ok := tofuOnlyLines([]byte("locals {\n  x = \"open\n}\n")); ok {
		t.Error("an unterminated string lexed without an error")
	}
}

// TestNonstandardLexError checks a file in the change the lexer cannot read
// leaves the rule undecided, and an untouched one does not.
func TestNonstandardLexError(t *testing.T) {
	const id = "profile.nonstandard-config-file"
	bad := "locals {\n  x = \"open\n}\n"
	if s := statusOf(runProfileTool(t, map[string]string{"main.tf": ""}, map[string]string{"main.tf": bad}).Manifest, id, "."); s != "not_evaluated: main.tf cannot be lexed, so its blocks are not read" {
		t.Errorf("edited file: %s", s)
	}
	if s := statusOf(runProfileTool(t, map[string]string{"main.tf": bad, "a.tf": ""}, map[string]string{"main.tf": bad, "a.tf": "#\n"}).Manifest, id, "."); s != StatusClean {
		t.Errorf("untouched file: %s", s)
	}
	r := runProfileTool(t, map[string]string{"main.tf": ""}, map[string]string{"main.tf": "", "a.tofu.json": "{}", "b.tofu": ""})
	if got := findingSpots(r, id); !slices.Equal(got, []string{"a.tofu.json", "b.tofu"}) {
		t.Errorf("tofu files: %q", got)
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
	// With no variables.tf the finding names the root, which the change adds.
	delete(head, "variables.tf")
	if got := findingSpots(runProfileTool(t, map[string]string{}, head), "profile.putin-khuylo-missing"); !slices.Equal(got, []string{"."}) {
		t.Errorf("no variables.tf: %q", got)
	}
}

// putinVar is the input a base root declares for the creation gate rule to
// judge it; it goes last so the line numbers of the other blocks hold.
const putinVar = "\nvariable \"putin_khuylo\" {\n  type = bool\n}\n"

// TestPutinGateLostInBaseInput checks a creation input named only by the
// base: the change drops the gate from the create_<x> local, and the
// untouched block that reads it is reported at that local.
func TestPutinGateLostInBaseInput(t *testing.T) {
	block := "\nresource \"a\" \"b\" {\n  count = local.create_bucket ? 1 : 0\n}\n"
	base := map[string]string{"main.tf": "locals {\n  create_bucket = var.create_bucket && var.putin_khuylo\n}\n" + block + putinVar}
	head := map[string]string{"main.tf": "locals {\n  create_bucket = var.create_bucket\n}\n" + block + putinVar}
	if got := findingSpots(runProfileTool(t, base, head), "profile.putin-khuylo-missing"); !slices.Equal(got, []string{"main.tf:2"}) {
		t.Errorf("findings %q", got)
	}
}

// TestPutinGateThroughLocals checks a gate read through a local counts, and
// only create and create_<x> variables are creation inputs.
func TestPutinGateThroughLocals(t *testing.T) {
	locals := "locals {\n  gate          = var.putin_khuylo\n  create_bucket = var.create_bucket && local.gate\n  subnets       = length(var.subnets) > 0 && local.gate\n}\n"
	base := map[string]string{"main.tf": locals + putinVar}
	head := map[string]string{"main.tf": locals +
		"\nresource \"a\" \"gated\" {\n  count = local.create_bucket ? 1 : 0\n}\n" +
		"\nresource \"a\" \"bypass\" {\n  count = var.create_bucket ? 1 : 0\n}\n" +
		"\nresource \"a\" \"subnets\" {\n  count = length(var.subnets)\n}\n" + putinVar}
	if got := findingSpots(runProfileTool(t, base, head), "profile.putin-khuylo-missing"); !slices.Equal(got, []string{"main.tf:12"}) {
		t.Errorf("findings %q", got)
	}
}

// TestPutinCreationInputs checks a create_<x> input a local combines with
// the gate is a creation input, and a block that reads it past the gate is
// a finding.
func TestPutinCreationInputs(t *testing.T) {
	locals := "locals {\n  create_bucket = var.create_bucket && var.putin_khuylo\n}\n"
	base := map[string]string{"main.tf": locals + putinVar}
	head := map[string]string{"main.tf": locals +
		"\nresource \"a\" \"gated\" {\n  count = local.create_bucket ? 1 : 0\n}\n" +
		"\nresource \"a\" \"bypass\" {\n  count = var.create_bucket ? 1 : 0\n}\n" +
		"\nresource \"a\" \"other\" {\n  count = var.create ? 1 : 0\n}\n" + putinVar}
	if got := findingSpots(runProfileTool(t, base, head), "profile.putin-khuylo-missing"); !slices.Equal(got, []string{"main.tf:10"}) {
		t.Errorf("findings %q", got)
	}
}

// TestPutinGateFromSyntax checks the gate decides a count only as an operand
// of the && chain, as a gated condition or true branch beside a branch that
// creates nothing, or as a gated for filter.
func TestPutinGateFromSyntax(t *testing.T) {
	locals := "locals {\n  create = var.create && var.putin_khuylo\n  gate   = var.putin_khuylo\n  c2     = var.create || var.putin_khuylo\n}\n"
	for name, c := range map[string]struct {
		meta string
		want bool
	}{
		"or":                {"count = var.create || var.putin_khuylo ? 1 : 0", true},
		"or local":          {"count = var.create || local.gate ? 1 : 0", true},
		"or in local":       {"count = local.c2 ? 1 : 0", true},
		"gate in false":     {"count = var.create ? 1 : (var.putin_khuylo ? 1 : 0)", true},
		"gate creates both": {"count = var.create ? (var.putin_khuylo ? 1 : 1) : 0", true},
		"not":               {"count = var.create && !var.putin_khuylo ? 1 : 0", true},
		"function argument": {"for_each = { for k, v in var.m : k => v if var.create && length(tostring(var.putin_khuylo)) > 0 }", true},
		"and local":         {"count = var.create && local.create ? 1 : 0", false},
		"gate outside":      {"count = var.putin_khuylo ? (var.create ? 1 : 0) : 0", false},
		"gate in true":      {"count = var.create ? (var.putin_khuylo ? 1 : 0) : 0", false},
		"parentheses":       {"count = (var.create && (local.gate)) ? 1 : 0", false},
		"empty for_each":    {"for_each = local.create ? var.m : toset([])", false},
		"for filter":        {"for_each = { for k, v in var.m : k => v if local.create }", false},
	} {
		head := map[string]string{"main.tf": locals + "\nresource \"a\" \"b\" {\n  " + c.meta + "\n}\n" + putinVar}
		got := findingSpots(runProfileTool(t, map[string]string{"main.tf": locals + putinVar}, head), "profile.putin-khuylo-missing")
		if want := map[bool][]string{true: {"main.tf:8"}}[c.want]; !slices.Equal(got, want) {
			t.Errorf("%s: %q, want %q", name, got, want)
		}
	}
}

// TestPutinBaseRootWithoutInput checks the base decides the scope: a root on
// the base that never declared the input is silent, a root the change adds is
// judged whole, and a base root that declared it and loses it is reported.
func TestPutinBaseRootWithoutInput(t *testing.T) {
	const id = "profile.putin-khuylo-missing"
	locals := "locals {\n  create = var.create\n}\n"
	block := "\nresource \"a\" \"b\" {\n  count = local.create ? 1 : 0\n}\n"
	edited := "\nresource \"a\" \"b\" {\n  count = local.create ? 2 : 0\n}\n"
	base := map[string]string{"main.tf": locals + block}
	if got := findingSpots(runProfileTool(t, base, map[string]string{"main.tf": locals + edited}), id); len(got) != 0 {
		t.Errorf("base root without the input: %q", got)
	}
	// A new root with the same blocks is still judged.
	if got := findingSpots(runProfileTool(t, map[string]string{}, map[string]string{"main.tf": locals + block}), id); len(got) == 0 {
		t.Error("new root without the input: no finding")
	}
	// A base root that declared the input and gated the local; the head drops both.
	gated := "locals {\n  create = var.create && var.putin_khuylo\n}\n" + block + putinVar
	if got := findingSpots(runProfileTool(t, map[string]string{"main.tf": gated}, map[string]string{"main.tf": locals + block}), id); !slices.Equal(got, []string{"main.tf:2"}) {
		t.Errorf("input removed at head: %q", got)
	}
}
