package facts

import (
	"io/fs"
	"os"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// scanCases are inputs whose brackets, quotes and operators sit where a
// byte count without the lexer's states would read them wrong.
var scanCases = []string{
	"a = \"}}}]]])))\"\n",
	"a = \"{{{[[[(((\"\n",
	"a = \"x ${b} y ${c[\"k\"]} z\"\n",
	"a = \"${ {a = \"}\"} }\"\n",
	"a = \"%{ if b }x%{ endif }\"\n",
	"a = \"$${not} %%{not} $x %x\"\n",
	"a = \"esc \\\" ${ \"in\\\"ner\" } \\${x}\"\n",
	"a = \"${~ b ~}\"\n",
	"a = \"${b}\" # \"}}} -!-!\n",
	"a = 1 // ]]] \"\n",
	"/* \"{{{ */ a = [[1]]\n",
	"a = - - - ! ! 1\n",
	"a = -1e-5 - -2\n",
	"a = { b = [ ( 1 ) ] }\n",
	"}}} a = 1\n",
	"a = ~} b\n",
	"a = \"unterminated\n",
	"a = \"\\\nb\"\n",
	"a = \"" + strings.Repeat("${", 70) + strings.Repeat("}", 70) + "\"\n",
	"a = " + strings.Repeat("[", 64) + strings.Repeat("]", 64) + "\n",
	"a = " + strings.Repeat("- ", 65) + "1\n",
	"a = \"\xc0{\" \xff [\n",
}

// TestScanLimitsMatchLexer holds scanLimits to the lexer: on every input it
// does not give up on, its nesting depth equals the one the tokens give, its
// unary operator run is at least theirs and the tokens number at most one
// more than the bytes. The inputs are the cases above and every .tf file of
// testdata.
func TestScanLimitsMatchLexer(t *testing.T) {
	srcs := append([]string(nil), scanCases...)
	root, err := os.OpenRoot("../../testdata")
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	fsys := root.FS()
	err = fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".tf") {
			return err
		}
		data, err := fs.ReadFile(fsys, p)
		if err == nil {
			srcs = append(srcs, string(data))
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	scanned := 0
	for _, src := range srcs {
		depth, prefix, ok := scanLimits([]byte(src))
		if !ok {
			continue
		}
		scanned++
		tokens, _ := hclsyntax.LexConfig([]byte(src), "x.tf", hcl.InitialPos)
		if d := nestingDepth(tokens); depth != d {
			t.Errorf("depth %d, lexer %d: %q", depth, d, src)
		}
		if n := prefixRun(tokens); prefix < n {
			t.Errorf("unary run %d under the lexer's %d: %q", prefix, n, src)
		}
		if len(tokens) > len(src)+1 {
			t.Errorf("%d tokens for %d bytes: %q", len(tokens), len(src), src)
		}
	}
	if scanned < len(scanCases) {
		t.Errorf("scanned %d inputs, want at least %d", scanned, len(scanCases))
	}
}

// scanLimits gives up on a heredoc, on a block comment with no end and on a
// byte over 0x7f in an expression, so the exact check reads them.
func TestScanLimitsGivesUp(t *testing.T) {
	for _, src := range []string{
		"a = <<EOT\n}}}\nEOT\n",
		"a = <<-EOT\n  x\n  EOT\n",
		"a = 1 << 2\n",
		"a = 1 /* no end\n",
		"\xcf\"",
		"a = [\xcf] [ [\n",
	} {
		if _, _, ok := scanLimits([]byte(src)); ok {
			t.Errorf("scanned %q", src)
		}
	}
}

// parseBody gives the same error as parseSyntax, and the same message for
// a limit, whether or not the scan proves the file within the limits.
func TestParseBodyMatchesParseSyntax(t *testing.T) {
	small := DefaultLimits
	small.MaxTokens = 20
	for _, lim := range []Limits{DefaultLimits, small} {
		for _, src := range append(scanCases, "a = \n", "a = <<EOT\nx\nEOT\n", "a = "+strings.Repeat("[", 65)+strings.Repeat("]", 65)+"\n") {
			_, _, want := parseSyntax("x.tf", []byte(src), lim)
			_, _, got := parseBody("x.tf", []byte(src), lim)
			if errText(got) != errText(want) {
				t.Errorf("%q: got %v, want %v", src, got, want)
			}
		}
	}
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
