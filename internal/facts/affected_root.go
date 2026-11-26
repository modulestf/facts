package facts

import (
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"
)

// Caps of the repository-root examples a module root run resolves: past
// either, every repository-root example is listed with reason all and
// unresolved, and none of them is parsed.
const (
	rootExampleMaxFiles = 1000    // .tf files directly in the repository-root examples
	rootExampleMaxBytes = 8 << 20 // bytes of those files
)

// pathName is one path component a module root may hold: the characters of
// an example name, and not "." or "..".
func pathName(s string) bool {
	return exampleName.MatchString(s) && s != "." && s != ".."
}

// checkModuleRoot refuses a module root that is not a clean
// repository-relative path of plain names, or not a directory of the head
// tree. ReadTree never follows a link, so a directory it lists is reached
// through no link.
func checkModuleRoot(head *Tree, m string) error {
	parts := strings.Split(m, "/")
	if !slices.ContainsFunc(parts, func(s string) bool { return !pathName(s) }) &&
		parts[0] != "examples" && head.Dirs[m] {
		return nil
	}
	return fmt.Errorf("module root %q is not a directory of the tree under a clean relative path of names in [A-Za-z0-9._-] outside examples/", clean(m))
}

// inDir reports whether p is d or a path below it.
func inDir(p, d string) bool { return p == d || strings.HasPrefix(p, d+"/") }

// lazyGraph is the local call graph of a tree, a directory parsed only when
// a walk reaches it, so a directory no walk reaches is never parsed.
type lazyGraph struct {
	t       *Tree
	lim     Limits
	config  map[string]bool     // directories that hold configuration
	edges   map[string][]string // per parsed directory, the local sources it names
	problem map[string]string   // per parsed directory, FallbackUnparsed or FallbackDynamicSource
	parsed  map[string]bool
}

func newLazyGraph(t *Tree, lim Limits) *lazyGraph {
	return &lazyGraph{t: t, lim: lim, config: configDirs(t), edges: map[string][]string{},
		problem: map[string]string{}, parsed: map[string]bool{}}
}

