package behaviour

import (
	"maps"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/modulestf/facts/internal/facts"
	"github.com/modulestf/facts/internal/hclnorm"
	"github.com/modulestf/facts/internal/tffunc"
	"github.com/zclconf/go-cty/cty"
)

// maxExprBytes caps the source text of an expression in a trigger.
const maxExprBytes = 4096

// pureFuncs is the pure-function allowlist: built-in functions whose result
// depends on their arguments only. Functions that read files, the clock, a
// random source or the environment, or that mark values sensitive, are not
// on it, nor is any provider-defined function. The rest of what it leaves
// out (base64gzip, convert, formatdate, list, map, textdecodebase64,
// textencodebase64, timeadd, timecmp) was never on it and stays off. type,
// which only the console has, is on it.
var pureFuncs = func() map[string]bool {
	m := tffunc.Set(
		"file", "filebase64", "filebase64sha256", "filebase64sha512", "fileexists", "filemd5",
		"filesha1", "filesha256", "filesha512", "fileset", "templatefile", "templatestring",
		"abspath", "pathexpand", "dirname", "basename",
		"timestamp", "plantimestamp", "timeadd", "timecmp", "formatdate",
		"uuid", "bcrypt", "rsadecrypt", "ephemeralasnull",
		"sensitive", "nonsensitive", "issensitive",
		"base64gzip", "textdecodebase64", "textencodebase64",
		"convert", "list", "map",
	)
	m["type"] = true
	return m
}()

// exprInfo is one expression of one revision with its file's source.
type exprInfo struct {
	expr hclsyntax.Expression
	src  []byte
}

func (x *exprInfo) text() string { return string(x.expr.Range().SliceBytes(x.src)) }

func (x *exprInfo) key() string { return hclnorm.Key(x.expr.Range().SliceBytes(x.src), false) }

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

// knownAtoms is the set of the subexpressions whose values decide whether x
// is wholly known: a literal part of a template and a literal element of a
// tuple or object never makes it unknown, so those structures are looked
// through; any other node that is not a literal is one atom, by its
// normalized text. A literal inside such an atom (a function argument, an
// index key, a condition) stays in its text: lookup(local.m, "a") and
// lookup(local.m, "b") can differ in what is known. ok is false when x
// holds a structure the set cannot stand for: a template with a directive,
// whose parts are no expressions of their own, or an object with a key that
// is no static name or string, or with a key twice, where the last entry
// wins.
func knownAtoms(x *exprInfo) (atoms map[string]bool, ok bool) {
	out, ok := map[string]bool{}, true
	var walk func(e hclsyntax.Expression)
	walk = func(e hclsyntax.Expression) {
		switch n := unwrap(e).(type) {
		case *hclsyntax.LiteralValueExpr:
		case *hclsyntax.TemplateExpr:
			for _, p := range n.Parts {
				switch p.(type) {
				case *hclsyntax.ConditionalExpr, *hclsyntax.TemplateJoinExpr:
					ok = false
				}
			}
			for _, p := range n.Parts {
				walk(p)
			}
		case *hclsyntax.TemplateWrapExpr:
			walk(n.Wrapped)
		case *hclsyntax.TupleConsExpr:
			for _, el := range n.Exprs {
				walk(el)
			}
		case *hclsyntax.ObjectConsExpr:
			keys := map[string]bool{}
			for _, it := range n.Items {
				k, static := staticKey(it.KeyExpr)
				if !static || keys[k] {
					ok = false
				}
				keys[k] = true
				walk(it.ValueExpr)
			}
		default:
			if a := (&exprInfo{n, x.src}); !isLiteral(a) {
				out[a.key()] = true
			}
		}
	}
	walk(x.expr)
	return out, ok
}

// staticKey is the name of an object key that is a bare name or a quoted
// string with no interpolation.
func staticKey(e hclsyntax.Expression) (string, bool) {
	k, isKey := e.(*hclsyntax.ObjectConsKeyExpr)
	if !isKey || k.ForceNonLiteral {
		return "", false
	}
	if name := hcl.ExprAsKeyword(k.Wrapped); name != "" {
		return name, true
	}
	var lit *hclsyntax.LiteralValueExpr
	switch w := k.Wrapped.(type) {
	case *hclsyntax.TemplateExpr:
		if len(w.Parts) == 1 {
			lit, _ = w.Parts[0].(*hclsyntax.LiteralValueExpr)
		}
	case *hclsyntax.LiteralValueExpr:
		lit = w
	}
	if lit == nil || lit.Val.Type() != cty.String || !lit.Val.IsKnown() || lit.Val.IsNull() {
		return "", false
	}
	return lit.Val.AsString(), true
}

// sameKnown reports two expressions known or unknown alike: both have the
// same known-atoms and both can be read that way.
func sameKnown(b, h *exprInfo) bool {
	ba, bok := knownAtoms(b)
	ha, hok := knownAtoms(h)
	return bok && hok && maps.Equal(ba, ha)
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

// varsNamed reports that every reference to var names a variable with a
// static attribute: refs drops var alone or var["x"], so a reader of the
// inputs would miss them.
func varsNamed(xs ...*exprInfo) bool {
	for _, x := range xs {
		if x == nil {
			continue
		}
		for _, tr := range x.expr.Variables() {
			tr = facts.Unaliased(tr)
			if tr.RootName() == "var" && (len(tr) < 2 || attrName(tr[1]) == "") {
				return false
			}
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
			tr = facts.Unaliased(tr)
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
