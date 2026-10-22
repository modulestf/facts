package facts

import (
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/hashicorp/go-version"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"github.com/modulestf/facts/internal/schema"
)

// compatResRules is the compat_res rule family: the Check A rules about
// resource addresses, generated policy documents and version floors. Each
// is a candidate rule that needs the base. A change the detector cannot
// decide, such as a moved block with an expression key or a constraint it
// cannot parse, is a candidate for the confirmer, never clean.
var compatResRules = []Rule{
	{ID: "compat.generated-policy-narrowed", Tier: "candidate", Class: ClassConfig, Scope: ScopeTouchedBlock, Kinds: compatResKinds, NeedsBase: true, Check: checkPolicyNarrowed},
	{ID: "compat.provider-floor-raised", Tier: "candidate", Class: ClassConfig, Scope: ScopeTouchedExpression, Kinds: compatResKinds, NeedsBase: true, Check: checkProviderFloorRaised},
	{ID: "compat.resource-address-changed", Tier: "candidate", Class: ClassConfig, Scope: ScopeTouchedBlock, Kinds: compatResKinds, CrossArea: true, NeedsBase: true, Check: checkResourceAddressChanged},
	{ID: "compat.version-constraint-raised", Tier: "candidate", Class: ClassConfig, Scope: ScopeTouchedExpression, Kinds: compatResKinds, NeedsBase: true, Check: checkVersionConstraintRaised},
}

var compatResKinds = []string{AreaRoot, AreaSubmodule}

// Caps on one candidate's evidence. A cap that bites sets evidence_complete
// to false.
const (
	maxResSourceLines = 8
	maxResSchemaFacts = 64
)

// resEvidence builds one candidate's evidence under the caps.
type resEvidence struct {
	a        *Area
	ev       Evidence
	complete bool
}

func newResEvidence(a *Area) *resEvidence {
	return &resEvidence{a: a, ev: Evidence{SourceLines: []SourceLine{}, SchemaFacts: []SchemaFact{}}, complete: true}
}

func (e *resEvidence) text(s string) string {
	if len(s) > maxEvidenceText {
		e.complete = false
		return s[:maxEvidenceText]
	}
	return s
}

// line adds line n of an area file at a revision, "head" or "merge_base".
// A file that is not a regular file there adds nothing.
func (e *resEvidence) line(rev, file string, n int) {
	t := e.a.headTree
	if rev == "merge_base" {
		t = e.a.baseTree
	}
	full := areaFile(e.a.Path, file)
	if t == nil || n < 1 {
		return
	}
	en := t.Entries[full]
	if en == nil || en.Kind != KindFile {
		return
	}
	ls := splitLines(en.Data)
	if n > len(ls) {
		return
	}
	if len(e.ev.SourceLines) == maxResSourceLines {
		e.complete = false
		return
	}
	e.ev.SourceLines = append(e.ev.SourceLines, SourceLine{Revision: rev, File: full, Line: n, Text: e.text(strings.TrimSuffix(string(ls[n-1]), "\r"))})
}

func (e *resEvidence) values(base, head *string) {
	if base != nil {
		b := e.text(*base)
		e.ev.Base = &b
	}
	if head != nil {
		h := e.text(*head)
		e.ev.Head = &h
	}
}

// candidate is the candidate of a rule in the area. A nil line is a subject
// with no head line, judged by its file.
func (e *resEvidence) candidate(rule, file string, line int, address string) Candidate {
	c := Candidate{RuleID: rule, File: areaFile(e.a.Path, file), Subject: subject(e.a.Path, e.text(address)),
		Evidence: e.ev, EvidenceComplete: e.complete}
	if line > 0 {
		c.Line = &line
	}
	return c
}

// parseExprAt parses an expression's source text at its own position, so
// the ranges it returns are those of the file.
func parseExprAt(x Expr) (hclsyntax.Expression, bool) {
	e, diags := hclsyntax.ParseExpression([]byte(x.Text), x.Range.Filename, x.Range.Start)
	return e, !diags.HasErrors()
}

// literalValue is the value of an expression that needs no variable or
// function, or false when it has none or is not wholly known.
func literalValue(e hclsyntax.Expression) (cty.Value, bool) {
	v, diags := e.Value(nil)
	if diags.HasErrors() || !v.IsWhollyKnown() {
		return cty.NilVal, false
	}
	return v, true
}

func bodyAttr(b Body, name string) *Attribute {
	for i := range b.Attributes {
		if b.Attributes[i].Name == name {
			return &b.Attributes[i]
		}
	}
	return nil
}

// ---- compat.resource-address-changed ----