// parse reads the module calls of d once, as callGraph does.
func (g *lazyGraph) parse(d string) error {
	if g.parsed[d] {
		return nil
	}
	g.parsed[d] = true
	if g.t.InGitlink(d) {
		g.problem[d] = FallbackUnparsed // its module calls are a commit the tree does not hold
		return nil
	}
	if !g.config[d] {
		return nil
	}
	m, err := ParseDir(g.t, d, g.lim)
	if err != nil {
		return err
	}
	if len(m.Unsupported) > 0 {
		g.problem[d] = FallbackUnparsed
		return nil
	}
	for _, c := range m.ModuleCalls {
		src := strings.ReplaceAll(c.Source, `\`, "/")
		switch {
		case src == "":
			g.problem[d] = FallbackDynamicSource
		case strings.HasPrefix(src, "./") || strings.HasPrefix(src, "../"):
			if to := path.Join(d, src); to != ".." && !strings.HasPrefix(to, "../") {
				g.edges[d] = append(g.edges[d], to)
			}
		}
	}
	return nil
}

// closure walks the graph from starts and returns every directory it
// reaches, starts included, and the worst problem on the way: unparsed over
// dynamic-source, "" for none.
func (g *lazyGraph) closure(starts ...string) (map[string]bool, string, error) {
	seen := map[string]bool{}
	todo := slices.Clone(starts)
	problem := ""
	for len(todo) > 0 {
		if err := g.lim.expired(); err != nil {
			return nil, "", err
		}
		d := todo[len(todo)-1]
		todo = todo[:len(todo)-1]
		if seen[d] {
			continue
		}
		seen[d] = true
		if err := g.parse(d); err != nil {
			return nil, "", err
		}
		switch p := g.problem[d]; {
		case p == FallbackUnparsed:
			problem = p
		case p != "" && problem == "":
			problem = p
		}
		todo = append(todo, g.edges[d]...)
	}
	return seen, problem, nil
}

// reachesRoot reports whether a set of directories holds the module root or
// a real directory below it.
func reachesRoot(t *Tree, dirs map[string]bool, m string) bool {
	for d := range dirs {
		if t.Dirs[d] && inDir(d, m) {
			return true
		}
	}
	return false
}

// subtree returns a tree of the entries of t that keep accepts.
func subtree(t *Tree, keep func(p string) bool) *Tree {
	s := &Tree{Entries: map[string]*Entry{}, Dirs: t.Dirs}
	for _, p := range t.Paths {
		if keep(p) {
			s.Entries[p] = t.Entries[p]
			s.Paths = append(s.Paths, p)
		}
	}
	s.index()
	return s
}

// rootExample is a repository-root example of a module root run.
type rootExample struct {
	dir        string
	unresolved bool
	reach      map[string]bool // the directories its module calls reach, itself included
}

// affectedModuleRoot is Affected for a module root M under the repository
// root Head. It lists M's own examples, M/examples/<name>, and the
// repository-root examples, examples/<name>, whose local module calls reach
// M or a directory below it, transitively; every path is
// repository-relative. A repository-root example it cannot resolve, one
// whose calls pass a directory that does not parse or a call with no literal
// source, and every repository-root example when they pass the caps, is
// listed with reason all and unresolved, and makes the change not inert.
//
// The change is read only in scope: under M, under a listed example, in a
// directory M or its examples reach, in a directory on the way from a
// listed repository-root example to M, or read by a read site of those
// directories. Any other changed path is left out of changed and inert.
//
// A report-level fallback, set when a directory M or its own examples reach
// cannot be resolved, a read site of the scope cannot be read, or no change
// is given, lists M's examples and every repository-root example.
func affectedModuleRoot(o AffectedOptions, head *Tree) (*AffectedReport, error) {
	m := o.ModuleRoot
	if err := checkModuleRoot(head, m); err != nil {
		return nil, err
	}
	own, roots, err := moduleRootExamples(head, m)
	if err != nil {
		return nil, err
	}
	r := &AffectedReport{SchemaVersion: AffectedSchemaVersion, ModuleRoot: m, Changed: []string{}}
	// extra holds the changed gitlinks in scope that no other test of a path
	// keeps in a fallback's changed list.
	extra := map[string]bool{}
	fallback := func(why string, changed []string) *AffectedReport {
		r.Fallback, r.Inert, r.Examples = &why, false, []AffectedExample{}
		for _, ex := range slices.Sorted(slices.Values(slices.Concat(own, roots))) {
			r.Examples = append(r.Examples, AffectedExample{Dir: ex, Reason: ReasonAll})
		}
		r.Changed = slices.DeleteFunc(changed, func(p string) bool {
			return !inDir(p, m) && !extra[p] && !slices.ContainsFunc(roots, func(ex string) bool { return inDir(p, ex) })
		})
		return r
	}
	var base *Tree
	switch {
	case o.Base != "":
		if base, err = ReadTree(o.Base, o.Limits); err != nil {
			return nil, fmt.Errorf("base tree: %w", err)
		}
	case o.Diff != "":
		if base, _, err = ReadDiffBase(head, o.Diff, o.Limits); err != nil {
			return nil, err
		}
	default:
		return fallback(FallbackNoChangeInput, []string{}), nil
	}
	all := withGitlinks(changedPaths(head, base), head, base)

	g := newLazyGraph(head, o.Limits)
	scope, problem, err := g.closure(slices.Concat([]string{m}, own)...)
	if err != nil {
		return nil, err
	}
	scopeDirs := slices.Collect(maps.Keys(scope))
	for _, p := range all {
		if slices.ContainsFunc(scopeDirs, func(d string) bool { return inDir(d, p) }) {
			extra[p] = true // a module call through a changed gitlink or link
		}
	}
	if problem != "" {
		return fallback(problem, all), nil
	}
	listed, err := listRootExamples(head, g, m, roots, scope)
	if err != nil {
		return nil, err
	}

	// The read sites of the scope: M's whole tree, as a run on M alone reads
	// it, the directories in scope outside it, and for each resolved listed
	// example every directory under it and every directory its module calls
	// reach, so a helper module that reads a file of the example counts. An
	// unresolved example is left out, so a file in it that does not parse
	// ends no run; it makes the change not inert anyway.
	siteDirs := maps.Clone(scope)
	for _, ex := range listed {
		if ex.unresolved {
			continue
		}
		maps.Copy(siteDirs, ex.reach)
		for d := range head.Dirs {
			if inDir(d, ex.dir) {
				siteDirs[d] = true
			}
		}
	}
	sub := subtree(head, func(p string) bool { return inDir(p, m) || siteDirs[path.Dir(p)] })
	sites, complete, err := ReadSites(sub, o.Limits)
	if err != nil {
		return nil, err
	}
	if !complete {
		return fallback(FallbackUnparsed, all), nil
	}
	siteRoots := slices.Concat([]string{".", m}, own)
	for _, ex := range listed {
		siteRoots = append(siteRoots, ex.dir)
	}
	if read := gitlinksRead(head, sites, siteRoots); len(read) > 0 {
		for _, g := range read {
			extra[g] = true
		}
		return fallback(FallbackUnparsed, all), nil
	}
	reachedBy := func(p string, dir bool) []string {
		var dirs []string
		for _, s := range sites {
			if siteDirs[s.Dir] || inDir(s.Dir, m) {
				reached := s.Reaches(p, siteRoots)
				if dir {
					reached = s.ReachesDir(p, siteRoots)
				}
				if reached {
					dirs = append(dirs, s.Dir)
				}
			}
		}
		return dirs
	}

	changedDirs := map[string]bool{}
	r.Inert = true
	gitlinkChanged := false
	for i, p := range all {
		if i%256 == 0 {
			if err := o.Limits.expired(); err != nil {
				return nil, err
			}
		}
		gitlink := head.IsGitlink(p) || base.IsGitlink(p)
		readers := reachedBy(p, gitlink)
		inScope := inDir(p, m) || scope[path.Dir(p)] || len(readers) > 0 || extra[p] ||
			slices.ContainsFunc(listed, func(ex rootExample) bool { return inDir(p, ex.dir) })
		if !inScope {
			continue
		}
		r.Changed = append(r.Changed, p)
		if gitlink {
			// Its commit is not in the tree, so nothing says what it changed.
			extra[p], gitlinkChanged = true, true
			continue
		}
		h, b := head.Entries[p], base.Entries[p]
		if dirLoaded(p, h, b) {
			r.Inert = false
			changedDirs[path.Dir(p)] = true
			continue
		}
		for _, d := range readers {
			changedDirs[d] = true
		}
		if len(readers) > 0 || !InertDocPath(p) || !inertEntry(h) || !inertEntry(b) || modeChanged(h, b) {
			r.Inert = false
		}
	}

	if gitlinkChanged {
		return fallback(FallbackUnparsed, all), nil
	}

	var touched, callers, rest []AffectedExample
	rank := func(ex string, unresolved bool) {
		switch {
		case slices.ContainsFunc(all, func(p string) bool { return strings.HasPrefix(p, ex+"/") }):
			touched = append(touched, AffectedExample{Dir: ex, Reason: ReasonTouched})
		case reaches(ex, g.edges, changedDirs):
			callers = append(callers, AffectedExample{Dir: ex, Reason: ReasonCaller})
		default:
			rest = append(rest, AffectedExample{Dir: ex, Reason: ReasonAll, Unresolved: unresolved})
		}
	}
	for _, ex := range own {
		rank(ex, false)
	}
	for _, ex := range listed {
		rank(ex.dir, ex.unresolved)
		if ex.unresolved {
			r.Inert = false
		}
	}
	byDir := func(a, b AffectedExample) int { return strings.Compare(a.Dir, b.Dir) }
	for _, group := range [][]AffectedExample{touched, callers, rest} {
		slices.SortFunc(group, byDir)
	}
	r.Examples = slices.Concat([]AffectedExample{}, touched, callers, rest)
	return r, nil
}

// moduleRootExamples lists, in path order, M's own examples and the
// repository-root examples: first-level directories under M/examples and
// examples that hold a .tf file, and under examples also those that hold
// only .tf.json files. A name a host could not use safely as a
// path is an error.
func moduleRootExamples(t *Tree, m string) (own, roots []string, err error) {
	for d := range t.Dirs {
		if path.Dir(d) == m+"/examples" && holdsTF(t, d) {
			own = append(own, d)
		}
	}
	for d := range t.gitlinks {
		if path.Dir(d) == m+"/examples" {
			own = append(own, d) // its module calls cannot be read; the walk reports it unresolved
		}
	}
	slices.Sort(own)
	roots = exampleRoots(t)[1:]
	for d := range t.gitlinks {
		if path.Dir(d) == "examples" {
			roots = append(roots, d)
		}
	}
	for d := range t.Dirs {
		if path.Dir(d) == "examples" && !holdsTF(t, d) && slices.ContainsFunc(t.DirEntries(d), func(e *Entry) bool {
			return strings.HasSuffix(e.Path, ".tf.json") && !ignoredName(path.Base(e.Path))
		}) {
			roots = append(roots, d)
		}
	}
	slices.Sort(roots)
	for _, d := range slices.Concat(own, roots) {
		if !exampleName.MatchString(path.Base(d)) {
			return nil, nil, fmt.Errorf("example directory %q has a name outside [A-Za-z0-9._-]", clean(d))
		}
	}
	return own, roots, nil
}

// listRootExamples returns the repository-root examples to list, in path
// order, and adds to scope each listed directory and every directory on the
// way from it to M.
func listRootExamples(t *Tree, g *lazyGraph, m string, roots []string, scope map[string]bool) ([]rootExample, error) {
	files, bytes := 0, 0
	for _, ex := range roots {
		for _, e := range t.DirEntries(ex) {
			if strings.HasSuffix(e.Path, ".tf") && e.Kind == KindFile {
				files++
				bytes += len(e.Data)
			}
		}
	}
	var out []rootExample
	if files > rootExampleMaxFiles || bytes > rootExampleMaxBytes {
		for _, ex := range roots {
			out = append(out, rootExample{dir: ex, unresolved: true})
		}
		return out, nil
	}
	for _, ex := range roots {
		dirs, problem, err := g.closure(ex)
		if err != nil {
			return nil, err
		}
		switch {
		case reachesRoot(t, dirs, m):
			out = append(out, rootExample{dir: ex, unresolved: problem != "", reach: dirs})
			for d := range dirs {
				if scope[d] {
					continue
				}
				via, _, err := g.closure(d)
				if err != nil {
					return nil, err
				}
				if reachesRoot(t, via, m) {
					scope[d] = true
				}
			}
		case problem != "":
			out = append(out, rootExample{dir: ex, unresolved: true})
		}
	}
	return out, nil
}
