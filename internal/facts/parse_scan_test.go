package facts

import (
	"io/fs"
	"math/rand/v2"
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
	"a = \"\\\"[[[\"\n",
	"a = -\t-\t!\t-1\n",
	"a = ~ b\n",
	"a = [ ~ [ b ] ]\n",
	"a = /* x *//y\n",
	"a = /* x *//y [\n",
	"a = /**//* c */ [\n",
	"a = /**/[\n",
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
	unary := DefaultLimits
	unary.MaxPrefixOps = 3
	for _, lim := range []Limits{DefaultLimits, small, unary} {
		for _, src := range append(scanCases, "a = \n", "a = <<EOT\nx\nEOT\n", "a = "+strings.Repeat("[", 65)+strings.Repeat("]", 65)+"\n") {
			_, _, want := parseSyntax("x.tf", []byte(src), lim)
			_, _, got := parseBody("x.tf", []byte(src), lim)
			if errText(got) != errText(want) {
				t.Errorf("%q: got %v, want %v", src, got, want)
			}
		}
	}
}

// A run of unary operators split by tabs, which the lexer emits no token
// for, is refused with the exact path's message.
func TestParseBodyUnaryRunWithTabs(t *testing.T) {
	lim := DefaultLimits
	lim.MaxPrefixOps = 3
	_, _, err := parseBody("x.tf", []byte("a = -\t-\t!\t-1\n"), lim)
	if !IsLimitError(err) || !strings.Contains(errText(err), "4 consecutive unary operators") {
		t.Errorf("got %v", err)
	}
}

// scanFragments are pieces that each move the scanner or the lexer between
// states, joined at random by TestScanLimitsDifferential.
var scanFragments = []string{
	"{", "}", "[", "]", "(", ")", "\"", "\\", "\\\"", "${", "%{", "${~", "~}", "~", "}~",
	"$${", "%%{", "$", "%", "#", "//", "/*", "*/", "/", "*", "!", "-", " ", "\t", "\n", "\r\n",
	"a", "1e-5", "x.y", "=", ":", ",", "\xcf", "\xff", "<", "<<", "<<EOT\n", "EOT\n",
}

// TestScanLimitsDifferential holds scanLimits to the lexer on inputs built
// from scanFragments with a fixed seed, so a failure reproduces.
func TestScanLimitsDifferential(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2)) //nolint:gosec // G404: a fixed seed, so a failing input reproduces
	scanned := 0
	for range 5000 {
		var b strings.Builder
		for range 1 + r.IntN(16) {
			b.WriteString(scanFragments[r.IntN(len(scanFragments))])
		}
		src := []byte(b.String())
		depth, prefix, ok := scanLimits(src)
		if !ok {
			continue
		}
		scanned++
		tokens, _ := hclsyntax.LexConfig(src, "x.tf", hcl.InitialPos)
		if d, n := nestingDepth(tokens), prefixRun(tokens); depth != d || prefix < n || len(tokens) > len(src)+1 {
			t.Fatalf("%q: scan depth %d run %d, lexer depth %d run %d, %d tokens", src, depth, prefix, d, n, len(tokens))
		}
	}
	if scanned < 1000 {
		t.Errorf("scanned %d inputs, want at least 1000", scanned)
	}
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// ParsePartial keeps the partial body of a file with a syntax error and
// refuses a file over a limit without running the parser, a lexer error
// included.
func TestParsePartial(t *testing.T) {
	lim := DefaultLimits
	body, err := ParsePartial("a.tf", []byte("module \"a\" {\n}\nmodule \"b\" {\n  x = \n"), lim)
	if err == nil || IsLimitError(err) || body == nil || len(body.Blocks) == 0 {
		t.Errorf("syntax error: body %v err %v, want a partial body and a parse error", body, err)
	}
	if body, err := ParsePartial("a.tf", []byte("module \"a\" {\n}\n"), lim); err != nil || len(body.Blocks) != 1 {
		t.Errorf("valid: body %v err %v", body, err)
	}
	for name, src := range map[string]string{
		"nesting":     "x = " + strings.Repeat("(", lim.MaxDepth+1) + "1" + strings.Repeat(")", lim.MaxDepth+1) + "\n",
		"unary":       "x = " + strings.Repeat("!", lim.MaxPrefixOps+1) + "true\n",
		"lexer error": "x = " + strings.Repeat("(", lim.MaxDepth+1) + "\x00\n",
	} {
		body, err := ParsePartial("a.tf", []byte(src), lim)
		if body != nil || !IsLimitError(err) {
			t.Errorf("%s: body %v err %v, want a limit error and no body", name, body, err)
		}
	}
}
