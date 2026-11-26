package facts

import (
	"fmt"
	"strings"
	"testing"
)

// budgetLimits are the default limits with a run token budget of n, for one
// run.
func budgetLimits(n int64) Limits {
	lim := DefaultLimits
	lim.MaxRunTokens = n
	return lim.StartRun()
}

// TestRunTokenBudget checks the run's token budget: the files parsed within
// it keep their facts, the first file past it and every file after it is
// refused as a limit, both on the scanned path and the lexed one, and a
// Limits with no run count or no budget refuses nothing.
func TestRunTokenBudget(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"a.tf": denseTF(100),                                                      // scanned: about 400 tokens
		"b.tf": "variable \"h\" {\n  default = <<EOT\nx\nEOT\n}\n" + denseTF(100), // a heredoc: lexed
		"c.tf": denseTF(100),
	}
	tree := mustTree(t, writeTree(t, files))
	parse := func(t *testing.T, lim Limits) *Module {
		t.Helper()
		m, err := ParseDir(tree, ".", lim)
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	t.Run("within the budget nothing is refused", func(t *testing.T) {
		t.Parallel()
		if m := parse(t, budgetLimits(DefaultLimits.MaxRunTokens)); len(m.Unsupported) != 0 {
			t.Errorf("unsupported: %q", m.Unsupported)
		}
	})
	t.Run("past the budget each later file is a limit", func(t *testing.T) {
		t.Parallel()
		m := parse(t, budgetLimits(600)) // a.tf fits, b.tf passes it
		want := []string{
			"b.tf: the run's token budget of 600 is spent",
			"c.tf: the run's token budget of 600 is spent",
		}
		if strings.Join(m.Unsupported, "\n") != strings.Join(want, "\n") || m.reasonCode() != ReasonLimitExceeded {
			t.Errorf("unsupported %q, code %q", m.Unsupported, m.reasonCode())
		}
		if len(m.Locals) == 0 {
			t.Error("a.tf, parsed within the budget, has no locals")
		}
	})
	t.Run("the lexed path is charged too", func(t *testing.T) {
		t.Parallel()
		lim := budgetLimits(1 << 20)
		if _, _, err := ParseBody("b.tf", []byte(files["b.tf"]), lim); err != nil {
			t.Fatal(err)
		}
		if n := lim.runTokens.Load(); n < 400 || n > 1000 {
			t.Errorf("charged %d tokens", n)
		}
	})
	t.Run("copies share one count and a new run starts at zero", func(t *testing.T) {
		t.Parallel()
		lim := budgetLimits(600)
		parse(t, lim)
		if _, err := ParseBodyOnly("x.tf", []byte(denseTF(1)), lim); !IsLimitError(err) {
			t.Errorf("a copy of the spent run: %v", err)
		}
		if _, err := ParseBodyOnly("x.tf", []byte(denseTF(1)), lim.StartRun()); err != nil {
			t.Errorf("a new run: %v", err)
		}
	})
	t.Run("no run count or no budget refuses nothing", func(t *testing.T) {
		t.Parallel()
		lim := DefaultLimits
		lim.MaxRunTokens = 1
		if m := parse(t, lim); len(m.Unsupported) != 0 {
			t.Errorf("no run count: %q", m.Unsupported)
		}
		lim = DefaultLimits
		lim.MaxRunTokens = 0
		if m := parse(t, lim.StartRun()); len(m.Unsupported) != 0 {
			t.Errorf("no budget: %q", m.Unsupported)
		}
	})
	t.Run("ParseBodyOnly gives ParseBody's body and error", func(t *testing.T) {
		t.Parallel()
		for name, src := range files {
			only, err1 := ParseBodyOnly(name, []byte(src), DefaultLimits)
			body, _, err2 := ParseBody(name, []byte(src), DefaultLimits)
			if errText(err1) != errText(err2) || len(only.Blocks) != len(body.Blocks) {
				t.Errorf("%s: %v and %v", name, err1, err2)
			}
		}
	})
}

// BenchmarkRunTokenBudget parses dense files near the per-file token cap
// under the default run budget until it is spent, so a change of the budget
// or of what a parse charges shows its time and memory.
func BenchmarkRunTokenBudget(b *testing.B) {
	srcs := make([][]byte, 48)
	for i := range srcs {
		srcs[i] = []byte(denseTF(65000))
	}
	b.ReportAllocs()
	for b.Loop() {
		lim := DefaultLimits.StartRun()
		refused := 0
		for i, src := range srcs {
			if _, err := ParseBodyOnly(fmt.Sprintf("f%02d.tf", i), src, lim); IsLimitError(err) {
				refused++
			} else if err != nil {
				b.Fatal(err)
			}
		}
		if refused == 0 {
			b.Fatal("the budget was never spent")
		}
	}
}
