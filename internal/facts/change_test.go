package facts

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestChangedLines(t *testing.T) {
	for name, tc := range map[string]struct {
		base, head string
		want       []lineRange
	}{
		"same":                 {base: "a\nb\n", head: "a\nb\n", want: nil},
		"added file":           {base: "", head: "a\nb\nc\n", want: []lineRange{{1, 3}}},
		"insert in the middle": {base: "a\nc\n", head: "a\nb\nc\n", want: []lineRange{{2, 2}}},
		"edit one line":        {base: "a\nb\nc\n", head: "a\nB\nc\n", want: []lineRange{{2, 2}}},
		"delete only":          {base: "a\nb\nc\n", head: "a\nc\n", want: nil},
		"two hunks":            {base: "a\nb\nc\nd\ne\n", head: "x\nb\nc\ny\nz\ne\n", want: []lineRange{{1, 1}, {4, 5}}},
		"moved line":           {base: "a\nb\nc\n", head: "b\nc\na\n", want: []lineRange{{3, 3}}},
		"no final newline":     {base: "a\nb", head: "a\nb\n", want: nil},
		"crlf differs":         {base: "a\nb\n", head: "a\r\nb\n", want: []lineRange{{1, 1}}},
		"repeated lines":       {base: "}\n}\n", head: "}\nx\n}\n}\n", want: []lineRange{{2, 3}}},
	} {
		budget := maxDiffTotalCells
		got, ok := changedLines([]byte(tc.base), []byte(tc.head), &budget)
		if !ok || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %v %v, want %v", name, got, ok, tc.want)
		}
	}
}

// distinctLines returns n lines that share nothing with another prefix, so
// a diff of the two can trim nothing.
func distinctLines(prefix string, n int) string {
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, "# %s%d\n", prefix, i)
	}
	return b.String()
}

func TestChangedLinesLimit(t *testing.T) {
	// 2048 x 2048 cells is at the limit: the table needs one row and column
	// more, so it is past it.
	budget := maxDiffTotalCells
	if _, ok := changedLines([]byte(distinctLines("a", 2048)), []byte(distinctLines("b", 2048)), &budget); ok {
		t.Error("a diff past the limit was computed")
	}
	// The common prefix and suffix do not count toward the limit.
	same := distinctLines("s", 5000)
	got, ok := changedLines([]byte(same+"x\n"+same), []byte(same+"y\n"+same), &budget)
	if !ok || !reflect.DeepEqual(got, []lineRange{{5001, 5001}}) {
		t.Errorf("trimmed diff: got %v %v", got, ok)
	}
}

// The base and head of the scope tests. In head, line 1 is in an untouched
// block, lines 5 to 7 an edited block whose line 6 changed, line 9 an added
// block, line 4 an unchanged line outside every block and line 11 a changed
// line outside every block. variable "gone" is removed.
const (
	scopeBase = "variable \"same\" {\n  type = string\n}\n\nvariable \"edited\" {\n  type = string\n}\n\nvariable \"gone\" {}\n"
	scopeHead = "variable \"same\" {\n  type = string\n}\n\nvariable \"edited\" {\n  type = number\n}\n\nvariable \"new\" {}\n\n# note\n"
)

func scopeAreas(t *testing.T, base map[string]string, head map[string]string) ([]*Area, *ChangeMap) {
	t.Helper()
	ht := mustTree(t, writeTree(t, head))
	var bt *Tree
	if base != nil {
		bt = mustTree(t, writeTree(t, base))
	}
	areas, err := FindAreas(ht, bt, DefaultLimits, true)
	if err != nil {
		t.Fatal(err)
	}
	return areas, mustChange(t, ht, bt, areas)
}

