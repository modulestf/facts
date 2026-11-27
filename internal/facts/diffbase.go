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
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/modulestf/facts/internal/readcap"
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
	// oldStated and newStated are set when a mode line of a unified diff
	// states the mode of that side; a hunk line then does not override it.
	oldStated, newStated bool
	// oldGit and newGit are set when the diff shows the path as a gitlink
	// (mode 160000 or a "Subproject commit" line) at the base or at the head.
	oldGit, newGit bool
	// newMode is the head mode a unified diff gives in a "new mode", "new
	// file mode" or "index" line, or "" when it gives none. It must be the
	// mode of the head entry.
	newMode string
}

// gitlinkMode is the mode of a submodule entry in a diff.
const gitlinkMode = "160000"

// subprojectLine reports a hunk body line, past its sign, that names a
// submodule commit.
func subprojectLine(l string) bool { return strings.HasPrefix(l, "Subproject commit ") }

// markGitlink sets oldGit and newGit from the patch, for a side whose mode no
// line states. A hunk line that removes or keeps a "Subproject commit" line
// shows a gitlink at the base, and one that adds it a gitlink at the head. A
// files list states no mode, so there the hunk lines alone decide.
func (c *fileChange) markGitlink() {
	for l := range strings.Lines(string(c.patch)) {
		l = strings.TrimRight(l, "\n")
		if len(l) < 2 || !subprojectLine(l[1:]) {
			continue
		}
		switch l[0] {
		case '-', ' ':
			c.oldGit = c.oldGit || !c.oldStated
		case '+':
			c.newGit = c.newGit || !c.newStated
		}
	}
}

// ReadDiffBase rebuilds the tree a change started from out of the head tree
// and the change as a diff: a GitHub pull request files list or a unified
// diff. Each file the diff changes gets its base content by reversing its
// hunks against the head file; every other file is the head's. A file the
// diff has no patch for, such as a binary or very large one, is KindNoPatch at
// the base, so its content there is unknown. A diff that does not fit the
// head tree is an error.
func ReadDiffBase(head *Tree, file string, lim Limits) (*Tree, *DiffInput, error) {
	data, err := readcap.File(file, maxDiffBytes)
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

// ghFileEntry is the part of a files list entry the rebuild reads. The
// counts are not used, but a value that is no number is refused.
type ghFileEntry struct {
	Filename         string  `json:"filename"`
	Status           string  `json:"status"`
	PreviousFilename string  `json:"previous_filename"`
	Patch            *string `json:"patch"`
	Additions        *int    `json:"additions"`
	Deletions        *int    `json:"deletions"`
}

// ghStatuses is the set of statuses GitHub gives a files list entry. Any
// other value is refused, so an input cannot name a status of the rebuild's
// own.
var ghStatuses = []string{"added", "removed", "modified", "renamed", "copied", "changed", "unchanged"}

// KnownFileStatus reports a status GitHub gives a files list entry.
func KnownFileStatus(s string) bool { return slices.Contains(ghStatuses, s) }

// parseGitHubFiles reads a files list. A blank patch is no patch: GitHub
// gives none for a change it shows no line of. An entry other than an added
// one with no patch is KindNoPatch at the base, whatever its counts: a list
// shows neither side's content, so it cannot prove a rename or a mode change
// kept the text, as a binary base with a text head counts 0 and 0 too, nor
// that a removed file was empty, as a host may list as removed a file a
// rename moved out of the tree. --base and a unified diff prove those. An
// entry with no filename, an unchanged entry with a patch, a previous
// filename on an entry that is no rename or copy, and a patch that is not
// whole hunks each changing a line are refused: GitHub sends none of them,
// and the rebuild would read each as no change.
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
		if e.Filename == "" {
			return nil, errors.New("files list: an entry with no filename")
		}
		if !slices.Contains(ghStatuses, e.Status) {
			return nil, fmt.Errorf("files list: %q: unknown status %q", e.Filename, e.Status)
		}
		if e.PreviousFilename != "" && e.Status != "renamed" && e.Status != "copied" {
			return nil, fmt.Errorf("files list: %q: a previous filename on a %s entry", e.Filename, e.Status)
		}
		c := fileChange{path: e.Filename, prev: e.PreviousFilename, status: e.Status}
		if e.Patch != nil && len(bytes.TrimSpace([]byte(*e.Patch))) > 0 {
			if e.Status == "unchanged" {
				return nil, fmt.Errorf("files list: %q: unchanged with a patch", e.Filename)
			}
			if err := checkHunks(*e.Patch); err != nil {
				return nil, fmt.Errorf("files list: %q: %w", e.Filename, err)
			}
			c.patch = []byte(*e.Patch)
			c.markGitlink()
		}
		out = append(out, c)
	}
	return out, nil
}

