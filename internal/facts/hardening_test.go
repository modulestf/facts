//go:build unix

package facts

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// Regression tests for the review of the first engine version.

func TestUnreadableDirectory(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads every directory")
	}
	files := map[string]string{
		"main.tf":                "",
		"modules/m/variables.tf": "variable \"x\" {}\n",
		"examples/e/main.tf":     "",
	}
	head := writeTree(t, files)
	base := writeTree(t, files)
	for _, d := range []string{"modules/m", "examples/e"} {
		if err := os.Chmod(filepath.Join(head, d), 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(filepath.Join(head, d), 0o755) })
	}
	for _, o := range []Options{{Head: head}, {Head: head, Base: base}} {
		o.Limits = DefaultLimits
		r, err := Run(o)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range []struct{ rule, area string }{
			{"structure.variable-untyped", "modules/m"},
			{"structure.missing-description", "modules/m"},
			{"examples.versions-missing", "examples/e"},
		} {
			want := "not_evaluated: " + c.area + " is a directory that cannot be read"
			if s := statusOf(r.Manifest, c.rule, c.area); s != want {
				t.Errorf("base=%v %s %s: %s", o.Base != "", c.rule, c.area, s)
			}
		}
		if len(r.Findings) != 0 {
			t.Errorf("findings %+v", r.Findings)
		}
	}
	// Unreadable at base: config rules cannot tell which variables are new.
	r, err := Run(Options{Head: base, Base: head, Limits: DefaultLimits})
	if err != nil {
		t.Fatal(err)
	}
	if s := statusOf(r.Manifest, "structure.variable-untyped", "modules/m"); s != "not_evaluated: at base: modules/m is a directory that cannot be read" {
		t.Error(s)
	}
}

func TestParserStackCaps(t *testing.T) {
	cases := map[string]struct{ src, want string }{
		"bang chain":     {"variable \"x\" {\n  default = " + strings.Repeat("!", 400000) + "true\n}\n", "tokens over the limit"},
		"minus chain":    {"variable \"x\" {\n  default = " + strings.Repeat("-", 400000) + "1\n}\n", "tokens over the limit"},
		"binary chain":   {"variable \"x\" {\n  description = " + strings.Repeat("1+", 520000) + "1\n}\n", "tokens over the limit"},
		"short bang run": {"variable \"x\" {\n  default = " + strings.Repeat("!", 1000) + "true\n}\n", "1000 consecutive unary operators over the limit 64"},
		"short minus":    {"variable \"x\" {\n  default = " + strings.Repeat("-", 65) + "1\n}\n", "65 consecutive unary operators over the limit 64"},
		"plus chain":     {"variable \"x\" {\n  default = " + strings.Repeat("+", 400000) + "1\n}\n", "tokens over the limit"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			r := runHead(t, map[string]string{"main.tf": tc.src})
			if s := statusOf(r.Manifest, "structure.missing-description", "."); !strings.Contains(s, tc.want) {
				t.Errorf("got %s", s)
			}
		})
	}
}

// A long binary chain under the token cap is read, never evaluated.
func TestBinaryChainUnderCap(t *testing.T) {
	src := "variable \"x\" {\n  type = string\n  description = " + strings.Repeat("1+", 45000) + "1\n}\n"
	r := runHead(t, map[string]string{"main.tf": src})
	if s := statusOf(r.Manifest, "structure.missing-description", "."); s != "clean" {
		t.Error(s)
	}
}

func TestNestingDepthClamp(t *testing.T) {
	src := strings.Repeat("]", 10) + strings.Repeat("[", 70)
	tokens, _ := hclsyntax.LexConfig([]byte(src), "x.tf", hcl.InitialPos)
	if d := nestingDepth(tokens); d != 70 {
		t.Errorf("depth %d, want 70", d)
	}
}

// The run time grows linearly with the number of variables: four times the
// files take well under the sixteen times a quadratic pass would. A ratio,
// not a wall-clock limit, so a slow or race-instrumented build still passes.
func TestManyVariablesAreLinear(t *testing.T) {
	// files of 2000 variables each stay under the per-file token cap.
	run := func(files int) (*Report, time.Duration) {
		head, base := map[string]string{"new.tf": "variable \"new\" {}\n"}, map[string]string{}
		for f := range files {
			var b strings.Builder
			for i := range 2000 {
				fmt.Fprintf(&b, "# v%d\nvariable \"v%d_%d\" {}\n", i, f, i)
			}
			name := fmt.Sprintf("v%02d.tf", f)
			head[name], base[name] = b.String(), b.String()
		}
		h, bs := writeTree(t, head), writeTree(t, base)
		start := time.Now()
		r, err := Run(Options{Head: h, Base: bs, Limits: DefaultLimits})
		if err != nil {
			t.Fatal(err)
		}
		return r, time.Since(start)
	}
	_, small := run(10)
	r, large := run(40)
	if ratio := float64(large) / float64(small); ratio > 8 {
		t.Errorf("40 files took %v, 10 files %v: ratio %.1f, want at most 8", large, small, ratio)
	}
	if len(r.Candidates) != 1 || len(r.Findings) != 1 {
		t.Errorf("candidates %d findings %d %+v", len(r.Candidates), len(r.Findings), r.Manifest)
	}
}

