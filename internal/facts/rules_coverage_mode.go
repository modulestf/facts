package facts

import (
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

// modeSafe reports whether an output value yields a value or null in every
// mode of the module, so it cannot fail when a counted block has no
// instance. The shapes are narrow; anything else stays a candidate:
//   - try(..., null): try() with a literal null as its last argument;
//   - a whole block with count or for_each, such as aws_x.this or module.m;
//   - a splat over a block with count, whose items read attributes only:
//     aws_x.this[*].arn;
//   - one() of such a splat when count is a conditional of 0 and 1;
//   - a for expression over a block with count or for_each whose key,
//     value and condition read only its own variables, by attribute.
func modeSafe(a *Area, x Expr) bool {
	e, ok := parseExprAt(x)
	return ok && safeValue(a, unparen(e))
}

func safeValue(a *Area, e hclsyntax.Expression) bool {
	switch t := e.(type) {
	case *hclsyntax.FunctionCallExpr:
		switch {
		case t.Name == "try" && len(t.Args) >= 2 && !t.ExpandFinal:
			return isNullLit(unparen(t.Args[len(t.Args)-1]))
		case t.Name == "one" && len(t.Args) == 1 && !t.ExpandFinal:
			s, ok := unparen(t.Args[0]).(*hclsyntax.SplatExpr)
			return ok && safeSplat(a, s) && zeroOrOne(a, s.Source)
		}
	case *hclsyntax.ScopeTraversalExpr:
		_, _, ok := switchedBlock(a, t)
		return ok
	case *hclsyntax.SplatExpr:
		return safeSplat(a, t)
	case *hclsyntax.ForExpr:
		if _, _, ok := switchedBlock(a, unparen(t.CollExpr)); !ok {
			return false
		}
		vars := map[string]bool{t.ValVar: true}
		if t.KeyVar != "" {
			vars[t.KeyVar] = true
		}
		for _, b := range []hclsyntax.Expression{t.KeyExpr, t.ValExpr, t.CondExpr} {
			if b != nil && !readsOnly(b, vars) {
				return false
			}
		}
		return true
	}
	return false
}

func isNullLit(e hclsyntax.Expression) bool {
	l, ok := e.(*hclsyntax.LiteralValueExpr)
	return ok && l.Val.IsNull()
}

// switchedBlock returns the body and meta-argument, count or for_each, of
// the block a bare traversal names whole: t.n, data.t.n or module.m.
func switchedBlock(a *Area, e hclsyntax.Expression) (Body, string, bool) {
	t, ok := e.(*hclsyntax.ScopeTraversalExpr)
	if !ok || !attrsOnly(t.Traversal[1:]) {
		return Body{}, "", false
	}
	names := []string{t.Traversal.RootName()}
	for _, s := range t.Traversal[1:] {
		names = append(names, s.(hcl.TraverseAttr).Name)
	}
	addr := strings.Join(names, ".")
	var bodies []Body
	for _, r := range a.Head.Resources {
		if coverageBlockAddr(r) == addr {
			bodies = append(bodies, r.Body)
		}
	}
	for _, m := range a.Head.ModuleCalls {
		if "module."+m.Name == addr {
			bodies = append(bodies, m.Body)
		}
	}
	if len(bodies) != 1 {
		return Body{}, "", false
	}
	for _, m := range []string{"count", "for_each"} {
		if bodies[0].Arg(m) != nil {
			return bodies[0], m, true
		}
	}
	return Body{}, "", false
}

// safeSplat: a splat over a block with count, each item read by attribute.
func safeSplat(a *Area, s *hclsyntax.SplatExpr) bool {
	if _, m, ok := switchedBlock(a, unparen(s.Source)); !ok || m != "count" {
		return false
	}
	switch each := s.Each.(type) {
	case *hclsyntax.AnonSymbolExpr:
		return true
	case *hclsyntax.RelativeTraversalExpr:
		_, ok := each.Source.(*hclsyntax.AnonSymbolExpr)
		return ok && attrsOnly(each.Traversal)
	}
	return false
}

// zeroOrOne: the count of the block is cond ? 1 : 0 or cond ? 0 : 1.
func zeroOrOne(a *Area, src hclsyntax.Expression) bool {
	b, _, _ := switchedBlock(a, unparen(src))
	x := b.Arg("count")
	if x == nil {
		return false
	}
	e, ok := parseExprAt(*x)
	if !ok {
		return false
	}
	c, ok := unparen(e).(*hclsyntax.ConditionalExpr)
	if !ok {
		return false
	}
	num := func(e hclsyntax.Expression) string {
		l, ok := unparen(e).(*hclsyntax.LiteralValueExpr)
		if !ok || l.Val.IsNull() || l.Val.Type() != cty.Number {
			return ""
		}
		return l.Val.AsBigFloat().String()
	}
	t, f := num(c.TrueResult), num(c.FalseResult)
	return t == "1" && f == "0" || t == "0" && f == "1"
}

func attrsOnly(t hcl.Traversal) bool {
	for _, s := range t {
		if _, ok := s.(hcl.TraverseAttr); !ok {
			return false
		}
	}
	return true
}

// readsOnly: e is a reference to one of vars by attribute, a literal, or
// an object or tuple of such, and nothing else.
func readsOnly(e hclsyntax.Expression, vars map[string]bool) bool {
	switch t := unparen(e).(type) {
	case *hclsyntax.LiteralValueExpr:
		return true
	case *hclsyntax.ScopeTraversalExpr:
		return vars[t.Traversal.RootName()] && attrsOnly(t.Traversal[1:])
	case *hclsyntax.TupleConsExpr:
		for _, x := range t.Exprs {
			if !readsOnly(x, vars) {
				return false
			}
		}
		return true
	case *hclsyntax.ObjectConsExpr:
		for _, it := range t.Items {
			k, ok := it.KeyExpr.(*hclsyntax.ObjectConsKeyExpr)
			if !ok || hcl.ExprAsKeyword(k.Wrapped) == "" && !readsOnly(k.Wrapped, vars) || !readsOnly(it.ValueExpr, vars) {
				return false
			}
		}
		return true
	}
	return false
}
