package facts

import (
	"fmt"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

// structure.passthrough-default-drift and structure.passthrough-field-drift
// follow the reviewer's pass-through procedure: every call of a local module
// from a root or submodule area, the arguments it forwards, and the two types
// and unset values compared. An argument that is not forwarding, such as a
// literal, a local or merge(), is not compared, so it is clean. An unset
// value written differently from the submodule's is a candidate, since the
// engine compares source text and never evaluates. A forwarded attribute
// path whose unset value depends on a non-empty variable default is a
// candidate too.
func checkPassthroughDefaultDrift(a *Area) Result {
	return passthrough(a, "structure.passthrough-default-drift")
}

func checkPassthroughFieldDrift(a *Area) Result {
	return passthrough(a, "structure.passthrough-field-drift")
}

// Kinds of a passType.
const (
	passAny = iota
	passObject
	passColl
	passPrim  // string, number or bool
	passOther // a type the walk cannot read, such as a tuple
)

// passType is a type constraint as the procedure walks it: collections are
// stepped through, object attributes are compared.
type passType struct {
	kind  int
	elem  *passType
	attrs []*passAttr
	line  int // of the object(...) expression
}

type passAttr struct {
	name     string
	optional bool
	def      *string // the optional() default; nil for optional(type) or a required attribute
	line     int
	typ      *passType
}

func (t *passType) attr(name string) *passAttr {
	for _, at := range t.attrs {
		if at.name == name {
			return at
		}
	}
	return nil
}

// unset is the attribute's unset value, or ok false for a required one.
func (at *passAttr) unset() (string, bool) {
	switch {
	case !at.optional:
		return "", false
	case at.def != nil:
		return *at.def, true
	}
	return "null", true
}

// varType reads a variable's type. No type is any.
func varType(v *Variable) *passType {
	x := v.Body.Arg("type")
	if x == nil {
		return &passType{kind: passAny}
	}
	e := reparse(v.File, *x)
	if e == nil {
		return &passType{kind: passOther}
	}
	return passTypeOf(*x, e)
}

func passTypeOf(x Expr, e hclsyntax.Expression) *passType {
	switch t := e.(type) {
	case *hclsyntax.ScopeTraversalExpr:
		if len(t.Traversal) == 1 && t.Traversal.RootName() == "any" {
			return &passType{kind: passAny}
		}
		if len(t.Traversal) == 1 && typePrimitives[t.Traversal.RootName()] {
			return &passType{kind: passPrim}
		}
	case *hclsyntax.FunctionCallExpr:
		switch {
		case (t.Name == "list" || t.Name == "set" || t.Name == "map") && len(t.Args) == 1:
			return &passType{kind: passColl, elem: passTypeOf(x, t.Args[0])}
		case t.Name == "object" && len(t.Args) == 1:
			obj, ok := t.Args[0].(*hclsyntax.ObjectConsExpr)
			if !ok {
				break
			}
			out := &passType{kind: passObject, line: e.Range().Start.Line}
			for _, it := range obj.Items {
				at := &passAttr{name: keyName(it.KeyExpr), line: it.KeyExpr.Range().Start.Line}
				inner := it.ValueExpr
				if opt, ok := inner.(*hclsyntax.FunctionCallExpr); ok && opt.Name == "optional" && len(opt.Args) >= 1 && len(opt.Args) <= 2 {
					at.optional, inner = true, opt.Args[0]
					if len(opt.Args) == 2 {
						d := sourceOf(x, opt.Args[1].Range())
						at.def = &d
					}
				}
				at.typ = passTypeOf(x, inner)
				out.attrs = append(out.attrs, at)
			}
			return out
		}
	}
	return &passType{kind: passOther}
}

// passRef is one forwarded reference: var.<name> or each.value, then an
// attribute path.
type passRef struct {
	each bool
	name string
	path []string
}

// forwardRefs returns the references an argument forwards: one reference, or
// several joined only by coalesce(), try() or <ref> != null ? <ref> : <other>.
// ok is false for any other expression. each.value counts only when eachOK.
func forwardRefs(e hclsyntax.Expression, eachOK bool) (refs []passRef, ok bool) {
	switch x := e.(type) {
	case *hclsyntax.ParenthesesExpr:
		return forwardRefs(x.Expression, eachOK)
	case *hclsyntax.ScopeTraversalExpr:
		r, ok := traversalRef(x.Traversal, eachOK)
		return []passRef{r}, ok
	case *hclsyntax.FunctionCallExpr:
		if (x.Name != "coalesce" && x.Name != "try") || len(x.Args) == 0 || x.ExpandFinal {
			return nil, false
		}
		for _, arg := range x.Args {
			rs, ok := forwardRefs(arg, eachOK)
			if !ok {
				return nil, false
			}
			refs = append(refs, rs...)
		}
		return refs, true
	case *hclsyntax.ConditionalExpr:
		cond, ok := x.Condition.(*hclsyntax.BinaryOpExpr)
		if !ok || cond.Op != hclsyntax.OpNotEqual || !isNull(cond.RHS) {
			return nil, false
		}
		l, lok := forwardRefs(cond.LHS, eachOK)
		t, tok := forwardRefs(x.TrueResult, eachOK)
		f, fok := forwardRefs(x.FalseResult, eachOK)
		if !lok || !tok || !fok || len(l) != 1 || len(t) != 1 || !sameRef(l[0], t[0]) {
			return nil, false
		}
		return append(t, f...), true
	}
	return nil, false
}

func isNull(e hclsyntax.Expression) bool {
	lit, ok := e.(*hclsyntax.LiteralValueExpr)
	return ok && lit.Val.IsNull()
}

func sameRef(a, b passRef) bool {
	return a.each == b.each && a.name == b.name && slices.Equal(a.path, b.path)
}

// traversalRef reads var.<name>.<attr>... or each.value.<attr>...; a
// string index such as ["mode"] is an attribute step too. Any other index,
// such as [0], selects an element, which is not an attribute path, so the
// reference is not forwarding.
func traversalRef(t hcl.Traversal, eachOK bool) (passRef, bool) {
	var names []string
	for _, step := range t[1:] {
		switch st := step.(type) {
		case hcl.TraverseAttr:
			names = append(names, st.Name)
		case hcl.TraverseIndex:
			if !st.Key.IsKnown() || st.Key.IsNull() || st.Key.Type() != cty.String {
				return passRef{}, false
			}
			names = append(names, st.Key.AsString())
		default:
			return passRef{}, false
		}
	}
	switch {
	case t.RootName() == "var" && len(names) >= 1:
		return passRef{name: names[0], path: names[1:]}, true
	case t.RootName() == "each" && eachOK && len(names) >= 1 && names[0] == "value":
		return passRef{each: true, path: names[1:]}, true
	}
	return passRef{}, false
}

// forEachVar returns the root variable whose elements a call's for_each map
// holds unchanged: var.<name>, { for k, v in var.<name> : k => v [if ...] },
// or a local holding either. "" when there is none.
func forEachVar(m *Module, file string, x *Expr, viaLocal bool) string {
	if x == nil {
		return ""
	}
	e := reparse(file, *x)
	switch t := e.(type) {
	case *hclsyntax.ScopeTraversalExpr:
		if n, ok := rootName(t.Traversal, "var"); ok {
			return n
		}
		if n, ok := rootName(t.Traversal, "local"); ok && !viaLocal {
			for _, l := range m.Locals {
				if l.Name == n {
					return forEachVar(m, l.File, &l.Value, true)
				}
			}
		}
	case *hclsyntax.ForExpr:
		coll, cok := t.CollExpr.(*hclsyntax.ScopeTraversalExpr)
		key, kok := t.KeyExpr.(*hclsyntax.ScopeTraversalExpr)
		val, vok := t.ValExpr.(*hclsyntax.ScopeTraversalExpr)
		if !cok || !kok || !vok || t.Group || t.KeyVar == "" || len(key.Traversal) != 1 || len(val.Traversal) != 1 ||
			key.Traversal.RootName() != t.KeyVar || val.Traversal.RootName() != t.ValVar {
			return ""
		}
		if n, ok := rootName(coll.Traversal, "var"); ok {
			return n
		}
	}
	return ""
}

// rootName returns <name> of a traversal that is exactly <root>.<name>.
func rootName(t hcl.Traversal, root string) (string, bool) {
	if len(t) != 2 || t.RootName() != root {
		return "", false
	}
	a, ok := t[1].(hcl.TraverseAttr)
	return a.Name, ok
}

// passCheck collects the candidates of one area's calls for one rule.
type passCheck struct {
	a     *Area
	rule  string
	lc    *lineChanges
	seen  map[string]bool
	cands []Candidate
}

func (p *passCheck) add(rule, addr string, anchors []anchor, head *string) {
	if rule != p.rule {
		return
	}
	c := crossCandidate(p.a, p.lc, rule, addr, anchors, nil, head)
	k := fmt.Sprintf("%s:%d %s", c.File, *c.Line, c.Subject)
	if !p.seen[k] {
		p.seen[k] = true
		p.cands = append(p.cands, c)
	}
}

func passthrough(a *Area, rule string) Result {
	p := &passCheck{a: a, rule: rule, lc: &lineChanges{a: a, cache: map[string][]lineRange{}}, seen: map[string]bool{}}
	vars := map[string]*Variable{}
	for i := range a.Head.Variables {
		vars[a.Head.Variables[i].Name] = &a.Head.Variables[i]
	}
	for _, call := range a.Head.ModuleCalls {
		if callee := calledArea(a, call); callee != nil {
			p.call(call, callee, vars)
		}
	}
	return Result{Candidates: p.cands}
}

// call compares the arguments one call forwards with the callee's variables.
func (p *passCheck) call(call ModuleCall, callee *Area, vars map[string]*Variable) {
	subVars := map[string]*Variable{}
	for i := range callee.Head.Variables {
		subVars[callee.Head.Variables[i].Name] = &callee.Head.Variables[i]
	}
	eachVar := forEachVar(p.a.Head, call.File, call.Body.Arg("for_each"), false)
	set := map[string]bool{}
	var objects []string // root variables whose attributes the call forwards
	for _, at := range call.Body.Attributes {
		if moduleMetaAttrs[at.Name] {
			continue
		}
		set[at.Name] = true
		sv := subVars[at.Name]
		e := reparse(call.File, at.Value)
		if sv == nil || e == nil {
			continue
		}
		refs, ok := forwardRefs(e, eachVar != "")
		if !ok {
			continue
		}
		arg := anchor{areaFile(p.a.Path, call.File), at.Range.Start.Line}
		for _, r := range refs {
			if r.each {
				r.name = eachVar
			}
			rv := vars[r.name]
			if rv == nil {
				continue
			}
			if len(r.path) > 0 && !slices.Contains(objects, r.name) {
				objects = append(objects, r.name)
			}
			p.ref(r, rv, sv, callee, arg, len(refs) > 1)
		}
	}
	// A callee variable the call sets by no expression, while it forwards
	// attributes of a root object, is an attribute the root cannot pass. The
	// call's for_each or source line is an anchor too, so a new call lands.
	callLine := anchor{areaFile(p.a.Path, call.File), call.SourceRange.Start.Line}
	if fe := call.Body.Arg("for_each"); fe != nil {
		callLine.line = fe.Range.Start.Line
	}
	for _, name := range objects {
		rv := vars[name]
		for i := range callee.Head.Variables {
			sv := &callee.Head.Variables[i]
			if set[sv.Name] {
				continue
			}
			p.add("structure.passthrough-field-drift", "var."+name, []anchor{p.typeLine(rv), p.rootFile(rv, rv.Line), callLine, p.subFile(callee, sv, sv.Line)}, typeText(rv))
		}
	}
}

func (p *passCheck) rootFile(v *Variable, line int) anchor {
	return anchor{areaFile(p.a.Path, v.File), line}
}

func (p *passCheck) subFile(callee *Area, v *Variable, line int) anchor {
	return anchor{areaFile(callee.Path, v.File), line}
}

// typeText is the source text of a variable's type, or nil with none.
func typeText(v *Variable) *string {
	if x := v.Body.Arg("type"); x != nil {
		return &x.Text
	}
	return nil
}

// typeLine is the line of a variable's type argument, or of the variable.
func (p *passCheck) typeLine(v *Variable) anchor {
	if x := v.Body.Arg("type"); x != nil {
		return p.rootFile(v, x.Range.Start.Line)
	}
	return p.rootFile(v, v.Line)
}

// ref compares one forwarded reference with the callee variable it lands in.
func (p *passCheck) ref(r passRef, rv, sv *Variable, callee *Area, arg anchor, several bool) {
	const field, def = "structure.passthrough-field-drift", "structure.passthrough-default-drift"
	addr := strings.Join(append([]string{"var." + r.name}, r.path...), ".")
	root := varType(rv)
	if r.each {
		switch root.kind {
		case passColl:
			root = root.elem
		case passAny:
		default:
			// The elements of an object are not one type: undecidable.
			p.add(field, addr, []anchor{p.typeLine(rv), arg, p.subFile(callee, sv, sv.Line)}, typeText(rv))
			return
		}
	}
	var last *passAttr
	declared := []anchor{p.rootFile(rv, rv.Line)}
	if d := rv.Body.Arg("default"); d != nil && len(r.path) == 0 && !r.each {
		declared = append(declared, p.rootFile(rv, d.Range.Start.Line))
	}
	for _, step := range r.path {
		switch root.kind {
		case passObject:
			if last = root.attr(step); last == nil {
				return // not an attribute of the type: an invalid reference
			}
			root = last.typ
			declared = []anchor{p.rootFile(rv, last.line)}
		case passColl:
			last, root = nil, root.elem
		case passAny:
			last = nil
		default:
			return
		}
	}
	subDecl := []anchor{p.subFile(callee, sv, sv.Line)}
	if d := sv.Body.Arg("default"); d != nil {
		subDecl = append([]anchor{p.subFile(callee, sv, d.Range.Start.Line)}, subDecl...)
	}
	// Step 2 at the top: the unset values of the reference and the variable.
	if sv.Default != nil {
		rootUnset, known, required := p.refUnset(r, rv, last)
		nullable := sv.Nullable != nil && normExpr(sv.Nullable.Text) == "false"
		switch {
		case required:
		case !known || several || !sameUnset(rootUnset, sv.Default.Text, nullable):
			p.add(def, addr, slices.Concat(declared, []anchor{arg}, subDecl), &rootUnset)
		}
	}
	rootAt := p.typeLine(rv)
	if last != nil {
		rootAt = p.rootFile(rv, last.line)
	}
	subAt := p.subFile(callee, sv, sv.Line)
	if x := sv.Body.Arg("type"); x != nil {
		subAt.line = x.Range.Start.Line
	}
	p.walk(root, varType(sv), addr, rv, sv, callee, arg, rootAt, subAt)
}

// refUnset is what a reference yields when the caller sets nothing. required
// is set when it has no unset value; known is false when the engine cannot
// tell without evaluating the variable's default.
func (p *passCheck) refUnset(r passRef, rv *Variable, last *passAttr) (value string, known, required bool) {
	switch {
	case len(r.path) == 0 && r.each:
		return "", true, true
	case len(r.path) == 0 && rv.Default == nil:
		return "", true, true
	case len(r.path) == 0:
		return rv.Default.Text, true, false
	case last == nil:
		return "", false, false
	case r.each || rv.Default == nil || normExpr(rv.Default.Text) == "{ }":
		// Unset, or set to an object with the attribute left out, the
		// attribute takes its optional() default.
		v, ok := last.unset()
		return v, true, !ok
	}
	// A default that is null or sets attributes needs evaluating.
	return rv.Default.Text, false, false
}

// sameUnset compares two unset values as source text. A root null is no
// difference where Terraform substitutes the callee's default for it.
func sameUnset(root, sub string, substitutes bool) bool {
	if normExpr(root) == "null" && substitutes {
		return true
	}
	return normExpr(root) == normExpr(sub)
}

// walk compares the attributes of two types at every depth. rootAt and
// subAt are where each side declares the type walked.
func (p *passCheck) walk(r, s *passType, addr string, rv, sv *Variable, callee *Area, arg, rootAt, subAt anchor) {
	const field, def = "structure.passthrough-field-drift", "structure.passthrough-default-drift"
	switch {
	case r.kind == passAny || s.kind == passAny:
		return
	case r.kind == passColl && s.kind == passColl:
		p.walk(r.elem, s.elem, addr, rv, sv, callee, arg, rootAt, subAt)
		return
	case r.kind != s.kind || r.kind == passOther:
		// The shapes differ, or neither side can be read: undecidable.
		p.add(field, addr, []anchor{rootAt, arg, subAt}, typeText(rv))
		return
	case r.kind == passPrim:
		return // two primitives have no attributes to compare
	}
	for _, sa := range s.attrs {
		if r.attr(sa.name) == nil {
			p.add(field, addr, []anchor{p.rootFile(rv, r.line), arg, p.subFile(callee, sv, sa.line)}, typeText(rv))
		}
	}
	for _, ra := range r.attrs {
		sub := addr + "." + ra.name
		sa := s.attr(ra.name)
		if sa == nil {
			p.add(field, sub, []anchor{p.rootFile(rv, ra.line), arg, p.subFile(callee, sv, s.line)}, typeText(rv))
			continue
		}
		ru, rok := ra.unset()
		su, sok := sa.unset()
		if rok && sok && !sameUnset(ru, su, sa.def != nil) {
			p.add(def, sub, []anchor{p.rootFile(rv, ra.line), arg, p.subFile(callee, sv, sa.line)}, &ru)
		}
		p.walk(ra.typ, sa.typ, sub, rv, sv, callee, arg, p.rootFile(rv, ra.line), p.subFile(callee, sv, sa.line))
	}
}
