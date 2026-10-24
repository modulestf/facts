package facts

import (
	"maps"
	"path"
	"slices"
	"sort"
	"strings"

	"github.com/modulestf/facts/internal/profile"
	"github.com/modulestf/facts/internal/schema"
)

// Area kinds, from the path classes of the reviewer's large-changes reference.
const (
	AreaRoot      = "root"
	AreaSubmodule = "submodule"
	AreaExample   = "example"
)

// Area is one module directory, with its head and base facts.
type Area struct {
	Path string // "." for the root module
	Kind string
	// Untouched is set for an area the change does not touch. Only a
	// cross-area rule's pass reads it, and nothing it finds there is kept.
	Untouched bool

	HeadExists bool
	BaseExists bool
	Head       *Module // known-empty when the area is absent at head
	Base       *Module // nil without a base tree; known-empty when absent at base
	// Issue is set when the area path exists but is not a real directory.
	Issue string
	// issueCode is the reason code of Issue.
	issueCode string
	// unreadConfig are the .tf and .tf.json files the change adds or edits
	// in a boundary directory no area reads: on the root, those under tests/,
	// where a test helper module lives; on a submodule modules/<m>, those more
	// than one level under it. The rules that set ReadsUnreadConfig judge them.
	unreadConfig []string

	// Schema is the run's schema input, set by applySchema; nil without one.
	// Its lookups answer for the area's directory, Path.
	Schema *schema.Set
	// Profile is the module profile, set by applyProfile; nil when the run
	// has none, and profileIssue then says why. Every area shares the one
	// value, so a rule reads it and never changes it. A rule that reads it
	// starts with needProfile.
	Profile      *profile.Profile
	profileIssue string

	headTree *Tree
	baseTree *Tree // nil without a base tree
	// peers are every area of the run by path, this one included, set by
	// Evaluate, so a cross-area rule can read the module an example calls.
	peers map[string]*Area
}

// headEntry returns the head entry of a file directly in the area, or nil.
func (a *Area) headEntry(name string) *Entry { return a.headTree.Entries[path.Join(a.Path, name)] }

// Classify maps a path to its area and the area kind. ok is false for
// paths that belong to no area: wrappers, tests, and loose files directly
// under examples/ or modules/. Paths under .github/ and the root profile files count as the
// root, as the rule registry says.
func Classify(p string) (area, kind string, ok bool) {
	parts := strings.Split(p, "/")
	switch {
	case parts[0] == "wrappers" || parts[0] == "tests":
		return "", "", false
	case strings.HasSuffix(p, ".tftest.hcl"):
		return "", "", false
	case parts[0] == "examples" || parts[0] == "modules":
		if len(parts) < 3 {
			return "", "", false
		}
		kind = AreaExample
		if parts[0] == "modules" {
			kind = AreaSubmodule
		}
		return parts[0] + "/" + parts[1], kind, true
	}
	return ".", AreaRoot, true
}

// unreadArea returns the area whose rules judge p, a changed entry in a
// boundary directory that no area reads: the root for one under tests/, and
// modules/<m> for one more than one level under modules/<m>/. A regular file
// counts when it is a .tf or .tf.json file. Any other entry counts whatever
// its name, from one level under modules/<m>/: a link, special or unreadable
// entry may be a directory of configuration the engine never follows.
func unreadArea(p string, e *Entry) (string, bool) {
	regular := e.Kind == KindFile || e.Kind == KindOversize
	if regular && !strings.HasSuffix(p, ".tf") && !strings.HasSuffix(p, ".tf.json") {
		return "", false
	}
	parts := strings.Split(p, "/")
	switch {
	case parts[0] == "tests" && len(parts) > 1:
		return rootArea, true
	case parts[0] == "modules" && (len(parts) > 3 || !regular && len(parts) == 3):
		return parts[0] + "/" + parts[1], true
	}
	return "", false
}