// resInstances are the managed resources of a module by address, the first
// block of each address.
func resInstances(m *Module) map[string]*Resource {
	out := map[string]*Resource{}
	if m == nil {
		return out
	}
	for i := range m.Resources {
		r := &m.Resources[i]
		if r.Mode != "resource" {
			continue // a data block has no address a moved block can name
		}
		addr := r.Type + "." + r.Name
		if out[addr] == nil {
			out[addr] = r
		}
	}
	return out
}

// repetition returns the repetition argument of a resource, "count",
// "for_each" or "", and its attribute.
func repetition(r *Resource) (string, *Attribute) {
	for _, m := range []string{"count", "for_each"} {
		if a := bodyAttr(r.Body, m); a != nil {
			return m, a
		}
	}
	return "", nil
}

// instanceKeys are the instance keys a resource can have, as address
// suffixes: "" with no repetition, "[0]" for count, `["a"]` for for_each.
// ok is false when the keys are not statically known: a reference, a
// function other than toset, or a value that is not a literal.
func instanceKeys(r *Resource) (keys []string, ok bool) {
	mode, a := repetition(r)
	if mode == "" {
		return []string{""}, true
	}
	e, ok := parseExprAt(a.Value)
	if !ok {
		return nil, false
	}
	set := map[string]bool{}
	if !collectKeys(mode, e, set) {
		return nil, false
	}
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys, true
}

// collectKeys adds the keys an expression can give. Both results of a
// conditional are possible, so both add their keys.
func collectKeys(mode string, e hclsyntax.Expression, set map[string]bool) bool {
	switch x := e.(type) {
	case *hclsyntax.ConditionalExpr:
		return collectKeys(mode, x.TrueResult, set) && collectKeys(mode, x.FalseResult, set)
	case *hclsyntax.ParenthesesExpr:
		return collectKeys(mode, x.Expression, set)
	case *hclsyntax.FunctionCallExpr:
		if mode == "for_each" && x.Name == "toset" && len(x.Args) == 1 && !x.ExpandFinal {
			return collectKeys(mode, x.Args[0], set)
		}
		return false
	}
	v, ok := literalValue(e)
	if !ok || v.IsNull() {
		return false
	}
	if mode == "count" {
		if v.Type() != cty.Number {
			return false
		}
		n, acc := v.AsBigFloat().Int64()
		if acc != 0 || n < 0 || n > 1024 { // shortcut: a larger literal count is left to the confirmer
			return false
		}
		for i := range n {
			set["["+strconv.FormatInt(i, 10)+"]"] = true
		}
		return true
	}
	t := v.Type()
	switch {
	case t.IsTupleType() || t.IsListType() || t.IsSetType():
		for it := v.ElementIterator(); it.Next(); {
			_, ev := it.Element()
			if ev.IsNull() || ev.Type() != cty.String {
				return false
			}
			set["["+strconv.Quote(ev.AsString())+"]"] = true
		}
		return true
	case t.IsObjectType() || t.IsMapType():
		for it := v.ElementIterator(); it.Next(); {
			k, _ := it.Element()
			set["["+strconv.Quote(k.AsString())+"]"] = true
		}
		return true
	}
	return false
}

// implicitKey is the key Terraform moves an instance to on its own when
// count is added or removed: no key and [0] are the same instance.
func implicitKey(k string, head []string) bool {
	switch k {
	case "":
		return slices.Contains(head, "[0]")
	case "[0]":
		return slices.Contains(head, "")
	}
	return false
}