// checkHunks checks a patch is whole hunks, whatever the status, so a patch
// the rebuild does not reverse, as of an added file, is held to it too.
func checkHunks(patch string) error {
	ls := strings.Split(strings.TrimSuffix(patch, "\n"), "\n")
	for i := 0; i < len(ls); {
		n, err := hunkLines(ls[i:])
		if err != nil {
			return fmt.Errorf("hunk: %w", err)
		}
		i += n
	}
	return nil
}

func unifiedStart(t []byte) bool {
	return bytes.HasPrefix(t, []byte("diff --git ")) || bytes.Contains(t, []byte("\n--- ")) ||
		bytes.Contains(t, []byte("\ndiff --git ")) || bytes.HasPrefix(t, []byte("--- "))
}

var hunkHeader = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// parseUnified splits a unified diff into files. Lines before the first file
// are a preamble and skipped, as the message of a patch mail. A file is a
// section git writes, read as an allowlist: a "diff --git" line and its
// extended header lines in git's order, then hunks or a binary marker; or,
// in a diff with no "diff --git" line, a ---/+++ pair and its hunks. A line,
// an order or a combination outside that grammar refuses the whole input, as
// does a section that shows no change and non-blank input with no section.
func parseUnified(data []byte) ([]fileChange, error) {
	lines := strings.Split(string(data), "\n")
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	// The signature git format-patch ends a mail with: "-- " and its version.
	if n := len(lines); n >= 2 && lines[n-2] == "-- " && versionLine.MatchString(lines[n-1]) {
		lines = lines[:n-2]
	}
	i := 0
	for i < len(lines) && !gitStart(lines[i]) && !sidesAt(lines, i) {
		i++ // preamble
	}
	var out []fileChange
	git := i < len(lines) && gitStart(lines[i])
	for i < len(lines) {
		var (
			c   fileChange
			err error
		)
		switch {
		case git: // a git section ends at the next "diff --git" line or the end
			c, i, err = gitSection(lines, i)
		case sidesAt(lines, i):
			c, i, err = plainSection(lines, i)
		default:
			return nil, fmt.Errorf("unified diff: line %d is not part of a file section", i+1)
		}
		if err != nil {
			return nil, fmt.Errorf("unified diff: %w", err)
		}
		c.markGitlink()
		out = append(out, c)
	}
	if len(out) == 0 && len(bytes.TrimSpace(data)) > 0 {
		return nil, errors.New("unified diff: no file section")
	}
	return out, nil
}

func gitStart(l string) bool { return strings.HasPrefix(l, "diff --git ") }

// sidesAt reports a ---/+++ pair at line i.
func sidesAt(ls []string, i int) bool {
	return strings.HasPrefix(ls[i], "--- ") && i+1 < len(ls) && strings.HasPrefix(ls[i+1], "+++ ")
}

// The ranks of git's extended header lines, in the order git writes them.
const (
	rankMode = iota + 1 // old mode and new mode, new file mode, deleted file mode
	rankSimilarity
	rankMove // rename or copy from and to
	rankIndex
	rankBody // ---/+++ and hunks, Binary files, GIT binary patch
)

