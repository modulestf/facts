package facts

import (
	"bytes"
	"path"
	"slices"
	"sort"
	"strings"
)

// Block statuses: how the change treats one block address of an area.
const (
	BlockAdded     = "added"
	BlockEdited    = "edited"
	BlockRemoved   = "removed"
	BlockUntouched = "untouched"
)

// maxDiffCells bounds the line diff of one file: the product of the base and
// head line counts left after the common prefix and suffix are trimmed. A
// file past it has no changed line ranges; see ChangeMap.overLimit. At 4
// bytes a cell that is 16 MiB for the worst file.
const maxDiffCells = 1 << 22

// lineRange is a run of head lines, 1-based and inclusive.
type lineRange struct{ Start, End int }

// ChangeMap is what the change touches: the paths it adds, changes or
// removes, the changed head line ranges per file and the status of every
// block address per area. With no base tree every head path is added, every
// head line is changed and every block is added.
type ChangeMap struct {
	paths map[string]bool // added, changed or removed paths
	added map[string]bool // paths with no base entry
	// lines are the changed head line ranges of each head file in the change,
	// sorted. A file that is not a regular file at head has none.
	lines map[string][]lineRange
	// overLimit names the files whose diff passed maxDiffCells. Which of
	// their lines changed is unknown.
	overLimit map[string]bool
	// blocks is the status of every block address, per area.
	blocks map[string]map[string]string
	// spans are the top-level block spans of each head file, with the status
	// of their address.
	spans map[string][]blockSpan
}

type blockSpan struct {
	start, end int
	status     string
}

// BuildChangeMap compares the head and base trees, and the parsed head and
// base modules of the areas. base is nil when there is no base tree.
func BuildChangeMap(head, base *Tree, areas []*Area) *ChangeMap {
	ch := &ChangeMap{paths: map[string]bool{}, added: map[string]bool{}, lines: map[string][]lineRange{},
		overLimit: map[string]bool{}, blocks: map[string]map[string]string{}, spans: map[string][]blockSpan{}}
	for _, p := range head.Paths {
		h := head.Entries[p]
		var b *Entry
		if base != nil {
			b = base.Entries[p]
		}
		if sameEntry(h, b) {
			continue
		}
		ch.paths[p] = true
		if b == nil {
			ch.added[p] = true
		}
		if h.Kind != KindFile {
			continue
		}
		var old []byte
		if b != nil && b.Kind == KindFile {
			old = b.Data
		}
		if r, ok := changedLines(old, h.Data); ok {
			ch.lines[p] = r
		} else {
			ch.overLimit[p] = true
		}
	}
	if base != nil {
		for _, p := range base.Paths {
			if head.Entries[p] == nil {
				ch.paths[p] = true
			}
		}
	}
	for _, a := range areas {
		ch.addBlocks(a)
	}
	return ch
}

// addBlocks sets the status of every block address of an area. An address
// with several blocks, such as two terraform blocks, compares them as a
// multiset of hashes: they are edited together when any one differs.
func (ch *ChangeMap) addBlocks(a *Area) {
	headBlocks, baseBlocks := moduleBlocks(a.Head), moduleBlocks(a.Base)
	hashes := func(bs []addressedSpan) map[string][]string {
		m := map[string][]string{}
		for _, b := range bs {
			m[b.addr] = append(m[b.addr], b.Hash)
		}
		for _, h := range m {
			slices.Sort(h)
		}
		return m
	}
	hh, bh := hashes(headBlocks), hashes(baseBlocks)
	status := map[string]string{}
	for addr, h := range hh {
		switch b, ok := bh[addr]; {
		case a.Base == nil || !ok:
			status[addr] = BlockAdded
		case slices.Equal(h, b):
			status[addr] = BlockUntouched
		default:
			status[addr] = BlockEdited
		}
	}
	for addr := range bh {
		if _, ok := hh[addr]; !ok {
			status[addr] = BlockRemoved
		}
	}
	ch.blocks[a.Path] = status
	for _, b := range headBlocks {
		f := path.Join(a.Path, b.File)
		ch.spans[f] = append(ch.spans[f], blockSpan{start: b.Range.Start.Line, end: b.Range.End.Line, status: status[b.addr]})
	}
}

// BlockStatus returns the status of a block address in an area, or "" when
// neither revision has it.
func (ch *ChangeMap) BlockStatus(area, addr string) string { return ch.blocks[area][addr] }

type addressedSpan struct {
	Span
	addr string
}

// moduleBlocks lists the top-level blocks of a module with their addresses,
// as BuildIndex writes them. A Local is listed as well as its locals block.
func moduleBlocks(m *Module) []addressedSpan {
	if m == nil {
		return nil
	}
	var out []addressedSpan
	add := func(s Span, addr string) { out = append(out, addressedSpan{Span: s, addr: addr}) }
	for _, v := range m.Variables {
		add(v.Span, "var."+v.Name)
	}
	for _, o := range m.Outputs {
		add(o.Span, "output."+o.Name)
	}
	for _, l := range m.Locals {
		add(l.Span, "local."+l.Name)
	}
	for _, o := range m.Other {
		add(o.Span, strings.Join(append([]string{o.Type}, o.Labels...), "."))
	}
	for _, t := range m.Terraform {
		add(t.Span, "terraform")
	}
	for _, p := range m.Providers {
		addr := "provider." + p.Name
		if alias, ok := literalAlias(p.Body); ok {
			addr += "." + alias
		}
		add(p.Span, addr)
	}
	for _, r := range m.Resources {
		addr := r.Type + "." + r.Name
		if r.Mode == "data" {
			addr = "data." + addr
		}
		add(r.Span, addr)
	}
	for _, c := range m.ModuleCalls {
		add(c.Span, "module."+c.Name)
	}
	for _, mv := range m.Moved {
		add(mv.Span, "moved."+fromAddr(mv.FromAddr, mv.From))
	}
	for _, rm := range m.Removed {
		add(rm.Span, "removed."+fromAddr(rm.FromAddr, rm.From))
	}
	return out
}

