package facts

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/hcl/v2/hclsyntax"
)

const changesFixtures = "../../testdata/changes"

// changesExpected is the oracle of one fixture: the report's files and
// data sources, which hold no hash.
type changesExpected struct {
	Files       []ChangedFile    `json:"files"`
	DataSources []DirDataSources `json:"data_sources"`
}

// copyChangesFixture copies a fixture to a temp dir and creates the links
// its symlinks.txt lists, each "<link> <target>" with the link under head/
// or base/ and the target relative to the link's directory.
func copyChangesFixture(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.CopyFS(dir, os.DirFS(filepath.Join(changesFixtures, name))); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(filepath.Join(dir, "symlinks.txt"))
	if os.IsNotExist(err) {
		return dir
	}
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		link, target, ok := strings.Cut(sc.Text(), " ")
		if !ok || strings.Contains(link, "..") || (!strings.HasPrefix(link, "head/") && !strings.HasPrefix(link, "base/")) {
			t.Fatalf("bad symlinks.txt line %q", sc.Text())
		}
		if err := os.Symlink(target, filepath.Join(dir, filepath.FromSlash(link))); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestChangesOracle runs changes on each fixture under testdata/changes
// and compares the files and data sources with expected.json.
func TestChangesOracle(t *testing.T) {
	ents, err := os.ReadDir(changesFixtures)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		t.Run(e.Name(), func(t *testing.T) {
			dir := copyChangesFixture(t, e.Name())
			r, err := Changes(ChangesOptions{Head: filepath.Join(dir, "head"), Base: filepath.Join(dir, "base"), Limits: DefaultLimits})
			if err != nil {
				t.Fatal(err)
			}
			got := changesExpected{Files: r.Files, DataSources: r.DataSources}
			gotJSON, _ := json.MarshalIndent(got, "", "  ")
			data, err := os.ReadFile(filepath.Join(changesFixtures, e.Name(), "expected.json"))
			if err != nil {
				t.Fatalf("%v\ngot:\n%s", err, gotJSON)
			}
			var want changesExpected
			if err := json.Unmarshal(data, &want); err != nil {
				t.Fatal(err)
			}
			wantJSON, _ := json.MarshalIndent(want, "", "  ") // JSON, so unexported fields do not count
			if string(gotJSON) != string(wantJSON) {
				t.Errorf("report differs from expected.json\ngot:\n%s", gotJSON)
			}
		})
	}
}

// changesOf runs Changes on two trees given as file maps.
func changesOf(t *testing.T, head, base map[string]string) *ChangesReport {
	t.Helper()
	r, err := Changes(ChangesOptions{Head: writeTree(t, head), Base: writeTree(t, base), Limits: DefaultLimits})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// TestChangesBytes covers what the Perl reader refused and the parser
// reads: a non-ASCII byte in a value is an attribute change, and a carriage
// return outside a value is a structural edit.
func TestChangesBytes(t *testing.T) {
	base := "resource \"aws_lb\" \"this\" {\n  name    = var.name\n  subnets = var.subnets\n}\n"
	r := changesOf(t, map[string]string{"main.tf": strings.Replace(base, "var.name", "\"caf\u00e9\"", 1)}, map[string]string{"main.tf": base})
	b := r.Files[0].Blocks[0]
	if b.Structural || len(b.Attributes) != 1 || b.Attributes[0].Name != "name" || !b.Attributes[0].Plain.Head {
		t.Errorf("non-ASCII value: got %+v", b)
	}
	r = changesOf(t, map[string]string{"main.tf": strings.Replace(base, "var.name\n", "var.name\r\n", 1)}, map[string]string{"main.tf": base})
	if b := r.Files[0].Blocks[0]; !b.Structural || len(b.Attributes) != 0 {
		t.Errorf("carriage return: got %+v", b)
	}
}

// TestChangesDiffMatchesBase checks a report from a GitHub files list
// equals the one from the base tree it describes.
func TestChangesDiffMatchesBase(t *testing.T) {
	dir := copyChangesFixture(t, "datasources")
	files := `[{"filename":"main.tf","status":"modified","patch":"@@ -1,3 +1,3 @@\n resource \"aws_sns_topic\" \"t\" {\n-  name = var.topic\n+  name = var.name\n }"}]`
	diff := filepath.Join(t.TempDir(), "files.json")
	if err := os.WriteFile(diff, []byte(files), 0o644); err != nil {
		t.Fatal(err)
	}
	fromDiff, err := Changes(ChangesOptions{Head: filepath.Join(dir, "head"), Diff: diff, Limits: DefaultLimits})
	if err != nil {
		t.Fatal(err)
	}
	fromBase, err := Changes(ChangesOptions{Head: filepath.Join(dir, "head"), Base: filepath.Join(dir, "base"), Limits: DefaultLimits})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fromDiff.Files, fromBase.Files) || !reflect.DeepEqual(fromDiff.DataSources, fromBase.DataSources) {
		t.Errorf("diff and base reports differ:\n%+v\n%+v", fromDiff.Files, fromBase.Files)
	}
	if fromDiff.Base.Kind != "diff" || fromDiff.Base.Diff == nil || fromDiff.Base.TreeSHA256 != nil {
		t.Errorf("diff base: got %+v", fromDiff.Base)
	}
	if _, err := Changes(ChangesOptions{Head: filepath.Join(dir, "head"), Limits: DefaultLimits}); err == nil {
		t.Error("no base and no diff: want an error")
	}
}

