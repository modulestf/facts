package behaviour

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/modulestf/facts/internal/facts"
)

// eks75 is recorded from a terraform-aws-modules/terraform-aws-eks-pod-identity
// pull request: aws_ebs_csi.tf and main.tf at the merge base and the head.
const eks75 = "testdata/eks75"

// l1Lines lists the lines of the triggers that select L1 in file.
func l1Lines(r *Report, file string) []int {
	var out []int
	for _, tr := range r.Triggers {
		if tr.File == file && slices.Contains(tr.Levels, LevelL1) {
			out = append(out, *tr.Line)
		}
	}
	return out
}

// TestDataReadL1Recorded: a statement's resources list whose strings change
// only in their literal parts keeps its L4 trigger and gets no data-read L1
// trigger; a list that gains a reference keeps L1.
func TestDataReadL1Recorded(t *testing.T) {
	r, err := Run(Options{Head: filepath.Join(eks75, "head"), Base: filepath.Join(eks75, "base"), Limits: facts.DefaultLimits})
	if err != nil {
		t.Fatal(err)
	}
	got := l1Lines(r, "aws_ebs_csi.tf")
	// The four lists that go from ["*"] to a list of templates on
	// local.partition. Before the known-atoms test, six more lists whose
	// strings changed only in their literal parts selected L1 too.
	if want := []int{98, 109, 123, 138}; !slices.Equal(got, want) {
		t.Errorf("L1 lines %v, want %v", got, want)
	}
	if n := len(r.Triggers); n != 35 {
		t.Errorf("%d triggers, want 35", n)
	}
	for _, tr := range r.Triggers {
		if tr.File == "aws_ebs_csi.tf" && len(tr.Levels) == 0 {
			t.Errorf("%s selects no level", tr.ID)
		}
	}
}

// TestDataReadL1Atoms: the data-read L1 trigger is left out only when both
// sides have the same set of subexpressions that decide what is known.
func TestDataReadL1Atoms(t *testing.T) {
	data := func(v string) map[string]string {
		return map[string]string{"main.tf": "locals {\n  p = \"aws\"\n  m = {}\n  k = \"k\"\n  l = []\n}\n" + varX +
			"data \"aws_look\" \"l\" {\n  name = " + v + "\n}\n"}
	}
	cases := []struct {
		name, base, head string
		l1               bool
	}{
		{"same refs, changed literal", `"${local.p}-a"`, `"${local.p}-b"`, false},
		{"literal tuple element added", `["${local.p}-a"]`, `["${local.p}-a", "x"]`, false},
		{"reference repeated", `["${local.p}-a"]`, `["${local.p}-a", "${local.p}-b"]`, false},
		{"object literal value", `{ k = local.p, v = "a" }`, `{ k = local.p, v = "b" }`, false},
		{"reference added", `"${local.p}-a"`, `"${local.p}-${var.x}"`, true},
		{"reference removed", `"${local.p}-${var.x}"`, `"${local.p}-a"`, true},
		{"literal to reference", `["*"]`, `["${local.p}-a"]`, true},
		{"function added", `"${local.p}-a"`, `"${upper(local.p)}-a"`, true},
		{"function argument literal", `lookup(local.m, "a", "")`, `lookup(local.m, "b", "")`, true},
		{"index key literal", `local.m["a"]`, `local.m["b"]`, true},
		{"conditional added", `"${local.p}-a"`, `"${var.x ? local.p : "a"}-a"`, true},
		{"for added", `[local.p]`, `[for s in [local.p] : s]`, true},
		{"interpolation wrap", `"${local.p}"`, `["${local.p}", "x"]`, false},
		{"parentheses", `"${(local.p)}-a"`, `"${local.p}-b"`, false},
		{"splat changed", `local.l[*].a`, `local.l[*].b`, true},
		{"splat in tuple", `[local.l[*].a, "x"]`, `[local.l[*].a, "y"]`, false},
		{"directive gains a hidden reference", `"%{if var.x}ok%{endif}"`, `"%{if var.x}ok /* ${local.p} */%{endif}"`, true},
		{"for directive gains a hidden reference", `"%{for s in local.l}${s}%{endfor}"`, `"%{for s in local.l}${s} /* ${var.x} */%{endfor}"`, true},
		{"directive unchanged but literal", `"%{if var.x}a%{endif}"`, `"%{if var.x}b%{endif}"`, true},
		{"computed key reordered", `{ a = var.x, (local.k) = "b" }`, `{ (local.k) = "b", a = var.x }`, true},
		{"literal key to computed key", `{ k = "a" }`, `{ (local.p) = "a" }`, true},
		{"duplicate key removed", `{ a = var.x, a = "lit" }`, `{ a = var.x }`, true},
		{"bare and quoted key are one key", `{ a = var.x, "a" = "b" }`, `{ "a" = "b", a = var.x }`, true},
		{"bare and escaped key are one key", `{ a = var.x, "\u0061" = "b" }`, `{ "\u0061" = "b", a = var.x }`, true},
		{"quoted key literal value", `{ "a" = local.p, b = "x" }`, `{ "a" = local.p, b = "y" }`, false},
	}
	for _, c := range cases {
		r := runPair(t, data(c.base), data(c.head), "")
		var l1, other bool
		for _, tr := range atAttr(r, "data.aws_look.l", "name") {
			l1 = l1 || slices.Contains(tr.Levels, LevelL1)
			other = other || len(tr.Levels) > 0 && !slices.Equal(tr.Levels, []string{LevelL1})
		}
		if l1 != c.l1 {
			t.Errorf("%s: L1 %v, want %v", c.name, l1, c.l1)
		}
		if !other {
			t.Errorf("%s: no trigger beside L1 needs the plan", c.name)
		}
	}
	// Formatting alone is no change at all.
	if r := runPair(t, data(`[ "${local.p}-a" ]`), data("[\n    \"${local.p}-a\"\n  ]"), ""); len(atAttr(r, "data.aws_look.l", "name")) != 0 {
		t.Errorf("formatting: %v", categories(r))
	}
}
