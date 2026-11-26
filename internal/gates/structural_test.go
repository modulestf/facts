package gates

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modulestf/facts/internal/facts"
)

// g2Lines runs one file in clone scope and returns its G2 lines, the path
// cut to the file name.
func g2Lines(t *testing.T, src string, runner, downloaded bool) []string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "mod"), 0o755); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(root, "main.tf")
	if err := os.WriteFile(f, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := Run([]string{f}, Options{Runner: runner, Downloaded: downloaded, Root: root,
		FileBytes: 1 << 20, TotalBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	var g2 []string
	for l := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		if strings.HasPrefix(l, "G2 ") {
			g2 = append(g2, strings.ReplaceAll(l, root+"/", ""))
		}
	}
	return g2
}

func TestStructuralSource(t *testing.T) {
	cases := []struct {
		name, src string
		laptop    []string // G2 lines in laptop mode
		runner    []string // and in runner mode
	}{
		// A source the text read passed while Terraform reads another.
		{"comment decoy", "module \"m\" {\n  # source = \"terraform-aws-modules/vpc/aws\"\n  source = \"git::https://example.invalid/m\"\n}\n",
			[]string{"G2 main.tf:1"}, []string{"G2 main.tf:1"}},
		{"slash comment decoy", "module \"m\" {\n  // source = \"./mod\"\n  source = \"git::https://example.invalid/m\"\n}\n",
			[]string{"G2 main.tf:1"}, []string{"G2 main.tf:1"}},
		{"nested object decoy", "module \"m\" {\n  tags = {\n    source = \"./mod\"\n  }\n  source = \"git::https://example.invalid/m\"\n}\n",
			[]string{"G2 main.tf:1"}, []string{"G2 main.tf:1"}},
		{"heredoc decoy", "module \"m\" {\n  description = <<EOT\nsource = \"./mod\"\nEOT\n  source = \"git::https://example.invalid/m\"\n}\n",
			[]string{"G2 main.tf:1"}, []string{"G2 main.tf:1"}},
		{"interpolation", "module \"m\" {\n  source = \"./mod${\"x\"}\"\n}\n",
			[]string{"G2 main.tf:1"}, []string{"G2 main.tf:1"}},
		{"escape", "module \"m\" {\n  source = \"./m\\u006fd\"\n}\n",
			[]string{"G2 main.tf:1"}, []string{"G2 main.tf:1"}},
		{"variable", "module \"m\" {\n  source = \"./mod\"\n}\nmodule \"n\" {\n  source = var.s\n  x = \"source = \\\"./mod\\\"\"\n}\n",
			[]string{"G2 main.tf:4"}, []string{"G2 main.tf:4"}},
		{"null source", "module \"m\" {\n  # source = \"./mod\"\n  source = null\n}\n",
			[]string{"G2 main.tf:1"}, []string{"G2 main.tf:1"}},
		{"unseen block, null source", "module\"m\"{\n  source = null\n}\n",
			[]string{"G2 main.tf:1"}, []string{"G2 main.tf:1"}},
		{"number source", "module \"m\" {\n  # source = \"./mod\"\n  source = 1\n}\n",
			[]string{"G2 main.tf:1"}, []string{"G2 main.tf:1"}},
		{"no source attribute", "module \"m\" {\n  x = { source = \"./mod\" }\n}\n",
			[]string{"G2 main.tf:1"}, []string{"G2 main.tf:1"}},
		{"duplicate source", "module \"m\" {\n  source = \"./mod\"\n  source = \"git::https://example.invalid/m\"\n}\n",
			[]string{"G2 main.tf:1"}, []string{"G2 main.tf:1"}},
		{"file does not parse", "module \"m\" {\n  source = \"./mod\"\n}\nresource \"x\" \"y\" {\n",
			[]string{"G2 main.tf:1"}, []string{"G2 main.tf:1"}},
		{"unseen block, file does not parse", "module\"m\"{\n  source = \"../outside\"\n}\nlocals {\n  a =\n}\n",
			[]string{"G2 main.tf:1"}, []string{"G2 main.tf:1"}},
		{"unseen good block, file does not parse", "module\"m\"{\n  source = \"./mod\"\n}\nmodule \"n\" {\n  source = \"./mod\"\n}\nlocals {\n  a =\n}\n",
			[]string{"G2 main.tf:1", "G2 main.tf:4"}, []string{"G2 main.tf:1", "G2 main.tf:4"}},
		{"passed block missing from the partial body", "locals {\n  a = \"\nmodule \"x\" {\n  source = \"./mod\"\n}\n",
			[]string{"G2 main.tf:3"}, nil},
		{"unseen block, bad source", "module\"m\"{\n  source = \"git::https://example.invalid/m\"\n}\n",
			[]string{"G2 main.tf:1"}, []string{"G2 main.tf:1"}},

		// Sources both reads agree on: no line added.
		{"local", "module \"m\" {\n  source = \"./mod\"\n}\n", nil, nil},
		{"registry", "module \"m\" {\n  source  = \"terraform-aws-modules/vpc/aws\"\n  version = \"6.0.0\"\n}\n", nil, nil},
		{"comment after", "module \"m\" {\n  source = \"./mod\" # source = \"git::https://example.invalid/m\"\n}\n", nil, nil},
		{"unseen block, good source", "module\"m\"{\n  source = \"./mod\"\n}\n", nil, nil},
		{"github in runner", "module \"m\" {\n  source = \"github.com/o/r\"\n}\n", []string{"G2 main.tf:1"}, nil},
		{"no module", "resource \"x\" \"y\" {\n", nil, nil},

		// A block the text read refused keeps its one line.
		{"refused once", "module \"m\" {\n  source = \"git::https://example.invalid/m\"\n}\n",
			[]string{"G2 main.tf:1"}, []string{"G2 main.tf:1"}},
		{"refused, unparseable", "module \"m\" {\n  source = \"git::https://example.invalid/m\"\n}\nresource \"x\" \"y\" {\n",
			[]string{"G2 main.tf:1"}, []string{"G2 main.tf:1"}},
	}
	for _, c := range cases {
		for _, runner := range []bool{false, true} {
			want := c.laptop
			if runner {
				want = c.runner
			}
			got := g2Lines(t, c.src, runner, false)
			if strings.Join(got, "|") != strings.Join(want, "|") {
				t.Errorf("%s (runner %v): got %q, want %q", c.name, runner, got, want)
			}
		}
	}
}

// TestStructuralSourceDownloaded: downloaded scope has no G2 source rule,
// so the structural read adds nothing there.
func TestStructuralSourceDownloaded(t *testing.T) {
	src := "module \"m\" {\n  # source = \"./mod\"\n  source = \"git::https://example.invalid/m\"\n}\n"
	for _, runner := range []bool{false, true} {
		if got := g2Lines(t, src, runner, true); len(got) != 0 {
			t.Errorf("runner %v: got %q, want none", runner, got)
		}
	}
}

// A file over a parse limit is never handed to the HCL parser, which
// overflows its stack on deep nesting. Each is a G2 hit on the module block
// the text read passed, in both modes, and the run ends without error. The
// files stay small enough that a parser run on them (as a revert to
// hclsyntax.ParseConfig would do) returns normally, so the test fails
// rather than crashing the test binary.
func TestStructuralSourceOverLimit(t *testing.T) {
	mod := "module \"m\" {\n  source = \"./mod\"\n}\n"
	limits := facts.DefaultLimits
	cases := map[string]string{
		"deep parentheses": "locals {\n  x = " + strings.Repeat("(", limits.MaxDepth+1) + "1" + strings.Repeat(")", limits.MaxDepth+1) + "\n}\n",
		"unary chain":      "locals {\n  x = " + strings.Repeat("!", limits.MaxPrefixOps+1) + "true\n}\n",
		"token cap":        "locals {\n  x = [" + strings.Repeat("1,", limits.MaxTokens) + "]\n}\n",
	}
	if got := g2Lines(t, mod, false, false); len(got) != 0 {
		t.Fatalf("control: got %q, want none", got)
	}
	for name, extra := range cases {
		for _, runner := range []bool{false, true} {
			got := g2Lines(t, mod+extra, runner, false)
			if len(got) != 1 || got[0] != "G2 main.tf:1" {
				t.Errorf("%s, runner %v: got %q, want [G2 main.tf:1]", name, runner, got)
			}
		}
	}
}
