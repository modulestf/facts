package behaviour

import (
	"maps"
	"path"
	"slices"
	"strings"

	"github.com/modulestf/facts/internal/facts"
)

// callerDirs are the top-level directories of the module root that hold
// caller code: configurations that call the module, as a user does, and that
// no module user runs.
var callerDirs = []string{"examples", "tests"}

// callerReason is the reason claims-eval gives a caller_code trigger.
const callerReason = "a literal argument of caller code under examples/ or tests/; no module user runs it"

// callerPath reports a clean .tf path below a caller directory, at any depth.
func callerPath(p string) bool {
	first, rest, ok := strings.Cut(p, "/")
	return ok && rest != "" && safeRelPath(p) && slices.Contains(callerDirs, first) && isConfigName(p)
}

// underCaller reports a path in or below a caller directory, by its exact
// name: the seal skips the code there.
func underCaller(p string) bool {
	first, _, _ := strings.Cut(p, "/")
	return slices.Contains(callerDirs, first)
}

// isCallerName reports a caller directory name in any letter case: a case
// insensitive file system loads Examples/ as examples/.
func isCallerName(seg string) bool {
	return slices.ContainsFunc(callerDirs, func(d string) bool { return strings.EqualFold(seg, d) })
}

// callerSealed reports whether no configuration outside the caller
// directories of the given tree can load caller code as a module, at either
// revision. Every entry outside them is a regular file, so no link leads
// into them; no OpenTofu file is there, which nothing reads; every directory
// there with a .tf file parses, a JSON one included (ParseDir refuses it);
// and every module source there is sealed (see sealedSource). A parse past
// the deadline breaks the seal. Configuration outside the tree, such as
// elsewhere in a repository the tree is a directory of, or a consumer that
// names a caller directory as a module, is not read.
func (c *classifier) callerSealed() bool {
	for _, t := range []*facts.Tree{c.base, c.head} {
		dirs := map[string]bool{}
		for _, p := range t.Paths {
			if underCaller(p) {
				continue
			}
			name := path.Base(p)
			if t.Entries[p].Kind != facts.KindFile || strings.HasSuffix(name, ".tofu") || strings.HasSuffix(name, ".tofu.json") {
				return false
			}
			if isConfigName(p) {
				dirs[path.Dir(p)] = true
			}
		}
		for _, dir := range slices.Sorted(maps.Keys(dirs)) {
			m, err := facts.ParseDir(t, dir, c.lim)
			if err != nil || len(m.Unsupported) > 0 {
				return false
			}
			for _, mc := range m.ModuleCalls {
				if !sealedSource(t, dir, mc.Source) {
					return false
				}
			}
		}
	}
	return true
}

// sealedSource reports a module source in dir of t that cannot name caller
// code. A source with a backslash, an absolute path or no literal value
// fails. A local source (./ or ../) must resolve to a directory of t outside
// the caller directories, in any letter case, so a link, a case variant or a
// missing directory fails. Any other source fails when a path segment names
// a caller directory in any letter case.
func sealedSource(t *facts.Tree, dir, src string) bool {
	if src == "" || strings.Contains(src, "\\") || strings.HasPrefix(src, "/") {
		return false
	}
	if strings.HasPrefix(src, "./") || strings.HasPrefix(src, "../") {
		target := path.Join(dir, src)
		first, _, _ := strings.Cut(target, "/")
		return (target == "." || t.Dirs[target]) && !isCallerName(first)
	}
	return !slices.ContainsFunc(strings.FieldsFunc(src, func(r rune) bool { return r == '/' || r == '?' || r == ':' || r == '=' }),
		isCallerName)
}

// markCallerCode sets caller_code on each literal argument trigger of a
// caller .tf file, every merged category a literal argument change too,
// when the file is a regular file modified in place at both revisions, no
// read site may read it, the read-site scan is complete and the caller
// directories are sealed (see callerSealed).
func (c *classifier) markCallerCode(ts []Trigger, changes []change, sc *scan) {
	modified := map[string]bool{}
	for _, ch := range changes {
		if ch.kind == KindModified && ch.h.Kind == facts.KindFile && ch.b.Kind == facts.KindFile {
			modified[ch.path] = true
		}
	}
	var want []int
	for i, t := range ts {
		if t.Category == CatLiteral && callerPath(t.File) && modified[t.File] && t.Follows == nil &&
			!slices.ContainsFunc(t.AlsoCategories, func(cat string) bool { return cat != CatLiteral }) {
			if _, reached := sc.reachedBy(t.File); !reached {
				want = append(want, i)
			}
		}
	}
	if len(want) == 0 || !sc.complete || !c.callerSealed() {
		return
	}
	for _, i := range want {
		ts[i].CallerCode = true
	}
}

// callerInert reports a trigger claims-eval reads as inert caller code: the
// classifier marked it, and its category, merged categories and file still
// say so. A follower never reaches it: its state follows other triggers'.
func callerInert(t Trigger) bool {
	return t.CallerCode && t.Category == CatLiteral && callerPath(t.File) &&
		!slices.ContainsFunc(t.AlsoCategories, func(cat string) bool { return cat != CatLiteral })
}
