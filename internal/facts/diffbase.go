package facts

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Diff formats ReadDiffBase reads.
const (
	DiffFormatGitHubFiles = "github-files" // the pull request files list of the GitHub REST API
	DiffFormatUnified     = "unified"      // a unified diff, as git diff writes it
)

// maxDiffBytes caps the diff file.
const maxDiffBytes = 32 << 20

// binaryProbe is how much of a file git reads to tell text from binary.
const binaryProbe = 8000

// DiffInput binds a report to the diff its base tree was rebuilt from.
type DiffInput struct {
	Format string `json:"format"`
	SHA256 string `json:"sha256"`
}

// fileChange is one file of a diff. patch is nil when the diff has no patch
// for the file, and empty when it shows no line, as for a pure rename.
// oldMode is the base mode a unified diff gives in an "old mode" or
// "deleted file mode" line, or "" when it gives none.
type fileChange struct {
	path, prev, status string
	patch              []byte
	oldMode            string
}

// ReadDiffBase rebuilds the tree a change started from out of the head tree
// and the change as a diff: a GitHub pull request files list or a unified
// diff. Each file the diff changes gets its base content by reversing its
// hunks against the head file; every other file is the head's. A file the
// diff has no patch for, such as a binary or very large one, is KindNoPatch at
// the base, so its content there is unknown. A diff that does not fit the
// head tree is an error.
func ReadDiffBase(head *Tree, file string, lim Limits) (*Tree, *DiffInput, error) {
	data, err := readCapped(file, maxDiffBytes)
	if err != nil {
		return nil, nil, fmt.Errorf("diff: %w", err)
	}
	base, format, err := baseFromDiff(head, data, lim)
	if err != nil {
		return nil, nil, fmt.Errorf("diff: %w", err)
	}
	sum := sha256.Sum256(data)
	return base, &DiffInput{Format: format, SHA256: hex.EncodeToString(sum[:])}, nil
}

// readCapped reads a regular file, not a link, of at most limit bytes.
func readCapped(p string, limit int64) ([]byte, error) {
	if st, err := os.Lstat(p); err != nil || !st.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", p)
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("over %d bytes", limit)
	}
	return data, nil
}

func baseFromDiff(head *Tree, data []byte, lim Limits) (*Tree, string, error) {
	var (
		changes []fileChange
		format  string
		err     error
	)
	switch t := bytes.TrimSpace(data); {
	case len(t) > 0 && t[0] == '[':
		format = DiffFormatGitHubFiles
		changes, err = parseGitHubFiles(data)
	case len(t) == 0 || unifiedStart(t):
		format = DiffFormatUnified
		changes, err = parseUnified(data)
	default:
		return nil, "", errors.New("neither a GitHub files list nor a unified diff")
	}
	if err != nil {
		return nil, "", err
	}
	if len(changes) > lim.MaxFiles {
		return nil, "", fmt.Errorf("more than %d files", lim.MaxFiles)
	}
	base, err := applyChanges(head, changes, lim)
	return base, format, err
}

// ghFileEntry is the part of a files list entry the rebuild reads.
type ghFileEntry struct {
	Filename         string  `json:"filename"`
	Status           string  `json:"status"`
	PreviousFilename string  `json:"previous_filename"`
	Patch            *string `json:"patch"`
	Additions        int     `json:"additions"`
	Deletions        int     `json:"deletions"`
}

