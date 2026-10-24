package main

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/modulestf/facts/internal/facts"
	"github.com/modulestf/facts/internal/registry"
)

// registryEnv names a local copy of the reviewer skill's rule-facts.md. CI
// fetches it, without a token, from the public skills repository at the
// commit pinned in .github/workflows/ci.yml.
const registryEnv = "MTF_FACTS_REGISTRY"

// engineRule is what mtf-facts says of one implemented rule, in the
// registry's terms.
type engineRule struct {
	kinds        []string // sorted
	cross        bool
	scope, class string
	needsBase    bool
}

// engineRules maps each implemented rule to its engineRule.
func engineRules() map[string]engineRule {
	m := make(map[string]engineRule, len(facts.Rules))
	for _, r := range facts.Rules {
		m[r.ID] = engineRule{kinds: slices.Sorted(slices.Values(r.Kinds)), cross: r.CrossArea, scope: r.Scope, class: r.Class, needsBase: r.NeedsBase}
	}
	return m
}

// checkEngine compares each implemented rule's area kinds, as sets, its
// cross-area flag, its scope and its class with the registry's. It also
// requires the base where the registry text does; see needsBase.
func checkEngine(engine map[string]engineRule, reg *registry.Registry) error {
	var errs []error
	for _, id := range slices.Sorted(maps.Keys(engine)) {
		e := engine[id]
		r, ok := reg.Rule(id)
		if !ok {
			errs = append(errs, fmt.Errorf("rule %q: not in the registry", id))
			continue
		}
		if want := slices.Sorted(slices.Values(r.AreaKinds)); !slices.Equal(e.kinds, want) {
			errs = append(errs, fmt.Errorf("rule %q: mtf-facts area kinds %v, registry area_kinds %v", id, e.kinds, want))
		}
		if e.cross != r.CrossArea {
			errs = append(errs, fmt.Errorf("rule %q: mtf-facts cross-area %v, registry cross_area %v", id, e.cross, r.CrossArea))
		}
		if e.scope != r.Scope {
			errs = append(errs, fmt.Errorf("rule %q: mtf-facts scope %q, registry scope %q", id, e.scope, r.Scope))
		}
		if e.class != r.Class {
			errs = append(errs, fmt.Errorf("rule %q: mtf-facts class %q, registry class %q", id, e.class, r.Class))
		}
		if needsBase(id) && !e.needsBase {
			errs = append(errs, fmt.Errorf("rule %q: does not need the base, which its check reads", id))
		}
	}
	return errors.Join(errs...)
}

// needsBase reports whether the registry text makes rule id read the base.
// The registry has no column for it. Check F reads the base for every
// scope.* rule and leaves each one not evaluated without it, or it would see
// every path as added; Check A compares every compat.* rule with the base and
// treats a base that could not be read as a check that cannot run.
func needsBase(id string) bool {
	return strings.HasPrefix(id, "scope.") || strings.HasPrefix(id, "compat.")
}

// checkRules compares the `mtf-facts rules` listing with the registry tiers.
// It is an equality check: every listed rule must be a tool or candidate rule
// that the registry tiers the same way, and every tool or candidate rule of
// the registry must be listed.
func checkRules(listing string, reg *registry.Registry) error {
	if listing == "" {
		return errors.New("mtf-facts rules lists no rules")
	}
	var errs []error
	listed := map[string]bool{}
	for line := range strings.SplitSeq(strings.TrimSuffix(listing, "\n"), "\n") {
		id, tier, ok := strings.Cut(line, "\t")
		listed[id] = true
		r, known := reg.Rule(id)
		switch {
		case !ok:
			errs = append(errs, fmt.Errorf("listing line %q: no tab", line))
		case tier != registry.EngineTool && tier != registry.EngineCandidate:
			errs = append(errs, fmt.Errorf("rule %q: mtf-facts tier %q is not tool or candidate", id, tier))
		case !known:
			errs = append(errs, fmt.Errorf("rule %q: not in the registry", id))
		case r.Engine != tier:
			errs = append(errs, fmt.Errorf("rule %q: mtf-facts tier %q, registry engine %q", id, tier, r.Engine))
		}
	}
	for _, r := range reg.Rules {
		if (r.Engine == registry.EngineTool || r.Engine == registry.EngineCandidate) && !listed[r.ID] {
			errs = append(errs, fmt.Errorf("rule %q: registry engine %q, not in mtf-facts rules", r.ID, r.Engine))
		}
	}
	return errors.Join(errs...)
}

