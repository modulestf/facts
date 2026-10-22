package facts

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/hashicorp/go-version"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// structureVersionsRules is the structure_versions rule family. Rule work for this family adds its rules and
// checks here and in no other rules file; Rules joins every family.
//
// Each is a candidate rule. A check emits a candidate for every construct its
// detector matches, touched or not, and the engine keeps those its scope
// judges; the cross-area rules keep only those on a changed line. What a
// detector cannot decide, such as a feature the core feature table does not
// list or a constraint that is not a plain string, is a candidate too, never
// clean: no reason code on the closed list says "undecidable".
var structureVersionsRules = []Rule{
	{ID: "structure.feature-above-version-floor", Tier: "candidate", Class: ClassConfig, Scope: ScopeTouchedExpression, Kinds: []string{AreaRoot, AreaSubmodule}, Check: checkFeatureAboveFloor},
	{ID: "structure.passthrough-default-drift", Tier: "candidate", Class: ClassConfig, Scope: ScopeTouchedBlock, CrossArea: true, Kinds: []string{AreaRoot, AreaSubmodule}, Check: checkPassthroughDefaultDrift},
	{ID: "structure.passthrough-field-drift", Tier: "candidate", Class: ClassConfig, Scope: ScopeTouchedBlock, CrossArea: true, Kinds: []string{AreaRoot, AreaSubmodule}, Check: checkPassthroughFieldDrift},
	{ID: "structure.version-floor-understated", Tier: "candidate", Class: ClassConfig, Scope: ScopeTouchedBlock, CrossArea: true, Kinds: []string{AreaRoot, AreaSubmodule, AreaExample}, Check: checkVersionFloorUnderstated},
}

// versionDecl is the required_version constraints of one module directory.
type versionDecl struct {
	cs version.Constraints
	// lines are the required_version arguments, file relative to the area.
	lines []declLine
	// ok is false when a constraint is not a plain string or does not parse.
	ok bool
}

type declLine struct {
	file string
	line int
}

func requiredVersions(m *Module) versionDecl {
	d := versionDecl{ok: true}
	for _, t := range m.Terraform {
		rv := t.RequiredVersion
		if rv == nil {
			continue
		}
		d.lines = append(d.lines, declLine{t.File, rv.Range.Start.Line})
		s, err := strconv.Unquote(rv.Text)
		if err != nil || strings.Contains(rv.Text, "${") || strings.Contains(rv.Text, "%{") {
			d.ok = false
			continue
		}
		cs, err := version.NewConstraint(s)
		if err != nil {
			d.ok = false
			continue
		}
		d.cs = append(d.cs, cs...)
	}
	return d
}

// lowestAdmitted returns the lowest release that satisfies every constraint,
// or nil when none does. The lowest point of such a set is 0.0.0, a bound's
// own version or the patch after it, so only those are tried.
func lowestAdmitted(cs version.Constraints) *version.Version {
	try := []*version.Version{version.Must(version.NewVersion("0.0.0"))}
	for _, c := range cs {
		v, err := version.NewVersion(strings.TrimLeft(c.String(), "=!<>~ "))
		if err != nil {
			return nil
		}
		s := v.Segments64()
		next := version.Must(version.NewVersion(fmt.Sprintf("%d.%d.%d", s[0], s[1], s[2]+1)))
		try = append(try, version.Must(version.NewVersion(fmt.Sprintf("%d.%d.%d", s[0], s[1], s[2]))), next)
	}
	slices.SortFunc(try, func(a, b *version.Version) int { return a.Compare(b) })
	for _, v := range try {
		if cs.Check(v) {
			return v
		}
	}
	return nil
}

// lineChanges answers whether a head line is changed, from the trees of an
// area, with one diff per file.
type lineChanges struct {
	a     *Area
	cache map[string][]lineRange
}

func (lc *lineChanges) changed(file string, line int) bool {
	r, ok := lc.cache[file]
	if !ok {
		if h := lc.a.headTree.Entries[file]; h != nil && h.Kind == KindFile && !unchanged(lc.a, file) {
			var base []byte
			if lc.a.baseTree != nil {
				if b := lc.a.baseTree.Entries[file]; b != nil && b.Kind == KindFile {
					base = b.Data
				}
			}
			r = headChanged(base, h.Data)
		}
		lc.cache[file] = r
	}
	return inRanges(r, line)
}

