package facts

import (
	"strings"
	"testing"
)

// TestOversizeSameContent: two oversize files with the same bytes are the
// same entry; different bytes, or files over the hash cap, are not.
func TestOversizeSameContent(t *testing.T) {
	big := strings.Repeat("z", int(DefaultLimits.MaxFileBytes)+1)
	a := mustTree(t, writeTree(t, map[string]string{"x.zip": big, "y.zip": big + "1"}))
	b := mustTree(t, writeTree(t, map[string]string{"x.zip": big, "y.zip": big + "2"}))
	if e := a.Entries["x.zip"]; e.Kind != KindOversize || e.Data != nil || e.SHA256 != "" {
		t.Fatalf("x.zip: kind %s, %d bytes kept, sha %q", e.Kind, len(e.Data), e.SHA256)
	}
	if !a.Entries["x.zip"].Same(b.Entries["x.zip"]) {
		t.Error("identical oversize files differ")
	}
	if a.Entries["y.zip"].Same(b.Entries["y.zip"]) {
		t.Error("different oversize files are the same")
	}
	if a.TreeSHA256() != mustTree(t, writeTree(t, map[string]string{"x.zip": big, "y.zip": big + "3"})).TreeSHA256() {
		t.Error("the tree hash must not depend on oversize content")
	}

	defer func(n int64) { oversizeHashCap = n }(oversizeHashCap)
	oversizeHashCap = DefaultLimits.MaxFileBytes + 1
	a = mustTree(t, writeTree(t, map[string]string{"y.zip": big + "1"}))
	b = mustTree(t, writeTree(t, map[string]string{"y.zip": big + "1"}))
	if a.Entries["y.zip"].Same(b.Entries["y.zip"]) {
		t.Error("files over the hash cap must stay changed")
	}
}