// TestChangesLongLine runs the read scan over a line of 40,000 unclosed
// indexes inside a string, which the Perl scan had to bound: it is no read
// and finishes at once. The same text as code is over the parser's limits,
// so the module's reads are unknown.
func TestChangesLongLine(t *testing.T) {
	res := "resource \"aws_lb\" \"this\" {\n  name = var.%s\n}\n"
	big := strings.Repeat("aws_lb.this[", 40000)
	start := time.Now()
	r := changesOf(t,
		map[string]string{"main.tf": fmt.Sprintf(res, "b"), "big.tf": "locals {\n  x = \"" + big + "\"\n}\n"},
		map[string]string{"main.tf": fmt.Sprintf(res, "a"), "big.tf": "locals {\n  x = \"" + big + "\"\n}\n"})
	if a := r.Files[0].Blocks[0].Attributes[0]; a.Read != ReadNone {
		t.Errorf("string text: got read %q", a.Read)
	}
	// Under a second normally; the race detector and a loaded machine slow it
	// by ten. A scan that is quadratic in the line takes minutes.
	if d := time.Since(start); d > 30*time.Second {
		t.Errorf("took %v", d)
	}
	r = changesOf(t,
		map[string]string{"main.tf": fmt.Sprintf(res, "b"), "big.tf": "locals {\n  x = " + big + "\n}\n"},
		map[string]string{"main.tf": fmt.Sprintf(res, "a"), "big.tf": "locals {\n  x = " + big + "\n}\n"})
	if a := r.Files[0].Blocks[0].Attributes[0]; a.Read != ReadUnknown {
		t.Errorf("code over the limits: got read %q", a.Read)
	}
}

// TestChangesReadersCap checks readers stop at maxReaders while the state
// still says read.
func TestChangesReadersCap(t *testing.T) {
	var b strings.Builder
	b.WriteString("locals {\n")
	for i := range maxReaders + 5 {
		fmt.Fprintf(&b, "  r%d = aws_lb.this.name\n", i)
	}
	b.WriteString("}\n")
	res := "resource \"aws_lb\" \"this\" {\n  name = var.%s\n}\n"
	r := changesOf(t, map[string]string{"main.tf": fmt.Sprintf(res, "b"), "reads.tf": b.String()},
		map[string]string{"main.tf": fmt.Sprintf(res, "a"), "reads.tf": b.String()})
	a := r.Files[0].Blocks[0].Attributes[0]
	if a.Read != ReadFound || len(a.Readers) != maxReaders || !a.ReadersTruncated {
		t.Errorf("got read %q, %d readers, truncated %v", a.Read, len(a.Readers), a.ReadersTruncated)
	}
}

// TestChangesDeadline checks a passed deadline stops the run.
func TestChangesDeadline(t *testing.T) {
	lim := DefaultLimits
	lim.Deadline = time.Now().Add(-time.Second)
	h := writeTree(t, map[string]string{"main.tf": "locals {\n  a = 1\n}\n"})
	b := writeTree(t, map[string]string{"main.tf": "locals {\n  a = 2\n}\n"})
	if _, err := Changes(ChangesOptions{Head: h, Base: b, Limits: lim}); !errors.Is(err, ErrDeadline) {
		t.Errorf("got %v, want ErrDeadline", err)
	}
}