// anchor is a line a cross-area candidate may land on, file relative to the
// module root.
type anchor struct {
	file string
	line int
}

// crossCandidate builds a cross-area candidate. It lands on the first anchor
// that is a changed line, the change that causes it, or on the first anchor
// when none is, which the engine then drops. Every anchor is a source line.
func crossCandidate(a *Area, lc *lineChanges, rule, addr string, anchors []anchor, base, head *string) Candidate {
	e := newEvidence(a)
	at := anchors[0]
	found := false
	for _, an := range anchors {
		if !found && lc.changed(an.file, an.line) {
			at, found = an, true
		}
	}
	seen := map[anchor]bool{}
	for _, an := range anchors {
		if seen[an] {
			continue
		}
		seen[an] = true
		if len(e.ev.SourceLines) == maxEvidenceComments {
			e.complete = false
			break
		}
		text, _ := treeLine(a.headTree, an.file, an.line)
		e.ev.SourceLines = append(e.ev.SourceLines, SourceLine{Revision: "head", File: an.file, Line: an.line, Text: e.trim(text)})
	}
	if base != nil {
		e.base(*base)
	}
	if head != nil {
		e.head(*head)
	}
	line := at.line
	return Candidate{RuleID: rule, File: at.file, Line: &line, Subject: subject(a.Path, e.trim(addr)), Evidence: e.ev, EvidenceComplete: e.complete}
}

// floorDecl is one declared floor of a module: its required_version, or the
// version constraints of one provider source address.
type floorDecl struct {
	addr string // subject address: required_version or required_providers.<name>
	d    versionDecl
	text []string // the constraints as written, for evidence
}

// moduleFloors returns the floors a module declares, keyed "" for its
// required_version and by provider source address otherwise. A provider
// entry with no version declares the provider and no floor. A source or
// version that is not a plain string makes that entry's floor unreadable.
func moduleFloors(m *Module) map[string]*floorDecl {
	out := map[string]*floorDecl{}
	if rv := requiredVersions(m); len(rv.lines) > 0 {
		f := &floorDecl{addr: "required_version", d: rv}
		if t := ifRequiredVersion(m); t != nil {
			f.text = []string{*t}
		}
		out[""] = f
	}
	for _, t := range m.Terraform {
		for _, nb := range t.Body.Blocks {
			if nb.Type != "required_providers" {
				continue
			}
			for _, at := range nb.Body.Attributes {
				addFloor(out, t.File, at)
			}
		}
	}
	return out
}

// addFloor adds one required_providers entry to the floors.
func addFloor(out map[string]*floorDecl, file string, at Attribute) {
	source, sourceOK := "hashicorp/"+at.Name, true
	line := at.Range.Start.Line
	var constraint *Expr
	switch e := reparse(file, at.Value).(type) {
	case *hclsyntax.ObjectConsExpr:
		for _, it := range e.Items {
			v := Expr{Text: sourceOf(at.Value, it.ValueExpr.Range()), Range: it.ValueExpr.Range()}
			switch keyName(it.KeyExpr) {
			case "source":
				if s, ok := literalString(it.ValueExpr); ok {
					source = s
				} else {
					sourceOK = false
				}
			case "version":
				constraint, line = &v, it.KeyExpr.Range().Start.Line
			}
		}
	default:
		constraint = &at.Value // the legacy form: a version constraint string
	}
	key := providerKey(source)
	if !sourceOK {
		key = "?" + at.Name
	}
	f := out[key]
	if f == nil {
		f = &floorDecl{addr: "required_providers." + at.Name, d: versionDecl{ok: true}}
		out[key] = f
	}
	f.d.lines = append(f.d.lines, declLine{file, line})
	f.d.ok = f.d.ok && sourceOK
	if constraint == nil {
		return
	}
	f.text = append(f.text, constraint.Text)
	s, err := strconv.Unquote(constraint.Text)
	if err != nil || strings.Contains(constraint.Text, "${") || strings.Contains(constraint.Text, "%{") {
		f.d.ok = false
		return
	}
	cs, err := version.NewConstraint(s)
	if err != nil {
		f.d.ok = false
		return
	}
	f.d.cs = append(f.d.cs, cs...)
}

