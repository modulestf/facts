// Package hclnorm compares HCL source by its tokens, so that layout and
// comments do not count as a change.
package hclnorm

import (
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// Key is the token sequence of src without comments and newlines. config
// lexes src as a whole file or block rather than one expression.
func Key(src []byte, config bool) string {
	var toks hclsyntax.Tokens
	if config {
		toks, _ = hclsyntax.LexConfig(src, "", hcl.InitialPos)
	} else {
		toks, _ = hclsyntax.LexExpression(src, "", hcl.InitialPos)
	}
	var b strings.Builder
	for _, t := range toks {
		switch t.Type {
		case hclsyntax.TokenComment, hclsyntax.TokenNewline, hclsyntax.TokenEOF:
			continue
		}
		b.WriteString(t.Type.String())
		b.WriteByte(0)
		b.Write(t.Bytes)
		b.WriteByte(0)
	}
	return b.String()
}
