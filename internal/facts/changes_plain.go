package facts

import (
	"bytes"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

// plainFuncs are the functions a plain expression may call: each only
// combines or reshapes the values it is given.
var plainFuncs = map[string]bool{
	"concat": true, "compact": true, "flatten": true, "distinct": true, "join": true, "format": true,
	"length": true, "toset": true, "tolist": true, "tomap": true, "element": true, "keys": true, "values": true,
}

// plainExpr reports whether e is a plain expression: a literal, a negative
// number, a quoted string whose interpolations are plain and that has no
// template directive, a reference with attribute, index and splat steps not
// rooted at each, count or self, a tuple, an object with name or literal
// string keys, or a call of a plainFuncs function with no expansion. An
// operator, a conditional, a for expression, parentheses, a heredoc and
// every other function are not plain. src is the file e was parsed from.
func plainExpr(e hclsyntax.Expression, src []byte) bool {
	switch x := e.(type) {
	case *hclsyntax.LiteralValueExpr:
		return true
	case *hclsyntax.UnaryOpExpr:
		lit, ok := x.Val.(*hclsyntax.LiteralValueExpr)
		return ok && x.Op == hclsyntax.OpNegate && lit.Val.Type() == cty.Number
	case *hclsyntax.TemplateWrapExpr:
		return !heredoc(x.SrcRange, src) && plainExpr(x.Wrapped, src)
	case *hclsyntax.TemplateExpr:
		if heredoc(x.SrcRange, src) {
			return false
		}
		for _, p := range x.Parts {
			if !plainExpr(p, src) {
				return false
			}
		}
		return true
	case *hclsyntax.ScopeTraversalExpr:
		switch x.Traversal.RootName() {
		case "each", "count", "self":
			return false
		}
		return true
	case *hclsyntax.RelativeTraversalExpr:
		return plainExpr(x.Source, src)
	case *hclsyntax.IndexExpr:
		return plainExpr(x.Collection, src) && plainExpr(x.Key, src)
	case *hclsyntax.SplatExpr:
		return plainExpr(x.Source, src) && splatEach(x.Each)
	case *hclsyntax.TupleConsExpr:
		for _, v := range x.Exprs {
			if !plainExpr(v, src) {
				return false
			}
		}
		return true
	case *hclsyntax.ObjectConsExpr:
		for _, it := range x.Items {
			if !plainKey(it.KeyExpr, src) || !plainExpr(it.ValueExpr, src) {
				return false
			}
		}
		return true
	case *hclsyntax.FunctionCallExpr:
		if !plainFuncs[x.Name] || x.ExpandFinal {
			return false
		}
		for _, a := range x.Args {
			if !plainExpr(a, src) {
				return false
			}
		}
		return true
	}
	return false
}

// splatEach reports whether the per-element part of a splat is only
// attribute and index steps on the element.
func splatEach(e hclsyntax.Expression) bool {
	switch x := e.(type) {
	case *hclsyntax.AnonSymbolExpr:
		return true
	case *hclsyntax.RelativeTraversalExpr:
		return splatEach(x.Source)
	}
	return false
}

// plainKey reports whether an object key is a bare name or a quoted string
// with no interpolation.
func plainKey(e hclsyntax.Expression, src []byte) bool {
	k, ok := e.(*hclsyntax.ObjectConsKeyExpr)
	if !ok || k.ForceNonLiteral {
		return false
	}
	switch w := k.Wrapped.(type) {
	case *hclsyntax.ScopeTraversalExpr:
		return len(w.Traversal) == 1
	case *hclsyntax.TemplateExpr:
		if heredoc(w.SrcRange, src) {
			return false
		}
		for _, p := range w.Parts {
			if _, lit := p.(*hclsyntax.LiteralValueExpr); !lit {
				return false
			}
		}
		return true
	}
	return false
}

// heredoc reports whether the source at r opens a heredoc.
func heredoc(r hcl.Range, src []byte) bool {
	return r.Start.Byte >= 0 && r.Start.Byte < len(src) && bytes.HasPrefix(src[r.Start.Byte:], []byte("<<"))
}
