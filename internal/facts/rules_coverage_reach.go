package facts

import (
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// coverageReachRules is the coverage_reach rule family. Rule work for this family adds its rules and
// checks here and in no other rules file; Rules joins every family.
//
// The rules ask what a caller can reach of a resource block: a Required
// argument nothing can set, an optional argument the module does not set,
// and a computed attribute no output reads. Like the coverage_schema rules
// they read every resource and data block the change adds or edits, so a
// block with no schema makes them not_evaluated with schema_unavailable, and
// an area whose change touches no such block is clean. Whether an omission
// is documented, or an attribute is worth an output, is prose the engine
// does not judge: each gap is a candidate, never clean.
var coverageReachRules = []Rule{
	{
		ID: "coverage.attribute-not-output", Tier: "candidate", Class: ClassConfig, Scope: ScopeTouchedBlock, Kinds: []string{AreaRoot, AreaSubmodule},
		Check: func(a *Area) Result {
			res := checkReach(a, "coverage.attribute-not-output", attributeNotOutput)
			if res.NotEvaluated == "" {
				res.Candidates = append(res.Candidates, modeOutputs(a)...)
			}
			return res
		},
	},
	coverageReachRule("coverage.optional-argument-unexposed", ScopeNewlyIntroduced, optionalUnexposed),
	coverageReachRule("coverage.required-argument-unreachable", ScopeTouchedBlock, requiredUnreachable),
}

// reachJudge returns the items of a touched block that are candidates. base
// is the block at the base, nil for an added block.
type reachJudge func(a *Area, ps providerSchema, r Resource, base *Resource) []schemaItem

func coverageReachRule(id, scope string, judge reachJudge) Rule {
	return Rule{
		ID: id, Tier: "candidate", Class: ClassConfig, Scope: scope, Kinds: []string{AreaRoot, AreaSubmodule},
		Check: func(a *Area) Result { return checkReach(a, id, judge) },
	}
}

func checkReach(a *Area, id string, judge reachJudge) Result {
	var res Result
	for _, r := range a.Head.Resources {
		base := baseResource(a, r)
		if base != nil && base.Hash == r.Hash {
			continue // untouched
		}
		ps, why := resolveSchema(a, r, false)
		if why != "" {
			return Result{NotEvaluated: why, Code: ReasonSchemaUnavailable}
		}
		for _, it := range judge(a, ps, r, base) {
			res.Candidates = append(res.Candidates, schemaCandidate(a, id, ps, it, baseArg(base, it)))
		}
	}
	return res
}

// requiredUnreachable: a Required attribute, or a nested block of at least
// one item, that the block leaves unset or sets to the literal null. Any
// other value, an input or a static one, can set it; a dynamic block counts
// as set unless its for_each is a literal with no element. In an edited
// block a gap the base block had too is not this change's: base gaps are a
// multiset by schema path.
func requiredUnreachable(_ *Area, ps providerSchema, r Resource, base *Resource) []schemaItem {
	had := map[string]int{}
	if base != nil {
		for _, it := range unsetItems(ps, coverageBlockAddr(*base), base.File, base.Line, base.Body, nil, true) {
			if required(it) {
				had[it.key()]++
			}
		}
	}
	var out []schemaItem
	for _, it := range unsetItems(ps, coverageBlockAddr(r), r.File, r.Line, r.Body, nil, true) {
		if !required(it) {
			continue
		}
		if k := it.key(); had[k] > 0 {
			had[k]--
			continue
		}
		out = append(out, it)
	}
	return out
}

// optionalUnexposed: an optional, not deprecated, attribute or nested block
// that a resource block the change adds leaves unset or sets to the literal
// null. Whether the module documents the omission is prose, left to the
// confirmer. A data source's arguments are filters the module chooses.
func optionalUnexposed(_ *Area, ps providerSchema, r Resource, base *Resource) []schemaItem {
	if base != nil || r.Mode == "data" {
		return nil
	}
	var out []schemaItem
	for _, it := range unsetItems(ps, coverageBlockAddr(r), r.File, r.Line, r.Body, nil, true) {
		switch f := it.field; {
		case f.Attribute != nil:
			// The id of a type is the provider's own, never an argument.
			providerID := len(it.path) == 1 && it.path[0] == "id"
			if f.Attribute.Optional && !f.Attribute.Deprecated && !providerID {
				out = append(out, it)
			}
		case f.BlockType != nil:
			if f.BlockType.MinItems == 0 && !deprecated(f) {
				out = append(out, it)
			}
		}
	}
	return out
}

// modeOutputs: an output the change adds or edits, in a module with a mode
// switch, may be wrong in one of its modes, which only the confirmer can
// judge (owner decision). A module has a mode switch when it declares a
// create flag, a variable named create or create_*, or when a resource or
// data block the output reads has count or for_each. The evidence is the
// output's lines, then each count or for_each line of the blocks it reads
// and each create flag's line.
func modeOutputs(a *Area) []Candidate {
	var flags []Variable
	for _, v := range a.Head.Variables {
		if v.Name == "create" || strings.HasPrefix(v.Name, "create_") {
			flags = append(flags, v)
		}
	}
	var out []Candidate
	for _, o := range a.Head.Outputs {
		var base *Output
		if a.Base != nil {
			if i := slices.IndexFunc(a.Base.Outputs, func(b Output) bool { return b.Name == o.Name }); i >= 0 {
				base = &a.Base.Outputs[i]
			}
		}
		if base != nil && base.Hash == o.Hash {
			continue // untouched
		}
		type line struct {
			file string
			n    int
		}
		var lines []line
		for n := o.Range.Start.Line; n <= o.Range.End.Line; n++ {
			lines = append(lines, line{o.File, n})
		}
		switched := len(flags) > 0
		for _, r := range a.Head.Resources {
			if !slices.Contains(o.References, coverageBlockAddr(r)) {
				continue
			}
			for _, m := range []string{"count", "for_each"} {
				if e := r.Body.Arg(m); e != nil {
					switched = true
					lines = append(lines, line{r.File, e.Range.Start.Line})
				}
			}
		}
		if !switched {
			continue
		}
		for _, v := range flags {
			lines = append(lines, line{v.File, v.Line})
		}
		complete := true
		capText := func(s string) string {
			if len(s) > maxEvidenceText {
				complete = false
				return s[:maxEvidenceText]
			}
			return s
		}
		ev := Evidence{SourceLines: []SourceLine{}, SchemaFacts: []SchemaFact{}}
		for _, l := range lines {
			if len(ev.SourceLines) == maxEvidenceLines {
				complete = false
				break
			}
			file := areaFile(a.Path, l.file)
			text := ""
			if e := a.headTree.Entries[file]; e != nil {
				text = coverageLineOf(e.Data, l.n)
			}
			ev.SourceLines = append(ev.SourceLines, SourceLine{Revision: "head", File: file, Line: l.n, Text: capText(text)})
		}
		if o.Value != nil {
			h := capText(o.Value.Text)
			ev.Head = &h
		}
		if base != nil && base.Value != nil {
			b := capText(base.Value.Text)
			ev.Base = &b
		}
		n := o.Line
		out = append(out, Candidate{RuleID: "coverage.attribute-not-output", File: areaFile(a.Path, o.File), Line: &n,
			Subject: subject(a.Path, "output."+short(o.Name)), Evidence: ev, EvidenceComplete: complete})
	}
	return out
}

// attributeNotOutput: a computed, not deprecated, top-level attribute of a
// resource block the change adds that no output of the area reads. An output
// reads it when its value names the attribute on the block, through indexes
// or a splat, or is the block itself. A read through a local or a for
// expression is not traced, so it stays a candidate.
func attributeNotOutput(a *Area, ps providerSchema, r Resource, base *Resource) []schemaItem {
	if base != nil || r.Mode == "data" {
		return nil
	}
	_, f := ps.lookup(ps.latest, nil)
	if f.Block == nil {
		return nil
	}
	read := map[string]bool{}
	for _, o := range a.Head.Outputs {
		if o.Value != nil && outputAttrs(*o.Value, o.File, r, read) {
			return nil // an output returns the whole block
		}
	}
	var out []schemaItem
	for _, name := range slices.Sorted(maps.Keys(f.Block.Attributes)) {
		at := f.Block.Attributes[name]
		if at.Computed && !at.Deprecated && !read[name] {
			out = append(out, ps.item(coverageBlockAddr(r)+"."+name, r.File, []string{name}, r.Line))
		}
	}
	return out
}

func required(it schemaItem) bool {
	switch f := it.field; {
	case f.Attribute != nil:
		return f.Attribute.Required
	case f.BlockType != nil:
		return f.BlockType.MinItems > 0
	}
	return false
}

// unsetItems lists what a body leaves unset in the latest schema of the
// block at path: each attribute and nested block type it does not write, at
// the body's line, and each attribute it sets to the literal null, at that
// line with its value. It descends into the nested blocks the body writes,
// static and dynamic content alike, and into the object constructors of a
// nested attribute's value. A name the schema lacks is another rule's.
func unsetItems(ps providerSchema, addr, file string, line int, b Body, path []string, top bool) []schemaItem {
	_, f := ps.lookup(ps.latest, path)
	if f.Block == nil {
		return nil
	}
	set := map[string]bool{}
	var out []schemaItem
	for _, at := range b.Attributes {
		if top && resourceMetaAttrs[at.Name] {
			continue
		}
		set[at.Name] = true
		sub := addr + "." + at.Name
		p := append(slices.Clone(path), at.Name)
		if nullLiteral(at.Value.Text) {
			it := ps.item(sub, file, p, at.Range.Start.Line)
			v := at.Value.Text
			it.value = &v
			out = append(out, it)
			continue
		}
		if s := f.Block.Attributes[at.Name]; s != nil && s.AttributeNestedType != nil {
			out = append(out, unsetKeys(ps, sub, file, p, s.AttributeNestedType, at.Value)...)
		}
	}
	seen := map[string]int{} // static blocks per type, for the file index
	for _, nb := range b.Blocks {
		if top && resourceMetaBlocks[nb.Type] {
			continue
		}
		sub := addr + "." + nb.Type
		if !nb.Dynamic {
			sub += "[" + strconv.Itoa(seen[nb.Type]) + "]"
			seen[nb.Type]++
		} else if n, known := dynamicCount(nb.ForEach); known && n == 0 {
			continue // generates no block
		}
		set[nb.Type] = true
		if f.Block.NestedBlocks[nb.Type] != nil {
			out = append(out, unsetItems(ps, sub, file, nb.Range.Start.Line, nb.Body, append(slices.Clone(path), nb.Type), false)...)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(f.Block.Attributes)) {
		if !set[name] {
			out = append(out, ps.item(addr+"."+name, file, append(slices.Clone(path), name), line))
		}
	}
	for _, name := range slices.Sorted(maps.Keys(f.Block.NestedBlocks)) {
		if !set[name] {
			it := ps.item(addr+"."+name, file, append(slices.Clone(path), name), line)
			it.block = true
			out = append(out, it)
		}
	}
	return out
}

// unsetKeys lists the attributes of a nested attribute type that an object
// constructor in its value leaves out or sets to the literal null. A value
// with no object constructor, such as var.x, is the caller's to fill. A
// computed key may be any name, so it sets none.
func unsetKeys(ps providerSchema, addr, file string, path []string, nt *nestedAttrType, v Expr) []schemaItem {
	src := []byte(v.Text)
	e, diags := hclsyntax.ParseExpression(src, file, hcl.Pos{Line: v.Range.Start.Line, Column: v.Range.Start.Column})
	if diags.HasErrors() {
		return nil
	}
	elems := []element{{e, ""}}
	if nt.NestingMode != "single" {
		elems = elements(e, nt.NestingMode == "map")
	}
	var out []schemaItem
	for _, el := range elems {
		for _, o := range objects(el.expr) {
			oaddr := addr + el.index
			set := map[string]bool{}
			for _, kv := range o.Items {
				name := keyName(kv.KeyExpr)
				if name == "" {
					continue
				}
				set[name] = true
				text := string(kv.ValueExpr.Range().SliceBytes(src))
				p := append(slices.Clone(path), name)
				if nullLiteral(text) {
					it := ps.item(oaddr+"."+name, file, p, kv.KeyExpr.Range().Start.Line)
					it.value = &text
					out = append(out, it)
				} else if s := nt.Attributes[name]; s != nil && s.AttributeNestedType != nil {
					out = append(out, unsetKeys(ps, oaddr+"."+name, file, p, s.AttributeNestedType, Expr{Text: text, Range: kv.ValueExpr.Range()})...)
				}
			}
			for _, name := range slices.Sorted(maps.Keys(nt.Attributes)) {
				if !set[name] {
					out = append(out, ps.item(oaddr+"."+name, file, append(slices.Clone(path), name), o.Range().Start.Line))
				}
			}
		}
	}
	return out
}

// nullLiteral reports whether an expression is the literal null.
func nullLiteral(text string) bool {
	e, diags := hclsyntax.ParseExpression([]byte(text), "", hcl.InitialPos)
	if diags.HasErrors() {
		return false
	}
	l, ok := unparen(e).(*hclsyntax.LiteralValueExpr)
	return ok && l.Val.IsNull()
}

func unparen(e hclsyntax.Expression) hclsyntax.Expression {
	for p, ok := e.(*hclsyntax.ParenthesesExpr); ok; p, ok = e.(*hclsyntax.ParenthesesExpr) {
		e = p.Expression
	}
	return e
}

// outputAttrs adds to read the attributes of resource r that an output value
// reads by name, and reports whether the value is the block itself.
func outputAttrs(v Expr, file string, r Resource, read map[string]bool) (whole bool) {
	e, diags := hclsyntax.ParseExpression([]byte(v.Text), file, hcl.Pos{Line: v.Range.Start.Line, Column: v.Range.Start.Column})
	if diags.HasErrors() {
		return false
	}
	if t, ok := unparen(e).(*hclsyntax.ScopeTraversalExpr); ok {
		if attr, of := attrOf(t.Traversal, r); of && attr == "" {
			return true
		}
	}
	_ = hclsyntax.VisitAll(e, func(n hclsyntax.Node) hcl.Diagnostics {
		switch x := n.(type) {
		case *hclsyntax.ScopeTraversalExpr:
			if attr, of := attrOf(x.Traversal, r); of && attr != "" {
				read[attr] = true
			}
		case *hclsyntax.RelativeTraversalExpr: // r[expr].attr
			if resourceRoot(x.Source, r) {
				if attr := firstAttr(x.Traversal); attr != "" {
					read[attr] = true
				}
			}
		case *hclsyntax.SplatExpr: // r[*].attr
			if each, ok := x.Each.(*hclsyntax.RelativeTraversalExpr); ok && resourceRoot(x.Source, r) {
				if attr := firstAttr(each.Traversal); attr != "" {
					read[attr] = true
				}
			}
		}
		return nil
	})
	return false
}

// attrOf returns the attribute a traversal reads of resource r, "" when it
// reads the block itself; of is false when it reads anything else.
func attrOf(t hcl.Traversal, r Resource) (attr string, of bool) {
	if len(t) < 2 || t.RootName() != r.Type {
		return "", false
	}
	if n, ok := t[1].(hcl.TraverseAttr); !ok || n.Name != r.Name {
		return "", false
	}
	return firstAttr(t[2:]), true
}

// resourceRoot reports whether e is resource r itself, through indexes and
// parentheses.
func resourceRoot(e hclsyntax.Expression, r Resource) bool {
	switch x := e.(type) {
	case *hclsyntax.ParenthesesExpr:
		return resourceRoot(x.Expression, r)
	case *hclsyntax.IndexExpr:
		return resourceRoot(x.Collection, r)
	case *hclsyntax.ScopeTraversalExpr:
		attr, of := attrOf(x.Traversal, r)
		return of && attr == ""
	}
	return false
}

// firstAttr returns the first attribute name of a traversal, past indexes and
// splats, or "".
func firstAttr(t hcl.Traversal) string {
	for _, s := range t {
		if a, ok := s.(hcl.TraverseAttr); ok {
			return a.Name
		}
	}
	return ""
}

// baseArg is the base value of a top-level argument, as evidence.
func baseArg(base *Resource, it schemaItem) *string {
	if base == nil || len(it.path) != 1 {
		return nil
	}
	for _, at := range base.Body.Attributes {
		if at.Name == it.path[0] {
			v := at.Value.Text
			return &v
		}
	}
	return nil
}
