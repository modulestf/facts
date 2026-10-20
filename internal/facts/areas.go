package facts

import (
	"path"
	"sort"
	"strings"
)

// Area kinds, from the path classes of the reviewer's large-changes reference.
const (
	AreaRoot      = "root"
	AreaSubmodule = "submodule"
	AreaExample   = "example"
)

// Area is one module directory in scope, with its head and base facts.
type Area struct {
	Path string // "." for the root module
	Kind string

	HeadExists bool
	BaseExists bool
	Head       *Module // known-empty when the area is absent at head
	Base       *Module // nil without a base tree; known-empty when absent at base
	// Issue is set when the area path exists but is not a real directory.
	Issue string

	headTree *Tree
}

// headEntry returns the head entry of a file directly in the area, or nil.
func (a *Area) headEntry(name string) *Entry { return a.headTree.Entries[path.Join(a.Path, name)] }

// rootOnlyFiles are root files owned by the profile or CI, not by an area.
var rootOnlyFiles = map[string]bool{
	"CHANGELOG.md": true, ".editorconfig": true, ".gitignore": true,
	".releaserc.json": true, ".pre-commit-config.yaml": true,
}

// classify maps a path to its area. ok is false for paths that belong to no
// area: generated content, tests, CI and profile-owned files, and loose
// files directly under examples/ or modules/.
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

// FindAreas returns the areas in scope. With a base tree they are the areas of
// changed, added or removed paths; without one, every area at head.
func FindAreas(head, base *Tree, lim Limits) ([]*Area, error) {
	set := map[string]string{}
	add := func(p string, t *Tree) {
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
		if a, k, ok := classify(p); ok {
			set[a] = k
		}
	}
	if base == nil {
		set["."] = AreaRoot
		for d := range head.Dirs {
			if strings.Count(d, "/") >= 1 {
				add(d+"/x", head) // a directory stands for any file inside it
			}
		}
		for _, p := range head.Paths {
			add(p, head)
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
		a := &Area{Path: p, Kind: k, headTree: head, Head: &Module{Dir: p}}
		var err error
		a.HeadExists, a.Issue = dirState(head, p)
		if a.HeadExists && a.Issue == "" {
			if a.Head, err = ParseDir(head, p, lim); err != nil {
				return nil, err
			}
		}
		if base != nil {
			var issue string
			a.BaseExists, issue = dirState(base, p)
			a.Base = &Module{Dir: p}
			if issue != "" {
				a.Base.Unsupported = append(a.Base.Unsupported, issue)
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

// dirState reports whether p is a directory of t, and an issue when p exists
// but is not a readable directory.
func dirState(t *Tree, p string) (exists bool, issue string) {
	e := t.Entries[p]
	switch {
	case e != nil && t.Dirs[p]:
		return true, p + " is a directory that cannot be read"
	case e != nil:
		return false, p + " is " + kindText(e.Kind) + ", not a directory"
	}
	return t.Dirs[p], ""
}
