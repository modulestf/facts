package facts

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

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
var coverageSchemaRules = []Rule{
	coverageSchemaRule("coverage.argument-above-min-version", ScopeNewlyIntroduced, true, aboveMinVersion),
	coverageSchemaRule("coverage.block-attribute-mismatch", ScopeTouchedBlock, false, blockAttributeMismatch),
	coverageSchemaRule("coverage.deprecated-argument", ScopeNewlyIntroduced, false, deprecatedArgument),
	coverageSchemaRule("coverage.unknown-argument", ScopeTouchedBlock, false, unknownArgument),
}

func coverageSchemaRule(id, scope string, needsMinimum bool, defect func(schemaItem) bool) Rule {
	return Rule{
		ID: id, Tier: "candidate", Class: ClassConfig, Scope: scope, Kinds: []string{AreaRoot, AreaSubmodule},
		Check: func(a *Area) Result { return checkSchemaCoverage(a, id, needsMinimum, defect) },
	}
}

// unknownArgument: an argument or block the latest schema lacks, or a block
// repeated past the max_items it states. Only static blocks are counted: a
// dynamic block's count is not known before plan.
func unknownArgument(it schemaItem) bool { return it.latest == schema.AbsentFromSchema || it.overMax }

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

// schemaItem is one argument or nested block a resource or data block sets,
// with what the schema says of it.
type schemaItem struct {
	addr    string   // subject address, static nested blocks indexed as in the file index
	path    []string // schema path: the names from the type down
	file    string   // relative to the area
	line    int
	value   *string // source text of an argument's value; nil for a block
	block   bool    // written as a block
	overMax bool    // a static block past the max_items of the latest schema
	// inNewBlock is set inside a block absent at the minimum, which is
	// reported alone.
	inNewBlock bool
	latest     schema.Outcome
	field      schema.Field // at latest
	minimum    schema.Outcome
	minimal    schema.Field // at the declared minimum
}

// providerSchema is what a resource block's lookups need.
type providerSchema struct {
	set                     *schema.Set
	source, latest, minimum string
	data                    bool
	typ                     string
}

func (p providerSchema) lookup(version string, path []string) (schema.Outcome, schema.Field) {
	return p.set.Lookup(schema.Query{Source: p.source, Version: version, DataSource: p.data, Type: p.typ, Path: path})
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
		addr := resourceAddr(r)
		// In an edited block, a defect the base block already had is not
		// this change's.
		before := map[string]schemaItem{}
		if base != nil {
			for _, it := range collectItems(ps, addr, r.File, base.Body, nil, true) {
				before[it.addr] = it
			}
		}
		for _, it := range collectItems(ps, addr, r.File, r.Body, nil, true) {
			if !defect(it) {
				continue
			}
			prior, had := before[it.addr]
			if had && defect(prior) {
				continue
			}
			var baseValue *string
			if had {
				baseValue = prior.value
			}
			res.Candidates = append(res.Candidates, schemaCandidate(a, id, ps, it, baseValue))
		}
	}
	return res
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
	local, _, _ := strings.Cut(r.Type, "_")
	if e := r.Body.Arg("provider"); e != nil {
		local, _, _ = strings.Cut(strings.TrimSpace(e.Text), ".")
	}
	addr := resourceAddr(r)
	res := a.Schema.Resolve(a.Path, local)
	if res.Outcome != schema.Present {
		return providerSchema{}, fmt.Sprintf("%s: provider %q has no resolved schema: %s", short(addr), short(local), res.Reason)
	}
	ps := providerSchema{set: a.Schema, source: res.Source, latest: res.Versions.Latest, minimum: res.Versions.DeclaredMinimum, data: r.Mode == "data", typ: r.Type}
	if o, _ := ps.lookup(ps.latest, nil); o != schema.Present {
		return providerSchema{}, fmt.Sprintf("%s: no schema of the type at %s %s", short(addr), ps.source, ps.latest)
	}
	if o, _ := ps.lookup(ps.minimum, nil); needsMinimum && o == schema.Unavailable {
		return providerSchema{}, fmt.Sprintf("%s: no schema at the declared minimum %s %s", short(addr), ps.source, ps.minimum)
	}
	return ps, ""
}

// collectItems lists the arguments and nested blocks of a body, depth first.
// At the top level the meta-arguments are left out. It does not descend into
// a block that is not a block of the latest schema, nor report the content of
// a block absent at the minimum: the block itself is the item.
func collectItems(ps providerSchema, addr, file string, b Body, path []string, top bool) []schemaItem {
	var out []schemaItem
	item := func(name, sub string, line int) schemaItem {
		p := append(slices.Clone(path), name)
		it := schemaItem{addr: sub, path: p, file: file, line: line}
		it.latest, it.field = ps.lookup(ps.latest, p)
		it.minimum, it.minimal = ps.lookup(ps.minimum, p)
		return it
	}
	for _, at := range b.Attributes {
		if top && resourceMetaAttrs[at.Name] {
			continue
		}
		it := item(at.Name, addr+"."+at.Name, at.Range.Start.Line)
		v := at.Value.Text
		it.value = &v
		out = append(out, it)
	}
	seen := map[string]int{}
	for _, nb := range b.Blocks {
		if top && resourceMetaBlocks[nb.Type] {
			continue
		}
		sub := addr + "." + nb.Type
		n := seen[nb.Type]
		if !nb.Dynamic {
			sub += "[" + strconv.Itoa(n) + "]"
			seen[nb.Type]++
		}
		it := item(nb.Type, sub, nb.Range.Start.Line)
		it.block = true
		bt := it.field.BlockType
		it.overMax = !nb.Dynamic && bt != nil && bt.MaxItems > 0 && uint64(n) >= bt.MaxItems //nolint:gosec // G115: n counts blocks
		out = append(out, it)
		if it.latest == schema.Present && bt != nil {
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