// checkRegistryText parses the registry text and runs checkRules and
// checkEngine against it.
func checkRegistryText(listing string, engine map[string]engineRule, text string) error {
	reg, err := registry.Parse(text)
	if err != nil {
		return err
	}
	return errors.Join(checkRules(listing, reg), checkEngine(engine, reg))
}

// mutateCell returns text with the named column of the row for id set to value.
func mutateCell(t *testing.T, text, id, col, value string) string {
	t.Helper()
	header := strings.Split(rowOf(t, text, "rule_id"), "|")
	c := slices.IndexFunc(header, func(h string) bool { return strings.Trim(strings.TrimSpace(h), "`") == col })
	if c < 0 {
		t.Fatalf("registry has no %s column", col)
	}
	row := rowOf(t, text, id)
	cells := strings.Split(row, "|")
	cells[c] = " " + value + " "
	return strings.Replace(text, row, strings.Join(cells, "|"), 1)
}

func rulesListing(t *testing.T) string {
	t.Helper()
	var out, errb bytes.Buffer
	if code := run([]string{"rules"}, &out, &errb); code != 0 {
		t.Fatalf("rules: exit %d: %s", code, errb.String())
	}
	return out.String()
}

// checkMutations is the negative self-check: each mutated copy of the
// registry must fail the comparison.
func checkMutations(t *testing.T, listing string, engine map[string]engineRule, text string) {
	t.Helper()
	scopes := []string{facts.ScopeTouchedBlock, facts.ScopeTouchedExpression, facts.ScopeNewlyIntroduced, facts.ScopeAnyFileInArea}
	for line := range strings.SplitSeq(strings.TrimSuffix(listing, "\n"), "\n") {
		id, tier, _ := strings.Cut(line, "\t")
		e := engine[id]
		other := map[string]string{"tool": "candidate", "candidate": "tool"}[tier]
		otherKinds := "root"
		if !slices.Contains(e.kinds, "example") {
			otherKinds = strings.Join(append(slices.Clone(e.kinds), "example"), ", ")
		}
		otherCross := map[bool]string{true: "no", false: "yes"}[e.cross]
		otherScope := scopes[(slices.Index(scopes, e.scope)+1)%len(scopes)]
		otherClass := map[string]string{facts.ClassPath: facts.ClassConfig, facts.ClassConfig: facts.ClassPath}[e.class]
		row := rowOf(t, text, id)
		cut := text[:strings.Index(text, row)+len(row)] // a download that stops after this row
		mutated := map[string]string{
			"id renamed":       mutateCell(t, text, id, "rule_id", "`"+id+"-renamed`"),
			"llm tier":         mutateCell(t, text, id, "engine", "llm"),
			"row duplicated":   strings.Replace(text, "\n"+row, "\n"+row+"\n"+row, 1),
			"engine col gone":  strings.Replace(text, "| `engine` |", "| `engines` |", 1),
			"other kinds":      mutateCell(t, text, id, "area_kinds", otherKinds),
			"kinds col gone":   strings.Replace(text, "| `area_kinds` |", "| `kinds` |", 1),
			"other cross_area": mutateCell(t, text, id, "cross_area", otherCross),
			"cross_area word":  mutateCell(t, text, id, "cross_area", "maybe"),
			"cross col gone":   strings.Replace(text, "| `cross_area` |", "| `cross` |", 1),
			"other scope":      mutateCell(t, text, id, "scope", otherScope),
			"scope word":       mutateCell(t, text, id, "scope", "touched block"),
			"scope col gone":   strings.Replace(text, "| `scope` |", "| `scopes` |", 1),
			"other class":      mutateCell(t, text, id, "class", otherClass),
			"class word":       mutateCell(t, text, id, "class", "semantic"),
			"class col gone":   strings.Replace(text, "| `class` |", "| `classes` |", 1),
			"overlap gone":     strings.Replace(text, "## Dedup Overlap\n", "## Dedup\n", 1),
			"cut, partial row": cut + "\n| `x.y` | no",
			"cut at row end":   cut + "\n",
		}
		// The other tier keeps the confirm class columns valid, so that only
		// the tier comparison can fail it.
		otherTier := mutateCell(t, text, id, "engine", other)
		if other == "candidate" {
			otherTier = mutateCell(t, mutateCell(t, otherTier, id, "confirm_class", "lead"), id, "proposed_confirm_class", "lead")
		} else {
			otherTier = mutateCell(t, mutateCell(t, otherTier, id, "confirm_class", "-"), id, "proposed_confirm_class", "-")
		}
		mutated["other tier"] = otherTier
		for name, m := range mutated {
			if err := checkRegistryText(listing, engine, m); err == nil {
				t.Errorf("%s %s: mutated registry passed", id, name)
			}
		}
		if needsBase(id) {
			noBase := maps.Clone(engine)
			e.needsBase = false
			noBase[id] = e
			if err := checkRegistryText(listing, noBase, text); err == nil {
				t.Errorf("%s: passed without needing the base", id)
			}
		}
	}
}