// judgedConfig returns the unread entries of area that a scope rule may judge.
// Under tests/ that is every entry, and so is an entry that is not a regular
// file. Under modules/<m>/ a nested directory that parses with no issue and
// only variables, outputs, locals, moved or removed blocks holds nothing those
// rules judge, so its files are dropped.
func judgedConfig(t *Tree, area string, files []string, lim Limits) ([]string, error) {
	if area == rootArea {
		return files, nil
	}
	var out []string
	inert := map[string]bool{}
	for _, f := range files {
		if k := t.Entries[f].Kind; k != KindFile && k != KindOversize {
			out = append(out, f)
			continue
		}
		d := path.Dir(f)
		ok, seen := inert[d]
		if !seen {
			m, err := ParseDir(t, d, lim)
			if err != nil {
				return nil, err
			}
			ok = len(m.Unsupported) == 0 && len(m.Terraform)+len(m.Providers)+len(m.Resources)+len(m.ModuleCalls) == 0 &&
				!slices.ContainsFunc(m.Other, func(o OtherBlock) bool { return o.Type != "locals" })
			inert[d] = ok
		}
		if !ok {
			out = append(out, f)
		}
	}
	return out, nil
}

// FindAreas returns the areas of the module. With a base tree the areas in
// scope are those of changed, added or removed paths, and a path in no area
// brings the root into scope; without one, every area
// at head is in scope. With untouched set and a base tree, every other area at
// head is listed and parsed too, marked Untouched, for a cross-area pass to
// read. Without it those areas are neither listed nor parsed.
func FindAreas(head, base *Tree, lim Limits, untouched bool) ([]*Area, error) {
	add := func(set map[string]string, p string, t *Tree) {
		// A link, special or unreadable entry where an example or submodule
		// directory belongs is that area, so its rules report it. That holds
		// for examples/ and modules/ themselves too.
		if e := t.Entries[p]; e != nil && e.Kind != KindFile && e.Kind != KindOversize {
			switch parts := strings.Split(p, "/"); {
			case len(parts) == 1 && p == "examples":
				set[p] = AreaExample
				return
			case len(parts) == 1 && p == "modules":
				set[p] = AreaSubmodule
				return
			case len(parts) == 2 && (parts[0] == "examples" || parts[0] == "modules"):
				p += "/x"
			}
		}
		a, k, ok := Classify(p)
		if !ok {
			// A path in no area, such as one under tests/ or wrappers/,
			// counts as the root's, so the root's path rules read it and a
			// change to such paths alone is not left with no area in scope.
			a, k = rootArea, AreaRoot
		}
		set[a] = k
	}
	// every collects each area at head.
	every := func(set map[string]string) {
		set["."] = AreaRoot
		for d := range head.Dirs {
			if strings.Count(d, "/") >= 1 {
				add(set, d+"/x", head) // a directory stands for any file inside it
			}
		}
		for _, p := range head.Paths {
			add(set, p, head)
		}
	}
	inScope := map[string]string{}
	all := inScope
	unread := map[string][]string{}
	if base == nil {
		every(inScope)
	} else {
		for _, p := range head.Paths {
			if !sameEntry(head.Entries[p], base.Entries[p]) {
				add(inScope, p, head)
				if a, ok := unreadArea(p, head.Entries[p]); ok {
					unread[a] = append(unread[a], p)
				}
			}
		}
		for _, p := range base.Paths {
			if head.Entries[p] == nil {
				add(inScope, p, base)
			}
		}
		if untouched {
			all = map[string]string{}
			every(all)
			maps.Copy(all, inScope)
		}
	}
	var out []*Area
	for p, k := range all {
		_, touched := inScope[p]
		a := &Area{Path: p, Kind: k, Untouched: !touched, headTree: head, baseTree: base, Head: &Module{Dir: p}}
		var err error
		if a.unreadConfig, err = judgedConfig(head, p, unread[p], lim); err != nil {
			return nil, err
		}
		a.HeadExists, a.Issue, a.issueCode = dirState(head, p)
		if a.HeadExists && a.Issue == "" {
			if a.Head, err = ParseDir(head, p, lim); err != nil {
				return nil, err
			}
		}
		if base != nil {
			var issue, code string
			a.BaseExists, issue, code = dirState(base, p)
			a.Base = &Module{Dir: p}
			if issue != "" {
				a.Base.unsupported(code, issue)
			} else if a.BaseExists {
				if a.Base, err = ParseDir(base, p, lim); err != nil {
					return nil, err
				}
			}
		}
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// dirState reports whether p is a directory of t, and an issue with its reason
// code when p exists but is not a readable directory.
func dirState(t *Tree, p string) (exists bool, issue, code string) {
	e := t.Entries[p]
	switch {
	case e != nil && t.Dirs[p]:
		return true, p + " is a directory that cannot be read", ReasonUnsupportedFile
	case e != nil:
		return false, p + " is " + kindText(e.Kind) + ", not a directory", kindCode(e.Kind)
	}
	return t.Dirs[p], "", ""
}