func mustChange(t *testing.T, head, base *Tree, areas []*Area) *ChangeMap {
	t.Helper()
	ch, err := BuildChangeMap(head, base, areas, DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	return ch
}

// changeFor is the change map, with no base, of a head tree that holds each
// file with ten lines, for areas a test builds by hand: every line of those
// files is changed.
func changeFor(t *testing.T, areas []*Area, files ...string) *ChangeMap {
	t.Helper()
	tree := map[string]string{}
	for _, f := range files {
		tree[f] = strings.Repeat("#\n", 10)
	}
	return mustChange(t, mustTree(t, writeTree(t, tree)), nil, areas)
}

func TestBlockStatus(t *testing.T) {
	_, ch := scopeAreas(t, map[string]string{"main.tf": scopeBase}, map[string]string{"main.tf": scopeHead})
	for addr, want := range map[string]string{
		"var.same": BlockUntouched, "var.edited": BlockEdited, "var.gone": BlockRemoved, "var.new": BlockAdded, "var.none": "",
	} {
		if got := ch.BlockStatus(".", addr); got != want {
			t.Errorf("%s: got %q, want %q", addr, got, want)
		}
	}
	// With no base every block is added and every line changed.
	_, ch = scopeAreas(t, nil, map[string]string{"main.tf": scopeHead})
	for _, addr := range []string{"var.same", "var.edited", "var.new"} {
		if got := ch.BlockStatus(".", addr); got != BlockAdded {
			t.Errorf("no base: %s is %q", addr, got)
		}
	}
	for line := 1; line <= 11; line++ {
		if c, _ := ch.rangeChanged("main.tf", line, line); !c {
			t.Errorf("no base: line %d unchanged", line)
		}
	}
}

// scopeRule finds on the given lines of main.tf, and has one candidate with
// no line.
func scopeRule(scope string, cross bool, lines ...int) Rule {
	return Rule{ID: "test.scope", Tier: "tool", Class: ClassConfig, Scope: scope, CrossArea: cross,
		Kinds: []string{AreaRoot},
		Check: func(*Area) Result {
			var res Result
			for _, l := range lines {
				res.Findings = append(res.Findings, Finding{RuleID: "test.scope", Severity: "LOW", File: "main.tf", Line: &l})
			}
			res.Candidates = []Candidate{{RuleID: "test.scope", File: "main.tf"}}
			return res
		}}
}

func keptLines(fs []Finding) []int {
	out := []int{}
	for _, f := range fs {
		out = append(out, *f.Line)
	}
	slices.Sort(out)
	return out
}

// Each scope keeps its own subset. The any-file-in-area case is the mutation
// for the rest: the same rule with that scope keeps every finding, so each
// drop below is the filter's. touched-expression keeps line 5: a variable's
// type argument has no index entry of its own, so the innermost entry that
// holds line 5 is the whole edited variable, whose line 6 changed. touched-expression keeps line 5: a variable's
// type argument has no index entry of its own, so the innermost entry that
// holds line 5 is the whole edited variable, whose line 6 changed. The cross-area case shows the line-level drop:
// line 5 is in a touched area and an edited block but is not a changed line.
func TestScopeFilters(t *testing.T) {
	areas, ch := scopeAreas(t, map[string]string{"main.tf": scopeBase}, map[string]string{"main.tf": scopeHead})
	all := []int{1, 4, 5, 6, 9, 11}
	for name, tc := range map[string]struct {
		rule          Rule
		want          []int
		wantCandidate bool
		status        string
	}{
		"any-file-in-area":                {rule: scopeRule(ScopeAnyFileInArea, false, all...), want: all, wantCandidate: true, status: StatusFinding},
		"touched-block":                   {rule: scopeRule(ScopeTouchedBlock, false, all...), want: []int{5, 6, 9, 11}, wantCandidate: true, status: StatusFinding},
		"touched-expression":              {rule: scopeRule(ScopeTouchedExpression, false, all...), want: []int{5, 6, 9, 11}, wantCandidate: true, status: StatusFinding},
		"newly-introduced":                {rule: scopeRule(ScopeNewlyIntroduced, false, all...), want: []int{9, 11}, status: StatusFinding},
		"cross-area, any-file-in-area":    {rule: scopeRule(ScopeAnyFileInArea, true, all...), want: []int{6, 9, 11}, wantCandidate: true, status: StatusFinding},
		"cross-area, touched-block":       {rule: scopeRule(ScopeTouchedBlock, true, all...), want: []int{6, 9, 11}, wantCandidate: true, status: StatusFinding},
		"newly-introduced, all dropped":   {rule: scopeRule(ScopeNewlyIntroduced, false, 1, 5), want: []int{}, status: StatusClean},
		"cross-area unchanged line only":  {rule: scopeRule(ScopeAnyFileInArea, true, 5), want: []int{}, wantCandidate: true, status: StatusCandidate},
		"mutation: per area, line 5 kept": {rule: scopeRule(ScopeAnyFileInArea, false, 5), want: []int{5}, wantCandidate: true, status: StatusFinding},
	} {
		t.Run(name, func(t *testing.T) {
			m, fs, cs := Evaluate(areas, []Rule{tc.rule}, ch)
			if got := keptLines(fs); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("kept lines %v, want %v", got, tc.want)
			}
			if (len(cs) == 1) != tc.wantCandidate {
				t.Errorf("candidates %+v, want one: %v", cs, tc.wantCandidate)
			}
			if len(m) != 1 || m[0].Status != tc.status {
				t.Errorf("manifest %+v, want %s", m, tc.status)
			}
		})
	}
}