// FuzzChangesFile compares two versions of one file and scans their reads:
// any input gives a report, never a panic.
func FuzzChangesFile(f *testing.F) {
	f.Add([]byte("resource \"a\" \"b\" {\n  x = var.y\n}\n"), []byte("resource \"a\" \"b\" {\n  x = var.z\n}\nlocals {\n  r = a.b[*].x\n}\n"))
	f.Add([]byte("data \"a\" \"b\" {\n  x = <<EOT\n${self.y}\nEOT\n}\n"), []byte("data \"a\" \"b\" {\n  x = 1\n}\n"))
	f.Fuzz(func(t *testing.T, base, head []byte) {
		tr := &Tree{Entries: map[string]*Entry{"main.tf": fileEntry("main.tf", head)}, Paths: []string{"main.tf"}}
		tr.index()
		files := []ChangedFile{diffFile("main.tf", fileEntry("main.tf", head), fileEntry("main.tf", base), DefaultLimits)}
		if err := markReads(files, tr, DefaultLimits); err != nil {
			t.Fatal(err)
		}
		if _, err := dataSources(tr, DefaultLimits); err != nil {
			t.Fatal(err)
		}
	})
}

// TestChangesWholeObjectBounded checks a whole-object read is kept once per
// block: many changed attributes and many whole-object references keep each
// attribute's readers and the block's record within maxReaders.
func TestChangesWholeObjectBounded(t *testing.T) {
	var hb, bb, reads strings.Builder
	for i := range 200 {
		fmt.Fprintf(&hb, "  a%d = var.b%d\n", i, i)
		fmt.Fprintf(&bb, "  a%d = var.a%d\n", i, i)
	}
	reads.WriteString("locals {\n")
	for i := range 500 {
		fmt.Fprintf(&reads, "  r%d = aws_lb.this\n", i)
	}
	reads.WriteString("}\n")
	r := changesOf(t,
		map[string]string{"main.tf": "resource \"aws_lb\" \"this\" {\n" + hb.String() + "}\n", "reads.tf": reads.String()},
		map[string]string{"main.tf": "resource \"aws_lb\" \"this\" {\n" + bb.String() + "}\n", "reads.tf": reads.String()})
	as := r.Files[0].Blocks[0].Attributes
	if len(as) != 200 {
		t.Fatalf("got %d attributes", len(as))
	}
	for _, a := range as {
		if a.Read != ReadFound || len(a.Readers) != maxReaders || !a.ReadersTruncated || a.Readers[0].How != HowObject {
			t.Fatalf("%s: read %q, %d readers, truncated %v", a.Name, a.Read, len(a.Readers), a.ReadersTruncated)
		}
	}
}

// TestChangesWalkDeadline checks the read walk stops at the deadline.
func TestChangesWalkDeadline(t *testing.T) {
	var b strings.Builder
	b.WriteString("locals {\n")
	for i := range 2 * walkCheckEvery {
		fmt.Fprintf(&b, "  r%d = aws_lb.this.name\n", i)
	}
	b.WriteString("}\n")
	body, _, err := parseSyntax("reads.tf", []byte(b.String()), DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	lim := DefaultLimits
	lim.Deadline = time.Now().Add(-time.Second)
	a := &ChangedAttribute{Name: "name", Read: ReadNone}
	w := &readWalker{file: "reads.tf", targets: map[string]*readTarget{"aws_lb.this": {attrs: []*ChangedAttribute{a}}}, counted: map[string]bool{}, lim: lim}
	for _, bl := range body.Blocks {
		_ = hclsyntax.Walk(bl, w)
	}
	if !errors.Is(w.err, ErrDeadline) || len(a.Readers) >= 2*walkCheckEvery {
		t.Errorf("err %v, %d readers", w.err, len(a.Readers))
	}
}

// TestChangesScanBounded checks the scan itself keeps at most maxReaders
// readers per attribute and whole-object reads per block while it walks,
// before finishReaders caps the report.
func TestChangesScanBounded(t *testing.T) {
	var b strings.Builder
	b.WriteString("locals {\n")
	for i := range 4 * maxReaders {
		fmt.Fprintf(&b, "  w%d = aws_lb.this\n  n%d = aws_lb.this.name\n", i, i)
	}
	b.WriteString("}\n")
	tr := mustTree(t, writeTree(t, map[string]string{"reads.tf": b.String(), "main.tf": "resource \"aws_lb\" \"this\" {\n  name = 1\n}\n"}))
	a := &ChangedAttribute{Name: "name", Read: ReadNone}
	tg := &readTarget{attrs: []*ChangedAttribute{a}}
	if err := scanDirReads(tr, ".", map[string]*readTarget{"aws_lb.this": tg}, DefaultLimits); err != nil {
		t.Fatal(err)
	}
	if len(tg.whole) != maxReaders || tg.wholeCount != 4*maxReaders || len(a.Readers) != maxReaders || !a.ReadersTruncated {
		t.Errorf("whole %d of %d, readers %d, truncated %v", len(tg.whole), tg.wholeCount, len(a.Readers), a.ReadersTruncated)
	}
}
