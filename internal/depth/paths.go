package depth

import (
	"os"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/modulestf/facts/internal/facts"
)

// File forms Terraform reads.
const (
	formNone    = ""
	formConfig  = "config"   // .tf, .tf.json
	formVars    = "vars"     // .tfvars, .tfvars.json
	formLock    = "lock"     // .terraform.lock.hcl
	formCLI     = "cli"      // .terraformrc, terraform.rc, *.tfrc, *.tfrc.json
	formTest    = "test"     // .tftest.hcl, .tftest.json, .tfmock.hcl, .tfmock.json
	formHCLFile = "hcl-file" // any other .hcl
)

// tfForm returns the Terraform-read form of a path, or formNone. Matching
// is case-insensitive, so a name Terraform would skip on a case-sensitive
// file system is still read here.
func tfForm(p string) string {
	b := strings.ToLower(path.Base(p))
	switch {
	case b == ".terraformrc", b == "terraform.rc", strings.HasSuffix(b, ".tfrc"), strings.HasSuffix(b, ".tfrc.json"):
		return formCLI
	case b == ".terraform.lock.hcl":
		return formLock
	case strings.HasSuffix(b, ".tftest.hcl"), strings.HasSuffix(b, ".tftest.json"),
		strings.HasSuffix(b, ".tfmock.hcl"), strings.HasSuffix(b, ".tfmock.json"):
		return formTest
	case strings.HasSuffix(b, ".tf"), strings.HasSuffix(b, ".tf.json"):
		return formConfig
	case strings.HasSuffix(b, ".tfvars"), strings.HasSuffix(b, ".tfvars.json"):
		return formVars
	case strings.HasSuffix(b, ".hcl"):
		return formHCLFile
	}
	return formNone
}

// isJSON reports whether a Terraform-read path is in the JSON form.
func isJSON(p string) bool { return strings.HasSuffix(strings.ToLower(p), ".json") }

// isOverride reports whether a configuration file is an override file,
// which merges into other blocks and is not modeled.
func isOverride(p string) bool {
	b := strings.ToLower(path.Base(p))
	b = strings.TrimSuffix(strings.TrimSuffix(b, ".json"), ".tf")
	return b == "override" || strings.HasSuffix(b, "_override")
}

// docPath is light rule 1: Markdown or CHANGELOG anywhere, anything under a
// docs directory at any level, or LICENSE, .gitignore or .editorconfig.
func docPath(p string) bool {
	b := path.Base(p)
	switch {
	case strings.HasSuffix(b, ".md"), strings.HasPrefix(b, "CHANGELOG"),
		b == "LICENSE", b == ".gitignore", b == ".editorconfig":
		return true
	}
	return segment(path.Dir(p), "docs", false)
}

// ciPath is a CI, hook or release configuration path, compared case-folded.
func ciPath(p string) bool {
	l := strings.ToLower(p)
	b := path.Base(l)
	switch {
	case segment(path.Dir(l), ".github", true), segment(path.Dir(l), ".circleci", true),
		b == "action.yml", b == "action.yaml", b == ".pre-commit-config.yaml", b == ".pre-commit-hooks.yaml",
		strings.HasPrefix(b, ".releaserc"), strings.HasPrefix(b, "release.config."), b == ".gitlab-ci.yml":
		return true
	}
	return false
}

// wrappersPath reports a path with a wrappers directory segment.
func wrappersPath(p string) bool { return segment(path.Dir(p), "wrappers", true) }

// segment reports whether a directory path has a segment equal to name,
// case-folded when fold is set.
func segment(dir, name string, fold bool) bool {
	for s := range strings.SplitSeq(dir, "/") {
		if s == name || fold && strings.EqualFold(s, name) {
			return true
		}
	}
	return false
}

// safePath reports whether p is a canonical relative path with printable
// ASCII names: no empty, . or .. segment, no leading or trailing slash, no
// backslash, no control or non-ASCII byte.
func safePath(p string) bool {
	if p == "" || !utf8.ValidString(p) || strings.ContainsRune(p, '\\') {
		return false
	}
	for i := 0; i < len(p); i++ {
		if p[i] < 0x20 || p[i] > 0x7e {
			return false
		}
	}
	for s := range strings.SplitSeq(p, "/") {
		if s == "" || s == "." || s == ".." {
			return false
		}
	}
	return true
}

// ancestors returns the directories holding p, "." included.
func ancestors(p string) []string {
	var out []string
	for d := path.Dir(p); ; d = path.Dir(d) {
		out = append(out, d)
		if d == "." || d == "/" {
			return out
		}
	}
}

// outside reports whether a cleaned relative path leaves the tree.
func outside(p string) bool {
	return p == ".." || strings.HasPrefix(p, "../") || strings.HasPrefix(p, "/")
}

// maxLinkHops bounds the symbolic links one resolution follows.
const maxLinkHops = 40

// linker resolves symbolic links of the head tree. Link targets are read
// with os.Root, so a target is text only and never followed by the OS.
type linker struct {
	tree *facts.Tree
	root *os.Root
}

// resolve returns p with every symbolic link on it resolved. ok is false
// when a link cannot be read, is absolute, leaves the tree or loops.
func (l *linker) resolve(p string) (string, bool) {
	hops := 0
	for i := 0; ; {
		segs := strings.Split(p, "/")
		if p == "." || i >= len(segs) {
			return p, true
		}
		prefix := strings.Join(segs[:i+1], "/")
		e := l.tree.Entries[prefix]
		if e == nil || e.Kind != facts.KindSymlink {
			i++
			continue
		}
		hops++
		if hops > maxLinkHops || l.root == nil {
			return "", false
		}
		target, err := l.root.Readlink(prefix)
		if err != nil || target == "" || strings.HasPrefix(target, "/") {
			return "", false
		}
		next := path.Clean(path.Join(path.Dir(prefix), target, strings.Join(segs[i+1:], "/")))
		if outside(next) {
			return "", false
		}
		p, i = next, 0
	}
}
