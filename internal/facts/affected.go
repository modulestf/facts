package facts

import (
	"errors"
	"fmt"
	"maps"
	"path"
	"regexp"
	"slices"
	"strings"
)

// AffectedSchemaVersion is the version of affected.schema.json.
const AffectedSchemaVersion = 1

// Fallbacks: why Affected could not rank the examples, so it lists them all.
const (
	FallbackUnparsed      = "unparsed"        // an HCL file of the head tree cannot be parsed
	FallbackDynamicSource = "dynamic-source"  // a module call has no literal source
	FallbackNoChangeInput = "no-change-input" // neither a base tree nor a diff was given
)

// Reasons an example is listed.
const (
	ReasonTouched = "touched" // the change touches a path under the example
	ReasonCaller  = "caller"  // the example calls, by local paths, a module the change touches
	ReasonAll     = "all"     // every other example
)

var exampleName = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// AffectedOptions are the inputs of Affected. Base and Diff are exclusive.
type AffectedOptions struct {
	Head   string // directory of the head tree
	Base   string // optional directory of the tree the change started from
	Diff   string // optional GitHub pull request files list or unified diff; see ReadDiffBase
	Limits Limits
}

// AffectedReport says which examples a change can affect, most likely first.
// Examples lists every example of the head tree: touched, then caller, then
// the rest, each group in path order. With a fallback all are reason all.
// Inert is set when every changed path is a documentation or metadata path
// no HCL read site reaches, and never with a fallback.
type AffectedReport struct {
	SchemaVersion int               `json:"schema_version"`
	Inert         bool              `json:"inert"`
	Fallback      *string           `json:"fallback"`
	Examples      []AffectedExample `json:"examples"`
	Changed       []string          `json:"changed"`
}

// AffectedExample is one example directory, "examples/<name>", and why it
// is listed.
type AffectedExample struct {
	Dir    string `json:"dir"`
	Reason string `json:"reason"`
}

// Affected reads the head tree and the change, and lists the examples the
// change can affect: those it touches, and those that call a module it
// touches through local module sources, transitively. Registry and git
// sources are not callers of the local tree. A changed file Terraform loads
// by its directory (configuration, variables, tests, the lock file) is a
// change of that directory; any other changed file, documentation included,
// is a change of every module whose read sites reach it. A change of the
// exec bit alone is a change and never inert.
func Affected(o AffectedOptions) (*AffectedReport, error) {
	if o.Base != "" && o.Diff != "" {
		return nil, errors.New("a base tree and a diff cannot be given together")
	}
	head, err := ReadTree(o.Head, o.Limits)
	if err != nil {
		return nil, fmt.Errorf("head tree: %w", err)
	}
	examples, err := exampleDirs(head)
	if err != nil {
		return nil, err
	}
	r := &AffectedReport{SchemaVersion: AffectedSchemaVersion, Changed: []string{}}
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
		return r.fallback(FallbackNoChangeInput, examples), nil
	}
	r.Changed = changedPaths(head, base)

	edges, fb, err := callGraph(head, o.Limits)
	if err != nil {
		return nil, err
	}
	if fb != "" {
		return r.fallback(fb, examples), nil
	}
	sites, complete, err := ReadSites(head, o.Limits)
	if err != nil {
		return nil, err
	}
	if !complete {
		return r.fallback(FallbackUnparsed, examples), nil
	}

	roots := append([]string{"."}, examples...)
	changedDirs := map[string]bool{}
	// readers marks the modules whose read sites reach p as changed.
	readers := func(p string) (bool, error) {
		reached := false
		for i, s := range sites {
			if i%1024 == 0 {
				if err := o.Limits.expired(); err != nil {
					return false, err
				}
			}
			if s.Reaches(p, roots) {
				reached = true
				changedDirs[s.Dir] = true
			}
		}
		return reached, nil
	}
	r.Inert = true
	for _, p := range r.Changed {
		if err := o.Limits.expired(); err != nil {
			return nil, err
		}
		h, b := head.Entries[p], base.Entries[p]
		if dirLoaded(p, h, b) {
			r.Inert = false
			changedDirs[path.Dir(p)] = true
			continue
		}
		reached, err := readers(p)
		if err != nil {
			return nil, err
		}
		if reached || !InertDocPath(p) || !inertEntry(h) || !inertEntry(b) || modeChanged(h, b) {
			r.Inert = false
		}
	}

	var touched, callers, rest []AffectedExample
	for _, ex := range examples {
		switch {
		case slices.ContainsFunc(r.Changed, func(p string) bool { return strings.HasPrefix(p, ex+"/") }):
			touched = append(touched, AffectedExample{ex, ReasonTouched})
		case reaches(ex, edges, changedDirs):
			callers = append(callers, AffectedExample{ex, ReasonCaller})
		default:
			rest = append(rest, AffectedExample{ex, ReasonAll})
		}
	}
	r.Examples = slices.Concat([]AffectedExample{}, touched, callers, rest)
	return r, nil
}