var (
	indexLine      = regexp.MustCompile(`^index ([0-9a-f]{4,64})\.\.([0-9a-f]{4,64})(?: ([0-7]{6}))?$`)
	versionLine    = regexp.MustCompile(`^[0-9]+(?:\.[0-9A-Za-z]+)+(?: \([ -~]+\))?$`)
	similarityLine = regexp.MustCompile(`^(?:dis)?similarity index (\d{1,3})%$`)
	binaryLine     = regexp.MustCompile("^(?:(?:literal|delta) [0-9]+|[A-Za-z][0-9A-Za-z!#$%&()*+;<=>?@^_`{|}~-]+)?$")
)

// gitModes are the modes git writes for a path: a file, an executable, a
// link and a gitlink.
var gitModes = []string{"100644", "100755", "120000", gitlinkMode}

func regularMode(m string) bool { return m == "100644" || m == "100755" }

// emptyBlob is the hash of the empty blob, which git abbreviates.
const emptyBlob = "e69de29bb2d1d6434b8b29ae775ad8c2e48c5391"

func zeroHash(h string) bool  { return strings.Trim(h, "0") == "" }
func emptyHash(h string) bool { return strings.HasPrefix(emptyBlob, h) }

// bodyless checks a section with no hunk and no binary marker. git writes
// one for a change of mode or path alone, and for an empty file added or
// removed; with an index line whose hashes differ, any other section hides
// a change of content, and the base would take the head's. A link
// removed always has a hunk: its target.
func bodyless(hdr string, c fileChange, oldHash, newHash string) error {
	if c.status == "removed" && c.oldMode == "120000" {
		return fmt.Errorf("%q: mode %s removed with no hunk", hdr, c.oldMode)
	}
	if oldHash == newHash {
		return nil
	}
	if (c.status == "added" && zeroHash(oldHash) && emptyHash(newHash)) ||
		(c.status == "removed" && emptyHash(oldHash) && zeroHash(newHash)) {
		return nil
	}
	return fmt.Errorf("%q: index %s..%s with no hunk", hdr, oldHash, newHash)
}

func gitMode(s string) (string, error) {
	if !slices.Contains(gitModes, s) {
		return "", fmt.Errorf("mode %q", s)
	}
	return s, nil
}