// checkResourceAddressChanged: a managed resource address at the base whose
// instances are not all at the head under the same address and key, nor
// named by a static moved block. A move into another area, such as
// module.child.<address>, is read from the head's moved blocks directly,
// whether the change touched them or not. A removed block, a moved block
// with an expression key and keys that are not static leave the address to
// the confirmer: they are candidates, never clean. A resource of a
// submodule moved to the root stays a candidate in the submodule: callers
// of the submodule itself still lose it.
func checkResourceAddressChanged(a *Area) Result {
	if !baseHasFiles(a) {
		return Result{} // a module new in this change breaks no caller
	}
	const id = "compat.resource-address-changed"
	base, head := resInstances(a.Base), resInstances(a.Head)
	movedFrom := map[string]*Moved{}
	movedTo := map[string]bool{}
	for i := range a.Head.Moved {
		m := &a.Head.Moved[i]
		if m.FromAddr == "" || m.ToAddr == "" {
			continue // an expression key maps nothing the detector can name
		}
		if movedFrom[m.FromAddr] == nil {
			movedFrom[m.FromAddr] = m
		}
		movedTo[m.ToAddr] = true
	}
	removedBlock := map[string]*Removed{}
	for i := range a.Head.Removed {
		if r := &a.Head.Removed[i]; r.FromAddr != "" && removedBlock[r.FromAddr] == nil {
			removedBlock[r.FromAddr] = r
		}
	}
	// A head address the base lacks and no moved block names: the other
	// half of a rename, paired in order with the removals of its type.
	var added []*Resource
	for addr, r := range head {
		if base[addr] == nil && !movedTo[addr] {
			added = append(added, r)
		}
	}
	sort.Slice(added, func(i, j int) bool { return lessSpan(added[i].Span, added[j].Span) })

	var res Result
	addrs := make([]string, 0, len(base))
	for addr := range base {
		addrs = append(addrs, addr)
	}
	sort.Slice(addrs, func(i, j int) bool { return lessSpan(base[addrs[i]].Span, base[addrs[j]].Span) })
	for _, addr := range addrs {
		b, h := base[addr], head[addr]
		if h != nil && modeText(repetition(b)) == modeText(repetition(h)) {
			continue // the same address and the same repetition give the same instances
		}
		bKeys, bOK := instanceKeys(b)
		var hKeys []string
		hOK := true
		if h != nil {
			hKeys, hOK = instanceKeys(h)
		}
		lost := false // an instance with no place at the head, or keys unknown
		if movedFrom[addr] == nil {
			if !bOK || (h != nil && !hOK) {
				lost = true
			}
			for _, k := range bKeys {
				if h != nil && (slices.Contains(hKeys, k) || implicitKey(k, hKeys)) {
					continue
				}
				if movedFrom[addr+k] == nil {
					lost = true
				}
			}
		}
		if !lost {
			continue
		}
		res.Candidates = append(res.Candidates, addressCandidate(a, id, addr, b, h, removedBlock[addr], &added))
	}
	return res
}

// addressCandidate is the candidate for one base address. Its line is a
// head line the change made: the head repetition argument, the removed
// block, or the block that took its place in a rename. With none it has no
// line and names the base file.
func addressCandidate(a *Area, id, addr string, b, h *Resource, rm *Removed, added *[]*Resource) Candidate {
	ev := newResEvidence(a)
	bMode, bArg := repetition(b)
	ev.line("merge_base", b.File, b.Line)
	if bArg != nil {
		ev.line("merge_base", b.File, bArg.Range.Start.Line)
	}
	switch {
	case h != nil:
		hMode, hArg := repetition(h)
		ev.values(new(modeText(bMode, bArg)), new(modeText(hMode, hArg)))
		line := 0
		// A repetition argument on one head line that differs from the base
		// is on a changed line; any other edit has no such line.
		if hArg != nil && (bArg == nil || hArg.Value.Text != bArg.Value.Text) && hArg.Range.Start.Line == hArg.Range.End.Line {
			line = hArg.Range.Start.Line
			ev.line("head", h.File, line)
		}
		if line == 0 {
			return ev.candidate(id, b.File, 0, addr)
		}
		return ev.candidate(id, h.File, line, addr)
	case rm != nil:
		ev.line("head", rm.File, rm.Line)
		ev.values(new(addr), nil)
		return ev.candidate(id, rm.File, rm.Line, addr)
	}
	if i := slices.IndexFunc(*added, func(r *Resource) bool { return r.Type == b.Type }); i >= 0 {
		n := (*added)[i]
		*added = slices.Delete(*added, i, i+1)
		ev.line("head", n.File, n.Line)
		ev.values(new(addr), new(n.Type+"."+n.Name))
		return ev.candidate(id, n.File, n.Line, n.Type+"."+n.Name)
	}
	ev.values(new(addr), nil)
	return ev.candidate(id, b.File, 0, addr)
}

func modeText(mode string, a *Attribute) string {
	if a == nil {
		return "single"
	}
	return mode + " = " + a.Value.Text
}

func lessSpan(x, y Span) bool {
	if x.File != y.File {
		return x.File < y.File
	}
	return x.Line < y.Line
}

// ---- version floors ----

var constraintPart = regexp.MustCompile(`^(>=|~>|=|>|<=|<|!=)?\s*v?([0-9]+(\.[0-9]+){0,2}(-[0-9A-Za-z.-]+)?)$`)

var zeroVersion = version.Must(version.NewVersion("0.0.0"))

// constraintFloor is the lowest version a constraint allows: the highest of
// its >=, ~>, = and bare lower bounds, or 0.0.0 with none. ok is false for a
// constraint it cannot read: > or != may move the floor by an amount the
// text does not give.
func constraintFloor(c string) (floor *version.Version, ok bool) {
	floor = zeroVersion
	if strings.TrimSpace(c) == "" {
		return floor, true
	}
	for p := range strings.SplitSeq(c, ",") {
		m := constraintPart.FindStringSubmatch(strings.TrimSpace(p))
		if m == nil {
			return nil, false
		}
		switch m[1] {
		case "<", "<=":
			continue
		case ">", "!=":
			return nil, false
		}
		v, err := version.NewVersion(m[2])
		if err != nil {
			return nil, false
		}
		if v.GreaterThan(floor) {
			floor = v
		}
	}
	return floor, true
}

