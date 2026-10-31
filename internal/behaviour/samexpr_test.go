package behaviour

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/modulestf/facts/internal/facts"
)

// TestMergeSameExpressionRun: the triggers of one line with the same base
// and head expressions are one trigger; the ids are renumbered and the
// entries list the merged ids.
func TestMergeSameExpressionRun(t *testing.T) {
	base := map[string]string{"main.tf": `resource "aws_instance" "x" {
  vpc_security_group_ids = compact(var.ids)
  ami                    = var.ami
}

variable "ids" {
  type = list(string)
}
`}
	head := map[string]string{"main.tf": `resource "aws_instance" "x" {
  vpc_security_group_ids = var.ids
  ami                    = local.ami
}

variable "ids" {
  type = list(string)
}
`}
	r := runPair(t, base, head, "")
	if len(r.Triggers) != 2 {
		t.Fatalf("want 2 triggers: %s", mustJSON(t, r))
	}
	w, l := r.Triggers[0], r.Triggers[1]
	if w.ID != "T-001" || w.Category != CatWrapper || !slices.Equal(w.AlsoCategories, []string{CatUnknownPropagation}) ||
		!slices.Equal(w.Levels, []string{LevelL1, LevelL3}) || len(w.Cases) == 0 {
		t.Errorf("T-001: %s", mustJSON(t, w))
	}
	if l.ID != "T-002" || l.Category != CatUnclassified || !slices.Equal(l.AlsoCategories, []string{CatUnknownPropagation}) ||
		!slices.Equal(l.Levels, []string{LevelL1, LevelL3, LevelL4}) || str(l.Attribute) != "ami" || l.Reason == nil {
		t.Errorf("T-002: %s", mustJSON(t, l))
	}
	if len(r.Entries) != 1 || !slices.Equal(r.Entries[0].TriggerIDs, []string{"T-001", "T-002"}) {
		t.Errorf("entries: %s", mustJSON(t, r.Entries))
	}
}

