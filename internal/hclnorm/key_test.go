package hclnorm

import "testing"

func TestKey(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name   string
		a, b   string
		config bool
		same   bool
	}{
		{"layout", "merge(a,b)", "merge( a ,\n b )", false, true},
		{"comment", "a = 1 # x\n", "a = 1\n", true, true},
		{"block comment", "f(/* x */ 1)", "f(1)", false, true},
		{"value", "a + 1", "a + 2", false, false},
		{"name", "x = 1\n", "y = 1\n", true, false},
		{"string versus name", `"a"`, "a", false, false},
	} {
		if got := Key([]byte(c.a), c.config) == Key([]byte(c.b), c.config); got != c.same {
			t.Errorf("%s: Key(%q) == Key(%q) is %v, want %v", c.name, c.a, c.b, got, c.same)
		}
	}
}
