package behaviour

import (
	"maps"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

// maxExprBytes caps the source text of an expression in a trigger.
const maxExprBytes = 4096

// pureFuncs is the pure-function allowlist: built-in functions whose result
// depends on their arguments only. Functions that read files, the clock, a
// random source or the environment, or that mark values sensitive, are not
// on it, nor is any provider-defined function.
var pureFuncs = map[string]bool{
	"abs": true, "ceil": true, "floor": true, "log": true, "max": true, "min": true, "parseint": true,
	"pow": true, "signum": true, "chomp": true, "endswith": true, "format": true, "formatlist": true,
	"indent": true, "join": true, "lower": true, "regex": true, "regexall": true, "replace": true,
	"split": true, "startswith": true, "strcontains": true, "strrev": true, "substr": true,
	"title": true, "trim": true, "trimprefix": true, "trimsuffix": true, "trimspace": true, "upper": true,
	"alltrue": true, "anytrue": true, "chunklist": true, "coalesce": true, "coalescelist": true,
	"compact": true, "concat": true, "contains": true, "distinct": true, "element": true,
	"flatten": true, "index": true, "keys": true, "length": true, "lookup": true, "matchkeys": true,
	"merge": true, "one": true, "range": true, "reverse": true, "setintersection": true,
	"setproduct": true, "setsubtract": true, "setunion": true, "slice": true, "sort": true,
	"sum": true, "transpose": true, "values": true, "zipmap": true, "base64decode": true,
	"base64encode": true, "csvdecode": true, "jsondecode": true, "jsonencode": true,
	"urlencode": true, "yamldecode": true, "yamlencode": true, "cidrhost": true, "cidrnetmask": true,
	"cidrsubnet": true, "cidrsubnets": true, "can": true, "tobool": true, "tolist": true,
	"tomap": true, "tonumber": true, "toset": true, "tostring": true, "try": true, "type": true,
	"base64sha256": true, "base64sha512": true, "md5": true, "sha1": true, "sha256": true,
	"sha512": true, "uuidv5": true,
}

// exprInfo is one expression of one revision with its file's source.
type exprInfo struct {
	expr hclsyntax.Expression
	src  []byte
}

func (x *exprInfo) text() string { return string(x.expr.Range().SliceBytes(x.src)) }

func (x *exprInfo) key() string { return normKey(x.expr.Range().SliceBytes(x.src), false) }

// normKey is the token sequence of src without comments and newlines, so
// that layout and comments do not count as a change. config lexes src as a
// whole file or block rather than one expression.
func normKey(src []byte, config bool) string {
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

func unwrap(e hclsyntax.Expression) hclsyntax.Expression {
	for {
		p, ok := e.(*hclsyntax.ParenthesesExpr)
		if !ok {
			return e
		}
		e = p.Expression
	}
}

// isWrapperChange reports whether one revision wraps the other in function
// calls, or calls another function on the same arguments.
func isWrapperChange(b, h *exprInfo) bool {
	bk, hk := b.key(), h.key()
	if wraps(unwrap(b.expr), b.src, hk) || wraps(unwrap(h.expr), h.src, bk) {
		return true
	}
	bf, ok1 := unwrap(b.expr).(*hclsyntax.FunctionCallExpr)
	hf, ok2 := unwrap(h.expr).(*hclsyntax.FunctionCallExpr)
	if !ok1 || !ok2 || bf.Name == hf.Name || len(bf.Args) != len(hf.Args) || bf.ExpandFinal != hf.ExpandFinal {
		return false
	}
	for i := range bf.Args {
		if (&exprInfo{bf.Args[i], b.src}).key() != (&exprInfo{hf.Args[i], h.src}).key() {
			return false
		}
	}
	return true
}

// wraps reports whether e is a function call with an argument, at any depth
// through function calls, whose key is k.
func wraps(e hclsyntax.Expression, src []byte, k string) bool {
	fc, ok := e.(*hclsyntax.FunctionCallExpr)
	if !ok {
		return false
	}
	for _, a := range fc.Args {
		a = unwrap(a)
		if (&exprInfo{a, src}).key() == k || wraps(a, src, k) {
			return true
		}
	}
	return false
}

func isConditional(x *exprInfo) bool {
	if x == nil {
		return false
	}
	_, ok := unwrap(x.expr).(*hclsyntax.ConditionalExpr)
	return ok
}

// isLiteral reports an expression with no reference and no function call.
func isLiteral(x *exprInfo) bool {
	if x == nil {
		return true
	}
	if len(x.expr.Variables()) > 0 {
		return false
	}
	lit := true
	_ = hclsyntax.VisitAll(x.expr, func(n hclsyntax.Node) hcl.Diagnostics {
		switch n.(type) {
		case *hclsyntax.FunctionCallExpr, *hclsyntax.ForExpr, *hclsyntax.SplatExpr:
			lit = false
		}
		return nil
	})
	return lit
}

// isPure reports that every function call is on the allowlist and no
// reference reads self.
func isPure(xs ...*exprInfo) bool {
	for _, x := range xs {
		if x == nil {
			continue
		}
		pure := true
		_ = hclsyntax.VisitAll(x.expr, func(n hclsyntax.Node) hcl.Diagnostics {
			if fc, ok := n.(*hclsyntax.FunctionCallExpr); ok && !pureFuncs[fc.Name] {
				pure = false
			}
			return nil
		})
		for _, tr := range x.expr.Variables() {
			if tr.RootName() == "self" {
				pure = false
			}
		}
		if !pure {
			return false
		}
	}
	return true
}

// refs returns the variable names and the other references the expressions
// read, each sorted and unique.
func refs(xs ...*exprInfo) (inputs, contextual []string) {
	in, ctx := map[string]bool{}, map[string]bool{}
	for _, x := range xs {
		if x == nil {
			continue
		}
		for _, tr := range x.expr.Variables() {
			root := tr.RootName()
			names := []string{root}
			for _, step := range tr[1:] {
				n := attrName(step)
				if n == "" {
					break
				}
				names = append(names, n)
			}
			switch root {
			case "var":
				if len(names) > 1 {
					in[names[1]] = true
				}
				continue
			case "data":
				names = names[:min(len(names), 3)]
			default:
				names = names[:min(len(names), 2)]
			}
			ctx[strings.Join(names, ".")] = true
		}
	}
	return slices.Sorted(maps.Keys(in)), slices.Sorted(maps.Keys(ctx))
}

// attrName is the name of an attribute traversal step, or "".
func attrName(s hcl.Traverser) string {
	if a, ok := s.(hcl.TraverseAttr); ok {
		return a.Name
	}
	return ""
}

// literalString returns the value of a string literal.
func literalString(e *hclsyntax.LiteralValueExpr) (string, bool) {
	if e.Val.Type() != cty.String || !e.Val.IsKnown() || e.Val.IsNull() {
		return "", false
	}
	return e.Val.AsString(), true
}

func sortedAttrNames(b *hclsyntax.Body) []string {
	return slices.Sorted(maps.Keys(b.Attributes))
}

// capped returns the expression text, or nil when it is over maxExprBytes.
func capped(x *exprInfo) (*string, bool) {
	if x == nil {
		return nil, true
	}
	s := x.text()
	if len(s) > maxExprBytes {
		return nil, false
	}
	return &s, true
}