// providerKey normalizes a provider source address: lower case, with the
// default registry host and namespace filled in.
func providerKey(source string) string {
	s := strings.ToLower(source)
	switch strings.Count(s, "/") {
	case 0:
		return "registry.terraform.io/hashicorp/" + s
	case 1:
		return "registry.terraform.io/" + s
	}
	return s
}

// structure.version-floor-understated: an area whose required_version, or
// the version constraints of a provider it requires, admit a lower release
// than a local module it calls, or no release that module admits. Provider
// floors are compared per source address. A constraint that is not a plain
// string is a candidate. One candidate per caller and floor, subject
// <area>:required_version or <area>:required_providers.<name>, lands on the
// caller's declaration, else the callee's, else the call's source, whichever
// the change touches first.
func checkVersionFloorUnderstated(a *Area) Result {
	const id = "structure.version-floor-understated"
	callerFloors := moduleFloors(a.Head)
	type finding struct {
		floor   *floorDecl // the callee's, when the caller declares none
		callees []anchor
		calls   []anchor
		seen    map[string]bool
	}
	found := map[string]*finding{}
	for _, call := range a.Head.ModuleCalls {
		callee := calledArea(a, call)
		if callee == nil {
			continue
		}
		for key, ce := range moduleFloors(callee.Head) {
			cr := callerFloors[key]
			var cd versionDecl
			if cr != nil {
				cd = cr.d
			}
			if !understated(cd, ce.d, a.Kind == AreaExample && len(callerFloors) == 0) {
				continue
			}
			f := found[key]
			if f == nil {
				f = &finding{floor: ce, seen: map[string]bool{}}
				found[key] = f
			}
			if !f.seen[callee.Path] {
				f.seen[callee.Path] = true
				for _, l := range ce.d.lines {
					f.callees = append(f.callees, anchor{areaFile(callee.Path, l.file), l.line})
				}
			}
			f.calls = append(f.calls, anchor{areaFile(a.Path, call.File), call.SourceRange.Start.Line})
		}
	}
	lc := &lineChanges{a: a, cache: map[string][]lineRange{}}
	var res Result
	for _, key := range slices.Sorted(maps.Keys(found)) {
		f := found[key]
		addr, head := f.floor.addr, (*string)(nil)
		var anchors []anchor
		if cr := callerFloors[key]; cr != nil {
			addr = cr.addr
			for _, l := range cr.d.lines {
				anchors = append(anchors, anchor{areaFile(a.Path, l.file), l.line})
			}
			head = joined(cr.text)
		}
		var base *string
		if a.Base != nil {
			if bf := moduleFloors(a.Base)[key]; bf != nil {
				base = joined(bf.text)
			}
		}
		anchors = slices.Concat(anchors, f.callees, f.calls)
		res.Candidates = append(res.Candidates, crossCandidate(a, lc, id, addr, anchors, base, head))
	}
	return res
}

func joined(parts []string) *string {
	if len(parts) == 0 {
		return nil
	}
	s := strings.Join(parts, ", ")
	return &s
}

// understated reports whether a caller's constraints admit a lower release
// than its callee's, or no release the callee's admit too. Either side not
// readable counts. A callee that declares nothing, or declares no floor,
// admits every release, so it is never understated. A caller that declares
// nothing against a callee floor is understated, except an example that
// declares no floor of any kind: that case is examples.versions-missing.
func understated(caller, callee versionDecl, bareExample bool) bool {
	switch {
	case len(callee.lines) == 0:
		return false
	case len(caller.lines) == 0 && bareExample:
		return false
	case !callee.ok:
		return true
	case len(caller.lines) == 0:
		low := lowestAdmitted(callee.cs)
		return low == nil || low.GreaterThan(version.Must(version.NewVersion("0.0.0")))
	case !caller.ok:
		return true
	}
	low, calleeLow := lowestAdmitted(caller.cs), lowestAdmitted(callee.cs)
	if low == nil || calleeLow == nil || lowestAdmitted(append(slices.Clone(caller.cs), callee.cs...)) == nil {
		return true
	}
	return low.LessThan(calleeLow)
}