// gitSection reads the file section that starts at line i, a "diff --git"
// line, and returns it and the line after it.
func gitSection(ls []string, i int) (fileChange, int, error) {
	hdr := strings.TrimPrefix(ls[i], "diff --git ")
	p, err := gitHeaderPath(hdr)
	if err != nil {
		return fileChange{}, 0, err
	}
	c := fileChange{path: p, prev: p, patch: []byte{}}
	rank, shown, similarity, moved, body := 0, false, false, false, false
	var oldHash, newHash string
	step := func(r int) error {
		if r <= rank {
			return fmt.Errorf("%q: line %d is out of git's header order", hdr, i+1)
		}
		rank = r
		return nil
	}
	for i++; i < len(ls) && !gitStart(ls[i]); i++ {
		l := ls[i]
		var err error
		switch {
		case strings.HasPrefix(l, "old mode "):
			if err = step(rankMode); err != nil {
				return c, 0, err
			}
			if i+1 >= len(ls) || !strings.HasPrefix(ls[i+1], "new mode ") {
				return c, 0, fmt.Errorf("%q: old mode with no new mode", hdr)
			}
			if c.oldMode, err = gitMode(strings.TrimPrefix(l, "old mode ")); err != nil {
				return c, 0, err
			}
			if c.newMode, err = gitMode(strings.TrimPrefix(ls[i+1], "new mode ")); err != nil {
				return c, 0, err
			}
			if c.oldMode == c.newMode {
				return c, 0, fmt.Errorf("%q: old and new mode are the same", hdr)
			}
			// git writes a change of type, file to link or gitlink, as a
			// removal and an addition: a mode pair is an exec bit change.
			if !regularMode(c.oldMode) || !regularMode(c.newMode) {
				return c, 0, fmt.Errorf("%q: old mode %s and new mode %s are of two types", hdr, c.oldMode, c.newMode)
			}
			c.oldGit, c.newGit, c.oldStated, c.newStated = c.oldMode == gitlinkMode, c.newMode == gitlinkMode, true, true
			shown = true
			i++
		case strings.HasPrefix(l, "new file mode "):
			if err = step(rankMode); err != nil {
				return c, 0, err
			}
			if c.newMode, err = gitMode(strings.TrimPrefix(l, "new file mode ")); err != nil {
				return c, 0, err
			}
			c.status, c.prev, c.newGit, c.newStated, shown = "added", "", c.newMode == gitlinkMode, true, true
		case strings.HasPrefix(l, "deleted file mode "):
			if err = step(rankMode); err != nil {
				return c, 0, err
			}
			if c.oldMode, err = gitMode(strings.TrimPrefix(l, "deleted file mode ")); err != nil {
				return c, 0, err
			}
			c.status, c.oldGit, c.oldStated, shown = "removed", c.oldMode == gitlinkMode, true, true
		case similarityLine.MatchString(l):
			if err = step(rankSimilarity); err != nil {
				return c, 0, err
			}
			if n, _ := strconv.Atoi(similarityLine.FindStringSubmatch(l)[1]); n > 100 {
				return c, 0, fmt.Errorf("%q: similarity over 100%%", hdr)
			}
			similarity = strings.HasPrefix(l, "similarity")
			if !similarity && c.status != "" {
				return c, 0, fmt.Errorf("%q: dissimilarity of an added or removed file", hdr)
			}
		case strings.HasPrefix(l, "rename from "), strings.HasPrefix(l, "copy from "):
			if err = step(rankMove); err != nil {
				return c, 0, err
			}
			kind, _, _ := strings.Cut(l, " ")
			from := strings.TrimPrefix(l, kind+" from ")
			if !similarity || c.status != "" || i+1 >= len(ls) || !strings.HasPrefix(ls[i+1], kind+" to ") {
				return c, 0, fmt.Errorf("%q: %s from with no similarity index or no %s to", hdr, kind, kind)
			}
			to := strings.TrimPrefix(ls[i+1], kind+" to ")
			if from == "" || to == "" || from == to || hdr != "a/"+from+" b/"+to {
				return c, 0, fmt.Errorf("%q: %s from %q to %q", hdr, kind, from, to)
			}
			c.prev, c.path, c.status, moved, shown = from, to, map[string]string{"rename": "renamed", "copy": "copied"}[kind], true, true
			i++
		case strings.HasPrefix(l, "index "):
			if err = step(rankIndex); err != nil {
				return c, 0, err
			}
			m := indexLine.FindStringSubmatch(l)
			if m == nil {
				return c, 0, fmt.Errorf("%q: index line %q", hdr, l)
			}
			oldHash, newHash = m[1], m[2]
			if m[3] != "" {
				// The mode of both sides, which git writes only when it is
				// the same: never beside a mode line.
				if c.oldStated || c.newStated {
					return c, 0, fmt.Errorf("%q: index line with a mode beside a mode line", hdr)
				}
				if c.newMode, err = gitMode(m[3]); err != nil {
					return c, 0, err
				}
				c.oldGit, c.newGit, c.oldStated, c.newStated = c.newMode == gitlinkMode, c.newMode == gitlinkMode, true, true
			}
		case sidesAt(ls, i):
			if err = step(rankBody); err != nil {
				return c, 0, err
			}
			oldP, newP := "a/"+c.prev, "b/"+c.path
			if c.status == "added" {
				oldP = "/dev/null"
			}
			if c.status == "removed" {
				newP = "/dev/null"
			}
			if sidePath(l[4:], "") != oldP || sidePath(ls[i+1][4:], "") != newP {
				return c, 0, fmt.Errorf("%q: ---/+++ lines name other paths", hdr)
			}
			end, err := hunks(ls, i+2, &c)
			if err != nil {
				return c, 0, fmt.Errorf("%q: %w", c.path, err)
			}
			i, shown, body = end-1, true, true
		case strings.HasPrefix(l, "Binary files "):
			if err = step(rankBody); err != nil {
				return c, 0, err
			}
			oldP, newP := "a/"+c.prev, "b/"+c.path
			if c.status == "added" {
				oldP = "/dev/null"
			}
			if c.status == "removed" {
				newP = "/dev/null"
			}
			if l != "Binary files "+oldP+" and "+newP+" differ" {
				return c, 0, fmt.Errorf("%q: binary marker %q", hdr, l)
			}
			c.patch, shown, body = nil, true, true
		case l == "GIT binary patch":
			if err = step(rankBody); err != nil {
				return c, 0, err
			}
			for i+1 < len(ls) && !gitStart(ls[i+1]) {
				if !binaryLine.MatchString(ls[i+1]) {
					return c, 0, fmt.Errorf("%q: binary patch line %d", hdr, i+2)
				}
				i++
			}
			c.patch, shown, body = nil, true, true
		default:
			return c, 0, fmt.Errorf("%q: line %d is not a git header line", hdr, i+1)
		}
	}
	if c.path == "" {
		return c, 0, fmt.Errorf("%q: no path", hdr)
	}
	if similarity && !moved {
		return c, 0, fmt.Errorf("%q: similarity index with no rename or copy", hdr)
	}
	if !shown {
		return c, 0, fmt.Errorf("%q: no hunk and no change of mode, path or type", hdr)
	}
	if !body {
		if err := bodyless(hdr, c, oldHash, newHash); err != nil {
			return c, 0, err
		}
	}
	switch c.status {
	case "":
		c.status = "modified"
	case "removed":
		c.prev = ""
	}
	return c, i, nil
}