// parseGitHubFiles reads a files list. A renamed entry with no patch and no
// added or removed line is a pure rename when the head file is text; GitHub
// counts a binary file 0 and 0 too, so applyChanges checks the content.
func parseGitHubFiles(data []byte) ([]fileChange, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	var list []ghFileEntry
	if err := dec.Decode(&list); err != nil {
		return nil, fmt.Errorf("files list: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("files list: more than one JSON value")
	}
	out := make([]fileChange, 0, len(list))
	for _, e := range list {
		c := fileChange{path: e.Filename, prev: e.PreviousFilename, status: e.Status}
		switch {
		case e.Patch != nil:
			c.patch = []byte(*e.Patch)
		case e.Status == "renamed" && e.Additions == 0 && e.Deletions == 0:
			c.status = statusPureRename
		case (e.Status == "modified" || e.Status == "changed") && e.Additions == 0 && e.Deletions == 0:
			c.status = statusModeOnly
		}
		out = append(out, c)
	}
	return out, nil
}

// statusPureRename marks a rename the diff shows with no line. For a files
// list it still needs a text file at head; see applyChanges.
const statusPureRename = "pure-rename"

// statusModeOnly marks a files list entry changed with no patch and no added
// or removed line: for a text file at head only its mode can have changed.
// A binary one may have changed content too; see rebuildEntry.
const statusModeOnly = "mode-only"

func unifiedStart(t []byte) bool {
	return bytes.HasPrefix(t, []byte("diff --git ")) || bytes.Contains(t, []byte("\n--- ")) ||
		bytes.Contains(t, []byte("\ndiff --git ")) || bytes.HasPrefix(t, []byte("--- "))
}

var hunkHeader = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// parseUnified splits a unified diff into files. A file starts at a
// "diff --git" line, or at a "---" line outside a file's hunks; lines before
// the first file are a preamble and skipped. A file with no hunk and no binary
// marker shows no line: its content did not change, or it is empty.
func parseUnified(data []byte) ([]fileChange, error) {
	lines := strings.Split(string(data), "\n")
	var (
		out   []fileChange
		cur   *fileChange
		sides bool // cur has had its ---/+++ pair
	)
	flush := func() error {
		sides = false
		if cur == nil {
			return nil
		}
		if cur.path == "" {
			return errors.New("unified diff: a file with no path")
		}
		if cur.status == "" {
			cur.status = "modified"
			if cur.prev != "" && cur.prev != cur.path {
				cur.status = "renamed"
			}
		}
		out = append(out, *cur)
		cur = nil
		return nil
	}
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		switch {
		case strings.HasPrefix(l, "diff --git "):
			if err := flush(); err != nil {
				return nil, err
			}
			p, err := gitHeaderPath(strings.TrimPrefix(l, "diff --git "))
			if err != nil {
				return nil, err
			}
			cur = &fileChange{path: p, prev: p, patch: []byte{}}
		case strings.HasPrefix(l, "--- ") && i+1 < len(lines) && strings.HasPrefix(lines[i+1], "+++ "):
			if cur == nil || sides {
				if err := flush(); err != nil {
					return nil, err
				}
				cur = &fileChange{patch: []byte{}}
			}
			sides = true
			oldP, newP := sidePath(l[4:], "a/"), sidePath(lines[i+1][4:], "b/")
			i++
			switch {
			case oldP == "/dev/null":
				cur.status, cur.path, cur.prev = "added", newP, ""
			case newP == "/dev/null":
				cur.status, cur.path, cur.prev = "removed", oldP, ""
			default:
				cur.path, cur.prev = newP, oldP
			}
		case cur == nil:
			// preamble
		case strings.HasPrefix(l, "@@"):
			n, err := hunkLines(lines[i:])
			if err != nil {
				return nil, fmt.Errorf("unified diff: %s: %w", cur.path, err)
			}
			cur.patch = append(cur.patch, strings.Join(lines[i:i+n], "\n")+"\n"...)
			i += n - 1
		case strings.HasPrefix(l, "new file mode"):
			cur.status, cur.prev = "added", ""
		case strings.HasPrefix(l, "deleted file mode "):
			cur.status, cur.prev = "removed", ""
			cur.oldMode = strings.TrimPrefix(l, "deleted file mode ")
		case strings.HasPrefix(l, "old mode "):
			cur.oldMode = strings.TrimPrefix(l, "old mode ")
		case strings.HasPrefix(l, "rename from "), strings.HasPrefix(l, "copy from "):
			cur.prev = l[strings.Index(l, "from ")+5:]
			if strings.HasPrefix(l, "copy") {
				cur.status = "copied"
			}
		case strings.HasPrefix(l, "rename to "), strings.HasPrefix(l, "copy to "):
			cur.path = l[strings.Index(l, "to ")+3:]
		case strings.HasPrefix(l, "Binary files "), l == "GIT binary patch":
			cur.patch = nil
		}
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return out, nil
}

// gitHeaderPath reads the path of a "diff --git a/<p> b/<p>" line, the form
// git writes when the two sides are the same path; a rename names its paths
// in later lines. Quoted paths are refused: they hold characters a path in a
// module should not.
func gitHeaderPath(s string) (string, error) {
	if strings.HasPrefix(s, `"`) {
		return "", fmt.Errorf("unified diff: unsafe path in %q", s)
	}
	half := (len(s) - 1) / 2
	if len(s)%2 == 0 || s[half] != ' ' || !strings.HasPrefix(s, "a/") || "b/"+s[2:half] != s[half+1:] {
		return "", nil // a rename or copy: its own lines name both paths
	}
	return s[2:half], nil
}

// sidePath is the path of a ---/+++ line: the prefix stripped, and any
// timestamp after a tab dropped.
func sidePath(s, prefix string) string {
	s, _, _ = strings.Cut(s, "\t")
	if s == "/dev/null" {
		return s
	}
	return strings.TrimPrefix(s, prefix)
}

// hunkLines returns how many lines, from its header, a hunk spans: the counts
// in its header say how many body lines follow, plus "\ No newline" markers.
func hunkLines(ls []string) (int, error) {
	h, err := parseHunkHeader(ls[0])
	if err != nil {
		return 0, err
	}
	oldLeft, newLeft, n := h.oldCount, h.newCount, 1
	for (oldLeft > 0 || newLeft > 0 || n < len(ls) && strings.HasPrefix(ls[n], `\`)) && n < len(ls) {
		switch l := ls[n]; {
		case strings.HasPrefix(l, `\`):
		case strings.HasPrefix(l, " "):
			oldLeft--
			newLeft--
		case strings.HasPrefix(l, "-"):
			oldLeft--
		case strings.HasPrefix(l, "+"):
			newLeft--
		default:
			return 0, fmt.Errorf("hunk line %q", l)
		}
		n++
	}
	if oldLeft != 0 || newLeft != 0 {
		return 0, errors.New("hunk shorter than its header says")
	}
	return n, nil
}

type hunk struct {
	oldStart, oldCount, newStart, newCount int
}

func parseHunkHeader(l string) (hunk, error) {
	m := hunkHeader.FindStringSubmatch(l)
	if m == nil {
		return hunk{}, fmt.Errorf("not a hunk header: %q", l)
	}
	num := func(s string) int {
		if s == "" {
			return 1
		}
		n, err := strconv.Atoi(s)
		if err != nil {
			return -1
		}
		return n
	}
	h := hunk{num(m[1]), num(m[2]), num(m[3]), num(m[4])}
	if h.oldStart < 0 || h.oldCount < 0 || h.newStart < 0 || h.newCount < 0 {
		return hunk{}, fmt.Errorf("hunk header out of range: %q", l)
	}
	return h, nil
}

// applyChanges builds the base tree: the head's entries, less every path the
// change adds or renames away, plus the base content of every path it
// changes, renames from or removes.
func applyChanges(head *Tree, changes []fileChange, lim Limits) (*Tree, error) {
	entries := maps.Clone(head.Entries)
	seenHead, seenBase := map[string]bool{}, map[string]bool{}
	type rebuild struct {
		at string
		c  fileChange
	}
	var todo []rebuild
	for _, c := range changes {
		for _, p := range []string{c.path, c.prev} {
			if p != "" && !safeDiffPath(p) {
				return nil, fmt.Errorf("unsafe path %q", p)
			}
		}
		if skippedPath(c.path) || c.prev != "" && skippedPath(c.prev) {
			continue
		}
		if seenHead[c.path] {
			return nil, fmt.Errorf("%s is in the diff more than once", c.path)
		}
		seenHead[c.path] = true
		at := c.path
		switch c.status {
		case "added", "copied":
			at = ""
		case "removed":
			if head.Entries[c.path] != nil {
				return nil, fmt.Errorf("%s is removed by the diff but still in the head tree", c.path)
			}
		case "modified", "changed", statusModeOnly:
		case "renamed", statusPureRename:
			if c.prev == "" {
				return nil, fmt.Errorf("%s is renamed with no previous path", c.path)
			}
			at = c.prev
		case "unchanged":
			continue
		default:
			return nil, fmt.Errorf("%s: unknown status %q", c.path, c.status)
		}
		if c.status != "removed" && head.Entries[c.path] == nil {
			return nil, fmt.Errorf("%s is in the diff but not in the head tree", c.path)
		}
		if c.status != "modified" && c.status != "changed" && c.status != statusModeOnly {
			delete(entries, c.path) // added or renamed away; a rebuild below may put it back
		}
		if at == "" {
			continue
		}
		if seenBase[at] {
			return nil, fmt.Errorf("%s is in the diff more than once", at)
		}
		seenBase[at] = true
		todo = append(todo, rebuild{at, c})
	}
	var total int64
	for _, r := range todo {
		e, err := rebuildEntry(r.at, head.Entries[r.c.path], r.c, lim)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", r.c.path, err)
		}
		entries[r.at] = e
	}
	for _, e := range entries {
		total += int64(len(e.Data))
	}
	if total > lim.MaxTotalBytes {
		return nil, fmt.Errorf("more than %d bytes", lim.MaxTotalBytes)
	}
	return indexTree(entries, head), nil
}

// safeDiffPath accepts a clean relative path with no control character.
func safeDiffPath(p string) bool {
	if p == "" || path.IsAbs(p) || path.Clean(p) != p || p == "." || strings.HasPrefix(p, "../") || p == ".." {
		return false
	}
	return !strings.ContainsFunc(p, func(r rune) bool { return r < 0x20 || r == 0x7f })
}

// skippedPath reports a path under a directory ReadTree never reads.
func skippedPath(p string) bool {
	return slices.ContainsFunc(strings.Split(p, "/"), skipDir)
}

// rebuildEntry returns the base entry at path at for one change, from the head
// entry h (nil for a removed file).
func rebuildEntry(at string, h *Entry, c fileChange, lim Limits) (*Entry, error) {
	e, err := rebuildContent(at, h, c, lim)
	if err != nil || e.Kind != KindFile {
		return e, err
	}
	switch {
	case c.oldMode != "":
		m, err := strconv.ParseUint(c.oldMode, 8, 32)
		if err != nil {
			return nil, fmt.Errorf("mode %q", c.oldMode)
		}
		e.exec = m&0o111 != 0
	case c.status == statusModeOnly:
		e.exec = !h.exec // the content is the head's, so the mode changed
	case h != nil:
		e.exec = h.exec // a diff with no mode line keeps the mode
	}
	return e, nil
}

// rebuildContent returns the base entry at path at for one change, its mode
// not yet set; see rebuildEntry.
func rebuildContent(at string, h *Entry, c fileChange, lim Limits) (*Entry, error) {
	noPatch := &Entry{Path: at, Kind: KindNoPatch}
	if c.status == statusModeOnly {
		if h.Kind != KindFile || bytes.IndexByte(h.Data[:min(len(h.Data), binaryProbe)], 0) >= 0 {
			return noPatch, nil // a binary file shows no line, changed or not
		}
		e := *h
		e.Path = at
		return &e, nil
	}
	if c.status == statusPureRename {
		if h.Kind != KindFile || bytes.IndexByte(h.Data[:min(len(h.Data), binaryProbe)], 0) >= 0 {
			return noPatch, nil // a binary rename shows no line either, changed or not
		}
		c.patch = []byte{}
	}
	if c.patch == nil {
		return noPatch, nil
	}
	if len(bytes.TrimSpace(c.patch)) == 0 {
		if h == nil {
			return fileEntry(at, nil), nil // a removed empty file
		}
		e := *h // no line changed: the same entry under its base path
		e.Path = at
		return &e, nil
	}
	var cur []byte
	if h != nil {
		if h.Kind != KindFile {
			return noPatch, nil // the head content was not read, so no hunk can be reversed
		}
		cur = h.Data
	}
	data, err := reverseHunks(cur, c.patch)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > lim.MaxFileBytes {
		return &Entry{Path: at, Kind: KindOversize}, nil
	}
	return fileEntry(at, data), nil
}

func fileEntry(p string, data []byte) *Entry {
	if data == nil {
		data = []byte{}
	}
	sum := sha256.Sum256(data)
	return &Entry{Path: p, Kind: KindFile, Data: data, SHA256: hex.EncodeToString(sum[:])}
}

// patchOp is one body line of a hunk. noEOL is set when a "\ No newline at
// end of file" marker follows it.
type patchOp struct {
	kind  byte
	text  string
	noEOL bool
}

// reverseHunks returns the base content of a file from its head content and
// the hunks of its patch. Every context and added line must match the head,
// line ending included, and the hunks must be in order.
func reverseHunks(head, patch []byte) ([]byte, error) {
	hl := splitKeep(head)
	ls := strings.Split(strings.TrimSuffix(string(patch), "\n"), "\n")
	var out bytes.Buffer
	pos := 0
	for i := 0; i < len(ls); {
		h, err := parseHunkHeader(ls[i])
		if err != nil {
			return nil, fmt.Errorf("hunk: %w", err)
		}
		n, err := hunkLines(ls[i:])
		if err != nil {
			return nil, fmt.Errorf("hunk: %w", err)
		}
		start := h.newStart - 1
		if h.newCount == 0 {
			start = h.newStart // a pure removal sits after head line newStart
		}
		if start < pos {
			return nil, errors.New("hunk out of order or overlapping")
		}
		if start > len(hl) {
			return nil, fmt.Errorf("does not match the head: hunk at line %d past the end", h.newStart)
		}
		for _, l := range hl[pos:start] {
			out.WriteString(l)
		}
		pos = start
		for _, op := range hunkOps(ls[i+1 : i+n]) {
			if op.kind == '-' {
				out.WriteString(op.text)
				if !op.noEOL {
					out.WriteByte('\n')
				}
				continue
			}
			want := op.text
			if !op.noEOL {
				want += "\n"
			}
			if pos >= len(hl) || hl[pos] != want {
				return nil, fmt.Errorf("does not match the head at line %d", pos+1)
			}
			if op.kind == ' ' {
				out.WriteString(hl[pos])
			}
			pos++
		}
		i += n
	}
	for _, l := range hl[pos:] {
		out.WriteString(l)
	}
	return out.Bytes(), nil
}

// hunkOps turns hunk body lines into operations, attaching each "\ No
// newline" marker to the line before it.
func hunkOps(body []string) []patchOp {
	var ops []patchOp
	for _, l := range body {
		if strings.HasPrefix(l, `\`) {
			if len(ops) > 0 {
				ops[len(ops)-1].noEOL = true
			}
			continue
		}
		ops = append(ops, patchOp{kind: l[0], text: l[1:]})
	}
	return ops
}

// splitKeep splits text into lines, each with its newline; a last line with
// no newline keeps none.
func splitKeep(b []byte) []string {
	var out []string
	for len(b) > 0 {
		i := bytes.IndexByte(b, '\n')
		if i < 0 {
			out = append(out, string(b))
			break
		}
		out = append(out, string(b[:i+1]))
		b = b[i+1:]
	}
	return out
}

// indexTree builds a Tree from entries. Its directories are those that hold
// an entry, and those of like the head that hold nothing at all.
func indexTree(entries map[string]*Entry, head *Tree) *Tree {
	t := &Tree{Entries: entries, Dirs: map[string]bool{".": true}}
	for p := range entries {
		t.Paths = append(t.Paths, p)
		if head.Dirs[p] {
			t.Dirs[p] = true // an unreadable directory, recorded as an entry
		}
		for d := path.Dir(p); d != "."; d = path.Dir(d) {
			t.Dirs[d] = true
		}
	}
	held := map[string]bool{}
	for _, p := range head.Paths {
		for d := p; d != "."; d = path.Dir(d) {
			held[d] = true
		}
	}
	for d := range head.Dirs {
		if !held[d] {
			t.Dirs[d] = true // empty at head, so the change left it alone
		}
	}
	t.index()
	return t
}
