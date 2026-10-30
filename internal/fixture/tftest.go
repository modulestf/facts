package fixture

import (
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

// The grammar of the one .tftest.hcl file of an L2 tree. It plans only:
// every run sets command = plan, every provider is a mock_provider with
// literal defaults, and nothing names a module, a provider configuration or a
// file. Mock and override values, and variables, are literals; asserts use
// the pure functions and references into the tree.

// Roots an assert may reference, besides the resource types.
var testRoots = set("var", "local", "output", "run", "data")

// Roots expect_failures may name, besides the resource types.
var expectRoots = set("var", "output", "data")

// Roots an override target may name, besides the resource types.
var overrideRoots = set("data")

// Arguments of an override_resource or override_data block.
var overrideArgs = set("target", "values")

// planOverride reports override_during = plan, the one argument a mock block
// may set besides its values, and records it: mock values then make computed
// attributes known at plan. Apply is the default and never runs.
func (c *checker) planOverride(a *hclsyntax.Attribute) bool {
	ok := a.Name == "override_during" && hcl.ExprAsKeyword(a.Expr) == "plan"
	c.duringPlan = c.duringPlan || ok
	return ok
}

// filled reports a literal object with at least one item.
func filled(e hclsyntax.Expression) bool {
	o, ok := e.(*hclsyntax.ObjectConsExpr)
	return ok && len(o.Items) > 0 && isLiteral(o)
}

// testTop checks the top-level body of a test file.
func (c *checker) testTop(file string, body *hclsyntax.Body) {
	for _, a := range sortedAttrs(body) {
		c.add(file, a.NameRange.Start.Line, RuleBlockDenied, a.Name)
	}
	s := scope{roots: testRoots, funcs: pureFunctionSet, anyResource: true}
	for _, b := range body.Blocks {
		switch b.Type {
		case "mock_provider":
			c.mockProvider(file, b)
		case "override_resource", "override_data":
			c.override(file, b, true)
		case "variables":
			c.literals(file, b.Body)
		case "run":
			c.run(file, b, s)
		case "provider":
			c.add(file, b.TypeRange.Start.Line, RuleProviderDenied, b.Type)
		default: // test, override_module and anything newer
			c.add(file, b.TypeRange.Start.Line, RuleBlockDenied, b.Type)
		}
	}
}

// mockProvider checks a mock_provider block: no argument but
// override_during = plan, so no source of mock files and no alias, and only
// mock and override blocks inside.
func (c *checker) mockProvider(file string, b *hclsyntax.Block) {
	line := b.TypeRange.Start.Line
	if len(b.Labels) != 1 {
		c.add(file, line, RuleProviderDenied, "")
		return
	}
	c.mocks[b.Labels[0]] = append(c.mocks[b.Labels[0]], place{file, line})
	for _, a := range sortedAttrs(b.Body) {
		if !c.planOverride(a) {
			c.add(file, a.NameRange.Start.Line, RuleBlockAttributeDenied, a.Name)
		}
	}
	for _, nb := range b.Body.Blocks {
		switch nb.Type {
		case "mock_resource", "mock_data":
			c.mockDefaults(file, nb, b.Labels[0])
		case "override_resource", "override_data":
			c.override(file, nb, true)
		default:
			c.nestedDenied(file, nb)
		}
	}
}

// mockDefaults checks a mock_resource or mock_data block of the mock of
// provider: literal defaults. A mock_data with some fixes its data type.
func (c *checker) mockDefaults(file string, b *hclsyntax.Block, provider string) {
	for _, a := range sortedAttrs(b.Body) {
		switch {
		case c.planOverride(a):
		case a.Name != "defaults":
			c.add(file, a.NameRange.Start.Line, RuleBlockAttributeDenied, a.Name)
		default:
			c.literal(file, a)
			if len(b.Labels) == 1 && filled(a.Expr) {
				kind := map[string]string{"mock_data": "data", "mock_resource": "resource"}[b.Type]
				c.mocked[kind+"\x00"+provider+"\x00"+b.Labels[0]] = true
			}
		}
	}
	for _, nb := range b.Body.Blocks {
		c.nestedDenied(file, nb)
	}
}

// wholeBlock returns the address of a target naming a whole block, with no
// instance key: data.type.name for a data source, type.name for a resource.
func wholeBlock(e hclsyntax.Expression, data bool) (string, bool) {
	tr, diags := hcl.AbsTraversalForExpr(e)
	if diags.HasErrors() {
		return "", false
	}
	parts := []string{tr.RootName()}
	for _, step := range tr[1:] {
		a, ok := step.(hcl.TraverseAttr)
		if !ok {
			return "", false
		}
		parts = append(parts, a.Name)
	}
	if data {
		return strings.Join(parts, "."), len(parts) == 3 && parts[0] == "data"
	}
	return strings.Join(parts, "."), len(parts) == 2 && parts[0] != "data"
}

// override checks an override_resource or override_data block: a target in
// the tree and literal values. Outside a run block (every run), an override
// with values on a whole block, no instance key, fixes its values.
func (c *checker) override(file string, b *hclsyntax.Block, everyRun bool) {
	target, values := b.Body.Attributes["target"], b.Body.Attributes["values"]
	if everyRun && target != nil && values != nil && filled(values.Expr) {
		if key, ok := wholeBlock(target.Expr, b.Type == "override_data"); ok {
			c.overrides[key] = true
		}
	}
	for _, a := range sortedAttrs(b.Body) {
		switch {
		case c.planOverride(a):
		case !overrideArgs[a.Name]:
			c.add(file, a.NameRange.Start.Line, RuleBlockAttributeDenied, a.Name)
		case a.Name == "target":
			c.traversal(file, a.Name, a.Expr, overrideRoots)
		default:
			c.literal(file, a)
		}
	}
	for _, nb := range b.Body.Blocks {
		c.nestedDenied(file, nb)
	}
}

// literals checks a variables block: every value a literal.
func (c *checker) literals(file string, body *hclsyntax.Body) {
	for _, a := range sortedAttrs(body) {
		c.literal(file, a)
	}
	for _, nb := range body.Blocks {
		c.nestedDenied(file, nb)
	}
}

func (c *checker) literal(file string, a *hclsyntax.Attribute) {
	if !isLiteral(a.Expr) {
		c.add(file, a.NameRange.Start.Line, RuleTestValueNotLiteral, a.Name)
	}
}

// run checks a run block: command = plan, said in so many words, since the
// default is apply.
func (c *checker) run(file string, b *hclsyntax.Block, s scope) {
	if cmd := b.Body.Attributes["command"]; cmd == nil || hcl.ExprAsKeyword(cmd.Expr) != "plan" {
		c.add(file, b.TypeRange.Start.Line, RuleTestCommandNotPlan, "")
	}
	for _, a := range sortedAttrs(b.Body) {
		switch a.Name {
		case "command":
		case "expect_failures":
			c.expectFailures(file, a)
		default: // providers, state_key, parallel and the rest
			c.add(file, a.NameRange.Start.Line, RuleBlockAttributeDenied, a.Name)
		}
	}
	for _, nb := range b.Body.Blocks {
		switch nb.Type {
		case "variables":
			c.literals(file, nb.Body)
		case "assert":
			c.fixed(file, nb.Body, s, conditionArgs, nil)
		case "plan_options":
			c.planOptions(file, nb.Body)
		case "override_resource", "override_data":
			c.override(file, nb, false)
		case "module", "override_module":
			c.add(file, nb.TypeRange.Start.Line, RuleBlockDenied, nb.Type)
		default:
			c.nestedDenied(file, nb)
		}
	}
}

// expectFailures checks a list of addresses in the tree.
func (c *checker) expectFailures(file string, a *hclsyntax.Attribute) {
	t, ok := a.Expr.(*hclsyntax.TupleConsExpr)
	if !ok {
		c.add(file, a.NameRange.Start.Line, RuleBlockAttributeDenied, a.Name)
		return
	}
	for _, item := range t.Exprs {
		c.traversal(file, a.Name, item, expectRoots)
	}
}

// planOptions allows mode = normal and nothing else: no refresh, replace or
// target.
func (c *checker) planOptions(file string, body *hclsyntax.Body) {
	for _, a := range sortedAttrs(body) {
		if a.Name != "mode" || hcl.ExprAsKeyword(a.Expr) != "normal" {
			c.add(file, a.NameRange.Start.Line, RuleTestPlanOptionsDenied, a.Name)
		}
	}
	for _, nb := range body.Blocks {
		c.add(file, nb.TypeRange.Start.Line, RuleTestPlanOptionsDenied, nb.Type)
	}
}

// isLiteral reports a value written out in full: strings with no
// interpolation, numbers (negated too), bools, null, and lists and objects of
// them with bare or quoted keys. It reads syntax only.
func isLiteral(e hclsyntax.Expression) bool {
	switch e := e.(type) {
	case *hclsyntax.LiteralValueExpr:
		return true
	case *hclsyntax.TemplateExpr:
		_, ok := literalString(e)
		return ok
	case *hclsyntax.TupleConsExpr:
		for _, item := range e.Exprs {
			if !isLiteral(item) {
				return false
			}
		}
		return true
	case *hclsyntax.ObjectConsExpr:
		for _, item := range e.Items {
			if !literalKey(item.KeyExpr) || !isLiteral(item.ValueExpr) {
				return false
			}
		}
		return true
	case *hclsyntax.UnaryOpExpr:
		l, ok := e.Val.(*hclsyntax.LiteralValueExpr)
		return e.Op == hclsyntax.OpNegate && ok && l.Val.Type() == cty.Number
	}
	return false
}

// literalKey reports an object key that is a bare name or a quoted string,
// never a parenthesized expression.
func literalKey(e hclsyntax.Expression) bool {
	k, ok := e.(*hclsyntax.ObjectConsKeyExpr)
	if !ok || k.ForceNonLiteral {
		return false
	}
	if t, ok := k.Wrapped.(*hclsyntax.ScopeTraversalExpr); ok {
		return len(t.Traversal) == 1
	}
	return isLiteral(k.Wrapped)
}
