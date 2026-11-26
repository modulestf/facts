package fixture

import (
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// rename is one reference an expression's text names and the local the
// fixture reads in its place.
type rename struct {
	from string
	to   string
	toks hclsyntax.Tokens
}

// renameRefs replaces each reference of rs in the expression src by its
// local. A reference matches a run of the expression's own tokens equal to
// its tokens, with no byte between them and no name byte or dot before or
// name byte after the run. The lexer's tokens keep a quoted string, a
// heredoc and a comment out: text there is not a reference, and stays as it
// is. rs is in the order matches are tried at each token: longest first.
// The expression need not lex cleanly; fixture-check judges the tree.
func renameRefs(src string, rs []rename) string {
	b := []byte(src)
	toks, _ := hclsyntax.LexExpression(b, "expr", hcl.InitialPos)
	var out strings.Builder
	last := 0
	for i := 0; i < len(toks); i++ {
		r, n := matchRef(b, toks, i, rs)
		if n == 0 {
			continue
		}
		start, end := toks[i].Range.Start.Byte, toks[i+n-1].Range.End.Byte
		out.Write(b[last:start])
		out.WriteString(r.to)
		last, i = end, i+n-1
	}
	out.Write(b[last:])
	return out.String()
}

func matchRef(src []byte, toks hclsyntax.Tokens, i int, rs []rename) (rename, int) {
	for _, r := range rs {
		n := len(r.toks)
		if n == 0 || i+n > len(toks) {
			continue
		}
		ok := true
		for k := range n {
			t, w := toks[i+k], r.toks[k]
			if t.Type != w.Type || string(t.Bytes) != string(w.Bytes) ||
				(k > 0 && toks[i+k-1].Range.End.Byte != t.Range.Start.Byte) {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		start, end := toks[i].Range.Start.Byte, toks[i+n-1].Range.End.Byte
		if start > 0 && (nameByte(src[start-1]) || src[start-1] == '.') {
			continue
		}
		if end < len(src) && nameByte(src[end]) {
			continue
		}
		return r, n
	}
	return rename{}, 0
}

// nameByte reports a byte a name may hold: a letter, digit, underscore or
// dash.
func nameByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-'
}

// refTokens lexes a reference's text into the tokens it matches, without
// the end of file token.
func refTokens(text string) hclsyntax.Tokens {
	toks, _ := hclsyntax.LexExpression([]byte(text), "ref", hcl.InitialPos)
	for len(toks) > 0 && toks[len(toks)-1].Type == hclsyntax.TokenEOF {
		toks = toks[:len(toks)-1]
	}
	return toks
}
