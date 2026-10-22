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
	for _, b := range topBlocks(m) {
		out = append(out, IndexEntry{File: b.File, StartLine: b.Range.Start.Line, EndLine: b.Range.End.Line, Subject: b.addr})
		for _, in := range b.inner {
			out = append(out, IndexEntry{File: b.File, StartLine: in.Range.Start.Line, EndLine: in.Range.End.Line, Subject: in.addr})
		}
		if b.body != nil {
			out = bodyIndex(out, b.File, *b.body, b.addr)
		}
	}
	return out
}

// topBlock is one top-level block of a module with its address. inner are
// the entries indexed inside it that are not in a body: the object
// attributes of a variable's type, and the arguments, nested blocks and
// required_providers entries of a terraform block. body, when set, is
// indexed under the address.
//
// args, set for a variable or an output, are its arguments and validation
// blocks. BuildIndex leaves them out, since var.<name>.<arg> would read as
// an object attribute of the type; the change map uses them to find the
// innermost argument that holds a line.
type topBlock struct {
	Span
	addr  string
	inner []innerEntry
	body  *Body
	args  *Body
}

type innerEntry struct {
	Range hcl.Range
	addr  string
}

// topBlocks lists the top-level blocks of a module in index order. A Local
// is listed as well as its locals block. It is the one place the address
// rules of top-level blocks live; see BuildIndex.
func topBlocks(m *Module) []topBlock {
	if m == nil {
		return nil
	}
	var out []topBlock
	for _, v := range m.Variables {
		b := topBlock{Span: v.Span, addr: "var." + v.Name, args: &v.Body}
		for _, ta := range v.TypeAttrs {
			b.inner = append(b.inner, innerEntry{ta.Range, b.addr + "." + ta.Path})
		}
		out = append(out, b)
	}
	for _, o := range m.Outputs {
		out = append(out, topBlock{Span: o.Span, addr: "output." + o.Name, args: &o.Body})
	}
	for _, o := range m.Other {
		out = append(out, topBlock{Span: o.Span, addr: strings.Join(append([]string{o.Type}, o.Labels...), "."), body: &o.Body})
	}
	for _, l := range m.Locals {
		out = append(out, topBlock{Span: l.Span, addr: "local." + l.Name})
	}
	for _, t := range m.Terraform {
		b := topBlock{Span: t.Span, addr: "terraform"}
		for _, a := range t.Body.Attributes {
			b.inner = append(b.inner, innerEntry{a.Range, a.Name})
		}
		for _, nb := range t.Body.Blocks {
			b.inner = append(b.inner, innerEntry{nb.Range, nb.Type})
		}
		for _, rp := range t.RequiredProviders {
			b.inner = append(b.inner, innerEntry{rp.Range, "required_providers." + rp.Name})
		}
		out = append(out, b)
	}
	for _, p := range m.Providers {
		addr := "provider." + p.Name
		if alias, ok := literalAlias(p.Body); ok {
			addr += "." + alias
		}
		out = append(out, topBlock{Span: p.Span, addr: addr, body: &p.Body})
	}
	for _, r := range m.Resources {
		addr := r.Type + "." + r.Name
		if r.Mode == "data" {
			addr = "data." + addr
		}
		out = append(out, topBlock{Span: r.Span, addr: addr, body: &r.Body})
	}
	for _, c := range m.ModuleCalls {
		out = append(out, topBlock{Span: c.Span, addr: "module." + c.Name, body: &c.Body})
	}
	for _, mv := range m.Moved {
		out = append(out, topBlock{Span: mv.Span, addr: "moved." + fromAddr(mv.FromAddr, mv.From)})
	}
	for _, rm := range m.Removed {
		out = append(out, topBlock{Span: rm.Span, addr: "removed." + fromAddr(rm.FromAddr, rm.From)})
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
