package behaviour

import (
	"strings"
	"testing"
)

func onlyTrigger(t *testing.T, r *Report) Trigger {
	t.Helper()
	if len(r.Triggers) != 1 {
		t.Fatalf("triggers %+v, want one", r.Triggers)
	}
	return r.Triggers[0]
}

func noLadder() *Ladder { return &Ladder{Coverage: Coverage{Class: "complete"}} }

func evalOne(tr Trigger, l *Ladder) TriggerState {
	return evalTrigger(tr, l, nil, nil, map[string]bool{}, false)
}

func TestLimitFileSize(t *testing.T) {
	t.Parallel()
	big := "# " + strings.Repeat("x", 3<<20) + "\n"
	tr := onlyTrigger(t, runPair(t, map[string]string{"main.tf": big + "locals {}\n"}, map[string]string{"main.tf": big + "locals {\n}\n"}, ""))
	if tr.Limit != LimitFileSize {
		t.Fatalf("limit %q, want %q (%s)", tr.Limit, LimitFileSize, str(tr.Reason))
	}
	if ts := evalOne(tr, noLadder()); ts.State != StateUnverifiable || ts.Limit != LimitFileSize {
		t.Fatalf("state %+v", ts)
	}
}

func TestLimitParser(t *testing.T) {
	t.Parallel()
	deep := "locals {\n  x = " + strings.Repeat("(", 100) + "1" + strings.Repeat(")", 100) + "\n}\n"
	tr := onlyTrigger(t, runPair(t, map[string]string{"main.tf": deep}, map[string]string{"main.tf": deep + "# c\n"}, ""))
	if tr.Limit != LimitParser {
		t.Fatalf("limit %q, want %q (%s)", tr.Limit, LimitParser, str(tr.Reason))
	}
	if ts := evalOne(tr, noLadder()); ts.Limit != LimitParser {
		t.Fatalf("state %+v", ts)
	}
	// A file that does not parse is no tool limit.
	bad := "locals {\n  x = \n}\n"
	tr = onlyTrigger(t, runPair(t, map[string]string{"main.tf": bad}, map[string]string{"main.tf": bad + "# c\n"}, ""))
	if tr.Limit != "" || evalOne(tr, noLadder()).Limit != "" {
		t.Fatalf("limit on a parse error: %+v", tr)
	}
}

func TestLimitNoInputStates(t *testing.T) {
	t.Parallel()
	out := func(v string) map[string]string {
		return map[string]string{"main.tf": "locals {\n  p = \"aws\"\n}\noutput \"o\" {\n  value = \"${local.p}-" + v + "\"\n}\n"}
	}
	tr := onlyTrigger(t, runPair(t, out("a"), out("b"), ""))
	if tr.Limit != LimitNoInputStates || len(tr.Cases) != 0 {
		t.Fatalf("limit %q cases %d (%s)", tr.Limit, len(tr.Cases), str(tr.Reason))
	}
	if ts := evalOne(tr, noLadder()); ts.State != StateUnverifiable || ts.Limit != LimitNoInputStates {
		t.Fatalf("state %+v", ts)
	}
	// With L4 too and no plan, the limit is not the only cause.
	both := tr
	both.Levels = []string{LevelL1, LevelL4}
	if ts := evalOne(both, noLadder()); ts.Limit != "" {
		t.Fatalf("limit beside an uncovered L4: %+v", ts)
	}
	// The token alone, the sweep not refused for it, or cases present: no limit.
	l4 := tr
	l4.Levels = []string{LevelL4}
	none := tr
	none.Limit = ""
	withCase := tr
	withCase.Cases = []Case{{ID: "c"}}
	for _, x := range []Trigger{l4, none, withCase} {
		if ts := evalOne(x, noLadder()); ts.Limit != "" {
			t.Fatalf("limit on %+v: %+v", x, ts)
		}
	}
	// Another refusal of the sweep is no tool limit.
	cmpc := func(v string) map[string]string {
		return map[string]string{"main.tf": "variable \"p\" {\n  type = string\n}\noutput \"o\" {\n  value = var.p == upper(\"" + v + "\")\n}\n"}
	}
	if tr := onlyTrigger(t, runPair(t, cmpc("a"), cmpc("b"), "")); tr.Limit != "" || !strings.Contains(str(tr.Reason), "computed constant") {
		t.Fatalf("computed constant trigger %+v %s", tr, str(tr.Reason))
	}
	// A variable has input states: no limit.
	vars := func(v string) map[string]string {
		return map[string]string{"main.tf": "variable \"p\" {\n  type = string\n}\noutput \"o\" {\n  value = \"${var.p}-" + v + "\"\n}\n"}
	}
	tr = onlyTrigger(t, runPair(t, vars("a"), vars("b"), ""))
	if tr.Limit != "" || evalOne(tr, noLadder()).Limit != "" {
		t.Fatalf("limit on a variable: %+v", tr)
	}
}