func rowOf(t *testing.T, text, id string) string {
	t.Helper()
	for line := range strings.SplitSeq(text, "\n") {
		if strings.HasPrefix(line, "| `"+id+"` |") {
			return line
		}
	}
	t.Fatalf("rule %q has no row", id)
	return ""
}

const fixtureRegistry = "# Rules\n\n" +
	"| `rule_id` | Carry | `engine` | `class` | `scope` | `cross_area` | `area_kinds` | `bundle_sections` | `severities` | `confirm_class` | `proposed_confirm_class` |\n" +
	"|-----------|-------|----------|---------|---------|--------------|--------------|-------------------|--------------|-----------------|--------------------------|\n" +
	"| `a.tool` | never | tool | path | any-file-in-area | no | example | tool-findings | MEDIUM | - | - |\n" +
	"| `b.candidate` | file | candidate | config-semantic | newly-introduced | yes | root, submodule | candidates | HIGH | lead | mid |\n" +
	"| `c.llm` | never | llm | path | touched-block | no | root | none | LOW | - | - |\n" +
	"| `scope.d` | never | candidate | path | touched-block | no | root | candidates | HIGH | lead | lead |\n" +
	"| `compat.e` | never | candidate | config-semantic | touched-block | no | root | candidates | HIGH | lead | lead |\n" +
	"\ntext after the table\n\n" +
	"## Dedup Overlap\n\n" +
	"| Pair | Kept | Dropped | Same defect when | From |\n" +
	"|------|------|---------|------------------|------|\n" +
	"| 1 | `b.candidate` | `c.llm` | Both on one subject. | Final Step |\n" +
	"\ntext after the overlap table\n"

// fixtureEngine is what the engine says of the fixture's implemented rules.
var fixtureEngine = map[string]engineRule{
	"a.tool":      {kinds: []string{"example"}, scope: facts.ScopeAnyFileInArea, class: facts.ClassPath},
	"b.candidate": {kinds: []string{"root", "submodule"}, cross: true, scope: facts.ScopeNewlyIntroduced, class: facts.ClassConfig},
	"scope.d":     {kinds: []string{"root"}, scope: facts.ScopeTouchedBlock, class: facts.ClassPath, needsBase: true},
	"compat.e":    {kinds: []string{"root"}, scope: facts.ScopeTouchedBlock, class: facts.ClassConfig, needsBase: true},
}

// withRule returns fixtureEngine with one rule replaced or added.
func withRule(id string, e engineRule) map[string]engineRule {
	m := maps.Clone(fixtureEngine)
	m[id] = e
	return m
}