func TestDeadline(t *testing.T) {
	lim := DefaultLimits
	lim.Deadline = time.Now().Add(-time.Second)
	if _, err := Run(Options{Head: writeTree(t, map[string]string{"main.tf": ""}), Limits: lim}); !errors.Is(err, ErrDeadline) {
		t.Errorf("err %v", err)
	}
}

func TestCheckManifest(t *testing.T) {
	areas := []*Area{{Path: ".", Kind: AreaRoot, Head: &Module{}}, {Path: "modules/a", Kind: AreaSubmodule, Head: &Module{}}}
	m, _, _ := Evaluate(areas, Rules)
	if err := CheckManifest(m, Rules, areas); err != nil {
		t.Fatal(err)
	}
	if err := CheckManifest(m[1:], Rules, areas); err == nil {
		t.Error("a deleted entry passed")
	}
	if err := CheckManifest(append(m, m[0]), Rules, areas); err == nil {
		t.Error("a duplicate entry passed")
	}
	extra := append(append([]ManifestEntry{}, m...), ManifestEntry{RuleID: "x.y", Area: ".", Status: StatusClean})
	if err := CheckManifest(extra, Rules, areas); err == nil {
		t.Error("an unknown entry passed")
	}
}

func TestSymlinkRetarget(t *testing.T) {
	base := writeTree(t, map[string]string{"a.tf": "", "b.tf": "", "main.tf": "->a.tf"})
	head := writeTree(t, map[string]string{"a.tf": "", "b.tf": "", "main.tf": "->b.tf"})
	same := writeTree(t, map[string]string{"a.tf": "", "b.tf": "", "main.tf": "->a.tf"})
	if mustTree(t, base).TreeSHA256() == mustTree(t, head).TreeSHA256() || mustTree(t, base).TreeSHA256() != mustTree(t, same).TreeSHA256() {
		t.Error("tree hash ignores the link target")
	}
	r, err := Run(Options{Head: head, Base: base, Limits: DefaultLimits})
	if err != nil {
		t.Fatal(err)
	}
	if s := statusOf(r.Manifest, "structure.variable-untyped", "."); s != "not_evaluated: main.tf is a symbolic link" {
		t.Error(s)
	}
	r, err = Run(Options{Head: same, Base: base, Limits: DefaultLimits})
	if err != nil || len(r.Manifest) != 0 {
		t.Errorf("unchanged link put an area in scope: %v %+v", err, r.Manifest)
	}
	data, _ := json.Marshal(r)
	if strings.Contains(string(data), "a.tf\",\"kind\":\"symlink") || strings.Contains(string(data), "->") {
		t.Errorf("link target leaked: %s", data)
	}
}

func TestLongNamesAreCapped(t *testing.T) {
	long := strings.Repeat("n", 5000)
	r := runHead(t, map[string]string{"variables.tf": "variable \"" + long + "\" {}\n"})
	if len(r.Candidates) != 1 || len(r.Findings) != 1 {
		t.Fatalf("candidates %+v", r.Candidates)
	}
	c := r.Candidates[0]
	if c.EvidenceComplete || len(c.Subject) > maxEvidenceText+20 || c.Subject != ".:var."+long[:maxEvidenceText] {
		t.Errorf("not capped: complete=%v subject=%d", c.EvidenceComplete, len(c.Subject))
	}
	if len(r.Findings[0].Summary) > maxEvidenceText+200 {
		t.Errorf("summary %d bytes", len(r.Findings[0].Summary))
	}
}

func TestAreasSameInBothModes(t *testing.T) {
	base := writeTree(t, map[string]string{"main.tf": "", "examples/README.md": "a", "examples/x/main.tf": ""})
	head := writeTree(t, map[string]string{"main.tf": "", "examples/README.md": "b", "examples/x/main.tf": "", "modules/l": "->../examples/x"})
	want := `modules/l:submodule:false:false:false:"modules/l is a symbolic link, not a directory"`
	if got := areaList(FindAreas(mustTree(t, head), mustTree(t, base), DefaultLimits, false)); got != want {
		t.Errorf("base mode: %s", got)
	}
	if got := areaList(FindAreas(mustTree(t, head), nil, DefaultLimits, false)); !strings.Contains(got, want) || strings.Contains(got, "examples:") {
		t.Errorf("head mode: %s", got)
	}
	r, err := Run(Options{Head: head, Base: base, Limits: DefaultLimits})
	if err != nil {
		t.Fatal(err)
	}
	// Kind is checked before the area's issue.
	if s := statusOf(r.Manifest, "examples.versions-missing", "modules/l"); s != "not_applicable" {
		t.Error(s)
	}
	if s := statusOf(r.Manifest, "structure.variable-untyped", "modules/l"); !strings.HasPrefix(s, "not_evaluated") {
		t.Error(s)
	}
}