func TestLimitModuleCallArgument(t *testing.T) {
	t.Parallel()
	addr, attr, line := "module.wafv2", "rules", 43
	tr := Trigger{ID: "T-001", File: "examples/complete/main.tf", Line: &line, Category: CatLiteral,
		Address: &addr, Attribute: &attr, Levels: []string{LevelL4}}
	ex := CoverageExample{Dir: "examples/complete", Head: "planned", Addresses: []string{"module.wafv2.aws_wafv2_web_acl.this"}}
	ts := evalOne(tr, &Ladder{Coverage: Coverage{Class: "complete", Examples: []CoverageExample{ex}}})
	if ts.State != StateUnverifiable || ts.Limit != LimitModuleCallArg || !strings.Contains(ts.Reason, "a plan shows no module call argument") {
		t.Fatalf("state %+v", ts)
	}
	// A compat family beside it: not the only cause.
	compat := tr
	compat.Levels = []string{LevelL4, LevelCompat}
	if ts := evalOne(compat, &Ladder{Coverage: Coverage{Class: "complete", Examples: []CoverageExample{ex}}}); ts.Limit != "" {
		t.Fatalf("limit beside compat: %+v", ts)
	}
	// Nothing under the call planned: not a tool limit.
	ex.Addresses = []string{"aws_s3_bucket.other"}
	ts = evalOne(tr, &Ladder{Coverage: Coverage{Class: "complete", Examples: []CoverageExample{ex}}})
	if ts.Limit != "" || strings.Contains(ts.Reason, "module call argument") {
		t.Fatalf("state %+v", ts)
	}
}

// Only the L1 sweep reads expression text, so only an L1 trigger says an
// expression was left out.
func TestExpressionCapReasonL1Only(t *testing.T) {
	t.Parallel()
	long := func(c string) string { return strings.Repeat(c, 5000) }
	res := func(v string) map[string]string {
		return map[string]string{"main.tf": "resource \"aws_s3_bucket\" \"b\" {\n  bucket = \"" + v + "\"\n}\n"}
	}
	tr := onlyTrigger(t, runPair(t, res(long("a")), res(long("b")), ""))
	if slices := tr.Levels; len(slices) != 1 || slices[0] != LevelL4 || strings.Contains(str(tr.Reason), "bytes") {
		t.Fatalf("L4 trigger %v reason %s", tr.Levels, str(tr.Reason))
	}
	out := func(v string) map[string]string {
		return map[string]string{"main.tf": "variable \"p\" {\n  type = string\n}\noutput \"o\" {\n  value = \"${var.p}" + v + "\"\n}\n"}
	}
	for _, pair := range [][2]string{{long("a"), "b"}, {"a", long("b")}} {
		tr = onlyTrigger(t, runPair(t, out(pair[0]), out(pair[1]), ""))
		if !strings.Contains(str(tr.Reason), "over 4096 bytes") {
			t.Fatalf("one side long: reason %s", str(tr.Reason))
		}
	}
	tr = onlyTrigger(t, runPair(t, out(long("a")), out(long("b")), ""))
	if !strings.Contains(str(tr.Reason), "over 4096 bytes") {
		t.Fatalf("L1 trigger %v %s reason %s", tr.Levels, tr.Category, str(tr.Reason))
	}
}

