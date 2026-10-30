package facts

import "testing"

func TestParseHCLLimits(t *testing.T) {
	lim := DefaultLimits
	lim.MaxDepth = 2
	if _, _, err := ParseHCL("x.tf", []byte("a = [[[1]]]\n"), lim); err == nil {
		t.Error("want a nesting limit error")
	}
	if _, _, err := ParseHCL("x.tf", []byte("a = \n"), DefaultLimits); err == nil {
		t.Error("want a parse error")
	}
	b, _, err := ParseHCL("x.tf", []byte("a = 1\n"), DefaultLimits)
	if err != nil || b.Attributes["a"] == nil {
		t.Errorf("want attribute a, got %v", err)
	}
}