// fallback lists every example with reason all.
func (r *AffectedReport) fallback(why string, examples []string) *AffectedReport {
	r.Fallback, r.Inert = &why, false
	r.Examples = []AffectedExample{}
	for _, ex := range examples {
		r.Examples = append(r.Examples, AffectedExample{ex, ReasonAll})
	}
	return r
}

// exampleDirs lists, in path order, the first-level directories under
// examples/ that hold a .tf file. A name a host could not use safely as a
// path is an error.
func exampleDirs(t *Tree) ([]string, error) {
	out := exampleRoots(t)[1:]
	for _, d := range out {
		if name := path.Base(d); !exampleName.MatchString(name) {
			return nil, fmt.Errorf("example directory %q has a name outside [A-Za-z0-9._-]", clean(d))
		}
	}
	return out, nil
}

// clean quotes a tree path for an error message: control and non-ASCII
// bytes are escaped.
func clean(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x20 || c >= 0x7f {
			fmt.Fprintf(&b, "\\x%02x", c)
		} else {
			b.WriteByte(c)
		}
	}
	return b.String()
}

// changedPaths lists, in path order, the head paths whose entry differs at
// base and the base paths the head lacks.
func changedPaths(head, base *Tree) []string {
	out := []string{}
	for _, p := range head.Paths {
		if !sameEntry(head.Entries[p], base.Entries[p]) {
			out = append(out, p)
		}
	}
	for _, p := range base.Paths {
		if head.Entries[p] == nil {
			out = append(out, p)
		}
	}
	slices.Sort(out)
	return out
}

// inertEntry reports whether an entry can be an inert documentation file:
// absent, or a regular file. A link may point at anything, and a file a diff
// gives no patch for may have changed its mode.
func inertEntry(e *Entry) bool {
	return e == nil || e.Kind == KindFile || e.Kind == KindOversize
}

// modeChanged reports whether a file's exec bit differs between trees.
func modeChanged(h, b *Entry) bool {
	return h != nil && b != nil && h.exec != b.exec
}

// dirLoaded reports whether Terraform reads p because of the directory it is
// in, so its change is one of that module directory: configuration, variable
// definitions, test and lock files, and an entry that is not a regular file,
// which may stand for a directory of configuration. Any other file changes
// only the modules whose read sites reach it.
func dirLoaded(p string, entries ...*Entry) bool {
	name := path.Base(p)
	if name == ".terraform.lock.hcl" {
		return true
	}
	for _, ext := range []string{".tf", ".tf.json", ".tfvars", ".tfvars.json", ".tftest.hcl"} {
		if strings.HasSuffix(name, ext) {
			return true
		}
	}
	return slices.ContainsFunc(entries, func(e *Entry) bool {
		return e != nil && e.Kind != KindFile && e.Kind != KindOversize && e.Kind != KindNoPatch
	})
}

// configDirs is the set of directories that hold a .tf or .tf.json file.
func configDirs(t *Tree) map[string]bool {
	out := map[string]bool{}
	for _, p := range t.Paths {
		name := path.Base(p)
		if !ignoredName(name) && (strings.HasSuffix(name, ".tf") || strings.HasSuffix(name, ".tf.json")) {
			out[path.Dir(p)] = true
		}
	}
	return out
}

// callGraph parses every module directory of the head tree and returns, per
// directory, the directories its local module sources name. fallback is set
// when a directory does not parse, or a module call has no literal source.
func callGraph(head *Tree, lim Limits) (edges map[string][]string, fallback string, err error) {
	edges = map[string][]string{}
	dirs := configDirs(head)
	for _, d := range slices.Sorted(maps.Keys(dirs)) {
		m, err := ParseDir(head, d, lim)
		if err != nil {
			return nil, "", err
		}
		if len(m.Unsupported) > 0 {
			return nil, FallbackUnparsed, nil
		}
		for _, c := range m.ModuleCalls {
			src := strings.ReplaceAll(c.Source, `\`, "/") // a local path written as on Windows
			switch {
			case src == "":
				fallback = FallbackDynamicSource
			case strings.HasPrefix(src, "./") || strings.HasPrefix(src, "../"):
				if to := path.Join(d, src); to != ".." && !strings.HasPrefix(to, "../") {
					edges[d] = append(edges[d], to)
				}
			}
		}
	}
	return edges, fallback, nil
}

// reaches reports whether a walk of the local call graph from ex reaches a
// changed directory.
func reaches(ex string, edges map[string][]string, changed map[string]bool) bool {
	seen := map[string]bool{ex: true}
	todo := []string{ex}
	for len(todo) > 0 {
		d := todo[len(todo)-1]
		todo = todo[:len(todo)-1]
		if changed[d] {
			return true
		}
		for _, to := range edges[d] {
			if !seen[to] {
				seen[to] = true
				todo = append(todo, to)
			}
		}
	}
	return false
}
