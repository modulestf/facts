package facts

import (
	"sort"
	"strings"
)

// Area kinds, from the path classes of the reviewer's large-changes reference.
const (
	AreaRoot      = "root"
	AreaSubmodule = "submodule"
	AreaExample   = "example"
	AreaUnknown   = "unknown"
)

// Area is one module directory in scope, with its head and base facts.
type Area struct {
	Path string // "." for the root module
	Kind string

	HeadExists bool
	BaseExists bool
	HasBase    bool    // a base tree was given
	Head       *Module // known-empty when the area is absent at head
	Base       *Module // nil without a base tree; known-empty when absent at base
	// Issue is set when the area path exists but is not a real directory.
	Issue string
}

// rootOnlyFiles are root files owned by the profile or CI, not by an area.
var rootOnlyFiles = map[string]bool{
	"CHANGELOG.md": true, ".editorconfig": true, ".gitignore": true,
	".releaserc.json": true, ".pre-commit-config.yaml": true,
}

// classify maps a path to its area. ok is false for paths that belong to no
// area: generated content, tests, CI and profile-owned files.
func classify(p string) (area, kind string, ok bool) {
	parts := strings.Split(p, "/")
	switch {
	case parts[0] == "wrappers" || parts[0] == "tests" || parts[0] == ".github":
		return "", "", false
	case strings.HasSuffix(p, ".tftest.hcl"):
		return "", "", false
	case len(parts) == 1 && rootOnlyFiles[p]:
		return "", "", false
	case parts[0] == "examples" || parts[0] == "modules":
		if len(parts) < 3 {
			return parts[0], AreaUnknown, true
		}
		kind = AreaExample
		if parts[0] == "modules" {
			kind = AreaSubmodule
		}
		return parts[0] + "/" + parts[1], kind, true
	}
	return ".", AreaRoot, true
}

// FindAreas returns the areas in scope. With a base tree they are the areas of
// changed, added or removed paths; without one, every area at head.
func FindAreas(head, base *Tree, lim Limits) []*Area {
	set := map[string]string{}
	add := func(p string, t *Tree) {
		// A link where an example or submodule directory belongs is that area.
		if parts := strings.Split(p, "/"); len(parts) == 2 && (parts[0] == "examples" || parts[0] == "modules") {
			if e := t.Entries[p]; e != nil && e.Kind == KindSymlink {
				p += "/x"
			}
		}
		if a, k, ok := classify(p); ok {
			set[a] = k
		}
	}
	if base == nil {
		set["."] = AreaRoot
		for d := range head.Dirs {
			if parts := strings.Split(d, "/"); len(parts) >= 2 {
				add(d+"/x", head) // a directory stands for any file inside it
			}
		}
		for _, p := range head.Paths {
			add(p, head)
		}
		for a, k := range set {
			if k == AreaUnknown {
				delete(set, a) // loose files under examples/ or modules/ are not areas
			}
		}
	} else {
		for _, p := range head.Paths {
			if !sameEntry(head.Entries[p], base.Entries[p]) {
				add(p, head)
			}
		}
		for _, p := range base.Paths {
			if head.Entries[p] == nil {
				add(p, base)
			}
		}
	}
	var out []*Area
	for p, k := range set {
		a := &Area{Path: p, Kind: k, HasBase: base != nil}
		a.HeadExists, a.Issue = dirState(head, p)
		a.Head = &Module{Dir: p}
		if a.HeadExists {
			a.Head = ParseDir(head, p, lim)
		}
		if base != nil {
			var issue string
			a.BaseExists, issue = dirState(base, p)
			a.Base = &Module{Dir: p}
			if issue != "" {
				a.Base.Unsupported = append(a.Base.Unsupported, issue)
			} else if a.BaseExists {
				a.Base = ParseDir(base, p, lim)
			}
		}
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func dirState(t *Tree, p string) (exists bool, issue string) {
	if t.Dirs[p] {
		return true, ""
	}
	if e := t.Entries[p]; e != nil {
		return false, p + " is " + kindText(e.Kind) + ", not a directory"
	}
	return false, ""
}
