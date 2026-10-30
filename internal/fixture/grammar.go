package fixture

import (
	"maps"
	"net/url"
	"path"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

// level is the grammar of one ladder level. Phase 5 adds L2 here, with its
// own file grammar for .tftest.hcl.
type level struct {
	providers map[string]providerRule // by local name
	underTest bool                    // exactly one resource of the type under test
}

func (l level) allows(provider string) bool {
	_, ok := l.providers[provider]
	return ok
}

// providerRule lists the arguments a provider block may set, each a literal.
// A provider with arguments needs one block setting all of them, endpoints
// included, before a resource of it may run.
type providerRule struct {
	args      map[string]valueKind
	endpoints bool // an endpoints block of loopback http URLs
}

type valueKind int

const (
	kindString valueKind = iota // a nonempty literal string
	kindTrue                    // true, or "true"
)

// builtinTerraform is the provider of terraform_data: it takes no arguments.
var builtinTerraform = providerRule{}

var awsProvider = providerRule{
	args: map[string]valueKind{
		"region":                      kindString,
		"access_key":                  kindString,
		"secret_key":                  kindString,
		"skip_credentials_validation": kindTrue,
		"skip_requesting_account_id":  kindTrue,
		"skip_metadata_api_check":     kindTrue,
		"skip_region_validation":      kindTrue,
	},
	endpoints: true,
}

// levels are the grammars by name. L1 runs the built-in provider only; L3
// adds one provider resource under test.
var levels = map[string]level{
	"L1": {providers: map[string]providerRule{"terraform": builtinTerraform}},
	"L3": {providers: map[string]providerRule{"terraform": builtinTerraform, "aws": awsProvider}, underTest: true},
}

// Levels returns the level names, sorted.
func Levels() []string { return slices.Sorted(maps.Keys(levels)) }

// Names a reference may start with, besides the resource types allowed.
var referenceRoots = map[string]bool{"var": true, "local": true, "each": true, "count": true, "self": true}

// Names a for expression or dynamic iterator may not bind, besides the
// reference roots: it would shadow a Terraform object.
var reservedNames = set("data", "ephemeral", "module", "path", "provider", "resource", "terraform")

// Names and functions a variable's type constraint adds.
var (
	typeKeywords  = map[string]bool{"string": true, "number": true, "bool": true, "any": true}
	typeFunctions = map[string]bool{"list": true, "map": true, "set": true, "tuple": true, "object": true, "optional": true}
)

// Arguments and nested blocks of the blocks with a fixed schema.
var (
	variableArgs  = set("default", "description", "nullable", "sensitive", "type")
	outputArgs    = set("depends_on", "description", "sensitive", "value")
	conditionArgs = set("condition", "error_message")
	lifecycleArgs = set("create_before_destroy", "ignore_changes", "prevent_destroy", "replace_triggered_by")
	dynamicArgs   = set("for_each", "iterator", "labels")
	// Blocks refused wherever they appear.
	deniedNested = set("backend", "cloud", "connection", "provider_meta", "provisioner")
)

func set(names ...string) map[string]bool {
	m := map[string]bool{}
	for _, n := range names {
		m[n] = true
	}
	return m
}

// scope is the reference roots and functions an expression may use.
type scope struct {
	roots map[string]bool
	funcs map[string]bool
}

func (s scope) with(roots, funcs map[string]bool) scope {
	n := scope{roots: maps.Clone(s.roots), funcs: maps.Clone(s.funcs)}
	maps.Copy(n.roots, roots)
	maps.Copy(n.funcs, funcs)
	return n
}

// reserved reports a name a binding may not shadow.
func (s scope) reserved(name string) bool { return s.roots[name] || reservedNames[name] }

func (c *checker) baseScope() scope {
	roots := maps.Clone(referenceRoots)
	roots["terraform_data"] = true
	if c.underTest != "" {
		roots[c.underTest] = true
	}
	return scope{roots: roots, funcs: pureFunctionSet}
}

// sortedAttrs returns the attributes of b in source order.
func sortedAttrs(b *hclsyntax.Body) []*hclsyntax.Attribute {
	return slices.SortedFunc(maps.Values(b.Attributes), func(x, y *hclsyntax.Attribute) int {
		return x.SrcRange.Start.Byte - y.SrcRange.Start.Byte
	})
}

// top checks the top-level body of a file.
func (c *checker) top(file string, body *hclsyntax.Body) {
	for _, a := range sortedAttrs(body) {
		c.add(file, a.NameRange.Start.Line, RuleBlockDenied, a.Name)
	}
	s := c.baseScope()
	for _, b := range body.Blocks {
		line := b.TypeRange.Start.Line
		switch b.Type {
		case "terraform":
			c.terraform(file, b.Body)
		case "variable":
			c.fixed(file, b.Body, s, variableArgs, map[string]bool{"validation": true})
		case "output":
			c.fixed(file, b.Body, s, outputArgs, map[string]bool{"precondition": true})
		case "locals":
			c.fixed(file, b.Body, s, nil, nil)
		case "provider":
			c.provider(file, b)
			if len(b.Labels) == 1 {
				c.providerBlocks[b.Labels[0]]++
			}
		case "resource":
			c.resource(file, b, s)
		default:
			c.add(file, line, RuleBlockDenied, b.Type)
		}
	}
}

// fixed checks a body whose arguments are those in args, or any when args
// is nil, and whose nested blocks are condition blocks named in blocks.
func (c *checker) fixed(file string, body *hclsyntax.Body, s scope, args, blocks map[string]bool) {
	for _, a := range sortedAttrs(body) {
		switch {
		case args != nil && !args[a.Name]:
			c.add(file, a.NameRange.Start.Line, RuleBlockAttributeDenied, a.Name)
		case a.Name == "type" && args != nil && args["type"]:
			c.expr(file, a.Expr, s.with(typeKeywords, typeFunctions))
		case a.Name == "ignore_changes" && args != nil && args["ignore_changes"]:
			// Attribute paths of the resource itself, never evaluated.
			if !attributePaths(a.Expr) {
				c.add(file, a.NameRange.Start.Line, RuleBlockAttributeDenied, a.Name)
			}
		default:
			c.expr(file, a.Expr, s)
		}
	}
	for _, b := range body.Blocks {
		if !blocks[b.Type] {
			c.nestedDenied(file, b)
			continue
		}
		c.fixed(file, b.Body, s, conditionArgs, nil)
	}
}

// attributePaths reports the keyword all or a list of plain traversals.
func attributePaths(e hclsyntax.Expression) bool {
	if hcl.ExprAsKeyword(e) == "all" {
		return true
	}
	t, ok := e.(*hclsyntax.TupleConsExpr)
	if !ok {
		return false
	}
	for _, item := range t.Exprs {
		if _, diags := hcl.AbsTraversalForExpr(item); diags.HasErrors() {
			return false
		}
	}
	return true
}

// nestedDenied reports a nested block outside the allowlist of its parent.
func (c *checker) nestedDenied(file string, b *hclsyntax.Block) {
	rule := RuleBlockAttributeDenied
	if deniedNested[b.Type] {
		rule = RuleBlockDenied
	}
	c.add(file, b.TypeRange.Start.Line, rule, b.Type)
}

// terraform checks a terraform block: required_providers entries only.
func (c *checker) terraform(file string, body *hclsyntax.Body) {
	for _, a := range sortedAttrs(body) {
		c.add(file, a.NameRange.Start.Line, RuleBlockAttributeDenied, a.Name)
	}
	for _, b := range body.Blocks {
		if b.Type != "required_providers" {
			c.nestedDenied(file, b)
			continue
		}
		for _, nb := range b.Body.Blocks {
			c.nestedDenied(file, nb)
		}
		for _, a := range sortedAttrs(b.Body) {
			c.requiredProvider(file, a)
		}
	}
}

// requiredProvider checks one required_providers entry: a provider of the
// level, as { source, version } literals equal to a pin whose type is the
// local name.
func (c *checker) requiredProvider(file string, a *hclsyntax.Attribute) {
	line := a.NameRange.Start.Line
	if !c.lv.allows(a.Name) {
		c.add(file, line, RuleProviderDenied, a.Name)
		return
	}
	c.declared[a.Name] = true
	obj, ok := a.Expr.(*hclsyntax.ObjectConsExpr)
	if !ok {
		c.add(file, line, RuleProviderNotPinned, a.Name)
		return
	}
	got := map[string]string{}
	for _, item := range obj.Items {
		key := hcl.ExprAsKeyword(item.KeyExpr)
		if key != "source" && key != "version" {
			c.add(file, item.KeyExpr.Range().Start.Line, RuleBlockAttributeDenied, key)
			continue
		}
		if v, ok := literalString(item.ValueExpr); ok {
			got[key] = v
		}
	}
	src, ver := got["source"], got["version"]
	if !c.pins[[2]string{src, ver}] || path.Base(src) != a.Name {
		c.add(file, line, RuleProviderNotPinned, a.Name)
	}
}

// provider checks a provider block against the allowlist of its provider.
func (c *checker) provider(file string, b *hclsyntax.Block) {
	line := b.TypeRange.Start.Line
	if len(b.Labels) != 1 || !c.lv.allows(b.Labels[0]) {
		c.add(file, line, RuleProviderDenied, strings.Join(b.Labels, "."))
		return
	}
	rule := c.lv.providers[b.Labels[0]]
	for _, name := range slices.Sorted(maps.Keys(rule.args)) {
		if b.Body.Attributes[name] == nil {
			c.add(file, line, RuleProviderValueDenied, name)
		}
	}
	endpoints := 0
	for _, a := range sortedAttrs(b.Body) {
		kind, ok := rule.args[a.Name]
		switch {
		case !ok:
			c.add(file, a.NameRange.Start.Line, RuleBlockAttributeDenied, a.Name)
		case !kind.accepts(a.Expr):
			c.add(file, a.NameRange.Start.Line, RuleProviderValueDenied, a.Name)
		}
	}
	for _, nb := range b.Body.Blocks {
		if nb.Type != "endpoints" || !rule.endpoints {
			c.nestedDenied(file, nb)
			continue
		}
		for _, eb := range nb.Body.Blocks {
			c.nestedDenied(file, eb)
		}
		for _, a := range sortedAttrs(nb.Body) {
			endpoints++
			if v, ok := literalString(a.Expr); !ok || !closedLoopback(v) {
				c.add(file, a.NameRange.Start.Line, RuleProviderValueDenied, a.Name)
			}
		}
	}
	if rule.endpoints && endpoints == 0 {
		c.add(file, line, RuleProviderValueDenied, "endpoints")
	}
}

func (k valueKind) accepts(e hclsyntax.Expression) bool {
	if k == kindTrue {
		if l, ok := e.(*hclsyntax.LiteralValueExpr); ok {
			return l.Val.Type() == cty.Bool && l.Val.IsKnown() && !l.Val.IsNull() && l.Val.True()
		}
		v, ok := literalString(e)
		return ok && v == "true"
	}
	v, ok := literalString(e)
	return ok && v != ""
}

// literalString returns the value of a quoted string with no interpolation.
// It reads syntax only: nothing is evaluated.
func literalString(e hclsyntax.Expression) (string, bool) {
	t, ok := e.(*hclsyntax.TemplateExpr)
	if !ok {
		return "", false
	}
	var b strings.Builder
	for _, p := range t.Parts {
		l, ok := p.(*hclsyntax.LiteralValueExpr)
		if !ok || l.Val.Type() != cty.String || !l.Val.IsKnown() || l.Val.IsNull() {
			return "", false
		}
		b.WriteString(l.Val.AsString())
	}
	return b.String(), true
}

// closedLoopback reports an http URL to port 1 of a loopback host, the
// closed port the design names, with no user info, path beyond "/", query
// or fragment: no endpoint can reach a service listening on the host.
func closedLoopback(s string) bool {
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "http" || u.User != nil || u.Opaque != "" || u.Port() != "1" ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return false
	}
	switch u.Hostname() {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}

// resource checks a resource block: terraform_data, or the type under test.
func (c *checker) resource(file string, b *hclsyntax.Block, s scope) {
	line := b.TypeRange.Start.Line
	if len(b.Labels) != 2 {
		c.add(file, line, RuleHCLParseError, "resource")
		return
	}
	switch typ := b.Labels[0]; {
	case typ == "terraform_data":
	case c.underTest != "" && typ == c.underTest:
		c.tested = append(c.tested, place{file, line})
	default:
		c.add(file, line, RuleResourceTypeDenied, typ)
		return
	}
	c.resourceBody(file, b.Body, s, true)
}

// resourceBody checks the arguments and nested blocks of a resource; meta is
// set for the resource's own body, where provider names a provider.
func (c *checker) resourceBody(file string, body *hclsyntax.Body, s scope, meta bool) {
	for _, a := range sortedAttrs(body) {
		if meta && a.Name == "provider" {
			if name := hcl.ExprAsKeyword(a.Expr); name == "" || !c.lv.allows(name) {
				c.add(file, a.NameRange.Start.Line, RuleProviderDenied, name)
			}
			continue
		}
		c.expr(file, a.Expr, s)
	}
	for _, b := range body.Blocks {
		switch {
		case deniedNested[b.Type]:
			c.nestedDenied(file, b)
		case b.Type == "lifecycle":
			c.fixed(file, b.Body, s, lifecycleArgs, map[string]bool{"precondition": true, "postcondition": true})
		case b.Type == "dynamic":
			c.dynamic(file, b, s)
		default:
			c.resourceBody(file, b.Body, s, false)
		}
	}
}

// dynamic checks a dynamic block. Its content may use its iterator.
func (c *checker) dynamic(file string, b *hclsyntax.Block, s scope) {
	line := b.TypeRange.Start.Line
	if len(b.Labels) != 1 || deniedNested[b.Labels[0]] || b.Labels[0] == "lifecycle" || b.Labels[0] == "dynamic" {
		c.add(file, line, RuleBlockDenied, "dynamic")
		return
	}
	iter := b.Labels[0]
	for _, a := range sortedAttrs(b.Body) {
		switch {
		case !dynamicArgs[a.Name]:
			c.add(file, a.NameRange.Start.Line, RuleBlockAttributeDenied, a.Name)
		case a.Name == "iterator":
			if name := hcl.ExprAsKeyword(a.Expr); name != "" {
				iter = name
			} else {
				c.add(file, a.NameRange.Start.Line, RuleBlockAttributeDenied, a.Name)
			}
		default:
			c.expr(file, a.Expr, s)
		}
	}
	if s.reserved(iter) {
		c.add(file, line, RuleExprReferenceDenied, iter)
	}
	inner := s.with(map[string]bool{iter: true}, nil)
	for _, nb := range b.Body.Blocks {
		if nb.Type != "content" {
			c.nestedDenied(file, nb)
			continue
		}
		c.resourceBody(file, nb.Body, inner, false)
	}
}

// expr checks every function call and reference in e against s.
func (c *checker) expr(file string, e hclsyntax.Expression, s scope) {
	_ = hclsyntax.VisitAll(e, func(n hclsyntax.Node) hcl.Diagnostics {
		switch n := n.(type) {
		case *hclsyntax.FunctionCallExpr:
			if !s.funcs[n.Name] {
				c.add(file, n.NameRange.Start.Line, RuleExprFunctionDenied, n.Name)
			}
		case *hclsyntax.ForExpr:
			for _, name := range []string{n.KeyVar, n.ValVar} {
				if name != "" && s.reserved(name) {
					c.add(file, n.OpenRange.Start.Line, RuleExprReferenceDenied, name)
				}
			}
		}
		return nil
	})
	// Variables leaves out the names a for expression binds.
	for _, tr := range e.Variables() {
		if root := tr.RootName(); !s.roots[root] {
			c.add(file, tr.SourceRange().Start.Line, RuleExprReferenceDenied, root)
		}
	}
}