func TestRegistryLink(t *testing.T) {
	head := writeTree(t, map[string]string{"main.tf": "", "real.md": "x", "rule-facts.md": "->real.md"})
	if _, err := Run(Options{Head: head, RegistryFile: filepath.Join(head, "rule-facts.md"), Limits: DefaultLimits}); err == nil {
		t.Error("a linked registry was accepted")
	}
}

func TestDirectoriesCountTowardMaxFiles(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"a", "b", "c"} {
		if err := os.Mkdir(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	lim := DefaultLimits
	lim.MaxFiles = 2
	if _, err := ReadTree(root, lim); err == nil {
		t.Error("directories not counted")
	}
}

func TestDescriptionForms(t *testing.T) {
	cases := map[string]struct{ src, status string }{
		"crlf":          {"variable \"x\" {\r\n  type        = string\r\n  description = \"Set.\"\r\n}\r\n", "clean"},
		"crlf missing":  {"variable \"x\" {\r\n  type = string\r\n}\r\n", "finding"},
		"null":          {"variable \"x\" {\n  type        = string\n  description = null\n}\n", "finding"},
		"whitespace":    {"variable \"x\" {\n  type        = string\n  description = \" \\t \"\n}\n", "finding"},
		"blank heredoc": {"variable \"x\" {\n  type        = string\n  description = <<-EOT\n\n  EOT\n}\n", "finding"},
		"heredoc":       {"variable \"x\" {\n  type        = string\n  description = <<-EOT\n    Set.\n  EOT\n}\n", "clean"},
		"interpolation": {"output \"x\" {\n  value       = 1\n  description = \"Set ${local.y}.\"\n}\n", "clean"},
		"bom":           {"\xef\xbb\xbfvariable \"x\" {\n  type        = string\n  description = \"Set.\"\n}\n", "clean"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			r := runHead(t, map[string]string{"main.tf": tc.src})
			if s := statusOf(r.Manifest, "structure.missing-description", "."); s != tc.status {
				t.Errorf("got %s, want %s", s, tc.status)
			}
		})
	}
}

// examples/ or modules/ itself as a link, a special file or an unreadable
// directory is an area whose applicable rules are not_evaluated.
func TestTopLevelAreaDirectories(t *testing.T) {
	type form struct {
		files map[string]string
		setup func(t *testing.T, root string)
		want  string
	}
	forms := map[string]form{
		"modules link":  {files: map[string]string{"real/main.tf": "variable \"x\" {}\n", "modules": "->real"}, want: "modules is a symbolic link, not a directory"},
		"examples link": {files: map[string]string{"real/e1/main.tf": "", "examples": "->real"}, want: "examples is a symbolic link, not a directory"},
		"examples fifo": {setup: func(t *testing.T, root string) {
			if err := syscall.Mkfifo(filepath.Join(root, "examples"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, want: "examples is a special file, not a directory"},
		"examples unreadable": {files: map[string]string{"examples/e1/main.tf": ""}, setup: func(t *testing.T, root string) {
			if os.Getuid() == 0 {
				t.Skip("root reads every directory")
			}
			p := filepath.Join(root, "examples")
			if err := os.Chmod(p, 0); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(p, 0o755) })
		}, want: "examples is a directory that cannot be read"},
	}
	for name, f := range forms {
		t.Run(name, func(t *testing.T) {
			files := map[string]string{"main.tf": ""}
			maps.Copy(files, f.files)
			head := writeTree(t, files)
			if f.setup != nil {
				f.setup(t, head)
			}
			base := writeTree(t, map[string]string{"main.tf": ""})
			area := strings.Fields(name)[0]
			for _, o := range []Options{{Head: head}, {Head: head, Base: base}} {
				o.Limits = DefaultLimits
				r, err := Run(o)
				if err != nil {
					t.Fatal(err)
				}
				rules := []string{"structure.variable-untyped", "structure.missing-description"}
				other := []string{"examples.versions-missing"}
				if area == "examples" {
					rules, other = other, rules
				}
				for _, rule := range other {
					if s := statusOf(r.Manifest, rule, area); s != "not_applicable" {
						t.Errorf("base=%v %s: %s", o.Base != "", rule, s)
					}
				}
				for _, rule := range rules {
					if s := statusOf(r.Manifest, rule, area); s != "not_evaluated: "+f.want {
						t.Errorf("base=%v %s: %s", o.Base != "", rule, s)
					}
				}
			}
		})
	}
}
