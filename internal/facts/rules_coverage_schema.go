package facts

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	tfjson "github.com/hashicorp/terraform-json"

	"github.com/modulestf/facts/internal/schema"
)

// coverageSchemaRules is the coverage_schema rule family. Rule work for this family adds its rules and
// checks here and in no other rules file; Rules joins every family.
//
// Each rule reads the resource and data blocks the change adds or edits and
// looks up every argument and nested block they set in the schema handoff:
// at the latest version the head allows and at its declared minimum. A
// schema it cannot read for such a block makes the rule not_evaluated with
// schema_unavailable; an area whose change touches no such block is clean.
// What the detector cannot decide statically is a candidate, never clean.
var coverageSchemaRules = []Rule{
	// shortcut: registry scope newly-introduced drops arguments added to edited blocks; flips to touched-expression with the registry pin bump (mtf-lcc.2.40)
	coverageSchemaRule("coverage.argument-above-min-version", ScopeNewlyIntroduced, true, aboveMinVersion),
	coverageSchemaRule("coverage.block-attribute-mismatch", ScopeTouchedBlock, false, blockAttributeMismatch),
	// shortcut: registry scope newly-introduced drops arguments added to edited blocks; flips to touched-expression with the registry pin bump (mtf-lcc.2.40)
	coverageSchemaRule("coverage.deprecated-argument", ScopeNewlyIntroduced, false, deprecatedArgument),
	coverageSchemaRule("coverage.unknown-argument", ScopeTouchedBlock, false, unknownArgument),
}

func coverageSchemaRule(id, scope string, needsMinimum bool, defect func(schemaItem) bool) Rule {
	return Rule{
		ID: id, Tier: "candidate", Class: ClassConfig, Scope: scope, Kinds: []string{AreaRoot, AreaSubmodule},
		Check: func(a *Area) Result { return checkSchemaCoverage(a, id, needsMinimum, defect) },
	}
}

// Flags of an item that only coverage.unknown-argument reports.
const (
	flagOverMax      = "past max_items"
	flagCountUnknown = "dynamic block count unknown"
	flagBlockForAttr = "block syntax for an attribute"
	flagComputedKey  = "computed key"
)

// unknownArgument: an argument, block or object key the latest schema lacks,
// a block that is or may be repeated past the max_items the schema states,
// block syntax for an attribute, or a key computed at plan.
func unknownArgument(it schemaItem) bool {
	return it.latest == schema.AbsentFromSchema || it.flag != ""
}

// blockAttributeMismatch: attribute syntax for a name the latest schema
// lists as a nested block.
func blockAttributeMismatch(it schemaItem) bool {
	return !it.block && it.latest == schema.Present && it.field.BlockType != nil
}

func deprecatedArgument(it schemaItem) bool {
	return it.latest == schema.Present && deprecated(it.field)
}

// aboveMinVersion: present at the latest version, absent at the declared
// minimum. The introducing version stays a page fact the confirmer reads; a
// candidate it cannot confirm that way becomes review.check-not-run.
func aboveMinVersion(it schemaItem) bool {
	return it.latest == schema.Present && it.minimum == schema.AbsentFromSchema && !it.inNewBlock
}

func deprecated(f schema.Field) bool {
	switch {
	case f.Attribute != nil:
		return f.Attribute.Deprecated
	case f.BlockType != nil:
		return f.Block != nil && f.Block.Deprecated
	}
	return false
}

// schemaItem is one argument, nested block or object key a resource or data
// block sets, with what the schema says of it.
type schemaItem struct {
	addr  string   // subject address, static nested blocks indexed as in the file index
	path  []string // schema path: the names from the type down; empty for the type
	file  string   // relative to the area
	line  int
	value *string // source text of a value; nil for a block
	block bool    // written as a block
	flag  string  // one of the flag constants, or ""
	// inNewBlock is set inside a block absent at the minimum, which is
	// reported alone.
	inNewBlock bool
	latest     schema.Outcome
	field      schema.Field // at latest
	minimum    schema.Outcome
	minimal    schema.Field // at the declared minimum
}

// key identifies an item's defect without nested block indices, so a block
// inserted before another does not make the base's defects new.
func (it schemaItem) key() string {
	v := ""
	if it.value != nil {
		v = *it.value
	}
	return strings.Join(it.path, ".") + "\x00" + it.flag + "\x00" + v
}

// providerSchema is what a resource block's lookups need.
type providerSchema struct {
	set                     *schema.Set
	source, latest, minimum string
	data                    bool
	typ                     string
	typeAtMinimum           schema.Outcome
}

