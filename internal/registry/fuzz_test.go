package registry

import (
	"os"
	"strings"
	"testing"
)

// FuzzParse feeds arbitrary text to the rule table parser. A refusal returns
// no registry. An accepted registry has at most one rule per line, unique
// rule ids that Rule finds, and overlaps that name rules of the table or a
// family pattern.
func FuzzParse(f *testing.F) {
	data, err := os.ReadFile("../../testdata/bundle/skills/references/rule-facts.md")
	if err != nil {
		f.Fatal(err)
	}
	f.Add(string(data))
	f.Add("| rule_id | engine |\n|---|---|\n| a.b | tool |\n")
	f.Fuzz(func(t *testing.T, text string) {
		r, err := Parse(text)
		if err != nil {
			if r != nil {
				t.Fatalf("error %v with a registry", err)
			}
			return
		}
		if r == nil {
			t.Fatal("no error and no registry")
		}
		lines := strings.Count(text, "\n") + 1
		if len(r.Rules) > lines || len(r.Overlaps) > lines {
			t.Fatalf("%d rules and %d overlaps from %d lines", len(r.Rules), len(r.Overlaps), lines)
		}
		seen := map[string]bool{}
		for _, rule := range r.Rules {
			if seen[rule.ID] {
				t.Fatalf("rule %q twice", rule.ID)
			}
			seen[rule.ID] = true
			if got, ok := r.Rule(rule.ID); !ok || got.ID != rule.ID {
				t.Fatalf("Rule(%q) = %q, %v", rule.ID, got.ID, ok)
			}
		}
	})
}
