package facts

import (
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
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
			if v.Default != nil || !a.BaseExists {
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
// default the head type no longer carries, and a data block the head no
// longer has, which the rule text sends here when it fed a value the caller
// sees. A default the base did not have was required before, so adding one
// changes nothing for an existing caller.
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
	for _, v := range firstVariables(a.Head) {
		b := base[v.Name]
		if b == nil {
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
// same tokens, `any` at the head, the same constructor with element types
// that are not narrowed, or an object whose attributes are kept, added as
// optional() or made optional(). Anything else, a removed object attribute
// included, is narrowed, for the confirmer to judge.
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
			// A new attribute narrows unless a caller may leave it out.
			w.diff.narrowed = w.diff.narrowed || !hOpt
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
	if e == nil || e.Kind != KindFile {
		return ""
	}
	ls := splitLines(e.Data)
	if n < 1 || n > len(ls) {
		return ""
	}
	return strings.TrimRight(string(ls[n-1]), "\r")
}