// constraint is one version constraint as written: its text, whether it is
// a literal string, and its attribute's file and lines.
type constraint struct {
	text    string
	literal bool
	file    string
	line    int
	oneLine bool
}

// floorOf is the floor of the constraints of one name taken together. With
// none the floor is 0.0.0.
func floorOf(cs []constraint) (*version.Version, bool) {
	floor := zeroVersion
	for _, c := range cs {
		if !c.literal {
			return nil, false
		}
		v, ok := constraintFloor(c.text)
		if !ok {
			return nil, false
		}
		if v.GreaterThan(floor) {
			floor = v
		}
	}
	return floor, true
}

func constraintText(cs []constraint) string {
	var ts []string
	for _, c := range cs {
		ts = append(ts, c.text)
	}
	return strings.Join(ts, ", ")
}

func exprConstraint(file string, a *Attribute) constraint {
	c := constraint{text: a.Value.Text, file: file, line: a.Range.Start.Line, oneLine: a.Range.Start.Line == a.Range.End.Line}
	if e, ok := parseExprAt(a.Value); ok {
		if v, ok := literalValue(e); ok && v.Type() == cty.String {
			c.text, c.literal = v.AsString(), true
		}
	}
	return c
}

// coreConstraints are the required_version arguments of a module.
func coreConstraints(m *Module) []constraint {
	var out []constraint
	for _, t := range m.Terraform {
		if a := bodyAttr(t.Body, "required_version"); a != nil {
			out = append(out, exprConstraint(t.File, a))
		}
	}
	return out
}

// providerConstraints are the version constraints of each required
// provider of a module, by local name. A name with no version has an empty
// list; the object and the legacy string form are both read.
func providerConstraints(m *Module) map[string][]constraint {
	out := map[string][]constraint{}
	for _, t := range m.Terraform {
		for _, nb := range t.Body.Blocks {
			if nb.Type != "required_providers" {
				continue
			}
			for i := range nb.Body.Attributes {
				a := &nb.Body.Attributes[i]
				cs := out[a.Name]
				if cs == nil {
					cs = []constraint{}
				}
				e, ok := parseExprAt(a.Value)
				obj, isObj := e.(*hclsyntax.ObjectConsExpr)
				switch {
				case !ok:
					cs = append(cs, constraint{text: a.Value.Text, file: t.File, line: a.Range.Start.Line})
				case isObj:
					for _, it := range obj.Items {
						if keyName(it.KeyExpr) != "version" {
							continue
						}
						r := hcl.RangeBetween(it.KeyExpr.Range(), it.ValueExpr.Range())
						ve := Expr{Text: exprSource(a.Value, it.ValueExpr.Range()), Range: it.ValueExpr.Range()}
						cs = append(cs, exprConstraint(t.File, &Attribute{Name: "version", Range: r, Value: ve}))
					}
				default:
					cs = append(cs, exprConstraint(t.File, a))
				}
				out[a.Name] = cs
			}
		}
	}
	return out
}

// exprSource is the source text of r, a range inside the expression x.
func exprSource(x Expr, r hcl.Range) string {
	s, e := r.Start.Byte-x.Range.Start.Byte, r.End.Byte-x.Range.Start.Byte
	if s < 0 || e > len(x.Text) || s > e {
		return ""
	}
	return x.Text[s:e]
}

// headLine is the line of the head constraint a candidate names: the first
// one-line constraint the base does not have, else none.
func headLine(base, head []constraint) (file string, line int) {
	for _, h := range head {
		if h.oneLine && !slices.ContainsFunc(base, func(b constraint) bool { return b.text == h.text }) {
			return h.file, h.line
		}
	}
	return "", 0
}

func floorEvidence(a *Area, base, head []constraint) *resEvidence {
	ev := newResEvidence(a)
	for _, b := range base {
		ev.line("merge_base", b.file, b.line)
	}
	for _, h := range head {
		ev.line("head", h.file, h.line)
	}
	ev.values(new(constraintText(base)), new(constraintText(head)))
	return ev
}

// floorMove compares the floors of two constraint lists: whether the head
// floor is above the base floor, whether it is in a later major version,
// and whether both could be read.
func floorMove(base, head []constraint) (raised, major, ok bool) {
	bf, bok := floorOf(base)
	hf, hok := floorOf(head)
	if !bok || !hok {
		return false, false, false
	}
	return hf.GreaterThan(bf), hf.Segments()[0] > bf.Segments()[0], true
}