func TestMergeSameExpression(t *testing.T) {
	line, other := 3, 4
	addr := "aws_instance.x"
	expr := func(s string) *string { return &s }
	cs := func(ids ...string) []Case {
		var out []Case
		for _, id := range ids {
			out = append(out, Case{ID: id, Inputs: map[string]CaseInput{"v": {Value: []byte(`"` + id + `"`)}}})
		}
		return out
	}
	tr := func(cat string, levels []string, cases []Case) Trigger {
		return Trigger{File: "main.tf", Line: &line, Category: cat, ChangeKind: KindModified,
			BaseExpr: expr("compact(var.v)"), HeadExpr: expr("var.v"), Levels: levels, Cases: cases}
	}
	many := make([]string, maxSweepCases)
	for i := range many {
		many[i] = fmt.Sprintf("c%d", i)
	}

	t.Run("merged", func(t *testing.T) {
		a := tr(CatLiteral, []string{LevelL4}, nil)
		a.Address = &addr
		b := tr(CatUnknownPropagation, []string{LevelL1, LevelL3}, cs("x", "y"))
		b.Attribute = new("tags")
		c := tr(CatWrapper, []string{LevelL1}, cs("y", "z"))
		c.Reason = new("why")
		got := mergeSameExpr([]Trigger{a, b, c})
		if len(got) != 1 {
			t.Fatalf("want one trigger: %s", mustJSON(t, got))
		}
		m := got[0]
		var ids []string
		for _, k := range m.Cases {
			ids = append(ids, k.ID)
		}
		if m.Category != CatLiteral || !slices.Equal(m.AlsoCategories, []string{CatUnknownPropagation, CatWrapper}) ||
			!slices.Equal(m.Levels, []string{LevelL1, LevelL3, LevelL4}) || !slices.Equal(ids, []string{"x", "y", "z"}) ||
			str(m.Address) != addr || str(m.Attribute) != "tags" || str(m.Reason) != "why" {
			t.Errorf("merged: %s", mustJSON(t, m))
		}
	})
	t.Run("not merged", func(t *testing.T) {
		otherLine := tr(CatUnknownPropagation, []string{LevelL1}, cs("x"))
		otherLine.Line = &other
		otherExpr := tr(CatUnknownPropagation, []string{LevelL1}, cs("x"))
		otherExpr.HeadExpr = expr("var.w")
		nullBase := tr(CatUnknownPropagation, []string{LevelL1}, cs("x"))
		nullBase.BaseExpr = nil
		clash := tr(CatUnknownPropagation, []string{LevelL1}, []Case{{ID: "x", Inputs: map[string]CaseInput{"v": {Null: true}}}})
		lacks := tr(CatDynamic, []string{LevelL4}, nil)
		lacks.Levels = []string{LevelL1}
		lacks.Reason = new("the combined input sweep needs more than 32 cases")
		addrA, addrB := tr(CatWrapper, []string{LevelL1}, cs("x")), tr(CatUnknownPropagation, []string{LevelL1}, cs("x"))
		addrA.Address, addrB.Address = new("aws_instance.x"), new("aws_instance.y")
		attrA, attrB := tr(CatWrapper, []string{LevelL1}, cs("x")), tr(CatUnknownPropagation, []string{LevelL1}, cs("x"))
		attrA.Attribute, attrB.Attribute = new("a"), new("b")
		noLevels := tr(CatMoved, []string{}, nil)
		for name, pair := range map[string][2]Trigger{
			"other addresses":             {addrA, addrB},
			"other attributes":            {attrA, attrB},
			"no levels first":             {noLevels, tr(CatUnknownPropagation, []string{LevelL1}, cs("x"))},
			"no levels second":            {tr(CatWrapper, []string{LevelL4}, nil), noLevels},
			"another line":                {tr(CatWrapper, []string{LevelL1}, cs("x")), otherLine},
			"another expression":          {tr(CatWrapper, []string{LevelL1}, cs("x")), otherExpr},
			"a null expression":           {tr(CatWrapper, []string{LevelL1}, cs("x")), nullBase},
			"a case id with other inputs": {tr(CatWrapper, []string{LevelL1}, cs("x")), clash},
			"over the case cap":           {tr(CatWrapper, []string{LevelL1}, cs(many...)), tr(CatUnknownPropagation, []string{LevelL1}, cs("extra"))},
			"one lacks its cases":         {lacks, tr(CatUnknownPropagation, []string{LevelL1}, cs("x"))},
		} {
			if got := mergeSameExpr(pair[:]); len(got) != 2 || len(got[0].AlsoCategories) != 0 || len(got[1].AlsoCategories) != 0 {
				t.Errorf("%s: %s", name, mustJSON(t, got))
			}
		}
	})
	t.Run("at the cap", func(t *testing.T) {
		got := mergeSameExpr([]Trigger{tr(CatWrapper, []string{LevelL1}, cs(many[:maxSweepCases-1]...)), tr(CatUnknownPropagation, []string{LevelL1}, cs(many[maxSweepCases-1]))})
		if len(got) != 1 || len(got[0].Cases) != maxSweepCases {
			t.Errorf("want one trigger with %d cases: %d triggers", maxSweepCases, len(got))
		}
	})
}

// TestMergedGolden: the real report of a merged change, with a trigger of
// three levels, is the schema example behaviour.valid.merged.json, so the
// schema checks real merged output.
func TestMergedGolden(t *testing.T) {
	r, err := Run(Options{Head: filepath.Join("testdata", "merged", "head"), Base: filepath.Join("testdata", "merged", "base"), Limits: facts.DefaultLimits})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Triggers) != 2 || len(r.Triggers[1].Levels) != 3 {
		t.Fatalf("want a merged trigger of three levels: %s", mustJSON(t, r))
	}
	want := mustJSON(t, r)
	example := "../../schema/examples/behaviour.valid.merged.json"
	if *update {
		if err := os.WriteFile(example, want, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := os.ReadFile(example); err != nil || !bytes.Equal(got, want) {
		t.Errorf("%s is not the real report (go test -update rewrites it):\n%s", example, want)
	}
}

func TestReadBehaviourDuplicateCase(t *testing.T) {
	r, err := Run(Options{Head: filepath.Join("testdata", "merged", "head"), Base: filepath.Join("testdata", "merged", "base"), Limits: facts.DefaultLimits})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ReadBehaviour(writeJSON(t, "b.json", string(mustJSON(t, r)))); err != nil {
		t.Fatalf("the real report: %v", err)
	}
	r.Triggers[0].Cases = append(r.Triggers[0].Cases, r.Triggers[0].Cases[0])
	if _, err := ReadBehaviour(writeJSON(t, "b.json", string(mustJSON(t, r)))); err == nil || !strings.Contains(err.Error(), "case") {
		t.Errorf("a repeated case id: %v", err)
	}
}
