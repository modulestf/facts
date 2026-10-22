package facts

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/hashicorp/go-version"
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

// structure.version-floor-understated: an area whose required_version admits
// a lower release than a local module it calls, or no release that module
// admits. A constraint that is not a plain string is a candidate. One
// candidate per caller, subject <area>:required_version, lands on the
// caller's required_version, else the callee's, else the call's source,
// whichever the change touches first.
func checkVersionFloorUnderstated(a *Area) Result {
	const id = "structure.version-floor-understated"
	caller := requiredVersions(a.Head)
	var anchors []anchor
	for _, d := range caller.lines {
		anchors = append(anchors, anchor{areaFile(a.Path, d.file), d.line})
	}
	var calls []ModuleCall
	offending := map[string]bool{}
	for _, call := range a.Head.ModuleCalls {
		callee := calledArea(a, call)
		if callee == nil {
			continue
		}
		if _, done := offending[callee.Path]; !done {
			d := requiredVersions(callee.Head)
			offending[callee.Path] = understated(caller, d)
			for _, l := range d.lines {
				if offending[callee.Path] {
					anchors = append(anchors, anchor{areaFile(callee.Path, l.file), l.line})
				}
			}
		}
		if offending[callee.Path] {
			calls = append(calls, call)
		}
	}
	for _, call := range calls {
		anchors = append(anchors, anchor{areaFile(a.Path, call.File), call.SourceRange.Start.Line})
	}
	if len(calls) == 0 {
		return Result{}
	}
	lc := &lineChanges{a: a, cache: map[string][]lineRange{}}
	var base *string
	if a.Base != nil {
		base = ifRequiredVersion(a.Base)
	}
	return Result{Candidates: []Candidate{crossCandidate(a, lc, id, "required_version", anchors, base, ifRequiredVersion(a.Head))}}
}

// understated reports whether a caller's constraints admit a lower release
// than its callee's, or no release the callee's admit too. Either side not
// readable counts. A caller that declares no required_version declares no
// floor, which examples.versions-missing reports for an example, and a callee
// with none admits every release, so neither is understated.
func understated(caller, callee versionDecl) bool {
	switch {
	case len(caller.lines) == 0 || len(callee.lines) == 0:
		return false
	case !caller.ok || !callee.ok:
		return true
	}
	low, calleeLow := lowestAdmitted(caller.cs), lowestAdmitted(callee.cs)
	if low == nil || calleeLow == nil || lowestAdmitted(append(slices.Clone(caller.cs), callee.cs...)) == nil {
		return true
	}
	return low.LessThan(calleeLow)
}