// checkVersionConstraintRaised: the Terraform core floor raised, or a
// provider floor in a later major version than the base floor. A provider
// the base does not name is scope.provider's. A constraint it cannot read
// that the change edits is a candidate.
func checkVersionConstraintRaised(a *Area) Result {
	if !baseHasFiles(a) {
		return Result{} // a module new in this change breaks no caller
	}
	const id = "compat.version-constraint-raised"
	var res Result
	flag := func(subj string, base, head []constraint, hit func(raised, major bool) bool) {
		if constraintText(base) == constraintText(head) {
			return
		}
		raised, major, ok := floorMove(base, head)
		if ok && !hit(raised, major) {
			return
		}
		file, line := headLine(base, head)
		ev := floorEvidence(a, base, head)
		if line == 0 {
			file = firstFile(base, head)
		}
		res.Candidates = append(res.Candidates, ev.candidate(id, file, line, subj))
	}
	flag("required_version", coreConstraints(a.Base), coreConstraints(a.Head), func(raised, _ bool) bool { return raised })
	bp, hp := providerConstraints(a.Base), providerConstraints(a.Head)
	for _, name := range resSortedKeys(hp) {
		if b, ok := bp[name]; ok {
			flag("required_providers."+name, b, hp[name], func(raised, major bool) bool { return raised && major })
		}
	}
	return res
}

func firstFile(lists ...[]constraint) string {
	for _, l := range lists {
		if len(l) > 0 {
			return l[0].file
		}
	}
	return "versions.tf"
}

