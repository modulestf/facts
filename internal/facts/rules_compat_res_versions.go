package facts

// The version floor checks of the compat_res family, whose rules are in
// rules_compat_res.go.

import (
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/hashicorp/go-version"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"github.com/modulestf/facts/internal/schema"
)

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

// constraintHeadLine is the line of the head constraint a candidate names: the first
// one-line constraint the base does not have, else none.
func constraintHeadLine(base, head []constraint) (file string, line int) {
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
		file, line := constraintHeadLine(base, head)
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
// provider set, but only those the floors tell apart: the fact at the old
// floor, and the fact at the new floor, the declared minimum, when its
// state differs. A name present at both floors, or deprecated at both, has
// no fact, and neither has one present at the old floor when the handoff
// has no schema at the new one; one absent or unknown at the old floor
// always has.
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
		// One provider without versions makes the area's entry
		// not_evaluated, and Evaluate keeps no candidate of a not_evaluated
		// entry, so evaluating the other providers first would find nothing
		// that is kept; the model evaluates the rule for the whole area.
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
		// base and head carry the constraint text, the candidate its head line
		ev := newResEvidence(a)
		ev.values(new(constraintText(base)), new(constraintText(head)))
		schemaFacts(a, ev, name, r.Source, *r.Versions.OldFloor, r.Versions.DeclaredMinimum)
		file, line := constraintHeadLine(base, head)
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

func schemaFacts(a *Area, ev *resEvidence, local, source, oldFloor, newFloor string) {
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
			state := func(ver string) string {
				return factState(a.Schema.Lookup(schema.Query{Source: source, Version: ver, DataSource: r.Mode == "data", Type: r.Type, Path: []string{n}}))
			}
			old, cur := state(oldFloor), state(newFloor)
			// unknown at the new floor is no schema there, so only the old floor speaks
			if (old == "present" || old == "deprecated") && (cur == old || cur == "unknown") {
				continue
			}
			facts := []SchemaFact{{ProviderSource: source, Version: oldFloor, Type: r.Type, Path: n, State: old}}
			if cur != old {
				facts = append(facts, SchemaFact{ProviderSource: source, Version: newFloor, Type: r.Type, Path: n, State: cur})
			}
			if len(ev.ev.SchemaFacts)+len(facts) > maxResSchemaFacts {
				ev.complete = false
				return
			}
			ev.ev.SchemaFacts = append(ev.ev.SchemaFacts, facts...)
		}
	}
}
