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
//   - try() of two or more arguments whose last is a literal, such as
//     null, "", [], {}, 0 or false, or a bare var.<name>;
//   - a whole block with count or for_each, such as aws_x.this or module.m;
//   - a splat over a block with count, whose items read attributes only:
//     aws_x.this[*].arn;
//   - one() of such a splat when count is a conditional of 0 and 1;
//   - a for expression over a block with count or for_each, with no
//     condition and no grouping, whose key is its key variable and whose
//     value forValue accepts.
func modeSafe(a *Area, x Expr) bool {
	e, ok := parseExprAt(x)
	return ok && safeValue(a, unparen(e))
}

func safeValue(a *Area, e hclsyntax.Expression) bool {
	switch t := e.(type) {
	case *hclsyntax.FunctionCallExpr:
		switch {
		case t.Name == "try" && len(t.Args) >= 2 && !t.ExpandFinal:
			return safeFallback(unparen(t.Args[len(t.Args)-1]))
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
		if _, _, ok := switchedBlock(a, unparen(t.CollExpr)); !ok || t.CondExpr != nil || t.Group {
			return false
		}
		if t.KeyExpr != nil {
			k, ok := unparen(t.KeyExpr).(*hclsyntax.ScopeTraversalExpr)
			if !ok || t.KeyVar == "" || len(k.Traversal) != 1 || k.Traversal.RootName() != t.KeyVar {
				return false // another key can repeat, and a null one fails
			}
		}
		return forValue(t.ValExpr, t.KeyVar, t.ValVar)
	}
	return false
}

// safeFallback: the last argument of try() cannot fail, so try() yields a
// value in every mode: a literal, or a bare var.<name>.
func safeFallback(e hclsyntax.Expression) bool {
	if t, ok := e.(*hclsyntax.ScopeTraversalExpr); ok {
		return t.Traversal.RootName() == "var" && len(t.Traversal) == 2 && attrsOnly(t.Traversal[1:])
	}
	return literal(e)
}

// literal: a literal value, a string with no interpolation, or a tuple or
// object of literals with keyword or literal keys.
func literal(e hclsyntax.Expression) bool {
	switch t := unparen(e).(type) {
	case *hclsyntax.LiteralValueExpr:
		return true
	case *hclsyntax.TemplateExpr:
		return t.IsStringLiteral()
	case *hclsyntax.TupleConsExpr:
		for _, x := range t.Exprs {
			if !literal(x) {
				return false
			}
		}
		return true
	case *hclsyntax.ObjectConsExpr:
		for _, it := range t.Items {
			k, ok := it.KeyExpr.(*hclsyntax.ObjectConsKeyExpr)
			if !ok || !literalKey(k.Wrapped) || !literal(it.ValueExpr) {
				return false
			}
		}
		return true
	}
	return false
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
		// one attribute only: a second one fails where the first is null
		_, ok := each.Source.(*hclsyntax.AnonSymbolExpr)
		return ok && len(each.Traversal) == 1 && attrsOnly(each.Traversal)
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

// forValue: the value of a for expression is an iteration variable, the
// value variable with one attribute, or a tuple or object of those with
// keyword or literal keys. A deeper read fails where an attribute is null.
func forValue(e hclsyntax.Expression, keyVar, valVar string) bool {
	switch t := unparen(e).(type) {
	case *hclsyntax.ScopeTraversalExpr:
		root := t.Traversal.RootName()
		switch len(t.Traversal) {
		case 1:
			return root == valVar || keyVar != "" && root == keyVar
		case 2:
			return root == valVar && attrsOnly(t.Traversal[1:])
		}
	case *hclsyntax.TupleConsExpr:
		for _, x := range t.Exprs {
			if !forValue(x, keyVar, valVar) {
				return false
			}
		}
		return true
	case *hclsyntax.ObjectConsExpr:
		for _, it := range t.Items {
			k, ok := it.KeyExpr.(*hclsyntax.ObjectConsKeyExpr)
			if !ok || !literalKey(k.Wrapped) || !forValue(it.ValueExpr, keyVar, valVar) {
				return false
			}
		}
		return true
	}
	return false
}

func literalKey(e hclsyntax.Expression) bool {
	if hcl.ExprAsKeyword(e) != "" {
		return true
	}
	t, ok := e.(*hclsyntax.TemplateExpr)
	return ok && t.IsStringLiteral()
}
