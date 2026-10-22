package facts

import (
	"bytes"
	"path"
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

// Bounds of the line diff. maxDiffCells bounds one file: the product of the
// base and head line counts, each plus one, left after the common prefix
// and suffix are trimmed. At 4 bytes a cell that is 16 MiB for the worst
// file. maxDiffTotalCells bounds every file of one run together, so many
// files near the per-file cap cannot run long. A file past either has no
// changed line ranges; see ChangeMap.overLimit.
const (
	maxDiffCells      = 1 << 22
	maxDiffTotalCells = 1 << 24
)

// lineRange is a run of head lines, 1-based and inclusive.
type lineRange struct{ Start, End int }

// ChangeMap is what the change touches: the paths it adds, changes or
// removes, the changed head line ranges per file and the status of every
// block per area. With no base tree every head path is added, every head
// line is changed and every block is added.
type ChangeMap struct {
	base  *Tree           // nil with no base tree
	paths map[string]bool // added, changed or removed paths
	// lines are the changed head line ranges of each head file in the change,
	// sorted. A file that is not a regular file at head has none.
	lines map[string][]lineRange
	// overLimit names the files whose diff passed a bound. Which of their
	// lines changed is unknown.
	overLimit map[string]bool
	// blocks is the status of every block address, per area.
	blocks map[string]map[string]string
	// spans are the top-level block spans of each head file, each with its
	// own status.
	spans map[string][]blockSpan
	// index is the index of the areas in scope, for the innermost entry that
	// holds a line.
	index []IndexEntry
}

type blockSpan struct {
	start, end int
	status     string
}

// BuildChangeMap compares the head and base trees, and the parsed head and
// base modules of the areas. base is nil when there is no base tree. It
// stops with ErrDeadline when lim's deadline passes.
func BuildChangeMap(head, base *Tree, areas []*Area, lim Limits) (*ChangeMap, error) {
	ch := &ChangeMap{base: base, paths: map[string]bool{}, lines: map[string][]lineRange{},
		overLimit: map[string]bool{}, blocks: map[string]map[string]string{}, spans: map[string][]blockSpan{},
		index: BuildIndex(areas)}
	budget := maxDiffTotalCells
	for _, p := range head.Paths {
		if err := lim.expired(); err != nil {
			return nil, err
		}
		h := head.Entries[p]
		var b *Entry
		if base != nil {
			b = base.Entries[p]
		}
		if sameEntry(h, b) {
			continue
		}
		ch.paths[p] = true
		if h.Kind != KindFile {
			continue
		}
		var old []byte
		if b != nil && b.Kind == KindFile {
			old = b.Data
		}
		if r, ok := changedLines(old, h.Data, &budget); ok {
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
	return ch, nil
}

// addBlocks sets the status of every block of an area. Each head block of an
// address takes one base block of that address with the same hash, and is
// untouched when it finds one and edited when it does not; with no base
// block of that address it is added. An address is untouched when all its
// head blocks are and no base block is left, removed when it has no head
// block, and otherwise edited, or added when it has no base block.
func (ch *ChangeMap) addBlocks(a *Area) {
	left := map[string]map[string]int{} // address -> hash -> base blocks not yet taken
	if a.Base != nil {
		for _, b := range topBlocks(a.Base) {
			if left[b.addr] == nil {
				left[b.addr] = map[string]int{}
			}
			left[b.addr][b.Hash]++
		}
	}
	status := map[string]string{}
	for _, b := range topBlocks(a.Head) {
		hashes, atBase := left[b.addr]
		st := BlockAdded
		switch {
		case !atBase:
		case hashes[b.Hash] > 0:
			hashes[b.Hash]--
			st = BlockUntouched
		default:
			st = BlockEdited
		}
		if prev, ok := status[b.addr]; !ok || prev == BlockUntouched {
			status[b.addr] = st
		}
		f := path.Join(a.Path, b.File)
		ch.spans[f] = append(ch.spans[f], blockSpan{start: b.Range.Start.Line, end: b.Range.End.Line, status: st})
	}
	for addr, hashes := range left {
		_, atHead := status[addr]
		switch {
		case !atHead:
			status[addr] = BlockRemoved
		case status[addr] == BlockUntouched:
			for _, n := range hashes {
				if n > 0 {
					status[addr] = BlockEdited
				}
			}
		}
	}
	ch.blocks[a.Path] = status
}

// BlockStatus returns the status of a block address in an area, or "" when
// neither revision has it.
func (ch *ChangeMap) BlockStatus(area, addr string) string { return ch.blocks[area][addr] }

// changedLines returns the head lines that are not in a longest common
// subsequence of the base and head lines, as sorted ranges. ok is false when
// the diff is past maxDiffCells or the cells left in budget, which it
// lowers by the cells it uses. A file with no base has every line changed.
// Lines removed from base change no head line.
func changedLines(base, head []byte, budget *int) (r []lineRange, ok bool) {
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
		if n+1 > maxDiffCells/(m+1) || (n+1)*(m+1) > *budget {
			return nil, false
		}
		*budget -= (n + 1) * (m + 1)
		lcsChanged(o, h, changed)
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

// lcsChanged marks in changed the lines of h that are not in a longest
// common subsequence of o and h. Both are non-empty.
func lcsChanged(o, h [][]byte, changed []bool) {
	n, m := len(o), len(h)
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
	// lcs[i*w+j] is the LCS length of o[i:] and h[j:].
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

// rangeChanged reports whether any head line from start to end of a file is
// changed. unknown is set for a file past the diff bounds.
func (ch *ChangeMap) rangeChanged(file string, start, end int) (changed, unknown bool) {
	if ch.overLimit[file] {
		return false, true
	}
	r := ch.lines[file]
	i := sort.Search(len(r), func(i int) bool { return r[i].End >= start })
	return i < len(r) && r[i].Start <= end, false
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

// pathAdded reports whether the base has no file, link or directory at p.
func (ch *ChangeMap) pathAdded(p string) bool {
	return ch.base == nil || (ch.base.Entries[p] == nil && !ch.base.Dirs[p])
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

// entryAt returns the lines of the innermost index entry that holds the
// line, or the line alone when none does.
func (ch *ChangeMap) entryAt(file string, line int) (start, end int) {
	start, end = line, line
	span := -1
	for _, e := range ch.index {
		if e.File == file && line >= e.StartLine && line <= e.EndLine && (span < 0 || e.EndLine-e.StartLine <= span) {
			start, end, span = e.StartLine, e.EndLine, e.EndLine-e.StartLine
		}
	}
	return start, end
}

// inScope reports whether a result at file and line is inside what a rule of
// the given scope judges:
//   - any-file-in-area: always.
//   - touched-expression: a line whose innermost index entry, such as a
//     whole multi-line argument, has a changed line, in a block that is not
//     untouched. A block whose hash is unchanged is out, so a block moved to
//     another file is not judged again.
//   - touched-block: a line of an added or edited block.
//   - newly-introduced: a line of an added block.
//
// A line outside every block is in scope for the last two when it is
// changed. A result with no line is in scope when its path, or a path under
// it, is in the change; for newly-introduced only when the base has nothing
// at that path, so an added file or directory. A finding about a removed
// block has no head line, so it has a null line and is judged by its file.
// unknown is set when the answer needs the changed lines of a file past the
// diff bounds.
func (ch *ChangeMap) inScope(scope, file string, line *int) (in, unknown bool) {
	if scope == ScopeAnyFileInArea {
		return true, false
	}
	if line == nil {
		if scope == ScopeNewlyIntroduced {
			return ch.pathAdded(file), false
		}
		return ch.pathChanged(file), false
	}
	st := ch.blockAt(file, *line)
	if scope == ScopeTouchedExpression {
		if st == BlockUntouched {
			return false, false
		}
		s, e := ch.entryAt(file, *line)
		return ch.rangeChanged(file, s, e)
	}
	switch st {
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
	return ch.rangeChanged(file, *line, *line)
}
