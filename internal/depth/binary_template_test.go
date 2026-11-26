//go:build unix

package depth

import (
	"path/filepath"
	"slices"
	"testing"
)

// TestBinaryTemplateUnresolved checks that a templatefile target whose bytes
// the tree reader did not keep, because it is binary, is an unresolved file
// dependency, not a template with no function call.
func TestBinaryTemplateUnresolved(t *testing.T) {
	t.Parallel()
	fx := loadFixture(t, filepath.Join("testdata", "light-root-readme.txt"))
	if r := fx.run(t); r.Depth != "light" {
		t.Fatalf("the text template fixture is %s, want light", r.Depth)
	}
	for _, s := range []string{"base/x.tpl", "head/x.tpl"} {
		fx.sections[s] = "\x00hello ${name}\n"
	}
	r := fx.run(t)
	if r.Depth == "light" || !slices.Contains(r.Reasons, ReasonUnresolvedFileDependency) {
		t.Errorf("binary template: depth %s, reasons %v, want %s", r.Depth, r.Reasons, ReasonUnresolvedFileDependency)
	}
}