func TestCheckRules(t *testing.T) {
	listing := "a.tool\ttool\nb.candidate\tcandidate\ncompat.e\tcandidate\nscope.d\tcandidate\n"
	if err := checkRegistryText(listing, fixtureEngine, fixtureRegistry); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	checkMutations(t, listing, fixtureEngine, fixtureRegistry)
	b := fixtureEngine["b.candidate"]
	short := strings.Replace(fixtureRegistry, "| tool | path | any-file-in-area |", "| tool | path |", 1)
	tableOnly := fixtureRegistry[:strings.Index(fixtureRegistry, "| `c.llm`")]
	for _, tt := range []struct {
		name, listing, text string
		engine              map[string]engineRule
	}{
		{name: "llm rule listed", listing: "c.llm\tllm\n", text: fixtureRegistry},
		{name: "llm rule listed as tool", listing: "c.llm\ttool\n", text: fixtureRegistry},
		{name: "unknown rule", listing: "e.unknown\ttool\n", text: fixtureRegistry},
		{name: "registry rule not listed", listing: "a.tool\ttool\n", text: fixtureRegistry},
		{name: "registry candidate not listed", listing: "a.tool\ttool\nb.candidate\tcandidate\nscope.d\tcandidate\n", text: fixtureRegistry},
		{name: "compat rule without the base", listing: listing, text: fixtureRegistry, engine: withRule("compat.e", engineRule{kinds: []string{"root"}, scope: facts.ScopeTouchedBlock, class: facts.ClassConfig})},
		{name: "scope rule without the base", listing: listing, text: fixtureRegistry, engine: withRule("scope.d", engineRule{kinds: []string{"root"}, scope: facts.ScopeTouchedBlock, class: facts.ClassPath})},
		{name: "registry rule not implemented", listing: listing, text: strings.Replace(fixtureRegistry, "| `c.llm` |", "| `d.unimplemented` | never | tool | path | touched-block | no | root | tool-findings | LOW | - | - |\n| `c.llm` |", 1)},
		{name: "empty listing", listing: "", text: fixtureRegistry},
		{name: "no tab", listing: "a.tool tool\n", text: fixtureRegistry},
		{name: "no table", listing: listing, text: "# Rules\n"},
		{name: "no rows", listing: listing, text: "| `rule_id` | `engine` |\n|---|---|\n\n"},
		{name: "short row", listing: listing, text: short},
		{name: "row cut off", listing: listing, text: strings.Replace(fixtureRegistry, "| `c.llm` | never | llm | path | touched-block | no | root | none | LOW | - | - |", "| `c.llm` | never", 1)},
		{name: "no overlap table", listing: listing, text: fixtureRegistry[:strings.Index(fixtureRegistry, "## Dedup")]},
		{name: "overlap of an unknown rule", listing: listing, text: strings.Replace(fixtureRegistry, "| `c.llm` | Both", "| `e.unknown` | Both", 1)},
		{name: "example kind added", listing: listing, text: fixtureRegistry, engine: withRule("b.candidate", engineRule{kinds: []string{"example", "root", "submodule"}, cross: true, scope: b.scope, class: b.class})},
		{name: "kind missing", listing: listing, text: fixtureRegistry, engine: withRule("b.candidate", engineRule{kinds: []string{"root"}, cross: true, scope: b.scope, class: b.class})},
		{name: "rule unknown to the registry", listing: listing, text: fixtureRegistry, engine: withRule("e.unknown", b)},
		{name: "cross-area in the engine only", listing: listing, text: fixtureRegistry, engine: withRule("a.tool", engineRule{kinds: []string{"example"}, cross: true, scope: facts.ScopeAnyFileInArea, class: facts.ClassPath})},
		{name: "cross-area in the registry only", listing: listing, text: fixtureRegistry, engine: withRule("b.candidate", engineRule{kinds: b.kinds, scope: b.scope, class: b.class})},
		{name: "other scope", listing: listing, text: fixtureRegistry, engine: withRule("b.candidate", engineRule{kinds: b.kinds, cross: true, scope: facts.ScopeTouchedBlock, class: b.class})},
		{name: "other class", listing: listing, text: fixtureRegistry, engine: withRule("b.candidate", engineRule{kinds: b.kinds, cross: true, scope: b.scope, class: facts.ClassPath})},
		{name: "table runs to end of file", listing: listing, text: tableOnly},
		{name: "table runs to end without newline", listing: listing, text: strings.TrimSuffix(tableOnly, "\n")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			engine := tt.engine
			if engine == nil {
				engine = fixtureEngine
			}
			if err := checkRegistryText(tt.listing, engine, tt.text); err == nil {
				t.Error("passed")
			}
		})
	}
}

// TestRulesMatchPinnedRegistry checks `mtf-facts rules` and each implemented
// rule's area kinds, cross-area flag, scope and class against the pinned
// registry. It skips without one, except in CI, where a skip would hide the
// check.
func TestRulesMatchPinnedRegistry(t *testing.T) {
	path := os.Getenv(registryEnv)
	if path == "" {
		if os.Getenv("CI") != "" {
			t.Fatalf("%s is not set", registryEnv)
		}
		t.Skipf("%s is not set", registryEnv)
	}
	data, err := os.ReadFile(path) //nolint:gosec // G703: the path is the CI-set registry copy
	if err != nil {
		t.Fatal(err)
	}
	listing, engine, text := rulesListing(t), engineRules(), string(data)
	if err := checkRegistryText(listing, engine, text); err != nil {
		t.Fatalf("mtf-facts does not match the registry:\n%v", err)
	}
	checkMutations(t, listing, engine, text)
}
