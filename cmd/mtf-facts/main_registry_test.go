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
)

// registryEnv names a local copy of the reviewer skill's rule-facts.md. CI
// fetches it, without a token, from the public skills repository at the
// commit pinned in .github/workflows/ci.yml.
const registryEnv = "MTF_FACTS_REGISTRY"

// registryTable is the rule table of rule-facts.md: its header cells and its
// rows, each row split into cells with spaces and backticks trimmed.
type registryTable struct {
	header []string
	rows   [][]string
	lines  []int // index into the file's lines of each row
}

func splitCells(line string) []string {
	cells := strings.Split(strings.TrimSpace(line), "|")
	cells = cells[1 : len(cells)-1] // outside the leading and trailing pipes
	for i, c := range cells {
		cells[i] = strings.Trim(strings.TrimSpace(c), "`")
	}
	return cells
}

// parseRegistry reads the first markdown table whose header has rule_id and
// engine columns. A cut-off download must not pass as a shorter table, so a
// line that starts with a pipe but does not end with one is refused, and so
// is a table with no line after it: the trailing newline does not count.
func parseRegistry(text string) (*registryTable, error) {
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	t := &registryTable{}
	closed := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		startsRow := strings.HasPrefix(trimmed, "|")
		if startsRow && (len(trimmed) < 2 || !strings.HasSuffix(trimmed, "|")) {
			return nil, fmt.Errorf("line %d: table row does not end with a pipe", i+1)
		}
		if t.header == nil {
			if startsRow {
				if cells := splitCells(line); colIndex(cells, "rule_id") >= 0 && colIndex(cells, "engine") >= 0 {
					t.header = cells
				}
			}
			continue
		}
		if !startsRow {
			closed = true
			break
		}
		cells := splitCells(line)
		if strings.Trim(strings.Join(cells, ""), "-:") == "" {
			continue // the separator row
		}
		if len(cells) != len(t.header) {
			return nil, fmt.Errorf("line %d: %d cells, header has %d", i+1, len(cells), len(t.header))
		}
		t.rows = append(t.rows, cells)
		t.lines = append(t.lines, i)
	}
	if t.header == nil {
		return nil, errors.New("no table with rule_id and engine columns")
	}
	if !closed {
		return nil, errors.New("rule table runs to the end of the file")
	}
	if len(t.rows) == 0 {
		return nil, errors.New("rule table has no rows")
	}
	return t, nil
}

func colIndex(cells []string, name string) int {
	for i, c := range cells {
		if c == name {
			return i
		}
	}
	return -1
}

// column maps each rule_id to its value in the named column.
func (t *registryTable) column(name string) (map[string]string, error) {
	id, c := colIndex(t.header, "rule_id"), colIndex(t.header, name)
	if c < 0 {
		return nil, fmt.Errorf("rule table has no %s column", name)
	}
	m := make(map[string]string, len(t.rows))
	for _, r := range t.rows {
		if _, dup := m[r[id]]; dup {
			return nil, fmt.Errorf("rule %q listed twice", r[id])
		}
		m[r[id]] = r[c]
	}
	return m, nil
}

// engineKinds maps each implemented rule to its area kinds, sorted.
func engineKinds() map[string][]string {
	m := make(map[string][]string, len(facts.Rules))
	for _, r := range facts.Rules {
		m[r.ID] = slices.Sorted(slices.Values(r.Kinds))
	}
	return m
}

// checkKinds compares each implemented rule's area kinds with the registry's
// area_kinds cell, a comma-separated list, as sets.
func checkKinds(kinds map[string][]string, registry map[string]string) error {
	var errs []error
	for _, id := range slices.Sorted(maps.Keys(kinds)) {
		cell, ok := registry[id]
		if !ok {
			errs = append(errs, fmt.Errorf("rule %q: not in the registry", id))
			continue
		}
		want := strings.Split(cell, ",")
		for i, k := range want {
			want[i] = strings.TrimSpace(k)
		}
		slices.Sort(want)
		if !slices.Equal(kinds[id], want) {
			errs = append(errs, fmt.Errorf("rule %q: mtf-facts area kinds %v, registry area_kinds %q", id, kinds[id], cell))
		}
	}
	return errors.Join(errs...)
}

// engineCross maps each implemented rule to whether it is cross-area.
func engineCross() map[string]bool {
	m := make(map[string]bool, len(facts.Rules))
	for _, r := range facts.Rules {
		m[r.ID] = r.CrossArea
	}
	return m
}

