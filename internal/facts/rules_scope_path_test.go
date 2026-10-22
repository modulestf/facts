package facts

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// runScopePath runs the scope_path rules on a base and a head tree with the
// test profile, or with none when profileDir is "".
func runScopePath(t *testing.T, base, head map[string]string, profileDir string) *Report {
	t.Helper()
	r, err := run(Options{Head: writeTree(t, head), Base: writeTree(t, base), ProfileDir: profileDir, Limits: DefaultLimits}, scopePathRules)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func readTemplate(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(testProfile, "templates", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func candidatesOf(r *Report, rule string) []Candidate {
	return slices.DeleteFunc(slices.Clone(r.Candidates), func(c Candidate) bool { return c.RuleID != rule })
}

// The readers take every template of the test profile, so a template the
// profile ships never makes a rule not_evaluated.
func TestScopePathReadsEveryTemplate(t *testing.T) {
	entries, err := os.ReadDir(filepath.Join(testProfile, "templates"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		data := []byte(readTemplate(t, e.Name()))
		var n int
		var ok bool
		switch {
		case strings.HasPrefix(e.Name(), "github-workflows-"):
			var items []profileItem
			items, ok = readWorkflow(data)
			n = len(items)
		case e.Name() == ".releaserc.json.template":
			var items []profileItem
			items, ok = readReleaserc(data)
			n = len(items)
		case e.Name() == ".pre-commit-config.yaml.template":
			var pairs []hookPair
			pairs, ok = readPreCommit(data)
			n = len(pairs)
		default:
			continue
		}
		if !ok || n == 0 {
			t.Errorf("%s: ok %v, %d items", e.Name(), ok, n)
		}
	}
}

func TestScopePathRuleShape(t *testing.T) {
	for _, r := range scopePathRules {
		if strings.HasPrefix(r.ID, "scope.") && !r.NeedsBase {
			t.Errorf("%s does not need the base", r.ID)
		}
	}
}

func TestScopePathNeedsTheProfile(t *testing.T) {
	r := runScopePath(t, map[string]string{"main.tf": ""}, map[string]string{"main.tf": "# x\n", "examples/a/main.tf": ""}, "")
	for _, e := range r.Manifest {
		if e.Status != StatusNotApplicable && e.ReasonCode != ReasonProfileUnavailable {
			t.Errorf("%s in %s: %s %s", e.RuleID, e.Area, e.Status, e.ReasonCode)
		}
	}
}

func TestUnpinned(t *testing.T) {
	for _, c := range []struct{ a, b string }{
		{"      - uses: actions/stale@v10\n", "      - uses: actions/stale@0123456789abcdef0123456789abcdef01234567 # v10.1.0\n"},
		{"            @semantic-release/git@10.0.1 \\\n", "            @semantic-release/git@10.2.0 \\\n"},
		{"  TFLINT_VERSION: v0.64.0\n", "  TFLINT_VERSION: 'v0.70.1'\n"},
		{"          node-version: 24\n", "          node-version: 22\n"},
		{"    rev: v1.108.1\n", "    rev: v1.110.0\n"},
	} {
		if a, b := string(unpinned([]byte(c.a))), string(unpinned([]byte(c.b))); a != b {
			t.Errorf("%q and %q differ unpinned: %q %q", c.a, c.b, a, b)
		}
	}
	for _, c := range []struct{ a, b string }{
		{"      - uses: actions/stale@v10\n", "      - uses: example/stale@v10\n"},
		{"          terraform-version: ${{ a }}\n", "          terraform-version: ${{ b }}\n"},
		{"  indent_size = 2\n", "  indent_size = 4\n"},
	} {
		if string(unpinned([]byte(c.a))) == string(unpinned([]byte(c.b))) {
			t.Errorf("%q and %q are equal unpinned", c.a, c.b)
		}
	}
}

// A file the rule must read that is a link, or YAML outside the subset the
// engine reads, is not_evaluated, never clean.
func TestScopePathUnreadable(t *testing.T) {
	banner := "[![SWUbanner](x)](y)\n"
	r := runScopePath(t,
		map[string]string{"README.md": banner, ".editorconfig": readTemplate(t, ".editorconfig.template"),
			".pre-commit-config.yaml": readTemplate(t, ".pre-commit-config.yaml.template"),
			".github/workflows/x.yml": "jobs:\n  a:\n    steps:\n      - run: a\n"},
		map[string]string{"README.md": "->other.md", ".editorconfig": "->README.md",
			".pre-commit-config.yaml": "repos: &r\n  - repo: local\n",
			".github/workflows/x.yml": "jobs:\n  a:\n    steps:\n      - run: a\n        if: a long\n          condition\n"}, testProfile)
	for rule, want := range map[string]string{
		"scope.readme-banner":    ReasonSymlink,
		"profile.template-drift": ReasonSymlink,
		"scope.pre-commit-hook":  ReasonUnsupportedFile,
		"scope.profile-file":     ReasonUnsupportedFile,
	} {
		i := slices.IndexFunc(r.Manifest, func(e ManifestEntry) bool { return e.RuleID == rule })
		if i < 0 {
			t.Fatalf("%s: no manifest entry", rule)
		}
		if e := r.Manifest[i]; e.Status != StatusNotEvaluated || e.ReasonCode != want {
			t.Errorf("%s: %+v, want not_evaluated %s", rule, e, want)
		}
	}
}

// Many drifted lines, and one past the text cap, set evidence_complete false.
func TestTemplateDriftEvidenceCap(t *testing.T) {
	tpl := readTemplate(t, ".editorconfig.template")
	head := tpl + strings.Repeat("x = 1\n", maxEvidenceLines+1)
	long := tpl[:strings.Index(tpl, "\n")+1] + strings.Repeat("y", maxEvidenceText+1) + "\n" + tpl[strings.Index(tpl, "\n")+1:]
	for name, h := range map[string]string{"lines": head, "text": long} {
		r := runScopePath(t, map[string]string{".editorconfig": tpl}, map[string]string{".editorconfig": h}, testProfile)
		c := candidatesOf(r, "profile.template-drift")
		if len(c) != 1 || c[0].EvidenceComplete || len(c[0].Evidence.SourceLines) > maxEvidenceLines {
			t.Errorf("%s: %+v", name, c)
		}
	}
}

// A release option is not a key or a plugin: the edit is template drift and
// no scope.profile-file candidate. Removing a plugin the base has is one.
func TestReleasercItems(t *testing.T) {
	tpl := readTemplate(t, ".releaserc.json.template")
	for name, c := range map[string]struct {
		head        string
		drift, file int
	}{
		"option":         {strings.Replace(tpl, `"ci": false`, `"ci": true`, 1), 1, 0},
		"plugin removed": {strings.Replace(tpl, `"@semantic-release/git"`, `"@semantic-release/exec"`, 1), 1, 1},
		"key added":      {strings.Replace(tpl, `"ci": false,`, `"ci": false, "dryRun": true,`, 1), 1, 1},
	} {
		r := runScopePath(t, map[string]string{".releaserc.json": tpl}, map[string]string{".releaserc.json": c.head}, testProfile)
		if d, f := len(candidatesOf(r, "profile.template-drift")), len(candidatesOf(r, "scope.profile-file")); d != c.drift || f != c.file {
			t.Errorf("%s: %d drift and %d profile-file candidates, want %d and %d", name, d, f, c.drift, c.file)
		}
	}
}

// When the change creates .pre-commit-config.yaml, a hook the template lacks
// is a candidate and a template hook is not. Deleting the file removes every
// pair, a candidate with no line.
func TestPreCommitHookAgainstTemplate(t *testing.T) {
	tpl := readTemplate(t, ".pre-commit-config.yaml.template")
	r := runScopePath(t, map[string]string{"main.tf": ""},
		map[string]string{"main.tf": "", ".pre-commit-config.yaml": tpl + "  - repo: local\n    hooks:\n      - id: checkov\n"}, testProfile)
	idLine := strings.Count(tpl, "\n") + 3
	if c := candidatesOf(r, "scope.pre-commit-hook"); len(c) != 1 || c[0].Line == nil || *c[0].Line != idLine || *c[0].Evidence.Head != "checkov" {
		t.Errorf("added hook: %+v", c)
	}
	r = runScopePath(t, map[string]string{"main.tf": ""}, map[string]string{"main.tf": "", ".pre-commit-config.yaml": tpl}, testProfile)
	if c := candidatesOf(r, "scope.pre-commit-hook"); len(c) != 0 {
		t.Errorf("template hooks: %+v", c)
	}
	r = runScopePath(t, map[string]string{"main.tf": "", ".pre-commit-config.yaml": tpl}, map[string]string{"main.tf": "# x\n"}, testProfile)
	if c := candidatesOf(r, "scope.pre-commit-hook"); len(c) != 1 || c[0].Line != nil {
		t.Errorf("deleted file: %+v", c)
	}
}

// A workflow the change adds that is one of the profile's workflows is not a
// new workflow file; any other is. Removing a job the base has is a candidate.
func TestProfileFileWorkflows(t *testing.T) {
	stale := readTemplate(t, "github-workflows-stale-actions.yaml.template")
	r := runScopePath(t, map[string]string{"main.tf": ""},
		map[string]string{"main.tf": "", ".github/workflows/stale-actions.yaml": stale, ".github/workflows/scan.yml": "on: push\n"}, testProfile)
	if c := candidatesOf(r, "scope.profile-file"); len(c) != 1 || c[0].File != ".github/workflows/scan.yml" {
		t.Errorf("new workflows: %+v", c)
	}
	base := stale + "  extra:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo\n"
	r = runScopePath(t, map[string]string{".github/workflows/stale-actions.yaml": base},
		map[string]string{".github/workflows/stale-actions.yaml": stale}, testProfile)
	if c := candidatesOf(r, "scope.profile-file"); len(c) != 1 || c[0].Line != nil || *c[0].Evidence.Base != "job extra" {
		t.Errorf("removed job: %+v", c)
	}
}

// A README that lacked the banner at the base and still does is clean; one
// the change deletes after it had the banner is a candidate; a change with no
// README at either revision is clean.
func TestReadmeBannerCases(t *testing.T) {
	banner := "[![SWUbanner](https://raw.githubusercontent.com/vshymanskyy/StandWithUkraine/main/banner2-direct.svg)](https://github.com/vshymanskyy/StandWithUkraine/blob/main/docs/README.md)\r\n"
	for name, c := range map[string]struct {
		base, head map[string]string
		want       int
	}{
		"lacked and lacks": {map[string]string{"README.md": "# a\n"}, map[string]string{"README.md": "# b\n"}, 0},
		"deleted":          {map[string]string{"README.md": banner}, map[string]string{"main.tf": ""}, 1},
		"none":             {map[string]string{"main.tf": ""}, map[string]string{"main.tf": "# x\n"}, 0},
		"kept with CRLF":   {map[string]string{"README.md": banner}, map[string]string{"README.md": "# a\r\n" + banner}, 0},
	} {
		r := runScopePath(t, c.base, c.head, testProfile)
		if got := len(candidatesOf(r, "scope.readme-banner")); got != c.want {
			t.Errorf("%s: %d candidates, want %d", name, got, c.want)
		}
	}
}

// Each form is valid YAML the subset reader would misread, so it refuses
// it; the rule is then not_evaluated(unsupported_file). The control parses.
func TestReadYAMLRefusals(t *testing.T) {
	const hooks = "    hooks:\n      - id: a\n"
	for name, c := range map[string]struct {
		text string
		ok   bool
	}{
		"control":                 {"---\nrepos:\n  - repo: local\n" + hooks, true},
		"flow item":               {"repos:\n  - {repo: local, hooks: [{id: a}]}\n", false},
		"flow list item":          {"repos:\n  - repo: local\n    hooks:\n      - [a]\n", false},
		"flow hooks value":        {"repos:\n  - repo: local\n    hooks: [{id: a}]\n", false},
		"flow repos value":        {"repos: [{repo: local}]\n", false},
		"space before colon":      {"repos:\n  - repo: local\n    hooks:\n      - id : a\n", false},
		"escape in quoted key":    {"repos:\n  - repo: local\n    hooks:\n      - \"i\\u0064\": a\n", false},
		"escape in quoted value":  {"repos:\n  - repo: local\n    hooks:\n      - id: \"a\\\"b\"\n", false},
		"doubled single quote":    {"repos:\n  - repo: local\n    hooks:\n      - id: 'it''s'\n", false},
		"duplicate key":           {"repos:\n  - repo: local\n    repo: https://example.com/x\n" + hooks, false},
		"duplicate top-level key": {"repos:\n  - repo: a\n" + hooks + "repos:\n  - repo: b\n" + hooks, false},
		"second document":         {"repos:\n  - repo: local\n" + hooks + "---\nother:\n  x: 1\n", false},
		"anchor":                  {"repos:\n  - repo: &r local\n" + hooks, false},
		"alias":                   {"repos:\n  - repo: *r\n" + hooks, false},
		"nested item on one line": {"repos:\n  - - repo: local\n", false},
		"tab indent":              {"repos:\n\t- repo: local\n", false},
		"continued plain scalar":  {"repos:\n  - repo: local\n    rev: v1\n      continued\n" + hooks, false},
	} {
		if _, ok := readPreCommit([]byte(c.text)); ok != c.ok {
			t.Errorf("%s: ok %v, want %v", name, ok, c.ok)
		}
	}
	r := runScopePath(t, map[string]string{".pre-commit-config.yaml": "repos:\n  - repo: local\n" + hooks},
		map[string]string{".pre-commit-config.yaml": "repos:\n  - repo: local\n    hooks:\n      - {id: a}\n      - {id: b}\n"}, testProfile)
	if s := statusOf(r.Manifest, "scope.pre-commit-hook", "."); !strings.HasPrefix(s, StatusNotEvaluated) {
		t.Errorf("flow hooks: %s", s)
	}
}

// Flow-style jobs and steps would collapse onto one identity, and a step
// with a key used twice is ambiguous, so the workflow reader refuses them.
func TestReadWorkflowRefusals(t *testing.T) {
	for name, c := range map[string]struct {
		text string
		ok   bool
	}{
		"control":        {"jobs:\n  a:\n    steps:\n      - name: x\n      - name: y\n", true},
		"flow steps":     {"jobs:\n  a:\n    steps:\n      - {name: x}\n      - {name: y}\n", false},
		"flow job":       {"jobs:\n  a: {runs-on: x, steps: [{name: x}]}\n", false},
		"duplicate key":  {"jobs:\n  a:\n    steps:\n      - name: x\n        name: y\n", false},
		"duplicate jobs": {"jobs:\n  a:\n    runs-on: x\n  a:\n    runs-on: y\n", false},
	} {
		if _, ok := readWorkflow([]byte(c.text)); ok != c.ok {
			t.Errorf("%s: ok %v, want %v", name, ok, c.ok)
		}
	}
}

// A uses: repository the head no longer names is not a removal of its own:
// only the new repository is reported, with no base value.
func TestProfileFileUsesNotARemoval(t *testing.T) {
	wf := "jobs:\n  a:\n    steps:\n      - name: S\n        uses: %s@v1\n"
	r := runScopePath(t, map[string]string{".github/workflows/x.yml": strings.Replace(wf, "%s", "old-org/act", 1)},
		map[string]string{".github/workflows/x.yml": strings.Replace(wf, "%s", "new-org/act", 1)}, testProfile)
	c := candidatesOf(r, "scope.profile-file")
	if len(c) != 1 || c[0].Evidence.Base != nil || *c[0].Evidence.Head != "uses new-org/act" {
		t.Errorf("%+v", c)
	}
}

// A plugin written as an empty array names no plugin, so .releaserc.json is
// not read and the rule is not_evaluated.
func TestReleasercEmptyPluginTuple(t *testing.T) {
	if _, ok := readReleaserc([]byte(`{"plugins": [[]]}`)); ok {
		t.Error("empty plugin tuple read")
	}
	tpl := readTemplate(t, ".releaserc.json.template")
	r := runScopePath(t, map[string]string{".releaserc.json": tpl},
		map[string]string{".releaserc.json": strings.Replace(tpl, `"plugins": [`, `"plugins": [[],`, 1)}, testProfile)
	if s := statusOf(r.Manifest, "scope.profile-file", "."); !strings.HasPrefix(s, StatusNotEvaluated) {
		t.Error(s)
	}
}

// Four-letter partitions and escaped literals are regions too; an escaped
// allowed region is not a candidate.
func TestRegionForms(t *testing.T) {
	head := "provider \"aws\" {\n  region = \"eusc-de-east-1\"\n}\n\nlocals {\n  a = \"\\u0075s-west-2\"\n  b = \"\\u0065u-west-1\"\n  c = \"us-isob-east-1\"\n}\n"
	r := runScopePath(t, map[string]string{"examples/x/main.tf": ""}, map[string]string{"examples/x/main.tf": head}, testProfile)
	var lines []int
	for _, c := range candidatesOf(r, "profile.region-not-allowed") {
		lines = append(lines, *c.Line)
	}
	if !slices.Equal(lines, []int{2, 6, 8}) {
		t.Errorf("candidate lines %v, want [2 6 8]", lines)
	}
	if _, ok := decodeQuoted(`"\q"`); ok {
		t.Error("an invalid escape decoded")
	}
}
