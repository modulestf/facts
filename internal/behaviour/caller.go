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

// underCaller reports a path in or below a caller directory.
func underCaller(p string) bool {
	first, _, _ := strings.Cut(p, "/")
	return slices.Contains(callerDirs, first)
}

// callerSealed reports whether no configuration outside the caller
// directories can load caller code as a module, at either revision: every
// directory outside them with a .tf file parses, every module source there
// is a literal, a local source stays in the tree and names no caller
// directory, and no other source names one as a path segment. A JSON or
// OpenTofu configuration file outside them, which the engine does not
// parse, breaks the seal.
func (c *classifier) callerSealed() (bool, error) {
	for _, t := range []*facts.Tree{c.base, c.head} {
		dirs := map[string]bool{}
		for _, p := range t.Paths {
			if underCaller(p) {
				continue
			}
			name := path.Base(p)
			if strings.HasSuffix(name, ".tf.json") || strings.HasSuffix(name, ".tofu") || strings.HasSuffix(name, ".tofu.json") {
				return false, nil
			}
			if isConfigName(p) {
				dirs[path.Dir(p)] = true
			}
		}
		for _, dir := range slices.Sorted(maps.Keys(dirs)) {
			m, err := facts.ParseDir(t, dir, c.lim)
			if err != nil {
				return false, err
			}
			if len(m.Unsupported) > 0 {
				return false, nil
			}
			for _, mc := range m.ModuleCalls {
				if !sealedSource(dir, mc.Source) {
					return false, nil
				}
			}
		}
	}
	return true, nil
}

// sealedSource reports a module source in dir that cannot name caller code.
func sealedSource(dir, src string) bool {
	if src == "" {
		return false
	}
	if strings.HasPrefix(src, "./") || strings.HasPrefix(src, "../") {
		target := path.Join(dir, src)
		return target != ".." && !strings.HasPrefix(target, "../") && !underCaller(target)
	}
	return !slices.ContainsFunc(strings.FieldsFunc(src, func(r rune) bool { return r == '/' || r == '?' || r == ':' }),
		func(seg string) bool { return slices.Contains(callerDirs, seg) })
}

// markCallerCode sets caller_code on each literal argument trigger of a
// caller .tf file, every merged category a literal argument change too,
// when the file is a regular file modified in place at both revisions, no
// read site may read it, the read-site scan is complete and the caller
// directories are sealed (see callerSealed).
func (c *classifier) markCallerCode(ts []Trigger, changes []change, sc *scan) error {
	modified := map[string]bool{}
	for _, ch := range changes {
		if ch.kind == KindModified && ch.prev == "" && ch.h.Kind == facts.KindFile && ch.b.Kind == facts.KindFile {
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
	if len(want) == 0 || !sc.complete {
		return nil
	}
	sealed, err := c.callerSealed()
	if err != nil || !sealed {
		return err
	}
	for _, i := range want {
		ts[i].CallerCode = true
	}
	return nil
}

// callerInert reports a trigger claims-eval reads as inert caller code: the
// classifier marked it, and its category, merged categories and file still
// say so. A follower never reaches it: its state follows other triggers'.
func callerInert(t Trigger) bool {
	return t.CallerCode && t.Category == CatLiteral && callerPath(t.File) &&
		!slices.ContainsFunc(t.AlsoCategories, func(cat string) bool { return cat != CatLiteral })
}
