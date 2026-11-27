package behaviour

import (
	"bytes"
	"path"
	"strings"

	"github.com/modulestf/facts/internal/facts"
)

// reasonNewExample is the reason of each trigger of a new example
// directory.
const reasonNewExample = "a file of an example directory the change adds, which no other configuration file names: " +
	"it changes no behaviour of the module, and its own plan is the evidence it works"

// configLike reports a name a Terraform, OpenTofu, test or wrapper tool may
// load as configuration, so its text may call a module directory.
func configLike(name string) bool {
	for _, s := range []string{".tf", ".tf.json", ".tofu", ".tofu.json", ".hcl", ".tftest.json", ".tfvars", ".tfvars.json"} {
		if strings.HasSuffix(name, s) {
			return true
		}
	}
	return false
}

// newExample reports whether dir is an example directory the change adds
// that nothing else can call: it is under examples/, no entry at or under
// it exists at the base, every change under it adds a file, and no
// configuration file outside it, at either revision, holds its last path
// segment. Any link, submodule pointer or configuration file whose content
// is not read, in either tree, keeps the triggers, and so does any doubt.
func (c *classifier) newExample(dir string, changes []change) bool {
	if !strings.HasPrefix(dir, "examples/") {
		return false
	}
	under := func(p string) bool { return strings.HasPrefix(p, dir+"/") }
	for _, ch := range changes {
		if under(ch.path) && ch.kind != KindAdded {
			return false
		}
	}
	name := []byte(path.Base(dir))
	for _, t := range []*facts.Tree{c.base, c.head} {
		if t.HasGitlinks() {
			return false
		}
		for _, p := range t.Paths {
			e := t.Entries[p]
			if t == c.base && under(p) {
				return false
			}
			if e.Kind == facts.KindSymlink {
				return false
			}
			if under(p) || !configLike(path.Base(p)) {
				continue
			}
			if e.Data == nil || bytes.Contains(e.Data, name) {
				return false
			}
		}
	}
	return true
}

// newExampleSpecs returns one trigger per changed .tf file of a new
// example directory. It selects no level, so it stays unverifiable.
func newExampleSpecs(dir string, changes []change) []spec {
	var out []spec
	for _, ch := range changes {
		if isConfigName(ch.path) && path.Dir(ch.path) == dir {
			out = append(out, spec{file: ch.path, cat: CatUnclassified, kind: ch.kind, noLevels: true, reason: reasonNewExample})
		}
	}
	return out
}