// changedLines returns the head lines that are not in a longest common
// subsequence of the base and head lines, as sorted ranges. ok is false when
// the diff is past maxDiffCells. A file with no base has every line changed.
// Lines removed from base change no head line.
func changedLines(base, head []byte) (r []lineRange, ok bool) {
	o, h := splitLines(base), splitLines(head)
	p := 0
	for p < len(o) && p < len(h) && bytes.Equal(o[p], h[p]) {
		p++
	}
	s := 0
	for s < len(o)-p && s < len(h)-p && bytes.Equal(o[len(o)-1-s], h[len(h)-1-s]) {
		s++
	}
	o, h = o[p:len(o)-s], h[p:len(h)-s]
	n, m := len(o), len(h)
	if m == 0 {
		return nil, true
	}
	changed := make([]bool, m)
	if n == 0 {
		for j := range changed {
			changed[j] = true
		}
	} else {
		if n+1 > maxDiffCells/(m+1) {
			return nil, false
		}
		ids := map[string]int32{}
		id := func(l []byte) int32 {
			k, ok := ids[string(l)]
			if !ok {
				k = int32(len(ids)) // #nosec G115 -- at most n+m distinct lines, under maxDiffCells
				ids[string(l)] = k
			}
			return k
		}
		oi, hi := make([]int32, n), make([]int32, m)
		for i, l := range o {
			oi[i] = id(l)
		}
		for j, l := range h {
			hi[j] = id(l)
		}
		// lcs[i*(m+1)+j] is the LCS length of o[i:] and h[j:].
		w := m + 1
		lcs := make([]int32, (n+1)*w)
		for i := n - 1; i >= 0; i-- {
			for j := m - 1; j >= 0; j-- {
				if oi[i] == hi[j] {
					lcs[i*w+j] = lcs[(i+1)*w+j+1] + 1
				} else {
					lcs[i*w+j] = max(lcs[(i+1)*w+j], lcs[i*w+j+1])
				}
			}
		}
		i, j := 0, 0
		for i < n && j < m {
			switch {
			case oi[i] == hi[j]:
				i, j = i+1, j+1
			case lcs[(i+1)*w+j] >= lcs[i*w+j+1]:
				i++
			default:
				changed[j] = true
				j++
			}
		}
		for ; j < m; j++ {
			changed[j] = true
		}
	}
	for j, c := range changed {
		line := p + j + 1
		switch {
		case !c:
		case len(r) > 0 && r[len(r)-1].End == line-1:
			r[len(r)-1].End = line
		default:
			r = append(r, lineRange{line, line})
		}
	}
	return r, true
}

// splitLines splits text into lines without their newline. A final newline
// ends the last line and starts none.
func splitLines(b []byte) [][]byte {
	if len(b) == 0 {
		return nil
	}
	ls := bytes.Split(b, []byte("\n"))
	if len(ls[len(ls)-1]) == 0 {
		ls = ls[:len(ls)-1]
	}
	return ls
}

// LineChanged reports whether a head line of a file is in the change.
func (ch *ChangeMap) LineChanged(file string, line int) bool {
	r := ch.lines[file]
	i := sort.Search(len(r), func(i int) bool { return r[i].End >= line })
	return i < len(r) && r[i].Start <= line
}

// pathChanged reports whether p, or a path under p when p is a directory, is
// added, changed or removed.
func (ch *ChangeMap) pathChanged(p string) bool {
	if ch.paths[p] {
		return true
	}
	for q := range ch.paths {
		if p == "." || strings.HasPrefix(q, p+"/") {
			return true
		}
	}
	return false
}

// blockAt returns the status of the innermost top-level block of a head file
// that holds the line, or "" when no block does.
func (ch *ChangeMap) blockAt(file string, line int) string {
	best, size := "", -1
	for _, b := range ch.spans[file] {
		if line >= b.start && line <= b.end && (size < 0 || b.end-b.start < size) {
			best, size = b.status, b.end-b.start
		}
	}
	return best
}

// inScope reports whether a result at file and line is inside what a rule of
// the given scope judges:
//   - any-file-in-area: always.
//   - touched-expression: a changed line.
//   - touched-block: a line of an added or edited block.
//   - newly-introduced: a line of an added block.
//
// A line outside every block is in scope for those two when it is changed.
// A result with no line is in scope when its path is in the change, for
// newly-introduced only when the path is added. unknown is set when the
// answer needs the changed lines of a file whose diff passed the limit.
func (ch *ChangeMap) inScope(scope, file string, line *int) (in, unknown bool) {
	if scope == ScopeAnyFileInArea {
		return true, false
	}
	if line == nil {
		if scope == ScopeNewlyIntroduced {
			return ch.added[file], false
		}
		return ch.pathChanged(file), false
	}
	if scope == ScopeTouchedExpression {
		return ch.onChangedLine(file, line)
	}
	switch ch.blockAt(file, *line) {
	case "":
		return ch.onChangedLine(file, line)
	case BlockAdded:
		return true, false
	case BlockEdited:
		return scope == ScopeTouchedBlock, false
	}
	return false, false
}

// onChangedLine reports whether a result is on a changed head line, or, with
// no line, whether its path is in the change.
func (ch *ChangeMap) onChangedLine(file string, line *int) (in, unknown bool) {
	if line == nil {
		return ch.pathChanged(file), false
	}
	if ch.overLimit[file] {
		return false, true
	}
	return ch.LineChanged(file, *line), false
}
