package facts

import (
	"path/filepath"
	"slices"
	"testing"
)

// TestRulePanicIsNotEvaluated makes one per-area rule and one cross-area
// rule panic in every area. The run still returns a report that passes its
// integrity checks. Each entry of the two rules that a gate does not decide
// before the check runs is not_evaluated(internal_error), with the fixed
// reason text or, for the cross-area pass, the code as its reason, as every
// cross-area reason has. Every other entry is what a run without the panics
// gives.
func TestRulePanicIsNotEvaluated(t *testing.T) {
	o := Options{Base: filepath.Join(diffFixture, "base")}
	want := runDiffFixture(t, o)

	panicking := map[string]bool{}
	for _, cross := range []bool{false, true} {
		i := slices.IndexFunc(Rules, func(r Rule) bool { return r.CrossArea == cross })
		if i < 0 {
			t.Fatalf("no rule with CrossArea %v", cross)
		}
		orig := Rules[i].Check
		Rules[i].Check = func(*Area) Result { panic("rule bug") }
		t.Cleanup(func() { Rules[i].Check = orig })
		panicking[Rules[i].ID] = true
	}
	got := runDiffFixture(t, o)

	gated := map[[2]string]bool{} // entries a gate decides before the check runs
	for _, e := range want.Manifest {
		gated[[2]string{e.RuleID, e.Area}] = e.Status == StatusNotApplicable
	}
	hit := map[string]bool{}
	for _, e := range got.Manifest {
		if !panicking[e.RuleID] || gated[[2]string{e.RuleID, e.Area}] {
			continue
		}
		if e.Status != StatusNotEvaluated || e.ReasonCode != ReasonInternalError || (e.Reason != internalErrorReason && e.Reason != ReasonInternalError) {
			t.Errorf("%s in %s: %+v", e.RuleID, e.Area, e)
		}
		hit[e.RuleID] = true
	}
	if len(hit) != len(panicking) {
		t.Errorf("entries for %v, want every rule of %v", hit, panicking)
	}
	keep := func(m []ManifestEntry) []ManifestEntry {
		return slices.DeleteFunc(slices.Clone(m), func(e ManifestEntry) bool { return panicking[e.RuleID] })
	}
	if !slices.Equal(keep(got.Manifest), keep(want.Manifest)) {
		t.Error("the other manifest entries changed")
	}
	for _, f := range got.Findings {
		if panicking[f.RuleID] {
			t.Errorf("finding of a panicking rule: %+v", f)
		}
	}
}