// checkCross compares each implemented rule's CrossArea with the registry's
// cross_area cell, which must be yes or no.
func checkCross(cross map[string]bool, registry map[string]string) error {
	var errs []error
	for _, id := range slices.Sorted(maps.Keys(cross)) {
		cell, ok := registry[id]
		switch {
		case !ok:
			errs = append(errs, fmt.Errorf("rule %q: not in the registry", id))
		case cell != "yes" && cell != "no":
			errs = append(errs, fmt.Errorf("rule %q: registry cross_area %q is not yes or no", id, cell))
		case cross[id] != (cell == "yes"):
			errs = append(errs, fmt.Errorf("rule %q: mtf-facts cross-area %v, registry cross_area %q", id, cross[id], cell))
		}
	}
	return errors.Join(errs...)
}

// checkRules compares the `mtf-facts rules` listing with the registry tiers.
// Every listed rule must be a tool or candidate rule that the registry tiers
// the same way. It is a subset check: the registry has tool and candidate
// rules the engine does not implement yet. Once every one is implemented,
// this becomes an equality check.
func checkRules(listing string, tiers map[string]string) error {
	if listing == "" {
		return errors.New("mtf-facts rules lists no rules")
	}
	var errs []error
	for line := range strings.SplitSeq(strings.TrimSuffix(listing, "\n"), "\n") {
		id, tier, ok := strings.Cut(line, "\t")
		switch {
		case !ok:
			errs = append(errs, fmt.Errorf("listing line %q: no tab", line))
		case tier != "tool" && tier != "candidate":
			errs = append(errs, fmt.Errorf("rule %q: mtf-facts tier %q is not tool or candidate", id, tier))
		case tiers[id] == "":
			errs = append(errs, fmt.Errorf("rule %q: not in the registry", id))
		case tiers[id] != tier:
			errs = append(errs, fmt.Errorf("rule %q: mtf-facts tier %q, registry engine %q", id, tier, tiers[id]))
		}
	}
	return errors.Join(errs...)
}

// checkRegistryText runs checkRules, checkKinds and checkCross against the
// registry text.
func checkRegistryText(listing string, kinds map[string][]string, cross map[string]bool, text string) error {
	t, err := parseRegistry(text)
	if err != nil {
		return err
	}
	tiers, err := t.column("engine")
	if err != nil {
		return err
	}
	areaKinds, err := t.column("area_kinds")
	if err != nil {
		return err
	}
	crossArea, err := t.column("cross_area")
	if err != nil {
		return err
	}
	return errors.Join(checkRules(listing, tiers), checkKinds(kinds, areaKinds), checkCross(cross, crossArea))
}