func resSortedKeys[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// checkProviderFloorRaised: a provider floor raised within one major
// version. With a raise, or a constraint edit it cannot read, the floors
// come from the schema handoff, which resolves them: the declared minimum
// against the old floor. Without the handoff's versions for that provider
// the rule is not_evaluated(schema_unavailable). The schema facts are the
// arguments and blocks the area's resource and data blocks of that
// provider set, looked up at the old floor.
func checkProviderFloorRaised(a *Area) Result {
	if !baseHasFiles(a) {
		return Result{} // a module new in this change breaks no caller
	}
	const id = "compat.provider-floor-raised"
	var res Result
	bp, hp := providerConstraints(a.Base), providerConstraints(a.Head)
	for _, name := range resSortedKeys(hp) {
		base, ok := bp[name]
		head := hp[name]
		if !ok || constraintText(base) == constraintText(head) {
			continue
		}
		raised, major, ok := floorMove(base, head)
		if ok && (!raised || major) {
			continue
		}
		r := a.Schema.Resolve(a.Path, name)
		if r.Outcome != schema.Present || r.Versions.OldFloor == nil {
			reason := "no schema handoff versions for provider " + name
			if r.Reason != "" && r.Reason != string(schema.Unavailable) {
				reason += ": " + r.Reason
			}
			return Result{NotEvaluated: reason, Code: ReasonSchemaUnavailable}
		}
		dm, err1 := version.NewVersion(r.Versions.DeclaredMinimum)
		of, err2 := version.NewVersion(*r.Versions.OldFloor)
		if err1 != nil || err2 != nil {
			return Result{NotEvaluated: "unreadable handoff versions for provider " + name, Code: ReasonSchemaUnavailable}
		}
		if !dm.GreaterThan(of) || dm.Segments()[0] != of.Segments()[0] {
			continue // no raise, or a major one, which is compat.version-constraint-raised
		}
		ev := floorEvidence(a, base, head)
		schemaFacts(a, ev, name, r.Source, *r.Versions.OldFloor)
		file, line := headLine(base, head)
		if line == 0 {
			file = firstFile(base, head)
		}
		res.Candidates = append(res.Candidates, ev.candidate(id, file, line, "required_providers."+name))
	}
	return res
}

// resourceProvider is the local provider name of a resource block: the
// provider argument's name, else the first word of its type.
func resourceProvider(r *Resource) string {
	if p := bodyAttr(r.Body, "provider"); p != nil {
		name, _, _ := strings.Cut(strings.TrimSpace(p.Value.Text), ".")
		return name
	}
	name, _, _ := strings.Cut(r.Type, "_")
	return name
}

func schemaFacts(a *Area, ev *resEvidence, local, source, ver string) {
	seen := map[string]bool{}
	for i := range a.Head.Resources {
		r := &a.Head.Resources[i]
		if resourceProvider(r) != local {
			continue
		}
		names := append(slices.Clone(r.Attributes), r.Blocks...)
		for _, n := range names {
			key := r.Mode + " " + r.Type + " " + n
			if seen[key] {
				continue
			}
			seen[key] = true
			if len(ev.ev.SchemaFacts) == maxResSchemaFacts {
				ev.complete = false
				return
			}
			out, f := a.Schema.Lookup(schema.Query{Source: source, Version: ver, DataSource: r.Mode == "data", Type: r.Type, Path: []string{n}})
			ev.ev.SchemaFacts = append(ev.ev.SchemaFacts, SchemaFact{ProviderSource: source, Version: ver, Type: r.Type, Path: n, State: factState(out, f)})
		}
	}
}

func factState(o schema.Outcome, f schema.Field) string {
	switch o {
	case schema.Present:
		if (f.Attribute != nil && f.Attribute.Deprecated) || (f.Attribute == nil && f.Block != nil && f.Block.Deprecated) {
			return "deprecated"
		}
		return "present"
	case schema.AbsentFromSchema:
		return "absent"
	}
	return "unknown"
}

// ---- compat.generated-policy-narrowed ----

const policyDocType = "aws_iam_policy_document"

// policyAttr reports an argument that holds a policy document the module
// produces, such as policy or assume_role_policy.
func policyAttr(name string) bool { return name == "policy" || strings.HasSuffix(name, "_policy") }

// widening are the policy document arguments whose literal list only
// widens an allow statement when it gains entries.
var widening = map[string]bool{"actions": true, "resources": true, "identifiers": true, "values": true}

type policyChange struct {
	line       int // 0: the line of the enclosing head block
	path       string
	base, head *string
}

// checkPolicyNarrowed: a policy document the module produces that may
// permit less at the head. An aws_iam_policy_document is compared argument
// by argument: an allow statement that only gains literal actions,
// resources, identifiers or condition values, or a new allow statement, is
// a widening; any other edit is a candidate. Any other policy argument,
// such as a jsonencode() document, is a candidate when its text changes. A
// local the policy text reads, directly or through other locals, is a
// candidate when the change edits it. A removed document data block is a
// candidate with no head line. Variable defaults that feed a document are
// compat.default-changed's.
func checkPolicyNarrowed(a *Area) Result {
	if !baseHasFiles(a) {
		return Result{} // a module new in this change breaks no caller
	}
	const id = "compat.generated-policy-narrowed"
	var res Result
	baseBlocks := map[string]*Resource{}
	for i := range a.Base.Resources {
		r := &a.Base.Resources[i]
		if k := resKey(r); baseBlocks[k] == nil {
			baseBlocks[k] = r
		}
	}
	headSeen := map[string]bool{}
	var policyText []string
	for i := range a.Head.Resources {
		h := &a.Head.Resources[i]
		k := resKey(h)
		if h.Mode == "data" && h.Type == policyDocType {
			policyText = append(policyText, bodyText(h.Body)...)
		} else {
			policyText = append(policyText, policyTexts(h.Body)...)
		}
		if headSeen[k] {
			continue
		}
		headSeen[k] = true
		b := baseBlocks[k]
		if b == nil {
			continue
		}
		var changes []policyChange
		if h.Mode == "data" && h.Type == policyDocType {
			docDiff(b.Body, h.Body, "", h.Line, true, &changes)
		} else {
			policyArgDiff(b.Body, h.Body, "", h.Line, &changes)
		}
		for _, c := range changes {
			ev := newResEvidence(a)
			line := c.line
			if line == 0 {
				line = h.Line
			}
			ev.line("head", h.File, line)
			ev.values(c.base, c.head)
			res.Candidates = append(res.Candidates, ev.candidate(id, h.File, line, k+c.path))
		}
	}
	for _, k := range resSortedKeys(baseBlocks) {
		b := baseBlocks[k]
		if b.Mode == "data" && b.Type == policyDocType && !headSeen[k] {
			ev := newResEvidence(a)
			ev.line("merge_base", b.File, b.Line)
			ev.values(new(k), nil)
			res.Candidates = append(res.Candidates, ev.candidate(id, b.File, 0, k))
		}
	}
	res.Candidates = append(res.Candidates, policyLocals(a, id, policyText)...)
	return res
}

func resKey(r *Resource) string {
	if r.Mode == "data" {
		return "data." + r.Type + "." + r.Name
	}
	return r.Type + "." + r.Name
}

var localRef = regexp.MustCompile(`\blocal\.([A-Za-z_][A-Za-z0-9_-]*)`)

// policyLocals are the candidates for the locals the policy text reads,
// directly or through other locals, that the change edits.
func policyLocals(a *Area, id string, texts []string) []Candidate {
	head := map[string]*Local{}
	for i := range a.Head.Locals {
		head[a.Head.Locals[i].Name] = &a.Head.Locals[i]
	}
	base := map[string]*Local{}
	for i := range a.Base.Locals {
		base[a.Base.Locals[i].Name] = &a.Base.Locals[i]
	}
	read := map[string]bool{}
	queue := texts
	for len(queue) > 0 {
		t := queue[0]
		queue = queue[1:]
		for _, m := range localRef.FindAllStringSubmatch(t, -1) {
			if n := m[1]; !read[n] {
				read[n] = true
				if l := head[n]; l != nil {
					queue = append(queue, l.Value.Text)
				}
			}
		}
	}
	var out []Candidate
	for _, n := range resSortedKeys(read) {
		h, b := head[n], base[n]
		if h == nil || b == nil || h.Value.Text == b.Value.Text {
			continue // a new local is read by policy text the change also edits
		}
		ev := newResEvidence(a)
		ev.line("merge_base", b.File, b.Line)
		ev.line("head", h.File, h.Line)
		ev.values(new(b.Value.Text), new(h.Value.Text))
		out = append(out, ev.candidate(id, h.File, h.Line, "local."+n))
	}
	return out
}

// bodyText is the text of every argument of a body, nested blocks included.
func bodyText(b Body) []string {
	var out []string
	for _, a := range b.Attributes {
		out = append(out, a.Value.Text)
	}
	for _, nb := range b.Blocks {
		if nb.ForEach != nil {
			out = append(out, nb.ForEach.Text)
		}
		out = append(out, bodyText(nb.Body)...)
	}
	return out
}

// policyTexts is the text of every policy argument of a body.
func policyTexts(b Body) []string {
	var out []string
	for _, a := range b.Attributes {
		if policyAttr(a.Name) {
			out = append(out, a.Value.Text)
		}
	}
	for _, nb := range b.Blocks {
		out = append(out, policyTexts(nb.Body)...)
	}
	return out
}

// staticBlocks groups the nested blocks of a body by type, static blocks
// with their index and dynamic blocks apart.
func staticBlocks(b Body) (static map[string][]*NestedBlock, dynamic map[string][]*NestedBlock) {
	static, dynamic = map[string][]*NestedBlock{}, map[string][]*NestedBlock{}
	for i := range b.Blocks {
		nb := &b.Blocks[i]
		if nb.Dynamic {
			dynamic[nb.Type] = append(dynamic[nb.Type], nb)
		} else {
			static[nb.Type] = append(static[nb.Type], nb)
		}
	}
	return static, dynamic
}

// policyArgDiff adds a change for each policy argument of a block, at any
// depth, that the change adds, removes or edits.
func policyArgDiff(base, head Body, path string, line int, out *[]policyChange) {
	for _, h := range head.Attributes {
		if !policyAttr(h.Name) {
			continue
		}
		if b := bodyAttr(base, h.Name); b == nil || b.Value.Text != h.Value.Text {
			c := policyChange{line: h.Range.Start.Line, path: path + "." + h.Name, head: new(h.Value.Text)}
			if b != nil {
				c.base = new(b.Value.Text)
			}
			*out = append(*out, c)
		}
	}
	for _, b := range base.Attributes {
		if policyAttr(b.Name) && bodyAttr(head, b.Name) == nil {
			*out = append(*out, policyChange{line: line, path: path + "." + b.Name, base: new(b.Value.Text)})
		}
	}
	bs, bd := staticBlocks(base)
	hs, hd := staticBlocks(head)
	for _, t := range resSortedKeys(hs) {
		for i, h := range hs[t] {
			sub := path + "." + t + "[" + strconv.Itoa(i) + "]"
			var bb Body
			if i < len(bs[t]) {
				bb = bs[t][i].Body
			}
			policyArgDiff(bb, h.Body, sub, h.Range.Start.Line, out)
		}
	}
	for _, t := range resSortedKeys(bs) {
		for i := len(hs[t]); i < len(bs[t]); i++ {
			for _, p := range policyTexts(bs[t][i].Body) {
				*out = append(*out, policyChange{line: line, path: path + "." + t + "[" + strconv.Itoa(i) + "]", base: new(p)})
			}
		}
	}
	for _, t := range resSortedKeys(hd) {
		policyDynamicDiff(bd[t], hd[t], path+"."+t, line, out, func(b Body) bool { return len(policyTexts(b)) > 0 })
	}
	for _, t := range resSortedKeys(bd) {
		if hd[t] == nil {
			policyDynamicDiff(bd[t], nil, path+"."+t, line, out, func(b Body) bool { return len(policyTexts(b)) > 0 })
		}
	}
}

// policyDynamicDiff adds one change when the dynamic blocks of one type
// differ and relevant says one of them holds policy text.
func policyDynamicDiff(base, head []*NestedBlock, path string, line int, out *[]policyChange, relevant func(Body) bool) {
	if dynText(base) == dynText(head) {
		return
	}
	for _, nb := range slices.Concat(base, head) {
		if relevant(nb.Body) {
			l := line
			if len(head) > 0 {
				l = head[0].Range.Start.Line
			}
			*out = append(*out, policyChange{line: l, path: path, base: new(dynText(base)), head: new(dynText(head))})
			return
		}
	}
}

func dynText(bs []*NestedBlock) string {
	var parts []string
	for _, nb := range bs {
		if nb.ForEach != nil {
			parts = append(parts, nb.ForEach.Text)
		}
		parts = append(parts, nb.Iterator)
		parts = append(parts, bodyText(nb.Body)...)
		for _, a := range nb.Body.Attributes {
			parts = append(parts, a.Name)
		}
	}
	return strings.Join(parts, "\x00")
}

// allowEffect reports a statement whose effect is unset or the literal Allow.
func allowEffect(b Body) bool {
	e := bodyAttr(b, "effect")
	return e == nil || e.Value.Text == `"Allow"`
}

// stringSet is the set of a literal list of strings, or false.
func stringSet(x Expr) (map[string]bool, bool) {
	e, ok := parseExprAt(x)
	if !ok {
		return nil, false
	}
	v, ok := literalValue(e)
	if !ok || v.IsNull() {
		return nil, false
	}
	if t := v.Type(); !t.IsTupleType() && !t.IsListType() && !t.IsSetType() {
		return nil, false
	}
	out := map[string]bool{}
	for it := v.ElementIterator(); it.Next(); {
		_, ev := it.Element()
		if ev.IsNull() || ev.Type() != cty.String {
			return nil, false
		}
		out[ev.AsString()] = true
	}
	return out, true
}

// widens reports a literal list at the head that holds every entry of the
// base list.
func widens(b, h Expr) bool {
	bs, ok1 := stringSet(b)
	hs, ok2 := stringSet(h)
	if !ok1 || !ok2 {
		return false
	}
	for s := range bs {
		if !hs[s] {
			return false
		}
	}
	return true
}

// docDiff compares two bodies of an aws_iam_policy_document. allow is set
// inside an allow statement, or at the document level, where a new allow
// statement only widens the document.
func docDiff(base, head Body, path string, line int, allow bool, out *[]policyChange) {
	for _, h := range head.Attributes {
		if h.Name == "sid" {
			continue
		}
		b := bodyAttr(base, h.Name)
		switch {
		case b != nil && b.Value.Text == h.Value.Text:
		case b != nil && allow && path != "" && widening[h.Name] && widens(b.Value, h.Value):
		default:
			c := policyChange{line: h.Range.Start.Line, path: path + "." + h.Name, head: new(h.Value.Text)}
			if b != nil {
				c.base = new(b.Value.Text)
			}
			*out = append(*out, c)
		}
	}
	for _, b := range base.Attributes {
		if b.Name != "sid" && bodyAttr(head, b.Name) == nil {
			*out = append(*out, policyChange{line: line, path: path + "." + b.Name, base: new(b.Value.Text)})
		}
	}
	bs, bd := staticBlocks(base)
	hs, hd := staticBlocks(head)
	for _, t := range resSortedKeys(hs) {
		for i, h := range hs[t] {
			sub := path + "." + t + "[" + strconv.Itoa(i) + "]"
			if i >= len(bs[t]) {
				if path != "" || t != "statement" || !allowEffect(h.Body) {
					*out = append(*out, policyChange{line: h.Range.Start.Line, path: sub})
				}
				continue
			}
			inner := allow
			if t == "statement" {
				inner = allow && allowEffect(bs[t][i].Body) && allowEffect(h.Body)
			}
			docDiff(bs[t][i].Body, h.Body, sub, h.Range.Start.Line, inner, out)
		}
	}
	for _, t := range resSortedKeys(bs) {
		for i := len(hs[t]); i < len(bs[t]); i++ {
			*out = append(*out, policyChange{line: line, path: path + "." + t + "[" + strconv.Itoa(i) + "]"})
		}
	}
	all := func(Body) bool { return true }
	for _, t := range resSortedKeys(hd) {
		policyDynamicDiff(bd[t], hd[t], path+"."+t, line, out, all)
	}
	for _, t := range resSortedKeys(bd) {
		if hd[t] == nil {
			policyDynamicDiff(bd[t], nil, path+"."+t, line, out, all)
		}
	}
}

// baseHasFiles reports an area the base has any path in: the module existed
// before the change. An empty base is a new module, not a missing one.
func baseHasFiles(a *Area) bool {
	if !a.BaseExists || a.baseTree == nil {
		return false
	}
	return slices.ContainsFunc(a.baseTree.Paths, func(p string) bool {
		return a.Path == rootArea || strings.HasPrefix(p, a.Path+"/")
	})
}