func (ps providerSchema) lookup(version string, path []string) (schema.Outcome, schema.Field) {
	return ps.set.Lookup(schema.Query{Source: ps.source, Version: version, DataSource: ps.data, Type: ps.typ, Path: path})
}

func checkSchemaCoverage(a *Area, id string, needsMinimum bool, defect func(schemaItem) bool) Result {
	var res Result
	for _, r := range a.Head.Resources {
		base := baseResource(a, r)
		if base != nil && base.Hash == r.Hash {
			continue // untouched
		}
		ps, why := resolveSchema(a, r, needsMinimum)
		if why != "" {
			return Result{NotEvaluated: why, Code: ReasonSchemaUnavailable}
		}
		// In an edited block, a defect the base block already had is not
		// this change's: base defects are a multiset by key.
		had := map[string]int{}
		baseValue := map[string]*string{}
		if base != nil {
			for _, it := range resourceItems(ps, *base) {
				p := strings.Join(it.path, ".")
				if _, ok := baseValue[p]; !ok {
					baseValue[p] = it.value
				}
				if defect(it) {
					had[it.key()]++
				}
			}
		}
		for _, it := range resourceItems(ps, r) {
			if !defect(it) {
				continue
			}
			if k := it.key(); had[k] > 0 {
				had[k]--
				continue
			}
			res.Candidates = append(res.Candidates, schemaCandidate(a, id, ps, it, baseValue[strings.Join(it.path, ".")]))
		}
	}
	return res
}

// resourceItems lists the items of a resource block. A type absent at the
// declared minimum is one item on the block, with every other item inside it.
func resourceItems(ps providerSchema, r Resource) []schemaItem {
	items := collectItems(ps, resourceAddr(r), r.File, r.Body, nil, true)
	if ps.typeAtMinimum != schema.AbsentFromSchema {
		return items
	}
	for i := range items {
		items[i].inNewBlock = true
	}
	typ := schemaItem{addr: resourceAddr(r), file: r.File, line: r.Line, block: true, minimum: schema.AbsentFromSchema}
	typ.latest, typ.field = ps.lookup(ps.latest, nil)
	return append([]schemaItem{typ}, items...)
}

// baseResource returns the base block of the same address, or nil.
func baseResource(a *Area, r Resource) *Resource {
	if a.Base == nil {
		return nil
	}
	for i, b := range a.Base.Resources {
		if b.Mode == r.Mode && b.Type == r.Type && b.Name == r.Name {
			return &a.Base.Resources[i]
		}
	}
	return nil
}

func resourceAddr(r Resource) string {
	if r.Mode == "data" {
		return "data." + r.Type + "." + r.Name
	}
	return r.Type + "." + r.Name
}

// resolveSchema resolves a block's provider by its provider argument or its
// type's prefix, and checks the latest schema, and with needsMinimum the
// declared minimum's, has its type. It returns why not otherwise.
func resolveSchema(a *Area, r Resource, needsMinimum bool) (providerSchema, string) {
	addr := resourceAddr(r)
	if a.Schema != nil && a.Schema.Handoff == nil {
		// shortcut: raw schema files have no versions, so they are not read; upgrade when the follow-up reads them.
		return providerSchema{}, fmt.Sprintf("%s: the rule needs the schema handoff, and the run has raw schema files only", short(addr))
	}
	local, _, _ := strings.Cut(r.Type, "_")
	if e := r.Body.Arg("provider"); e != nil {
		local, _, _ = strings.Cut(strings.TrimSpace(e.Text), ".")
	}
	res := a.Schema.Resolve(a.Path, local)
	if res.Outcome != schema.Present {
		return providerSchema{}, fmt.Sprintf("%s: provider %q has no resolved schema: %s", short(addr), short(local), res.Reason)
	}
	ps := providerSchema{set: a.Schema, source: res.Source, latest: res.Versions.Latest, minimum: res.Versions.DeclaredMinimum, data: r.Mode == "data", typ: r.Type}
	if o, _ := ps.lookup(ps.latest, nil); o != schema.Present {
		return providerSchema{}, fmt.Sprintf("%s: no schema of the type at %s %s", short(addr), ps.source, ps.latest)
	}
	ps.typeAtMinimum, _ = ps.lookup(ps.minimum, nil)
	if needsMinimum && ps.typeAtMinimum == schema.Unavailable {
		return providerSchema{}, fmt.Sprintf("%s: no schema at the declared minimum %s %s", short(addr), ps.source, ps.minimum)
	}
	return ps, ""
}

func (ps providerSchema) item(addr, file string, path []string, line int) schemaItem {
	it := schemaItem{addr: addr, path: path, file: file, line: line}
	it.latest, it.field = ps.lookup(ps.latest, path)
	it.minimum, it.minimal = ps.lookup(ps.minimum, path)
	return it
}

