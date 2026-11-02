package depth

import (
	"path"
	"regexp"
	"slices"
	"strings"
)

// Patterns of the schema-pass tokens: see schema-pass.md, Inputs, in the
// skills repository. A token that fails one is not written.
var (
	schemaNameRe    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
	schemaLocalRe   = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`)
	schemaVersionRe = regexp.MustCompile(`^[0-9]{1,5}\.[0-9]{1,5}\.[0-9]{1,5}$`)
	schemaTypeRe    = regexp.MustCompile(`^[a-z][a-z0-9_]{0,127}$`)
	schemaSegRe     = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
	schemaConRe     = regexp.MustCompile(`^[0-9A-Za-z.,<>=~! -]{0,64}$`)
)

const (
	defaultRegistry = "registry.terraform.io"
	builtinSource   = "terraform.io/builtin/terraform"
)

// schemaDir reports a directory that fits the token pattern.
func schemaDir(d string) bool {
	if d == "." {
		return true
	}
	segs := strings.Split(d, "/")
	if len(segs) > 8 {
		return false
	}
	for _, s := range segs {
		if s == "." || s == ".." || !schemaSegRe.MatchString(s) {
			return false
		}
	}
	return true
}

// providerSource normalizes a provider source address. host is the
// registry host; ok is false for an address that cannot be read.
func providerSource(s string) (host, ns, name string, ok bool) {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(s)), "/")
	switch len(parts) {
	case 1:
		return defaultRegistry, "hashicorp", parts[0], parts[0] != ""
	case 2:
		return defaultRegistry, parts[0], parts[1], parts[0] != "" && parts[1] != ""
	case 3:
		return parts[0], parts[1], parts[2], parts[0] != "" && parts[1] != "" && parts[2] != ""
	}
	return "", "", "", false
}

// sources returns each local name's source of a module, and false when
// required_providers cannot be read or a local name has two sources.
func (mod *module) sources() (map[string]string, bool) {
	out := map[string]string{}
	if mod.reqBad {
		return out, false
	}
	for _, rp := range mod.req {
		src := rp.local
		if rp.hasSource {
			src = rp.source
		}
		host, ns, name, ok := providerSource(src)
		if !ok {
			return out, false
		}
		norm := host + "/" + ns + "/" + name
		if prev, seen := out[rp.local]; seen && prev != norm {
			return out, false
		}
		out[rp.local] = norm
	}
	return out, true
}

// schemaProviders returns the provider tokens of one revision.
func schemaProviders(rev string, m *model) []string {
	var out []string
	for _, d := range m.configDirs() {
		mod := m.mods[d]
		if !schemaDir(d) || len(mod.req) == 0 && !mod.reqBad {
			continue
		}
		srcs, ok := mod.sources()
		cons := map[string][]string{}
		var locals []string
		for _, rp := range mod.req {
			if !slices.Contains(locals, rp.local) {
				locals = append(locals, rp.local)
			}
			if rp.hasVersion {
				cons[rp.local] = append(cons[rp.local], strings.ReplaceAll(rp.version, " ", ""))
			}
		}
		var toks []string
		for _, l := range locals {
			c := strings.Join(cons[l], ",")
			if !schemaConRe.MatchString(c) {
				ok = false
				break
			}
			if !schemaLocalRe.MatchString(l) {
				continue
			}
			host, ns, name, _ := providerSource(srcs[l])
			switch {
			case host != defaultRegistry:
				toks = append(toks, rev+":"+d+":"+l+"=!registry_host")
			case schemaNameRe.MatchString(ns) && schemaNameRe.MatchString(name):
				toks = append(toks, rev+":"+d+":"+l+"="+ns+"/"+name+":"+c)
			}
		}
		if !ok {
			toks = []string{rev + ":" + d + ":!constraint_unparsable"}
		}
		out = append(out, toks...)
	}
	return out
}

// schemaTypes returns a token per resource and data block of the head,
// bound to its provider's source.
func schemaTypes(m *model) []string {
	var out []string
	for _, d := range m.configDirs() {
		mod := m.mods[d]
		if !schemaDir(d) {
			continue
		}
		srcs, _ := mod.sources()
		for _, it := range mod.items {
			if it.kind != kResource && it.kind != kData {
				continue
			}
			local, _, _ := strings.Cut(it.typ, "_")
			if a := it.arg("provider"); a != nil {
				if len(a.exprs) != 1 {
					continue
				}
				vars := a.exprs[0].Variables()
				if len(vars) != 1 {
					continue
				}
				local = vars[0].RootName()
			}
			src, ok := srcs[local]
			if !ok {
				src = defaultRegistry + "/hashicorp/" + local
				if local == "terraform" {
					src = builtinSource
				}
			}
			if src == builtinSource {
				continue
			}
			host, ns, name, ok := providerSource(src)
			if !ok || host != defaultRegistry || !schemaNameRe.MatchString(ns) || !schemaNameRe.MatchString(name) ||
				!schemaTypeRe.MatchString(it.typ) {
				continue
			}
			out = append(out, d+":"+it.kind+":"+it.typ+":"+ns+"/"+name)
		}
	}
	return sortedUnique(out)
}

// schemaLocks returns a token per provider of each head lock file.
func schemaLocks(m *model) []string {
	var out []string
	for p, fm := range m.files {
		d := path.Dir(p)
		if fm.form != formLock || !schemaDir(d) {
			continue
		}
		for _, it := range fm.items {
			if it.kind != kFileBlock || it.addr != "provider" || len(it.labels) != 1 {
				continue
			}
			host, ns, name, ok := providerSource(it.labels[0])
			va := it.arg("version")
			if !ok || host != defaultRegistry || va == nil || len(va.exprs) != 1 {
				continue
			}
			v, lit := litString(va.exprs[0])
			if !lit || !schemaVersionRe.MatchString(v) || !schemaNameRe.MatchString(ns) || !schemaNameRe.MatchString(name) {
				continue
			}
			out = append(out, d+":"+ns+"/"+name+"="+v)
		}
	}
	return sortedUnique(out)
}

func sortedUnique(s []string) []string {
	if s == nil {
		return []string{}
	}
	slices.Sort(s)
	return slices.Compact(s)
}
