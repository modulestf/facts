//go:build unix

package facts

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// binaryBody returns n bytes with a NUL byte first, as a build archive or
// object file starts.
func binaryBody(n int) string { return "\x00" + strings.Repeat("b", n-1) }

func sha(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// goldenTree is a tree with a binary file, text files of names no rule reads
// and a binary file of a name a rule reads. Its tree hash was recorded with
// the reader that kept every file in memory.
var goldenTree = map[string]string{
	"main.tf":                "variable \"x\" {}\n",
	"builds/lambda.zip":      binaryBody(3000),
	"src/index.py":           "print(1)\n",
	"templates/user_data.sh": "echo ${x}\n",
	"broken.tf":              binaryBody(10),
}

const goldenTreeSHA256 = "9a1dbae2d799ee91d0962fd2050a8360edef440e9920520f75f3c6fd63b5ffbd"

func TestTreeHashUnchangedByBinaryFiles(t *testing.T) {
	tr := mustTree(t, writeTree(t, goldenTree))
	if got := tr.TreeSHA256(); got != goldenTreeSHA256 {
		t.Errorf("tree hash %s, want %s", got, goldenTreeSHA256)
	}
	for p, body := range goldenTree {
		e := tr.Entries[p]
		if e == nil || e.Kind != KindFile || e.SHA256 != sha(body) {
			t.Errorf("%s: got %+v, want a file of hash %s", p, e, sha(body))
		}
	}
}

func TestReadTreeKeepsOnlyReadableFiles(t *testing.T) {
	tr := mustTree(t, writeTree(t, goldenTree))
	for p, wantKept := range map[string]bool{
		"main.tf": true, "src/index.py": true, "templates/user_data.sh": true,
		"broken.tf":         true, // a rule reads it by name, so a NUL byte does not drop it
		"builds/lambda.zip": false,
	} {
		e := tr.Entries[p]
		if kept := e.Data != nil; kept != wantKept || e.Opaque() == wantKept {
			t.Errorf("%s: kept %v, opaque %v, want kept %v", p, kept, e.Opaque(), wantKept)
		}
	}
}

func TestReadTreeBinaryBytesNotCounted(t *testing.T) {
	files := map[string]string{"main.tf": "# ok\n"}
	for _, n := range []string{"a.zip", "b.rlib", "c.pyc", "d", "e.tftpl"} {
		files["builds/"+n] = binaryBody(4000)
	}
	root := writeTree(t, files)
	lim := DefaultLimits
	lim.MaxTotalBytes = 100
	tr, err := ReadTree(root, lim)
	if err != nil {
		t.Fatalf("binary files counted toward the kept bytes: %v", err)
	}
	if e := tr.Entries["builds/e.tftpl"]; !e.Opaque() || e.SHA256 != sha(files["builds/e.tftpl"]) {
		t.Errorf("binary template: %+v", e)
	}
	lim.opaqueBudget = 3 * 4000
	if _, err := ReadTree(root, lim); err == nil || !strings.Contains(err.Error(), "binary") {
		t.Errorf("binary byte budget not enforced: %v", err)
	}
}

func TestReadTreeTextStillCounted(t *testing.T) {
	root := writeTree(t, map[string]string{"a.py": strings.Repeat("x", 60), "b.json": strings.Repeat("y", 60)})
	lim := DefaultLimits
	lim.MaxTotalBytes = 100
	if _, err := ReadTree(root, lim); err == nil {
		t.Error("text bytes not counted toward the kept bytes")
	}
}

func TestReadTreeNULPastProbeIsText(t *testing.T) {
	body := strings.Repeat("x", binaryProbe) + "\x00"
	e := mustTree(t, writeTree(t, map[string]string{"a.bin": body})).Entries["a.bin"]
	if e.Opaque() || string(e.Data) != body {
		t.Errorf("a NUL past the probe made the file opaque: %+v", e.Opaque())
	}
}

func TestReadTreeHostileLargeFiles(t *testing.T) {
	root := writeTree(t, map[string]string{
		"main.tf":    strings.Repeat("#", 2000),
		"zero":       "->/dev/zero",
		"zero.tf":    "->/dev/zero",
		"random.zip": "->/dev/urandom",
	})
	if err := syscall.Mkfifo(filepath.Join(root, "fifo.tftpl"), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := range 50 {
		name := filepath.Join(root, "builds", strings.Repeat("z", i+1)+".zip")
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte(binaryBody(1000)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	lim := DefaultLimits
	lim.MaxFileBytes = 1000
	lim.MaxTotalBytes = 1000
	tr, err := ReadTree(root, lim)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"main.tf": KindOversize, "zero": KindSymlink, "zero.tf": KindSymlink,
		"random.zip": KindSymlink, "fifo.tftpl": KindSpecial,
	}
	for p, k := range want {
		if e := tr.Entries[p]; e.Kind != k || e.Data != nil {
			t.Errorf("%s: kind %s, data %d bytes, want %s and no data", p, e.Kind, len(e.Data), k)
		}
	}
	lim.opaqueBudget = 49 * 1000
	if _, err := ReadTree(root, lim); err == nil {
		t.Error("many binary files passed a budget they exceed")
	}
}

func TestProfileLimitsKeepBinaryFiles(t *testing.T) {
	tr, err := ReadTree(writeTree(t, map[string]string{"a.bin": binaryBody(10)}), ProfileLimits())
	if err != nil {
		t.Fatal(err)
	}
	if e := tr.Entries["a.bin"]; e.Opaque() || len(e.Data) != 10 {
		t.Errorf("a profile file was not kept: %+v", e)
	}
}

func TestChangeMapOpaqueFileLinesUnknown(t *testing.T) {
	head := mustTree(t, writeTree(t, map[string]string{"a.zip": binaryBody(20)}))
	base := mustTree(t, writeTree(t, map[string]string{"a.zip": binaryBody(30)}))
	ch, err := BuildChangeMap(head, base, nil, DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	if changed, unknown := ch.rangeChanged("a.zip", 1, 1); changed || !unknown {
		t.Errorf("opaque file lines: changed %v, unknown %v", changed, unknown)
	}
}

func TestRebuildOpaqueHasNoPatch(t *testing.T) {
	h := mustTree(t, writeTree(t, map[string]string{"a.zip": binaryBody(20)})).Entries["a.zip"]
	for _, c := range []fileChange{{path: "a.zip", status: statusModeOnly}, {path: "b.zip", status: statusPureRename}, {path: "a.zip", patch: []byte("@@ -1 +1 @@\n-x\n+y\n")}} {
		e, err := rebuildContent(c.path, h, c, DefaultLimits)
		if err != nil || e.Kind != KindNoPatch {
			t.Errorf("%+v: got %+v, %v, want no patch", c.status, e, err)
		}
	}
}