// collectItems lists the arguments, nested blocks and the keys of nested
// attribute values of a body, depth first. At the top level the
// meta-arguments are left out. It descends into a block of the latest
// schema, and into block syntax for a list or set nested attribute, and
// marks what is inside a block absent at the minimum.
func collectItems(ps providerSchema, addr, file string, b Body, path []string, top bool) []schemaItem {
	var out []schemaItem
	for _, at := range b.Attributes {
		if top && resourceMetaAttrs[at.Name] {
			continue
		}
		it := ps.item(addr+"."+at.Name, file, append(slices.Clone(path), at.Name), at.Range.Start.Line)
		v := at.Value.Text
		it.value = &v
		out = append(out, it)
		if nt := nestedType(it); nt != nil && !it.block {
			out = append(out, valueItems(ps, it, nt, at.Value)...)
		}
	}
	seen := map[string]int{}  // static blocks per type, for the file index
	count := map[string]int{} // blocks per type so far, at most
	for _, nb := range b.Blocks {
		if top && resourceMetaBlocks[nb.Type] {
			continue
		}
		sub := addr + "." + nb.Type
		if !nb.Dynamic {
			sub += "[" + strconv.Itoa(seen[nb.Type]) + "]"
			seen[nb.Type]++
		}
		it := ps.item(sub, file, append(slices.Clone(path), nb.Type), nb.Range.Start.Line)
		it.block = true
		inside := false
		switch {
		case it.latest != schema.Present:
		case it.field.BlockType != nil:
			inside = true
			n, known := 1, true
			if nb.Dynamic {
				n, known = dynamicCount(nb.ForEach)
			}
			count[nb.Type] += n
			switch limit := it.field.BlockType.MaxItems; {
			case limit == 0:
			case !known:
				it.flag = flagCountUnknown
			case uint64(count[nb.Type]) > limit: //nolint:gosec // G115: count is a block count
				it.flag = flagOverMax
			}
		case nestedType(it) != nil && (nestedType(it).NestingMode == "list" || nestedType(it).NestingMode == "set"):
			inside = true
		case it.field.Attribute != nil:
			it.flag = flagBlockForAttr
		}
		out = append(out, it)
		if inside {
			children := collectItems(ps, sub, file, nb.Body, it.path, false)
			if it.minimum == schema.AbsentFromSchema { // children holds every descendant
				for i := range children {
					children[i].inNewBlock = true
				}
			}
			out = append(out, children...)
		}
	}
	return out
}

type nestedAttrType = tfjson.SchemaNestedAttributeType

func nestedType(it schemaItem) *nestedAttrType {
	if it.latest != schema.Present || it.field.Attribute == nil {
		return nil
	}
	return it.field.Attribute.AttributeNestedType
}

// dynamicCount bounds the blocks a dynamic block generates, from the forms
// of for_each that are decidable statically: a tuple or object literal, and
// a conditional over two of them. known is false for any other form.
func dynamicCount(forEach *Expr) (n int, known bool) {
	if forEach == nil {
		return 0, false
	}
	e, diags := hclsyntax.ParseExpression([]byte(forEach.Text), "", hcl.InitialPos)
	if diags.HasErrors() {
		return 0, false
	}
	return literalLen(e)
}

func literalLen(e hclsyntax.Expression) (int, bool) {
	switch x := e.(type) {
	case *hclsyntax.TupleConsExpr:
		return len(x.Exprs), true
	case *hclsyntax.ObjectConsExpr:
		return len(x.Items), true
	case *hclsyntax.ParenthesesExpr:
		return literalLen(x.Expression)
	case *hclsyntax.ConditionalExpr:
		t, tok := literalLen(x.TrueResult)
		f, fok := literalLen(x.FalseResult)
		return max(t, f), tok && fok
	}
	return 0, false
}

