package facts

import (
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/hashicorp/hcl/v2"
)

// BuildIndex maps the head lines of every block and argument of the areas in
// scope to their area-scoped subject. The host derives the subject of a lead
// finding from its file and line with SubjectAt. Untouched areas are left
// out: a finding lands only on lines in the change.
//
// Addresses follow the subjects the rules write: var.<name>, with
// var.<name>.<attr> for an object attribute of its type; output.<name>;
// local.<name>; provider.<name> or provider.<name>.<alias>; required_version
// and required_providers.<name>; module.<name>; <type>.<name> or
// data.<type>.<name> for a resource, with .<arg> for an argument and
// .<block>[i] for the i-th static nested block of that type, or .<block> for a
// dynamic block. Every dynamic block of one type shares that subject, and the
// static counter skips dynamic blocks, so a static block keeps its index when
// a dynamic one is added beside it. A block with no address of its own is
// named by its type: terraform, locals; moved and removed add their from
// address, as written with whitespace collapsed when it is not static; any
// other block, such as check or import, adds its labels and indexes its body.
//
// The entries are sorted by file, then start line, then end line descending.
// Entries with the same lines, such as a one-line block and the argument in
// it, keep their nesting order, outer first: the later entry is the inner
// one and names those lines.
func BuildIndex(areas []*Area) []IndexEntry {
	var out []IndexEntry
	for _, a := range areas {
		if a.Untouched || a.Head == nil {
			continue
		}
		start := len(out)
		out = moduleIndex(out, a.Head)
		for i := start; i < len(out); i++ {
			out[i].File = path.Join(a.Path, out[i].File)
			out[i].Subject = subject(a.Path, out[i].Subject)
		}
	}
	// Stable: of two equal ranges the outer one, added first, stays first.
	sort.SliceStable(out, func(i, j int) bool {
		x, y := out[i], out[j]
		if x.File != y.File {
			return x.File < y.File
		}
		if x.StartLine != y.StartLine {
			return x.StartLine < y.StartLine
		}
		return x.EndLine > y.EndLine
	})
	return out
}

// SubjectAt returns the subject of the innermost entry that holds the line,
// or "" when none does. Of two entries with the same lines, the later one,
// the inner, wins.
func SubjectAt(index []IndexEntry, file string, line int) string {
	best, span := "", -1
	for _, e := range index {
		if e.File != file || line < e.StartLine || line > e.EndLine {
			continue
		}
		if s := e.EndLine - e.StartLine; span < 0 || s <= span {
			best, span = e.Subject, s
		}
	}
	return best
}

// moduleIndex appends the entries of one module, with file names relative to
// its directory and addresses without the area.
func moduleIndex(out []IndexEntry, m *Module) []IndexEntry {
	add := func(file string, r hcl.Range, addr string) {
		out = append(out, IndexEntry{File: file, StartLine: r.Start.Line, EndLine: r.End.Line, Subject: addr})
	}
	for _, v := range m.Variables {
		addr := "var." + v.Name
		add(v.File, v.Range, addr)
		for _, ta := range v.TypeAttrs {
			add(v.File, ta.Range, addr+"."+ta.Path)
		}
	}
	for _, o := range m.Outputs {
		add(o.File, o.Range, "output."+o.Name)
	}
	for _, o := range m.Other {
		addr := strings.Join(append([]string{o.Type}, o.Labels...), ".")
		add(o.File, o.Range, addr)
		out = bodyIndex(out, o.File, o.Body, addr)
	}
	for _, l := range m.Locals {
		add(l.File, l.Range, "local."+l.Name)
	}
	for _, t := range m.Terraform {
		add(t.File, t.Range, "terraform")
		for _, a := range t.Body.Attributes {
			add(t.File, a.Range, a.Name)
		}
		for _, nb := range t.Body.Blocks {
			add(t.File, nb.Range, nb.Type)
		}
		for _, rp := range t.RequiredProviders {
			add(t.File, rp.Range, "required_providers."+rp.Name)
		}
	}
	for _, p := range m.Providers {
		addr := "provider." + p.Name
		if alias, ok := literalAlias(p.Body); ok {
			addr += "." + alias
		}
		add(p.File, p.Range, addr)
		out = bodyIndex(out, p.File, p.Body, addr)
	}
	for _, r := range m.Resources {
		addr := r.Type + "." + r.Name
		if r.Mode == "data" {
			addr = "data." + addr
		}
		add(r.File, r.Range, addr)
		out = bodyIndex(out, r.File, r.Body, addr)
	}
	for _, c := range m.ModuleCalls {
		addr := "module." + c.Name
		add(c.File, c.Range, addr)
		out = bodyIndex(out, c.File, c.Body, addr)
	}
	for _, mv := range m.Moved {
		add(mv.File, mv.Range, "moved."+fromAddr(mv.FromAddr, mv.From))
	}
	for _, rm := range m.Removed {
		add(rm.File, rm.Range, "removed."+fromAddr(rm.FromAddr, rm.From))
	}
	return out
}

// bodyIndex appends an entry per argument and nested block of a body.
func bodyIndex(out []IndexEntry, file string, b Body, addr string) []IndexEntry {
	for _, a := range b.Attributes {
		out = append(out, IndexEntry{File: file, StartLine: a.Range.Start.Line, EndLine: a.Range.End.Line, Subject: addr + "." + a.Name})
	}
	seen := map[string]int{}
	for _, nb := range b.Blocks {
		sub := addr + "." + nb.Type
		if !nb.Dynamic {
			sub += "[" + strconv.Itoa(seen[nb.Type]) + "]"
			seen[nb.Type]++
		}
		out = append(out, IndexEntry{File: file, StartLine: nb.Range.Start.Line, EndLine: nb.Range.End.Line, Subject: sub})
		out = bodyIndex(out, file, nb.Body, sub)
	}
	return out
}

// literalAlias returns a provider alias written as a plain string.
func literalAlias(b Body) (string, bool) {
	e := b.Arg("alias")
	if e == nil {
		return "", false
	}
	s, err := strconv.Unquote(e.Text)
	if err != nil || s == "" || strings.ContainsAny(s, "${}") {
		return "", false
	}
	return s, true
}

// fromAddr returns the static address, or else the source text with its
// whitespace collapsed, so a line break in the expression stays out of the
// subject.
func fromAddr(addr, text string) string {
	if addr != "" {
		return addr
	}
	return strings.Join(strings.Fields(text), " ")
}