// A token only when every cause of a directory that does not decompose,
// at both revisions, is the same review tool limit.
func TestLimitMixedCauses(t *testing.T) {
	t.Parallel()
	big := "# " + strings.Repeat("x", 3<<20) + "\n"
	deep := "locals {\n  x = " + strings.Repeat("(", 100) + "1" + strings.Repeat(")", 100) + "\n}\n"
	bad := "locals {\n  x = \n}\n"
	ok := "locals {}\n"
	cases := []struct {
		name       string
		base, head map[string]string
		want       string
	}{
		{"oversize and a parse error at the head", map[string]string{"a.tf": ok, "b.tf": ok},
			map[string]string{"a.tf": big, "b.tf": bad}, ""},
		{"a parse error before an oversize file", map[string]string{"a.tf": ok, "b.tf": ok},
			map[string]string{"a.tf": bad, "b.tf": big}, ""},
		{"oversize at the base, a parse error at the head", map[string]string{"a.tf": big, "b.tf": ok},
			map[string]string{"a.tf": big, "b.tf": bad}, ""},
		{"oversize at the base, a parser limit at the head", map[string]string{"a.tf": big, "b.tf": ok},
			map[string]string{"a.tf": ok, "b.tf": deep}, ""},
		{"oversize and an override file", map[string]string{"a.tf": ok, "override.tf": ok},
			map[string]string{"a.tf": big, "override.tf": ok}, ""},
		{"oversize and a block declared twice", map[string]string{"a.tf": ok, "b.tf": "locals {\n  y = 1\n}\n"},
			map[string]string{"a.tf": big, "b.tf": "locals {\n  y = 1\n}\nlocals {\n  y = 2\n}\n"}, ""},
		{"two oversize files", map[string]string{"a.tf": ok, "b.tf": ok},
			map[string]string{"a.tf": big, "b.tf": big + "#\n"}, LimitFileSize},
		{"oversize at the head only", map[string]string{"a.tf": ok, "b.tf": ok},
			map[string]string{"a.tf": big, "b.tf": ok + "#\n"}, LimitFileSize},
		{"oversize at the base only", map[string]string{"a.tf": big, "b.tf": ok},
			map[string]string{"a.tf": ok, "b.tf": ok + "#\n"}, LimitFileSize},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := runPair(t, tc.base, tc.head, "")
			if len(r.Triggers) == 0 {
				t.Fatal("no trigger")
			}
			for _, tr := range r.Triggers {
				if tr.Limit != tc.want {
					t.Fatalf("%s: limit %q, want %q (%s)", tr.File, tr.Limit, tc.want, str(tr.Reason))
				}
				if got := evalOne(tr, noLadder()).Limit; got != tc.want {
					t.Fatalf("%s: eval limit %q, want %q", tr.File, got, tc.want)
				}
			}
		})
	}
}

// An unverifiable claim is a cause of its own: no token beside it.
func TestLimitUnverifiableClaim(t *testing.T) {
	t.Parallel()
	addr, attr := "module.wafv2", "rules"
	l := &Ladder{Coverage: Coverage{Class: "complete", Examples: []CoverageExample{{Dir: "examples/complete", Head: "planned",
		Addresses: []string{"module.wafv2.aws_wafv2_web_acl.this"}}}}}
	claim := []ClaimState{{ClaimID: "C-1", State: StateUnverifiable}}
	for _, tc := range []struct {
		token string
		tr    Trigger
	}{
		{LimitFileSize, Trigger{ID: "T-001", File: "main.tf", Category: CatUnclassified, Levels: []string{LevelL4}, Limit: LimitFileSize}},
		{LimitParser, Trigger{ID: "T-001", File: "main.tf", Category: CatUnclassified, Levels: []string{LevelL4}, Limit: LimitParser}},
		{LimitNoInputStates, Trigger{ID: "T-001", File: "main.tf", Category: CatLocalsOutputs, Levels: []string{LevelL1}, Limit: LimitNoInputStates}},
		{LimitModuleCallArg, Trigger{ID: "T-001", File: "examples/complete/main.tf", Category: CatLiteral, Address: &addr, Attribute: &attr, Levels: []string{LevelL4}}},
	} {
		if ts := evalOne(tc.tr, l); ts.Limit != tc.token {
			t.Fatalf("%s: without a claim limit %q", tc.token, ts.Limit)
		}
		if ts := evalTrigger(tc.tr, l, claim, nil, map[string]bool{}, false); ts.State != StateUnverifiable || ts.Limit != "" {
			t.Fatalf("%s: beside an unverifiable claim %+v", tc.token, ts)
		}
	}
}