// valueItems lists the static keys of the object constructors in a nested
// attribute's value, as items of the attribute, and a computed key as a
// flagged item. A value with no object constructor, such as var.x, sets no
// key here.
func valueItems(ps providerSchema, parent schemaItem, nt *nestedAttrType, v Expr) []schemaItem {
	src := []byte(v.Text)
	e, diags := hclsyntax.ParseExpression(src, parent.file, hcl.Pos{Line: v.Range.Start.Line, Column: v.Range.Start.Column})
	if diags.HasErrors() {
		return nil
	}
	var objs []*hclsyntax.ObjectConsExpr
	if nt.NestingMode == "single" {
		objs = objects(e)
	} else {
		for _, el := range elements(e, nt.NestingMode == "map") {
			objs = append(objs, objects(el)...)
		}
	}
	var out []schemaItem
	for i, o := range objs {
		addr := parent.addr
		if nt.NestingMode != "single" {
			addr += "[" + strconv.Itoa(i) + "]"
		}
		for _, kv := range o.Items {
			text := string(kv.ValueExpr.Range().SliceBytes(src))
			line := kv.KeyExpr.Range().Start.Line
			name := keyName(kv.KeyExpr)
			if name == "" {
				// The key is known only at plan: flagged on the attribute.
				it := schemaItem{addr: addr, path: parent.path, file: parent.file, line: line, value: &text, flag: flagComputedKey,
					latest: schema.Present, minimum: parent.minimum, minimal: parent.minimal, inNewBlock: true}
				out = append(out, it)
				continue
			}
			it := ps.item(addr+"."+name, parent.file, append(slices.Clone(parent.path), name), line)
			it.value = &text
			it.inNewBlock = parent.inNewBlock || parent.minimum == schema.AbsentFromSchema
			out = append(out, it)
			if inner := nestedType(it); inner != nil {
				out = append(out, valueItems(ps, it, inner, Expr{Text: text, Range: kv.ValueExpr.Range()})...)
			}
		}
	}
	return out
}

// objects returns the object constructors an expression may yield.
func objects(e hclsyntax.Expression) []*hclsyntax.ObjectConsExpr {
	switch x := e.(type) {
	case *hclsyntax.ObjectConsExpr:
		return []*hclsyntax.ObjectConsExpr{x}
	case *hclsyntax.ParenthesesExpr:
		return objects(x.Expression)
	case *hclsyntax.ConditionalExpr:
		return append(objects(x.TrueResult), objects(x.FalseResult)...)
	}
	return nil
}

// elements returns the element expressions of a collection value: tuple
// elements, map values with asMap, and a for expression's value.
func elements(e hclsyntax.Expression, asMap bool) []hclsyntax.Expression {
	switch x := e.(type) {
	case *hclsyntax.TupleConsExpr:
		if !asMap {
			return x.Exprs
		}
	case *hclsyntax.ObjectConsExpr:
		if asMap {
			var out []hclsyntax.Expression
			for _, kv := range x.Items {
				out = append(out, kv.ValueExpr)
			}
			return out
		}
	case *hclsyntax.ForExpr:
		return []hclsyntax.Expression{x.ValExpr}
	case *hclsyntax.ParenthesesExpr:
		return elements(x.Expression, asMap)
	case *hclsyntax.ConditionalExpr:
		return append(elements(x.TrueResult, asMap), elements(x.FalseResult, asMap)...)
	}
	return nil
}

func schemaCandidate(a *Area, id string, ps providerSchema, it schemaItem, base *string) Candidate {
	complete := true
	capText := func(s *string) *string {
		if s == nil {
			return nil
		}
		t := *s
		if len(t) > maxEvidenceText {
			complete = false
			t = t[:maxEvidenceText]
		}
		return &t
	}
	file := areaFile(a.Path, it.file)
	text := ""
	if e := a.headTree.Entries[file]; e != nil {
		text = lineOf(e.Data, it.line)
	}
	key := strings.Join(it.path, ".")
	if key == "" {
		key = "." // the type itself
	}
	ev := Evidence{
		SourceLines: []SourceLine{{Revision: "head", File: file, Line: it.line, Text: *capText(&text)}},
		Base:        capText(base),
		Head:        capText(it.value),
		SchemaFacts: []SchemaFact{{ProviderSource: ps.source, Version: ps.latest, Type: ps.typ, Path: key, State: factState(it.latest, it.field)}},
	}
	if ps.minimum != ps.latest {
		ev.SchemaFacts = append(ev.SchemaFacts, SchemaFact{ProviderSource: ps.source, Version: ps.minimum, Type: ps.typ, Path: key, State: factState(it.minimum, it.minimal)})
	}
	line := it.line
	return Candidate{RuleID: id, File: file, Line: &line, Subject: subject(a.Path, short(it.addr)), Evidence: ev, EvidenceComplete: complete}
}

// factState is a lookup as a schema fact state.
func factState(o schema.Outcome, f schema.Field) string {
	switch {
	case o == schema.AbsentFromSchema:
		return "absent"
	case o != schema.Present:
		return "unknown"
	case deprecated(f):
		return "deprecated"
	}
	return "present"
}

// lineOf returns line n, 1-based, of src without its line ending.
func lineOf(src []byte, n int) string {
	for i, l := range strings.Split(string(src), "\n") {
		if i+1 == n {
			return strings.TrimRight(l, "\r")
		}
	}
	return ""
}