// A result that turns on the lines of a file past the diff limit is
// not_evaluated(limit_exceeded); a scope that needs no lines still runs.
func TestScopeOverLimit(t *testing.T) {
	areas, ch := scopeAreas(t, map[string]string{"main.tf": distinctLines("a", 2048)}, map[string]string{"main.tf": distinctLines("b", 2048)})
	for name, tc := range map[string]struct {
		rule Rule
		want ManifestEntry
	}{
		"cross-area": {rule: scopeRule(ScopeAnyFileInArea, true, 1),
			want: ManifestEntry{RuleID: "test.scope", Area: ".", Status: StatusNotEvaluated, Reason: ReasonLimitExceeded, ReasonCode: ReasonLimitExceeded}},
		"touched-expression": {rule: scopeRule(ScopeTouchedExpression, false, 1),
			want: ManifestEntry{RuleID: "test.scope", Area: ".", Status: StatusNotEvaluated, Reason: "a changed file is past the line diff limit", ReasonCode: ReasonLimitExceeded}},
		"any-file-in-area": {rule: scopeRule(ScopeAnyFileInArea, false, 1),
			want: ManifestEntry{RuleID: "test.scope", Area: ".", Status: StatusFinding}},
	} {
		m, _, _ := Evaluate(areas, []Rule{tc.rule}, ch)
		if !reflect.DeepEqual(m, []ManifestEntry{tc.want}) {
			t.Errorf("%s: got %+v, want %+v", name, m, tc.want)
		}
		if err := CheckManifest(m, []Rule{tc.rule}, areas); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// A rule that needs the base is not_evaluated(no_base) with no base tree,
// and runs with one. The mutation clears NeedsBase and the rule runs.
func TestNeedsBase(t *testing.T) {
	r := scopeRule(ScopeAnyFileInArea, false, 1)
	r.NeedsBase = true
	head := writeTree(t, map[string]string{"main.tf": scopeHead})
	rep, err := run(Options{Head: head, Limits: DefaultLimits}, []Rule{r})
	if err != nil {
		t.Fatal(err)
	}
	if want := []ManifestEntry{{RuleID: "test.scope", Area: ".", Status: StatusNotEvaluated, Reason: "no base revision to compare with", ReasonCode: ReasonNoBase}}; !reflect.DeepEqual(rep.Manifest, want) {
		t.Errorf("no base: %+v", rep.Manifest)
	}
	rep, err = run(Options{Head: head, Base: writeTree(t, map[string]string{"main.tf": scopeBase}), Limits: DefaultLimits}, []Rule{r})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Manifest) != 1 || rep.Manifest[0].Status != StatusFinding {
		t.Errorf("with base: %+v", rep.Manifest)
	}
	r.NeedsBase = false
	if rep, err = run(Options{Head: head, Limits: DefaultLimits}, []Rule{r}); err != nil || rep.Manifest[0].Status != StatusFinding {
		t.Errorf("mutation: %v %+v", err, rep)
	}
}

// Run applies the change map: a cross-area finding on an unchanged line of a
// touched submodule is dropped, and one on a changed line is kept.
func TestRunDropsCrossAreaFindingOnUnchangedLine(t *testing.T) {
	base := writeTree(t, map[string]string{"main.tf": "", "modules/a/main.tf": "variable \"x\" {}\n"})
	head := writeTree(t, map[string]string{"main.tf": "", "modules/a/main.tf": "variable \"x\" {}\nvariable \"y\" {}\n"})
	for line, want := range map[int]int{1: 0, 2: 1} {
		r := Rule{ID: "test.cross", Tier: "tool", Class: ClassConfig, Scope: ScopeAnyFileInArea, CrossArea: true,
			Kinds: []string{AreaRoot, AreaSubmodule},
			Check: func(a *Area) Result {
				if a.Path != "modules/a" {
					return Result{}
				}
				return Result{Findings: []Finding{finding("test.cross", a.Path, "main.tf", line, "s", "f")}}
			}}
		rep, err := run(Options{Head: head, Base: base, Limits: DefaultLimits}, []Rule{r})
		if err != nil {
			t.Fatal(err)
		}
		if len(rep.Findings) != want {
			t.Errorf("line %d: findings %+v, want %d", line, rep.Findings, want)
		}
	}
}

// Repeated addresses: each head block takes its own status. Of two
// terraform blocks, the one with an unchanged hash stays untouched.
func TestBlockStatusPerSpan(t *testing.T) {
	const one = "terraform {\n  required_version = \">= 1.0\"\n}\n"
	const two = "\nterraform {\n  experiments = []\n}\n"
	_, ch := scopeAreas(t, map[string]string{"main.tf": one + two},
		map[string]string{"main.tf": one + "\nterraform {\n  experiments = [x]\n}\n"})
	if got := ch.blockAt("main.tf", 2); got != BlockUntouched {
		t.Errorf("first terraform block: %q", got)
	}
	if got := ch.blockAt("main.tf", 6); got != BlockEdited {
		t.Errorf("second terraform block: %q", got)
	}
	if got := ch.BlockStatus(".", "terraform"); got != BlockEdited {
		t.Errorf("address: %q", got)
	}
	// One of two blocks removed: the address is edited, the head block
	// untouched.
	_, ch = scopeAreas(t, map[string]string{"main.tf": one + two}, map[string]string{"main.tf": one + "\n"})
	if got, st := ch.blockAt("main.tf", 2), ch.BlockStatus(".", "terraform"); got != BlockUntouched || st != BlockEdited {
		t.Errorf("block %q, address %q", got, st)
	}
}

// The innermost block wins: a finding on an unchanged local of an edited
// locals block is out of touched-block scope, one on the changed local is
// in. The outer locals block alone would keep both.
func TestInnermostSpanWins(t *testing.T) {
	areas, ch := scopeAreas(t,
		map[string]string{"main.tf": "locals {\n  a = 1\n  b = 2\n}\n"},
		map[string]string{"main.tf": "locals {\n  a = 1\n  b = 3\n}\n"})
	if got := ch.blockAt("main.tf", 1); got != BlockEdited {
		t.Fatalf("locals block: %q", got)
	}
	_, fs, _ := Evaluate(areas, []Rule{scopeRule(ScopeTouchedBlock, false, 2, 3)}, ch)
	if got := keptLines(fs); !reflect.DeepEqual(got, []int{3}) {
		t.Errorf("kept %v, want [3]", got)
	}
}

// touched-expression matches the innermost index entry: a finding on the
// first line of a multi-line argument whose later line changed is kept; a
// finding on an unchanged argument of the same block is not. The mutation
// checks only the finding's own line and drops line 2.
func TestTouchedExpressionMultiLine(t *testing.T) {
	const res = "resource \"x\" \"y\" {\n  tags = merge(\n    { a = 1 },\n    { b = %d },\n  )\n  name = \"n\"\n}\n"
	areas, ch := scopeAreas(t, map[string]string{"main.tf": fmt.Sprintf(res, 2)}, map[string]string{"main.tf": fmt.Sprintf(res, 3)})
	_, fs, _ := Evaluate(areas, []Rule{scopeRule(ScopeTouchedExpression, false, 2, 6)}, ch)
	if got := keptLines(fs); !reflect.DeepEqual(got, []int{2}) {
		t.Errorf("kept %v, want [2]", got)
	}
	if in, _ := ch.onChangedLine("main.tf", new(2)); in {
		t.Error("mutation: line 2 itself counts as changed")
	}
}

// A block moved to another file in the area keeps its hash, so it is
// untouched: touched-expression and touched-block drop a finding in it,
// although every line of the new file is changed.
func TestTouchedScopesSkipMovedBlock(t *testing.T) {
	const v = "variable \"x\" {\n  type = string\n}\n"
	areas, ch := scopeAreas(t, map[string]string{"a.tf": v}, map[string]string{"b.tf": v})
	if c, _ := ch.rangeChanged("b.tf", 2, 2); !c {
		t.Fatal("b.tf line 2 is not changed")
	}
	rule := func(scope string) Rule {
		r := scopeRule(scope, false)
		r.Check = func(*Area) Result {
			return Result{Findings: []Finding{{RuleID: "test.scope", Severity: "LOW", File: "b.tf", Line: new(2)}}}
		}
		return r
	}
	for scope, want := range map[string]int{ScopeTouchedExpression: 0, ScopeTouchedBlock: 0, ScopeAnyFileInArea: 1} {
		if _, fs, _ := Evaluate(areas, []Rule{rule(scope)}, ch); len(fs) != want {
			t.Errorf("%s: %d findings, want %d", scope, len(fs), want)
		}
	}
}

// A result with no line, such as a finding about a removed block, which has
// no head line, is judged by its path. newly-introduced keeps it only for a
// path the base does not have: an added file or directory.
func TestNoLineResults(t *testing.T) {
	areas, ch := scopeAreas(t,
		map[string]string{"main.tf": scopeBase, "gone.tf": "variable \"g\" {}\n", "modules/a/main.tf": ""},
		map[string]string{"main.tf": scopeHead, "new.tf": "", "modules/a/main.tf": "", "modules/new/main.tf": ""})
	for _, tc := range []struct {
		file, scope string
		want        bool
	}{
		{"main.tf", ScopeTouchedBlock, true}, // var "gone" was removed from it
		{"gone.tf", ScopeTouchedBlock, true},
		{"gone.tf", ScopeTouchedExpression, true},
		{"gone.tf", ScopeNewlyIntroduced, false},
		{"main.tf", ScopeNewlyIntroduced, false},
		{"new.tf", ScopeNewlyIntroduced, true},
		{"modules/new", ScopeNewlyIntroduced, true},
		{"modules/new", ScopeTouchedBlock, true},
		{"modules/a", ScopeNewlyIntroduced, false},
		{"modules/a", ScopeTouchedBlock, false},
	} {
		if in, _ := ch.inScope(tc.scope, tc.file, nil); in != tc.want {
			t.Errorf("%s %s: %v, want %v", tc.file, tc.scope, in, tc.want)
		}
	}
	if len(areas) == 0 {
		t.Fatal("no areas")
	}
}

// The total cell budget across files: four files at the per-file cap use it
// all, so a fifth is past it although it is under the per-file cap.
func TestChangeMapTotalBudget(t *testing.T) {
	base, head := map[string]string{}, map[string]string{}
	for _, f := range []string{"a", "b", "c", "d", "e"} {
		base[f+".txt"] = distinctLines("o"+f, 2047)
		head[f+".txt"] = distinctLines("n"+f, 2047)
	}
	_, ch := scopeAreas(t, base, head)
	var over []string
	for f := range ch.overLimit {
		over = append(over, f)
	}
	if !reflect.DeepEqual(over, []string{"e.txt"}) {
		t.Errorf("over the budget: %v, want [e.txt]", over)
	}
}

func TestChangeMapDeadline(t *testing.T) {
	head := mustTree(t, writeTree(t, map[string]string{"main.tf": ""}))
	lim := DefaultLimits
	lim.Deadline = time.Now().Add(-time.Second)
	if _, err := BuildChangeMap(head, nil, nil, lim); !errors.Is(err, ErrDeadline) {
		t.Errorf("err %v", err)
	}
	if _, err := BuildChangeMap(head, nil, nil, DefaultLimits); err != nil {
		t.Errorf("no deadline: %v", err)
	}
}

func TestEvaluateNeedsChangeMap(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("Evaluate ran with no change map")
		}
	}()
	Evaluate(nil, nil, nil)
}
