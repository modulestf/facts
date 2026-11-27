package facts

import (
	"maps"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

// compatIfaceRules is the compat_iface rule family. Rule work for this family adds its rules and
// checks here and in no other rules file; Rules joins every family.
//
// Each rule compares the variables and outputs of an area at the base and at
// the head, paired by name the way the interface diff pairs them: of two
// blocks with one name, the first in file order counts, and expressions are
// compared by their tokens. A case the detector cannot decide is a candidate
// for the confirmer, never clean.
var compatIfaceRules = []Rule{
	{ID: "compat.default-changed", Tier: "candidate", Class: ClassConfig, Scope: ScopeTouchedExpression, Kinds: compatKinds, NeedsBase: true, Check: checkDefaultChanged},
	{ID: "compat.type-narrowed", Tier: "candidate", Class: ClassConfig, Scope: ScopeTouchedBlock, Kinds: compatKinds, NeedsBase: true, Check: checkTypeNarrowed},
	{ID: "compat.variable-now-required", Tier: "candidate", Class: ClassConfig, Scope: ScopeTouchedBlock, Kinds: compatKinds, NeedsBase: true, Check: checkVariableNowRequired},
	{ID: "compat.variable-removed", Tier: "candidate", Class: ClassConfig, Scope: ScopeTouchedBlock, Kinds: compatKinds, NeedsBase: true, Check: checkVariableRemoved},
}

var compatKinds = []string{AreaRoot, AreaSubmodule}

const (
	revHead      = "head"
	revMergeBase = "merge_base"
)

// compat.variable-removed: a variable or output at the base with none of the
// same name at the head. It has no head line, so its line is null and its
// evidence is the merge base line of the block.
func checkVariableRemoved(a *Area) Result {
	const id = "compat.variable-removed"
	var res Result
	for _, v := range firstVariables(a.Base) {
		if !a.Head.varNames[v.Name] {
			res.Candidates = append(res.Candidates, removedCandidate(a, id, v.Span, "var."+v.Name))
		}
	}
	seen := map[string]bool{}
	for _, o := range a.Base.Outputs {
		if !seen[o.Name] && !a.Head.outNames[o.Name] {
			res.Candidates = append(res.Candidates, removedCandidate(a, id, o.Span, "output."+o.Name))
		}
		seen[o.Name] = true
	}
	return res
}

// removedCandidate is a candidate for a block the head no longer has.
func removedCandidate(a *Area, id string, sp Span, address string) Candidate {
	e := newCompatEvidence(a)
	e.line(revMergeBase, sp.File, sp.Line)
	e.ev.Base = e.ptr(address)
	return e.candidate(id, sp.File, nil, address)
}

// compat.variable-now-required: a variable that loses its default, gains
// nullable = false, or is new with no default in a module that exists at the
// base.
func checkVariableNowRequired(a *Area) Result {
	const id = "compat.variable-now-required"
	base := variablesByName(a.Base)
	var res Result
	for _, v := range firstVariables(a.Head) {
		b := base[v.Name]
		e := newCompatEvidence(a)
		var line int
		switch {
		case b == nil:
			if v.Default != nil || !moduleAtBase(a) {
				continue
			}
			line = v.Line
			e.line(revHead, v.File, v.Line)
		case b.Default != nil && v.Default == nil:
			line = v.Line
			e.line(revMergeBase, b.File, argLine(b.Body, "default"))
			e.line(revHead, v.File, v.Line)
			e.ev.Base = e.ptr(b.Default.Text)
		case isFalse(v.Nullable) && !isFalse(b.Nullable):
			line = argLine(v.Body, "nullable")
			if b.Nullable != nil {
				e.line(revMergeBase, b.File, argLine(b.Body, "nullable"))
				e.ev.Base = e.ptr(b.Nullable.Text)
			}
			e.line(revHead, v.File, line)
			e.ev.Head = e.ptr(v.Nullable.Text)
		default:
			continue
		}
		res.Candidates = append(res.Candidates, e.candidate(id, v.File, &line, "var."+v.Name))
	}
	return res
}

// compat.default-changed: a variable default whose tokens differ, an
// optional() attribute default that changed or was added, an optional()
// default the head type no longer carries, a new variable with a non-null
// default in a module that exists at the base, when configuration the base
// already had reads it at the head (see varsReadByBaseConfig), and a data
// block the head no longer has, which the rule text sends here when it fed a
// value the caller sees. A default the base did not have was required
// before, so adding one changes nothing for an existing caller.
func checkDefaultChanged(a *Area) Result {
	const id = "compat.default-changed"
	base := variablesByName(a.Base)
	var res Result
	seen := map[string]int{}
	// add keeps one candidate per subject, the dedup key. A second reason
	// for the same subject adds its source lines to the first.
	add := func(c Candidate) {
		i, ok := seen[c.Subject]
		if !ok {
			seen[c.Subject] = len(res.Candidates)
			res.Candidates = append(res.Candidates, c)
			return
		}
		k := &res.Candidates[i]
		k.EvidenceComplete = k.EvidenceComplete && c.EvidenceComplete
		for _, sl := range c.Evidence.SourceLines {
			if len(k.Evidence.SourceLines) == maxEvidenceLines {
				k.EvidenceComplete = false
				break
			}
			k.Evidence.SourceLines = append(k.Evidence.SourceLines, sl)
		}
	}
	var readByBase *varReads
	for _, v := range firstVariables(a.Head) {
		b := base[v.Name]
		if b == nil {
			// A new input with a default changes what an existing caller, who
			// sets nothing, gets where configuration the caller already had
			// reads it. A new argument or block behind it is a feature the
			// caller had not, which the review judges from the diff.
			if v.Default != nil && moduleAtBase(a) {
				if readByBase == nil {
					readByBase = varsReadByBaseConfig(a)
				}
				// A validation of a new variable can reject its own default,
				// null included, or read something else, so it fails a plan
				// for a caller who sets nothing.
				nonNull := strings.TrimSpace(v.Default.Text) != "null"
				if (!nonNull || !readByBase.has(v.Name)) && len(v.Validations) == 0 {
					continue
				}
				e := newCompatEvidence(a)
				line := argLine(v.Body, "default")
				e.line(revHead, v.File, line)
				e.ev.Head = e.ptr(v.Default.Text)
				add(e.candidate(id, v.File, &line, "var."+v.Name))
			}
			continue
		}
		if v.Default != nil && b.Default != nil && normExpr(v.Default.Text) != normExpr(b.Default.Text) {
			e := newCompatEvidence(a)
			line := argLine(v.Body, "default")
			e.line(revMergeBase, b.File, argLine(b.Body, "default"))
			e.line(revHead, v.File, line)
			e.ev.Base, e.ev.Head = e.ptr(b.Default.Text), e.ptr(v.Default.Text)
			add(e.candidate(id, v.File, &line, "var."+v.Name))
		}
		for _, d := range compareTypes(b, v).defaults {
			e := newCompatEvidence(a)
			e.line(revMergeBase, b.File, d.baseLine)
			// With no head type the default is gone with the type line, so
			// the candidate has no head line and is judged by its file.
			var line *int
			if d.head != nil {
				e.line(revHead, v.File, d.headLine)
				e.ev.Head = e.ptr(*d.head)
				line = &d.headLine
			}
			e.ev.Base = e.ptr(d.base)
			add(e.candidate(id, v.File, line, joinPath("var."+v.Name, d.path)))
		}
	}
	head := map[string]bool{}
	for _, r := range a.Head.Resources {
		head[r.Mode+"."+r.Type+"."+r.Name] = true
	}
	for _, r := range a.Base.Resources {
		if r.Mode == "data" && !head[r.Mode+"."+r.Type+"."+r.Name] {
			add(removedCandidate(a, id, r.Span, "data."+r.Type+"."+r.Name))
		}
	}
	return res
}

// varReads is what head expressions read: variables and locals by name, or
// every one of a kind when an expression reads the whole object (`var`,
// `{for k, v in var : ...}`), indexes it with a key it cannot resolve, or
// cannot be parsed, and the addresses of the resources, data sources and
// module calls it reads. Unknown reads count as reads of everything.
type varReads struct {
	vars, locals, refs map[string]bool
	allVars, allLocals bool
}

func newVarReads() *varReads {
	return &varReads{vars: map[string]bool{}, locals: map[string]bool{}, refs: map[string]bool{}}
}

func (r *varReads) add(e *Expr) {
	if e == nil {
		return
	}
	x := parseExpr(e)
	if x == nil {
		r.allVars, r.allLocals = true, true
		return
	}
	for _, t := range x.Variables() {
		names, all := r.vars, &r.allVars
		switch t.RootName() {
		case "var":
		case "local":
			names, all = r.locals, &r.allLocals
		default:
			if a := readAddr(t); a != "" {
				r.refs[a] = true
			}
			continue
		}
		if len(t) > 1 {
			switch s := t[1].(type) {
			case hcl.TraverseAttr:
				names[s.Name] = true
				continue
			case hcl.TraverseIndex:
				// A key in a traversal is a literal, so a string one is known.
				if s.Key.Type() == cty.String {
					names[s.Key.AsString()] = true
					continue
				}
			}
		}
		*all = true
	}
}

// readAddr is refAddress, with the three names of an ephemeral resource.
func readAddr(t hcl.Traversal) string {
	if t.RootName() != "ephemeral" {
		return refAddress(t)
	}
	if len(t) > 2 {
		x, ok1 := t[1].(hcl.TraverseAttr)
		y, ok2 := t[2].(hcl.TraverseAttr)
		if ok1 && ok2 {
			return "ephemeral." + x.Name + "." + y.Name
		}
	}
	return ""
}

func (r *varReads) has(name string) bool { return r.allVars || r.vars[name] }

// moduleID is what decides which module code a call runs: its source and
// its version constraint.
type moduleID struct {
	source, version string
	versionSet, ok  bool
}

func moduleIDOf(m ModuleCall) moduleID {
	return moduleID{
		source: m.Source, version: m.Version, versionSet: m.VersionSet,
		ok: m.Source != "" && (!m.VersionSet || m.VersionLiteral),
	}
}

// sameModule reports whether two calls run the same module code as far as
// the engine can tell: a source and version it can read at both, and equal.
func sameModule(b, h moduleID) bool { return b == h && b.ok }

func mergeBodies(bs []Body) Body {
	var m Body
	for _, b := range bs {
		m.Attributes = append(m.Attributes, b.Attributes...)
		m.Blocks = append(m.Blocks, b.Blocks...)
	}
	return m
}

// varsReadByBaseConfig finds what head configuration that exists at the
// base reads, directly or through head locals and new blocks: count and
// for_each of a resource, data or module block the base has, the arguments
// of such a block, of another block type or of an output that the base
// block also has, every argument and nested block of a provider the base
// has, and the nested blocks of such blocks, and the validation of a
// variable the base has. A block is found at the base by its address or by
// any address a chain of moved blocks leads back to, and compared with each.
// A resource, data source or module call the base lacks that such an
// argument reads counts with every argument it has, and so on from there.
// A module call at both revisions whose source or version differs, or is
// not a literal at either, counts with every argument too.
// Where the pairing of nested blocks is ambiguous, every argument counts.
func varsReadByBaseConfig(a *Area) *varReads {
	r := newVarReads()
	// Every block of a key is kept: Terraform rejects most duplicates, but
	// whatever the order, a head block compares with each base block of its
	// key, and a new block or a local declared twice counts every one.
	bodies := map[string][]Body{}
	addr := func(mode, typ, name string) string {
		if mode == "data" {
			return "data." + typ + "." + name
		}
		return typ + "." + name
	}
	for _, x := range a.Base.Resources {
		k := addr(x.Mode, x.Type, x.Name)
		bodies[k] = append(bodies[k], x.Body)
	}
	for _, m := range a.Base.ModuleCalls {
		bodies["module."+m.Name] = append(bodies["module."+m.Name], m.Body)
	}
	for _, p := range a.Base.Providers {
		k := "provider." + p.Name + "." + p.Alias
		bodies[k] = append(bodies[k], p.Body)
	}
	for _, o := range a.Base.Outputs {
		bodies["output."+o.Name] = append(bodies["output."+o.Name], o.Body)
	}
	baseOther := map[string][]Body{}
	for _, o := range a.Base.Other {
		k := o.Type + "." + strings.Join(o.Labels, ".")
		baseOther[k] = append(baseOther[k], o.Body)
	}
	from := map[string][]string{}
	type move struct {
		from, to string
		deep     bool
	}
	var moves []move
	malformedMove := false
	// A moved module call pairs its arguments by name across two modules'
	// inputs unless both calls have the same literal source and version.
	// A label declared twice names no one module code, so it pairs with
	// nothing whatever the block order.
	addMod := func(ids map[string]moduleID, m ModuleCall) {
		k := "module." + m.Name
		if _, dup := ids[k]; dup {
			ids[k] = moduleID{}
			return
		}
		ids[k] = moduleIDOf(m)
	}
	baseMod, headMod := map[string]moduleID{}, map[string]moduleID{}
	for _, m := range a.Base.ModuleCalls {
		addMod(baseMod, m)
	}
	for _, m := range a.Head.ModuleCalls {
		addMod(headMod, m)
	}
	for _, m := range a.Head.Moved {
		f, fd := movedKey(m.FromAddr)
		t, td := movedKey(m.ToAddr)
		if f == "" || t == "" {
			// An address the engine cannot read names no block, so any
			// block can be the one that moved: count every argument.
			malformedMove = true
			continue
		}
		from[t] = append(from[t], f)
		deep := fd || td || blockType(f) != blockType(t)
		if blockType(f) == "module" && !sameModule(baseMod[f], headMod[t]) {
			deep = true
		}
		moves = append(moves, move{f, t, deep})
	}
	// bases are the base bodies a head block can stand for: the one at its
	// own address and one at each address a chain of moved blocks reaches.
	bases := func(key string) []Body {
		var out []Body
		seen := map[string]bool{}
		for queue := []string{key}; len(queue) > 0; queue = queue[1:] {
			k := queue[0]
			if seen[k] {
				continue
			}
			seen[k] = true
			out = append(out, bodies[k]...)
			queue = append(queue, from[k]...)
		}
		return out
	}
	// A move across a module boundary or between resource types says
	// nothing about which argument of the block it lands in came from the
	// base, so every argument of that block counts, and so does every block
	// a chain of moves leads on to from it.
	countAll := map[string]bool{}
	for _, mv := range moves {
		if mv.deep {
			countAll[mv.to] = true
		}
	}
	// A module call that keeps its name but whose source or version changes,
	// or is not a literal, calls other module code: its arguments pair by
	// name across two modules' inputs, so every one counts, as across a move.
	for key, h := range headMod {
		if b, ok := baseMod[key]; ok && !sameModule(b, h) {
			countAll[key] = true
		}
	}
	for changed := true; changed; {
		changed = false
		for _, mv := range moves {
			if countAll[mv.from] && !countAll[mv.to] {
				countAll[mv.to], changed = true, true
			}
		}
	}
	newBlocks := map[string][]Body{} // head blocks with no base block
	shared := func(key string, h Body, top, all bool) {
		bs := bases(key)
		if len(bs) == 0 {
			newBlocks[key] = append(newBlocks[key], h)
			if malformedMove {
				// The unreadable move may have landed here: count it all.
				for _, e := range sharedArgs(Body{}, h, true, true) {
					r.add(e)
				}
			}
		}
		for _, b := range bs {
			for _, e := range sharedArgs(b, h, top, all) {
				r.add(e)
			}
		}
	}
	for _, x := range a.Head.Resources {
		shared(addr(x.Mode, x.Type, x.Name), x.Body, true, malformedMove || countAll[addr(x.Mode, x.Type, x.Name)])
	}
	for _, m := range a.Head.ModuleCalls {
		shared("module."+m.Name, m.Body, true, malformedMove || countAll["module."+m.Name])
	}
	for _, p := range a.Head.Providers {
		key := "provider." + p.Name + "." + p.Alias
		shared(key, p.Body, true, true)
		// A new provider is read by name.alias, with the alias unquoted;
		// one with no alias is the default of every resource of its type.
		if len(newBlocks[key]) > 0 {
			if p.Alias == "" {
				for _, e := range sharedArgs(Body{}, p.Body, true, true) {
					r.add(e)
				}
			} else {
				ref := p.Name + "." + strings.Trim(p.Alias, `"`)
				newBlocks[ref] = append(newBlocks[ref], p.Body)
			}
		}
	}
	for _, o := range a.Head.Outputs {
		shared("output."+o.Name, o.Body, false, false)
	}
	// Blocks with no label to pair them by (import) are read as one body per
	// type and labels; when the counts differ every argument counts.
	headOther := map[string]int{}
	for _, o := range a.Head.Other {
		headOther[o.Type+"."+strings.Join(o.Labels, ".")]++
	}
	for _, o := range a.Head.Other {
		k := o.Type + "." + strings.Join(o.Labels, ".")
		if len(baseOther[k]) == 0 {
			newBlocks[k] = append(newBlocks[k], o.Body) // only an ephemeral block can be named by a read
			continue
		}
		for _, e := range sharedArgs(mergeBodies(baseOther[k]), o.Body, true, len(baseOther[k]) != headOther[k]) {
			r.add(e)
		}
	}
	baseVars := variablesByName(a.Base)
	for i := range a.Head.Variables {
		// A validation can fail a plan, so every one of an existing variable
		// counts, an added one too, in every declaration of its name.
		if v := &a.Head.Variables[i]; baseVars[v.Name] != nil {
			for _, val := range v.Validations {
				r.add(val.Condition)
				r.add(val.ErrorMessage)
			}
		}
	}
	locals := map[string][]*Expr{}
	for i := range a.Head.Locals {
		l := &a.Head.Locals[i]
		locals[l.Name] = append(locals[l.Name], &l.Value)
	}
	done := map[string]bool{}
	for changed := true; changed; {
		changed = false
		for name, es := range locals {
			if !done["local."+name] && (r.allLocals || r.locals[name]) {
				done["local."+name], changed = true, true
				for _, e := range es {
					r.add(e)
				}
			}
		}
		for key, h := range newBlocks {
			if !done[key] && r.refs[key] {
				done[key], changed = true, true
				for _, e := range sharedArgs(Body{}, mergeBodies(h), true, true) {
					r.add(e)
				}
			}
		}
	}
	return r
}

// sharedArgs are the head arguments of a block that the base block also
// has, with count and for_each at the top level whether the base has them
// or not, and those of nested blocks of a type the base block has, static
// and dynamic together. The base blocks of one type are read as one body, so
// a reorder or an insertion cannot move a read onto a block that is new.
// When a type has a different number of blocks at the two revisions, or all
// is set, every argument and for_each of those blocks counts. So do the
// blocks that can fail a plan or hold a replacement trigger, whether the
// base had them or not: precondition, postcondition, assert and lifecycle.
func sharedArgs(base, head Body, top, all bool) []*Expr {
	return sharedArgsOf([]Body{base}, []Body{head}, top, all)
}

// sharedArgsOf is sharedArgs over the bodies of every block of one type at
// each revision. A nested type counts every argument when the number of its
// blocks per parent block differs between the revisions, as a sorted list,
// so blocks moved from one parent to another never pair by their union, and
// when one head block has another number of them than all base blocks.
func sharedArgsOf(bases, heads []Body, top, all bool) []*Expr {
	var out []*Expr
	base := mergeBodies(bases)
	bt := nestedByType(base)
	for _, hd := range heads {
		for i := range hd.Attributes {
			h := &hd.Attributes[i]
			// An argument that was blocks at the base is the same setting.
			if all || (top && instanceArgs[h.Name]) || bodyAttr(base, h.Name) != nil || len(bt[h.Name]) > 0 {
				out = append(out, &h.Value)
			}
		}
	}
	ht := nestedByType(mergeBodies(heads))
	bc, hc := countsByType(bases), countsByType(heads)
	for _, t := range slices.Sorted(maps.Keys(ht)) {
		hb := ht[t]
		known := len(bt[t]) > 0
		// Blocks of a type the base wrote as an argument are the same setting.
		ambiguous := all || failingBlocks[t] || bodyAttr(base, t) != nil ||
			(known && (oneDiffers(hc[t], len(bt[t])) || !sameProfile(bc[t], hc[t], len(bases), len(heads))))
		if !known && !ambiguous {
			continue
		}
		var bb, hbb []Body
		for _, b := range bt[t] {
			bb = append(bb, b.Body)
		}
		for _, h := range hb {
			if h.ForEach != nil {
				out = append(out, h.ForEach)
			}
			hbb = append(hbb, h.Body)
		}
		out = append(out, sharedArgsOf(bb, hbb, false, ambiguous)...)
	}
	return out
}

// nestedByType are the nested blocks of a body by type.
func nestedByType(b Body) map[string][]*NestedBlock {
	m := map[string][]*NestedBlock{}
	for i := range b.Blocks {
		m[b.Blocks[i].Type] = append(m[b.Blocks[i].Type], &b.Blocks[i])
	}
	return m
}

// countsByType is, for each nested block type, the number of its blocks in
// each body that has one, read in one pass over the bodies.
func countsByType(bodies []Body) map[string][]int {
	out := map[string][]int{}
	for _, b := range bodies {
		n := map[string]int{}
		for i := range b.Blocks {
			n[b.Blocks[i].Type]++
		}
		for t, c := range n {
			out[t] = append(out[t], c)
		}
	}
	return out
}

// oneDiffers reports a body that has blocks of a type, but not want of them.
func oneDiffers(counts []int, want int) bool {
	return slices.ContainsFunc(counts, func(c int) bool { return c != want })
}

// sameProfile reports whether two sets of bodies hold the same numbers of
// blocks per body, in any order, the bodies with none counting 0. Sets of
// another size differ.
func sameProfile(x, y []int, xBodies, yBodies int) bool {
	return slices.Equal(countProfile(x, xBodies), countProfile(y, yBodies))
}

func countProfile(counts []int, bodies int) []int {
	out := append(make([]int, bodies-len(counts)), counts...)
	slices.Sort(out)
	return out
}

// blockType is the resource type of a moved key, or "module" for a module
// call.
func blockType(key string) string {
	t, _, _ := strings.Cut(key, ".")
	return t
}

// movedKey is the top-level block an address of a moved block names,
// module.<name> or <type>.<name> with any index left out, and whether the
// address goes deeper than that block, as module.m.aws_r.x does.
func movedKey(addr string) (key string, deep bool) {
	var segs []string
	var cur strings.Builder
	depth, quoted := 0, false
	for i := 0; i < len(addr); i++ {
		c := addr[i]
		switch {
		case quoted:
			switch c {
			case '\\':
				i++
			case '"':
				quoted = false
			}
		case c == '"':
			quoted = true
		case c == '[':
			depth++
		case c == ']':
			depth--
		case c == '.' && depth == 0:
			segs = append(segs, cur.String())
			cur.Reset()
		case depth == 0:
			cur.WriteByte(c)
		}
	}
	segs = append(segs, cur.String())
	if len(segs) < 2 {
		return "", true
	}
	return segs[0] + "." + segs[1], len(segs) > 2
}

// instanceArgs are the arguments of a resource, data or module block, or an
// import, that change which instances exist or which provider they use: they
// count whether the base block had them or not.
var instanceArgs = map[string]bool{"count": true, "for_each": true, "provider": true, "providers": true}

// failingBlocks are the nested block types that can fail a plan or name what
// replaces a resource: a new one in an existing block can break an existing
// caller whose input is left at the new default.
var failingBlocks = map[string]bool{"precondition": true, "postcondition": true, "assert": true, "lifecycle": true}

// compat.type-narrowed: a type that compareTypes cannot show accepts every
// value the base type accepted, or a validation whose condition the base did
// not have. One candidate per variable, since the subject is the dedup key:
// the type when it changed, otherwise the first new validation, with every
// new condition among its source lines.
func checkTypeNarrowed(a *Area) Result {
	const id = "compat.type-narrowed"
	base := variablesByName(a.Base)
	var res Result
	for _, v := range firstVariables(a.Head) {
		b := base[v.Name]
		if b == nil {
			continue
		}
		narrowed := compareTypes(b, v).narrowed
		added := newValidations(b, v)
		if !narrowed && len(added) == 0 {
			continue
		}
		e := newCompatEvidence(a)
		var line int
		if narrowed {
			line = argLine(v.Body, "type")
			if line == 0 {
				line = v.Line
			}
			if b.HasType {
				e.line(revMergeBase, b.File, argLine(b.Body, "type"))
				e.ev.Base = e.ptr(b.TypeText)
			}
			e.line(revHead, v.File, line)
			e.ev.Head = e.ptr(v.TypeText)
		} else {
			first := added[0]
			line = first.head.Range.Start.Line
			if first.base != nil {
				e.ev.Base = e.ptr(first.base.Condition.Text)
			}
			e.ev.Head = e.ptr(first.head.Condition.Text)
		}
		for _, nv := range added {
			if nv.base != nil {
				e.line(revMergeBase, b.File, nv.base.Condition.Range.Start.Line)
			}
			e.line(revHead, v.File, nv.head.Condition.Range.Start.Line)
		}
		res.Candidates = append(res.Candidates, e.candidate(id, v.File, &line, "var."+v.Name))
	}
	return res
}

// validationPair is a head validation whose condition the base does not
// have, or that reads more than the variable itself, with the base validation at the same position when its condition is
// gone from the head, as the one it most likely tightens.
type validationPair struct{ base, head *Validation }

func newValidations(b, v *Variable) []validationPair {
	conds := func(vs []Validation) map[string]bool {
		m := map[string]bool{}
		for _, vd := range vs {
			if vd.Condition != nil {
				m[normExpr(vd.Condition.Text)] = true
			}
		}
		return m
	}
	baseConds, headConds := conds(b.Validations), conds(v.Validations)
	var out []validationPair
	for i := range v.Validations {
		h := &v.Validations[i]
		// A validation with no condition is one Terraform refuses. One that
		// reads anything but the variable itself, as Terraform 1.9 allows,
		// can reject more with the same text, so text alone cannot clear it.
		if h.Condition == nil || (baseConds[normExpr(h.Condition.Text)] && readsOnlySelf(h.Condition.Text, v.Name)) {
			continue
		}
		p := validationPair{head: h}
		if i < len(b.Validations) {
			if bv := &b.Validations[i]; bv.Condition != nil && !headConds[normExpr(bv.Condition.Text)] {
				p.base = bv
			}
		}
		out = append(out, p)
	}
	return out
}

// readsOnlySelf reports whether an expression reads no value but var.<name>.
// Text that does not parse reads something unknown.
func readsOnlySelf(text, name string) bool {
	x, diags := hclsyntax.ParseExpression([]byte(text), "", hcl.InitialPos)
	if diags.HasErrors() {
		return false
	}
	for _, tr := range x.Variables() {
		if tr.RootName() != "var" || len(tr) < 2 {
			return false
		}
		if at, ok := tr[1].(hcl.TraverseAttr); !ok || at.Name != name {
			return false
		}
	}
	return true
}

// typeDiff is what compareTypes found: whether the head type may reject a
// value the base type accepted, and the optional() defaults that changed.
type typeDiff struct {
	narrowed bool
	defaults []typeDefault
}

// typeDefault is one optional() default that changed, was added or was
// dropped: the type expression at path in each revision, with its absolute
// line. head is nil when the head has no type.
type typeDefault struct {
	path               string
	base               string
	head               *string
	baseLine, headLine int
}

// compareTypes compares the type constraints of a variable at the base and
// at the head. A missing type is `any`. It is not narrowed only when it can
// show that every value the base type accepted is accepted at the head: the
// same tokens, `any` at the head outside a collection, the same constructor
// with element types that are not narrowed, or an object whose attributes
// are kept or made optional(). Anything else, an added or removed object
// attribute included, is narrowed, for the confirmer to judge.
func compareTypes(b, v *Variable) typeDiff {
	w := typeWalk{}
	bx, ok1 := w.parse(b, &w.bSrc, &w.bLine)
	hx, ok2 := w.parse(v, &w.hSrc, &w.hLine)
	if !ok1 || !ok2 {
		return typeDiff{narrowed: true}
	}
	w.cmp("", bx, hx, false)
	return w.diff
}

type typeWalk struct {
	bSrc, hSrc   string
	bLine, hLine int // absolute line of the first line of each type
	diff         typeDiff
}

// parse reads a variable's type again from its text. A nil expression with
// ok is a variable with no type.
func (w *typeWalk) parse(v *Variable, src *string, line *int) (hclsyntax.Expression, bool) {
	if !v.HasType {
		return nil, true
	}
	x, diags := hclsyntax.ParseExpression([]byte(v.TypeText), "", hcl.InitialPos)
	if diags.HasErrors() {
		return nil, false
	}
	*src, *line = v.TypeText, v.Body.Arg("type").Range.Start.Line
	return x, true
}

func isAny(x hclsyntax.Expression) bool { return x == nil || hcl.ExprAsKeyword(x) == "any" }

func sliceText(src string, x hclsyntax.Expression) string {
	if x == nil {
		return "any"
	}
	r := x.Range()
	return src[r.Start.Byte:r.End.Byte]
}

// cmp compares the types at path. inColl is set below a list, set or map,
// whose elements must share one type, so `any` there rejects mixed elements
// the base accepted and is not a widening.
func (w *typeWalk) cmp(path string, b, h hclsyntax.Expression, inColl bool) {
	if normExpr(sliceText(w.bSrc, b)) == normExpr(sliceText(w.hSrc, h)) {
		return
	}
	if isAny(h) && !inColl {
		w.dropped(path, b, h)
		return
	}
	bc, ok1 := b.(*hclsyntax.FunctionCallExpr)
	hc, ok2 := h.(*hclsyntax.FunctionCallExpr)
	if ok1 && ok2 && bc.Name == hc.Name && len(bc.Args) == 1 && len(hc.Args) == 1 {
		switch bc.Name {
		case "list", "set", "map":
			w.cmp(path, bc.Args[0], hc.Args[0], true)
			return
		case "tuple":
			bt, ok1 := bc.Args[0].(*hclsyntax.TupleConsExpr)
			ht, ok2 := hc.Args[0].(*hclsyntax.TupleConsExpr)
			if ok1 && ok2 && len(bt.Exprs) == len(ht.Exprs) {
				for i := range bt.Exprs {
					w.cmp(path, bt.Exprs[i], ht.Exprs[i], inColl)
				}
				return
			}
		case "object":
			bo, ok1 := bc.Args[0].(*hclsyntax.ObjectConsExpr)
			ho, ok2 := hc.Args[0].(*hclsyntax.ObjectConsExpr)
			if ok1 && ok2 {
				w.object(path, bo, ho, inColl)
				return
			}
		}
	}
	w.diff.narrowed = true
	w.dropped(path, b, h)
}

// object compares the attributes of two object types.
func (w *typeWalk) object(path string, b, h *hclsyntax.ObjectConsExpr, inColl bool) {
	base := map[string]hclsyntax.ObjectConsItem{}
	for _, it := range b.Items {
		if k := keyName(it.KeyExpr); k != "" {
			if _, ok := base[k]; !ok {
				base[k] = it
			}
		}
	}
	kept := map[string]bool{}
	for _, it := range h.Items {
		k := keyName(it.KeyExpr)
		bi, ok := base[k]
		hOpt, hInner, hDef := unwrapOptional(it.ValueExpr)
		switch {
		case k == "":
			w.diff.narrowed = true
			continue
		case !ok:
			// The base dropped this attribute from a value it was given; the
			// head converts it, which can reject a value the base accepted.
			w.diff.narrowed = true
			continue
		}
		kept[k] = true
		bOpt, bInner, bDef := unwrapOptional(bi.ValueExpr)
		p := k
		if path != "" {
			p = path + "." + k
		}
		if bOpt && !hOpt {
			w.diff.narrowed = true
		}
		w.cmp(p, bInner, hInner, inColl)
		if bOpt && hOpt && defaultTokens(w.bSrc, bDef) != defaultTokens(w.hSrc, hDef) {
			ht := sliceText(w.hSrc, it.ValueExpr)
			w.diff.defaults = append(w.diff.defaults, typeDefault{path: p,
				base: sliceText(w.bSrc, bi.ValueExpr), head: &ht,
				baseLine: w.bLine + bi.KeyExpr.Range().Start.Line - 1, headLine: w.hLine + it.KeyExpr.Range().Start.Line - 1})
		}
	}
	for k := range base {
		if !kept[k] {
			w.diff.narrowed = true
		}
	}
}

// dropped records a base type with an optional() default that the head does
// not keep in a form compareTypes can pair, since a caller that left the
// attribute out may now get another value.
func (w *typeWalk) dropped(path string, b, h hclsyntax.Expression) {
	if b == nil || !hasOptionalDefault(b) {
		return
	}
	d := typeDefault{path: path, base: sliceText(w.bSrc, b), baseLine: w.bLine + b.Range().Start.Line - 1}
	if h != nil {
		ht := sliceText(w.hSrc, h)
		d.head, d.headLine = &ht, w.hLine+h.Range().Start.Line-1
	}
	w.diff.defaults = append(w.diff.defaults, d)
}

func hasOptionalDefault(x hclsyntax.Expression) bool {
	found := false
	_ = hclsyntax.VisitAll(x, func(n hclsyntax.Node) hcl.Diagnostics {
		if c, ok := n.(*hclsyntax.FunctionCallExpr); ok && c.Name == "optional" && len(c.Args) == 2 {
			found = true
		}
		return nil
	})
	return found
}

// unwrapOptional returns whether a type is optional(), its inner type and
// its default, nil when it has none.
func unwrapOptional(x hclsyntax.Expression) (optional bool, inner, def hclsyntax.Expression) {
	c, ok := x.(*hclsyntax.FunctionCallExpr)
	if !ok || c.Name != "optional" || len(c.Args) == 0 || len(c.Args) > 2 {
		return false, x, nil
	}
	if len(c.Args) == 2 {
		def = c.Args[1]
	}
	return true, c.Args[0], def
}

// defaultTokens is an optional() default in token form, "" when it has none.
func defaultTokens(src string, x hclsyntax.Expression) string {
	if x == nil {
		return ""
	}
	return normExpr(sliceText(src, x))
}

func joinPath(address, path string) string {
	if path == "" {
		return address
	}
	return address + "." + path
}

// firstVariables returns the variables of a module, the first of each name.
func firstVariables(m *Module) []*Variable {
	seen := map[string]bool{}
	var out []*Variable
	for i := range m.Variables {
		if v := &m.Variables[i]; !seen[v.Name] {
			seen[v.Name] = true
			out = append(out, v)
		}
	}
	return out
}

func variablesByName(m *Module) map[string]*Variable {
	out := map[string]*Variable{}
	for _, v := range firstVariables(m) {
		out[v.Name] = v
	}
	return out
}

// moduleAtBase reports whether the area has a module at the base: a
// directory with at least one block. An empty base is a new module, which
// no caller uses yet.
func moduleAtBase(a *Area) bool { return a.BaseExists && len(topBlocks(a.Base)) > 0 }

// argLine is the line an argument of a body starts on, or 0 without it.
func argLine(b Body, name string) int {
	for _, at := range b.Attributes {
		if at.Name == name {
			return at.Range.Start.Line
		}
	}
	return 0
}

// isFalse reports whether an argument is the literal false.
func isFalse(e *Expr) bool { return e != nil && strings.TrimSpace(e.Text) == "false" }

// compatEvidence builds one candidate's evidence under the caps. A cap that
// bites clears complete.
type compatEvidence struct {
	a        *Area
	ev       Evidence
	complete bool
}

func newCompatEvidence(a *Area) *compatEvidence {
	return &compatEvidence{a: a, ev: Evidence{SourceLines: []SourceLine{}, SchemaFacts: []SchemaFact{}}, complete: true}
}

func (e *compatEvidence) cap(s string) string {
	if len(s) > maxEvidenceText {
		e.complete = false
		return s[:maxEvidenceText]
	}
	return s
}

func (e *compatEvidence) ptr(s string) *string {
	c := e.cap(s)
	return &c
}

// line adds line n of a file of the area, as read at the revision.
func (e *compatEvidence) line(rev, file string, n int) {
	if len(e.ev.SourceLines) == maxEvidenceLines {
		e.complete = false
		return
	}
	t := e.a.headTree
	if rev == revMergeBase {
		t = e.a.baseTree
	}
	f := areaFile(e.a.Path, file)
	e.ev.SourceLines = append(e.ev.SourceLines, SourceLine{Revision: rev, File: f, Line: n, Text: e.cap(ifaceTreeLine(t, f, n))})
}

func (e *compatEvidence) candidate(rule, file string, line *int, address string) Candidate {
	subj := subject(e.a.Path, e.cap(address))
	return Candidate{RuleID: rule, File: areaFile(e.a.Path, file), Line: line, Subject: subj,
		Evidence: e.ev, EvidenceComplete: e.complete}
}

// ifaceTreeLine returns line n of a file of a tree, without its line ending, or
// "" when the tree has no such file or line.
func ifaceTreeLine(t *Tree, file string, n int) string {
	if t == nil {
		return ""
	}
	e := t.Entries[file]
	if e == nil || e.Kind != KindFile || e.opaque {
		return ""
	}
	ls := splitLines(e.Data)
	if n < 1 || n > len(ls) {
		return ""
	}
	return strings.TrimRight(string(ls[n-1]), "\r")
}
