package facts

import (
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	tfjson "github.com/hashicorp/terraform-json"
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
// one item, that the block leaves unset or sets to null, the literal or a
// local that is the literal null. Any other value, an input or a static one,
// can set it; a local the engine cannot follow to such a value is a
// candidate. A dynamic block counts as set unless its for_each is a literal
// with no element. In an edited block a gap the base block had too is not
// this change's: base gaps are a multiset by schema path.
func requiredUnreachable(a *Area, ps providerSchema, r Resource, base *Resource) []schemaItem {
	had := map[string]int{}
	if base != nil {
		for _, it := range unsetItems(ps, nullish(a.Base.Locals), coverageBlockAddr(*base), base.File, base.Line, base.Body, nil, true) {
			if required(it) {
				had[it.key()]++
			}
		}
	}
	var out []schemaItem
	for _, it := range unsetItems(ps, nullish(a.Head.Locals), coverageBlockAddr(r), r.File, r.Line, r.Body, nil, true) {
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
	for _, it := range unsetItems(ps, nullLiteral, coverageBlockAddr(r), r.File, r.Line, r.Body, nil, true) {
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
// create flag, a variable named create or create_*, or when a resource,
// data block or module call the output reads, directly or through locals at
// any depth, has count or for_each. The evidence is the output's lines, then
// each count or for_each line of what it reads and each create flag's line.
func modeOutputs(a *Area) []Candidate {
	var flags []Variable
	for _, v := range a.Head.Variables {
		if isCreationVar("var." + v.Name) {
			flags = append(flags, v)
		}
	}
	locals := localRefs(a.Head.Locals)
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
		reads := map[string]bool{}
		for refs := slices.Clone(o.References); len(refs) > 0; refs = refs[1:] {
			if !reads[refs[0]] {
				reads[refs[0]] = true
				if l, ok := strings.CutPrefix(refs[0], "local."); ok {
					refs = append(refs, locals[l]...)
				}
			}
		}
		switched := len(flags) > 0
		meta := func(file string, b Body) {
			for _, m := range []string{"count", "for_each"} {
				if e := b.Arg(m); e != nil {
					switched = true
					lines = append(lines, line{file, e.Range.Start.Line})
				}
			}
		}
		for _, r := range a.Head.Resources {
			if reads[coverageBlockAddr(r)] {
				meta(r.File, r.Body)
			}
		}
		for _, m := range a.Head.ModuleCalls {
			if reads["module."+m.Name] {
				meta(m.File, m.Body)
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

// attributeNotOutput: a computed, not deprecated attribute of a resource
// block the change adds that no output of the area reads, at the top level,
// inside a nested block at any depth, or inside a nested attribute that is
// not itself computed; the candidate's address and schema path name each
// level. An output reads an attribute when its value names it on the block,
// through indexes or a splat, or names a level above it or below it, or is
// the block itself. A read through a local or a for expression is not
// traced, so it stays a candidate.
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
	isRead := func(p string) bool {
		for q := range read {
			if q == p || strings.HasPrefix(p, q+".") || strings.HasPrefix(q, p+".") {
				return true
			}
		}
		return false
	}
	var out []schemaItem
	var walk func(attrs map[string]*tfjson.SchemaAttribute, blocks map[string]*tfjson.SchemaBlockType, path []string)
	walk = func(attrs map[string]*tfjson.SchemaAttribute, blocks map[string]*tfjson.SchemaBlockType, path []string) {
		for _, name := range slices.Sorted(maps.Keys(attrs)) {
			at, p := attrs[name], append(slices.Clone(path), name)
			switch {
			case at.Deprecated:
			case at.Computed:
				if !isRead(strings.Join(p, ".")) {
					out = append(out, ps.item(coverageBlockAddr(r)+"."+strings.Join(p, "."), r.File, p, r.Line))
				}
			case at.AttributeNestedType != nil:
				walk(at.AttributeNestedType.Attributes, nil, p)
			}
		}
		for _, name := range slices.Sorted(maps.Keys(blocks)) {
			if bt := blocks[name]; bt.Block != nil && !bt.Block.Deprecated {
				walk(bt.Block.Attributes, bt.Block.NestedBlocks, append(slices.Clone(path), name))
			}
		}
	}
	walk(f.Block.Attributes, f.Block.NestedBlocks, nil)
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
// line with its value; null decides that. It descends into the nested blocks the body writes,
// static and dynamic content alike, and into the object constructors of a
// nested attribute's value. A name the schema lacks is another rule's.
func unsetItems(ps providerSchema, null func(string) bool, addr, file string, line int, b Body, path []string, top bool) []schemaItem {
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
		if null(at.Value.Text) {
			it := ps.item(sub, file, p, at.Range.Start.Line)
			v := at.Value.Text
			it.value = &v
			out = append(out, it)
			continue
		}
		if s := f.Block.Attributes[at.Name]; s != nil && s.AttributeNestedType != nil {
			out = append(out, unsetKeys(ps, null, sub, file, p, s.AttributeNestedType, at.Value)...)
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
			out = append(out, unsetItems(ps, null, sub, file, nb.Range.Start.Line, nb.Body, append(slices.Clone(path), nb.Type), false)...)
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
func unsetKeys(ps providerSchema, null func(string) bool, addr, file string, path []string, nt *nestedAttrType, v Expr) []schemaItem {
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
				if null(text) {
					it := ps.item(oaddr+"."+name, file, p, kv.KeyExpr.Range().Start.Line)
					it.value = &text
					out = append(out, it)
				} else if s := nt.Attributes[name]; s != nil && s.AttributeNestedType != nil {
					out = append(out, unsetKeys(ps, null, oaddr+"."+name, file, p, s.AttributeNestedType, Expr{Text: text, Range: kv.ValueExpr.Range()})...)
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

// nullish returns what decides that a value is null for a body whose module
// has locals ls: the literal null, or a local whose value, followed through
// further locals, is. A local the engine cannot follow to a value, such as
// one read with an attribute or index, a missing one or a cycle, counts as
// null, so its argument stays a candidate.
func nullish(ls []Local) func(string) bool {
	value := map[string]string{}
	for _, l := range ls {
		value[l.Name] = l.Value.Text
	}
	return func(text string) bool {
		for range len(value) + 1 {
			if nullLiteral(text) {
				return true
			}
			name, whole, ok := readsLocal(text)
			if !ok {
				return false
			}
			v, found := value[name]
			if !whole || !found {
				return true
			}
			text = v
		}
		return true // a cycle
	}
}

// readsLocal returns the local an expression reads when it is a reference to
// one, and whole when it reads that local and nothing inside it.
func readsLocal(text string) (name string, whole, ok bool) {
	e, diags := hclsyntax.ParseExpression([]byte(text), "", hcl.InitialPos)
	if diags.HasErrors() {
		return "", false, false
	}
	t, isTraversal := unparen(e).(*hclsyntax.ScopeTraversalExpr)
	if !isTraversal || t.Traversal.RootName() != "local" || len(t.Traversal) < 2 {
		return "", false, false
	}
	a, isAttr := t.Traversal[1].(hcl.TraverseAttr)
	if !isAttr {
		return "", false, false
	}
	return a.Name, len(t.Traversal) == 2, true
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

// outputAttrs adds to read the attribute paths of resource r that an output
// value reads by name, the names past indexes and splats joined by dots, and
// reports whether the value is the block itself.
func outputAttrs(v Expr, file string, r Resource, read map[string]bool) (whole bool) {
	e, diags := hclsyntax.ParseExpression([]byte(v.Text), file, hcl.Pos{Line: v.Range.Start.Line, Column: v.Range.Start.Column})
	if diags.HasErrors() {
		return false
	}
	if p, of := readPath(e, r, nil); of && p == "" {
		return true
	}
	// A node that reads r records its whole path once; the nodes inside it,
	// such as the source of a splat, record nothing of their own.
	// Nodes are keyed by range: some, such as ChildScope, are not comparable.
	inside := map[hcl.Range]bool{}
	skip := func(n hclsyntax.Node) {
		_ = hclsyntax.VisitAll(n, func(c hclsyntax.Node) hcl.Diagnostics {
			inside[c.Range()] = true
			return nil
		})
	}
	_ = hclsyntax.VisitAll(e, func(n hclsyntax.Node) hcl.Diagnostics {
		if inside[n.Range()] {
			return nil
		}
		switch x := n.(type) {
		case *hclsyntax.ForExpr: // a read through a for expression is not traced
			if _, of := readPath(x.CollExpr, r, nil); of {
				skip(x.CollExpr)
			}
		case hclsyntax.Expression:
			if p, of := readPath(x, r, nil); of {
				if p != "" {
					read[p] = true
				}
				skip(x)
			}
		}
		return nil
	})
	return false
}

// readPath returns the attribute path an expression reads of resource r,
// through parentheses, indexes, traversals and splats, "" when it reads the
// block itself; of is false when it reads anything else. anon is the path the
// anonymous symbol of an enclosing splat stands for.
func readPath(e hclsyntax.Expression, r Resource, anon *string) (path string, of bool) {
	switch x := e.(type) {
	case *hclsyntax.ParenthesesExpr:
		return readPath(x.Expression, r, anon)
	case *hclsyntax.ScopeTraversalExpr:
		return attrOf(x.Traversal, r)
	case *hclsyntax.AnonSymbolExpr:
		if anon != nil {
			return *anon, true
		}
	case *hclsyntax.IndexExpr: // r.block[expr]
		return readPath(x.Collection, r, anon)
	case *hclsyntax.RelativeTraversalExpr: // r.block[expr].attr
		if p, ok := readPath(x.Source, r, anon); ok {
			return strings.TrimPrefix(joinPath(p, attrPath(x.Traversal)), "."), true
		}
	case *hclsyntax.SplatExpr: // r.block[*].attr
		if p, ok := readPath(x.Source, r, anon); ok {
			if q, ok := readPath(x.Each, r, &p); ok {
				return q, true
			}
			return p, true
		}
	}
	return "", false
}

// attrOf returns the attribute path a traversal reads of resource r, "" when
// it reads the block itself; of is false when it reads anything else.
func attrOf(t hcl.Traversal, r Resource) (attr string, of bool) {
	if len(t) < 2 || t.RootName() != r.Type {
		return "", false
	}
	if n, ok := t[1].(hcl.TraverseAttr); !ok || n.Name != r.Name {
		return "", false
	}
	return attrPath(t[2:]), true
}

// attrPath returns the attribute names of a traversal, past indexes and
// splats, joined by dots, or "".
func attrPath(t hcl.Traversal) string {
	var names []string
	for _, s := range t {
		if a, ok := s.(hcl.TraverseAttr); ok {
			names = append(names, a.Name)
		}
	}
	return strings.Join(names, ".")
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