// mutateCell returns text with the named column of the row for id set to value.
func mutateCell(t *testing.T, text, id, col, value string) string {
	t.Helper()
	tab, err := parseRegistry(text)
	if err != nil {
		t.Fatal(err)
	}
	idCol, c := colIndex(tab.header, "rule_id"), colIndex(tab.header, col)
	lines := strings.Split(text, "\n")
	for i, r := range tab.rows {
		if r[idCol] != id {
			continue
		}
		cells := strings.Split(lines[tab.lines[i]], "|")
		cells[c+1] = " " + value + " " // cells[0] is before the leading pipe
		lines[tab.lines[i]] = strings.Join(cells, "|")
		return strings.Join(lines, "\n")
	}
	t.Fatalf("rule %q not in the registry", id)
	return ""
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
func checkMutations(t *testing.T, listing string, kinds map[string][]string, cross map[string]bool, text string) {
	t.Helper()
	for line := range strings.SplitSeq(strings.TrimSuffix(listing, "\n"), "\n") {
		id, tier, _ := strings.Cut(line, "\t")
		other := map[string]string{"tool": "candidate", "candidate": "tool"}[tier]
		otherKinds := "root"
		if !slices.Contains(kinds[id], "example") {
			otherKinds = strings.Join(append(slices.Clone(kinds[id]), "example"), ", ")
		}
		otherCross := map[bool]string{true: "no", false: "yes"}[cross[id]]
		row := rowOf(t, text, id)
		cut := text[:strings.Index(text, row)+len(row)] // a download that stops after this row
		for name, mutated := range map[string]string{
			"id renamed":       mutateCell(t, text, id, "rule_id", "`"+id+"-renamed`"),
			"other tier":       mutateCell(t, text, id, "engine", other),
			"llm tier":         mutateCell(t, text, id, "engine", "llm"),
			"row duplicated":   strings.Replace(text, "\n"+row, "\n"+row+"\n"+row, 1),
			"engine col gone":  strings.Replace(text, "| `engine` |", "| `engines` |", 1),
			"other kinds":      mutateCell(t, text, id, "area_kinds", otherKinds),
			"kinds col gone":   strings.Replace(text, "| `area_kinds` |", "| `kinds` |", 1),
			"other cross_area": mutateCell(t, text, id, "cross_area", otherCross),
			"cross_area word":  mutateCell(t, text, id, "cross_area", "maybe"),
			"cross col gone":   strings.Replace(text, "| `cross_area` |", "| `cross` |", 1),
			"cut, partial row": cut + "\n| `x.y` | no",
			"cut at row end":   cut + "\n",
		} {
			if err := checkRegistryText(listing, kinds, cross, mutated); err == nil {
				t.Errorf("%s %s: mutated registry passed", id, name)
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
	"| `rule_id` | Carry | `engine` | `class` | `cross_area` | `area_kinds` |\n" +
	"|-----------|-------|----------|---------|--------------|--------------|\n" +
	"| `a.tool` | never | tool | path | no | example |\n" +
	"| `b.candidate` | file | candidate | config-semantic | yes | root, submodule |\n" +
	"| `c.llm` | never | llm | path | no | root |\n" +
	"| `d.unimplemented` | never | tool | path | no | root |\n" +
	"\ntext after the table\n"

// fixtureCross is whether each of the fixture's implemented rules is cross-area.
var fixtureCross = map[string]bool{"a.tool": false, "b.candidate": true}

// fixtureKinds are the area kinds of the fixture's implemented rules.
var fixtureKinds = map[string][]string{
	"a.tool":      {"example"},
	"b.candidate": {"root", "submodule"},
}

func TestCheckRules(t *testing.T) {
	listing := "a.tool\ttool\nb.candidate\tcandidate\n"
	if err := checkRegistryText(listing, fixtureKinds, fixtureCross, fixtureRegistry); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	checkMutations(t, listing, fixtureKinds, fixtureCross, fixtureRegistry)
	for _, tt := range []struct {
		name, listing, text string
		kinds               map[string][]string
		cross               map[string]bool
	}{
		{name: "llm rule listed", listing: "c.llm\tllm\n", text: fixtureRegistry},
		{name: "llm rule listed as tool", listing: "c.llm\ttool\n", text: fixtureRegistry},
		{name: "unknown rule", listing: "e.unknown\ttool\n", text: fixtureRegistry},
		{name: "empty listing", listing: "", text: fixtureRegistry},
		{name: "no tab", listing: "a.tool tool\n", text: fixtureRegistry},
		{name: "no table", listing: listing, text: "# Rules\n"},
		{name: "no rows", listing: listing, text: "| `rule_id` | `engine` |\n|---|---|\n"},
		{name: "short row", listing: listing, text: strings.Replace(fixtureRegistry, "| tool | path |", "| tool |", 1)},
		{name: "row cut off", listing: listing, text: strings.Replace(fixtureRegistry, "| `c.llm` | never | llm | path | no | root |", "| `c.llm` | never", 1)},
		{name: "example kind added", listing: listing, text: fixtureRegistry, kinds: map[string][]string{"b.candidate": {"example", "root", "submodule"}}},
		{name: "kind missing", listing: listing, text: fixtureRegistry, kinds: map[string][]string{"b.candidate": {"root"}}},
		{name: "kinds of an unknown rule", listing: listing, text: fixtureRegistry, kinds: map[string][]string{"e.unknown": {"root"}}},
		{name: "cross-area in the engine only", listing: listing, text: fixtureRegistry, cross: map[string]bool{"a.tool": true, "b.candidate": true}},
		{name: "cross-area in the registry only", listing: listing, text: fixtureRegistry, cross: map[string]bool{"a.tool": false, "b.candidate": false}},
		{name: "cross-area of an unknown rule", listing: listing, text: fixtureRegistry, cross: map[string]bool{"e.unknown": false}},
		{name: "table runs to end of file", listing: listing, text: fixtureRegistry[:strings.Index(fixtureRegistry, "| `c.llm`")]},
		{name: "table runs to end without newline", listing: listing, text: strings.TrimSuffix(fixtureRegistry[:strings.Index(fixtureRegistry, "| `c.llm`")], "\n")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			kinds := tt.kinds
			if kinds == nil {
				kinds = fixtureKinds
			}
			cross := tt.cross
			if cross == nil {
				cross = fixtureCross
			}
			if err := checkRegistryText(tt.listing, kinds, cross, tt.text); err == nil {
				t.Error("passed")
			}
		})
	}
}

// TestRulesMatchPinnedRegistry checks `mtf-facts rules` against the pinned
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
	listing, kinds, cross, text := rulesListing(t), engineKinds(), engineCross(), string(data)
	if err := checkRegistryText(listing, kinds, cross, text); err != nil {
		t.Fatalf("mtf-facts rules does not match the registry:\n%v", err)
	}
	checkMutations(t, listing, kinds, cross, text)
}