// plainSection reads the ---/+++ section at line i of a diff with no
// "diff --git" line, and returns it and the line after it.
func plainSection(ls []string, i int) (fileChange, int, error) {
	c := fileChange{patch: []byte{}}
	oldP, newP := sidePath(ls[i][4:], "a/"), sidePath(ls[i+1][4:], "b/")
	switch {
	case oldP == "/dev/null" && newP == "/dev/null":
		return c, 0, errors.New("a file with no path")
	case oldP == "/dev/null":
		c.status, c.path = "added", newP
	case newP == "/dev/null":
		c.status, c.path = "removed", oldP
	default:
		c.status, c.path, c.prev = "modified", newP, oldP
		if oldP != newP {
			c.status = "renamed"
		}
	}
	if c.path == "" {
		return c, 0, errors.New("a file with no path")
	}
	end, err := hunks(ls, i+2, &c)
	if err != nil {
		return c, 0, fmt.Errorf("%q: %w", c.path, err)
	}
	return c, end, nil
}

// hunks appends to c.patch the hunks that start at line i, at least one,
// and returns the line after them.
func hunks(ls []string, i int, c *fileChange) (int, error) {
	start := i
	for i < len(ls) && strings.HasPrefix(ls[i], "@@") {
		n, err := hunkLines(ls[i:])
		if err != nil {
			return 0, err
		}
		c.patch = append(c.patch, strings.Join(ls[i:i+n], "\n")+"\n"...)
		i += n
	}
	if i == start {
		return 0, errors.New("---/+++ lines with no hunk")
	}
	return i, nil
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
	oldLeft, newLeft, n, edits := h.oldCount, h.newCount, 1, 0
	for (oldLeft > 0 || newLeft > 0 || n < len(ls) && strings.HasPrefix(ls[n], `\`)) && n < len(ls) {
		switch l := ls[n]; {
		case strings.HasPrefix(l, `\`):
		case strings.HasPrefix(l, " "):
			oldLeft--
			newLeft--
		case strings.HasPrefix(l, "-"):
			oldLeft--
			edits++
		case strings.HasPrefix(l, "+"):
			newLeft--
			edits++
		default:
			return 0, fmt.Errorf("hunk line %q", l)
		}
		n++
	}
	if oldLeft != 0 || newLeft != 0 {
		return 0, errors.New("hunk shorter than its header says")
	}
	if edits == 0 {
		return 0, errors.New("hunk of context alone") // git writes none; it changes nothing
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
	if h.oldCount == 0 && h.newCount == 0 {
		// git never writes a hunk of no line, and reversing one changes nothing
		return hunk{}, fmt.Errorf("hunk header of no line: %q", l)
	}
	return h, nil
}

// applyChanges builds the base tree: the head's entries, less every path the
// change adds or renames away, plus the base content of every path it
// changes, renames from or removes.
func applyChanges(head *Tree, changes []fileChange, lim Limits) (*Tree, error) {
	entries := maps.Clone(head.Entries)
	seenHead, seenBase := map[string]bool{}, map[string]bool{}
	gitDiff := map[string]bool{} // the gitlinks the diff changes
	// involved holds the paths of the diff that a change shows as a gitlink,
	// at either side; wasGit those it shows as a gitlink at the base;
	// skipRebuild those a merge of two changes of one path rebuilds no
	// content for.
	involved, wasGit, skipRebuild := map[string]bool{}, map[string]bool{}, map[string]bool{}
	type rebuild struct {
		at string
		c  fileChange
	}
	var todo []rebuild
	for _, c := range changes {
		for _, p := range []string{c.path, c.prev} {
			if p != "" && (!safeDiffPath(p) || gitPath(p)) {
				return nil, fmt.Errorf("unsafe path %q", p)
			}
		}
		if c.newMode != "" && !headHasMode(head, c.path, c.newMode) {
			return nil, fmt.Errorf("%q: the diff gives mode %s, which is not the head entry's", c.path, c.newMode)
		}
		if (c.status == "renamed" || c.status == "copied") && c.prev == c.path {
			return nil, fmt.Errorf("%q is %s to itself", c.path, c.status)
		}
		if head.IsGitlink(c.path) {
			// The head holds a gitlink here, so the change is of one even when
			// the diff shows no hunk: a rename keeps the type, and a change
			// with no patch shows no line.
			c.newGit = true
			c.oldGit = c.oldGit || c.status == "renamed" || c.status == "copied"
		}
		if seenHead[c.path] {
			// A change of type, such as a gitlink that became a link, comes as
			// a removal and an addition of one path. Merge them into one
			// change of a gitlink, whose base content is unknown unless the
			// base held the gitlink.
			if !involved[c.path] && !c.oldGit && !c.newGit {
				return nil, fmt.Errorf("%q is in the diff more than once", c.path)
			}
			involved[c.path], wasGit[c.path] = true, wasGit[c.path] || c.oldGit
			gitDiff[c.path], skipRebuild[c.path] = true, true
			delete(entries, c.path)
			if !wasGit[c.path] {
				entries[c.path] = &Entry{Path: c.path, Kind: KindNoPatch}
			}
			continue
		}
		seenHead[c.path] = true
		involved[c.path], wasGit[c.path] = c.oldGit || c.newGit, c.oldGit
		if c.oldGit || c.newGit {
			// A gitlink holds no content to reverse, and a diff shows its
			// commit as a file of one line. The path is changed whatever
			// else is known; what the base held there is no file when it
			// held a gitlink, and unknown when it held a file.
			gitDiff[c.path] = true
			if c.prev != "" {
				gitDiff[c.prev] = true
			}
			delete(entries, c.path)
			if !c.oldGit && c.status != "added" {
				entries[c.path] = &Entry{Path: c.path, Kind: KindNoPatch}
			}
			continue
		}
		at := c.path
		switch c.status {
		case "added", "copied":
			at = ""
		case "removed":
			if head.Entries[c.path] != nil {
				return nil, fmt.Errorf("%q is removed by the diff but still in the head tree", c.path)
			}
		case "modified", "changed":
		case "renamed":
			if c.prev == "" {
				return nil, fmt.Errorf("%q is renamed with no previous path", c.path)
			}
			at = c.prev
		case "unchanged":
			if head.Entries[c.path] == nil {
				return nil, fmt.Errorf("%q is unchanged in the diff but not in the head tree", c.path)
			}
			continue
		default:
			return nil, fmt.Errorf("%q: unknown status %q", c.path, c.status)
		}
		if c.status != "removed" && head.Entries[c.path] == nil {
			return nil, fmt.Errorf("%q is in the diff but not in the head tree", c.path)
		}
		delete(entries, c.path) // added, renamed away or changed; the rebuild below puts back a base entry
		if at == "" {
			continue
		}
		if seenBase[at] {
			return nil, fmt.Errorf("%q is in the diff more than once", at)
		}
		seenBase[at] = true
		todo = append(todo, rebuild{at, c})
	}
	var total int64
	for _, r := range todo {
		if skipRebuild[r.c.path] {
			continue
		}
		e, err := rebuildEntry(r.at, head.Entries[r.c.path], r.c, lim)
		if err != nil {
			return nil, fmt.Errorf("%q: %w", r.c.path, err)
		}
		entries[r.at] = e
	}
	for _, e := range entries {
		total += int64(len(e.Data))
	}
	if total > lim.MaxTotalBytes {
		return nil, fmt.Errorf("more than %d bytes", lim.MaxTotalBytes)
	}
	t := indexTree(entries, head)
	t.gitlinks = maps.Clone(head.gitlinks)
	if t.gitlinks == nil {
		t.gitlinks = map[string]bool{}
	}
	maps.Copy(t.gitlinks, gitDiff)
	t.gitlinkDiff = gitDiff
	return t, nil
}

// headHasMode reports whether the head entry at p has the git mode mode, its
// kind and exec bit both. A gitlink mode needs no entry at p.
func headHasMode(head *Tree, p, mode string) bool {
	if mode == gitlinkMode {
		// A gitlink is no entry, and a checkout that did not fetch the
		// submodule holds nothing there either.
		return head.Entries[p] == nil
	}
	e := head.Entries[p]
	switch {
	case e == nil:
		return false
	case mode == "120000":
		return e.Kind == KindSymlink
	default:
		return (e.Kind == KindFile || e.Kind == KindOversize) && e.exec == (mode == "100755")
	}
}

// safeDiffPath accepts a clean relative path with no control character.
func safeDiffPath(p string) bool {
	if p == "" || path.IsAbs(p) || path.Clean(p) != p || p == "." || strings.HasPrefix(p, "../") || p == ".." {
		return false
	}
	return !strings.ContainsFunc(p, func(r rune) bool { return r < 0x20 || r == 0x7f })
}

// gitPath reports a path with a .git element, in any letter case. Git never
// tracks one, so a change that lists one is refused, on either side of a
// rename; every other path of the change is the change's own.
func gitPath(p string) bool {
	return slices.ContainsFunc(strings.Split(p, "/"), func(e string) bool { return strings.EqualFold(e, ".git") })
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
	case h != nil:
		e.exec = h.exec // a diff with no mode line keeps the mode
	}
	return e, nil
}

// rebuildContent returns the base entry at path at for one change, its mode
// not yet set; see rebuildEntry.
func rebuildContent(at string, h *Entry, c fileChange, lim Limits) (*Entry, error) {
	noPatch := &Entry{Path: at, Kind: KindNoPatch}
	if c.patch == nil {
		return noPatch, nil
	}
	if len(bytes.TrimSpace(c.patch)) == 0 {
		if h == nil {
			return fileEntry(at, nil), nil // a removed empty file of a unified diff
		}
		e := *h // no line changed: the same entry under its base path
		e.Path = at
		return &e, nil
	}
	var cur []byte
	if h != nil {
		if h.Kind != KindFile || h.opaque {
			return noPatch, nil // the head content was not read or kept, so no hunk can be reversed
		}
		cur = h.Data
	}
	data, err := reverseHunks(cur, c.patch)
	if err != nil {
		return nil, err
	}
	if h != nil && bytes.Equal(data, h.Data) {
		// hunks whose removals and additions cancel out: git writes none,
		// at the same path or after a rename
		return nil, errors.New("patch changes nothing")
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
	t := &Tree{Entries: entries, Dirs: map[string]bool{".": true}, gitlinks: head.gitlinks, gitlinksOfHead: true}
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
