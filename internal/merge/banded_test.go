package merge

import (
	"slices"
	"testing"

	"github.com/modulestf/facts/internal/registry"
)

const ruleClaim = "compat.claim-contradicts-analysis"

// mergeClaim merges a reply with a compat.expression-behaviour-changed code
// finding at codeSev and a compat.claim-contradicts-analysis description
// finding at claimSev, the PR 556 shape, and returns rule id to severity.
func mergeClaim(t *testing.T, reg *registry.Registry, codeSev, claimSev string) map[string]string {
	t.Helper()
	b := testBundle(t, `{"tool_findings":[],"candidates":[],"severity_needed":[]}`)
	reply := `{"candidate_decisions":[],"findings":[
 {"id":"F-001","rule_id":"compat.expression-behaviour-changed","severity":"` + codeSev + `","file":"main.tf","line":10,"summary":"s","suggested_fix":"f"},
 {"id":"F-002","rule_id":"` + ruleClaim + `","severity":"` + claimSev + `","file":".","line":null,"summary":"s","suggested_fix":"no code change: f"}]}`
	got, err := Final(Inputs{Registry: reg, Bundle: b}, []byte(reply), nil)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, f := range got {
		out[f.RuleID] = f.Severity
	}
	if len(got) != 2 {
		t.Fatalf("want the code and the description finding, both kept: %v", out)
	}
	return out
}

// TestFinalClaimBanded: the description finding is banded, so each
// severity its rule lists is kept as returned, and one above the list is
// capped to its top.
func TestFinalClaimBanded(t *testing.T) {
	reg := testRegistry(t)
	for sev, want := range map[string]string{"LOW": "LOW", "MEDIUM": "MEDIUM", "HIGH": "HIGH", "CRITICAL": "HIGH"} {
		if got := mergeClaim(t, reg, "HIGH", sev)[ruleClaim]; got != want {
			t.Errorf("%s claim merged as %s, want %s", sev, got, want)
		}
	}
	// PR 556: a LOW code finding and a LOW description finding stay LOW.
	if got := mergeClaim(t, reg, "LOW", "LOW"); got[ruleClaim] != "LOW" || got["compat.expression-behaviour-changed"] != "LOW" {
		t.Errorf("PR 556 shape: %v", got)
	}
}

// TestFinalPinnedClaimBanded runs the same merge on the pinned registry.
// It is registry-version specific: up to the banded registry the rule lists
// HIGH only, so a lower claim is raised to HIGH; from it the rule lists
// HIGH, MEDIUM and LOW, each kept as returned.
func TestFinalPinnedClaimBanded(t *testing.T) {
	reg := pinnedRegistry(t)
	r, ok := reg.Rule(ruleClaim)
	if !ok {
		t.Fatalf("no rule %s", ruleClaim)
	}
	var want map[string]string
	switch {
	case slices.Equal(r.Severities, []string{"HIGH"}):
		want = map[string]string{"LOW": "HIGH", "MEDIUM": "HIGH", "HIGH": "HIGH", "CRITICAL": "HIGH"}
	case sameSet(r.Severities, []string{"HIGH", "MEDIUM", "LOW"}):
		want = map[string]string{"LOW": "LOW", "MEDIUM": "MEDIUM", "HIGH": "HIGH", "CRITICAL": "HIGH"}
	default:
		t.Fatalf("%s severities %v: neither the HIGH-only nor the banded registry", ruleClaim, r.Severities)
	}
	for sev, w := range want {
		if got := mergeClaim(t, reg, "LOW", sev)[ruleClaim]; got != w {
			t.Errorf("%s claim merged as %s, want %s (severities %v)", sev, got, w, r.Severities)
		}
	}
}
