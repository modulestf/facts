package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
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
// engine columns.
func parseRegistry(text string) (*registryTable, error) {
	lines := strings.Split(text, "\n")
	t := &registryTable{}
	for i, line := range lines {
		isRow := strings.HasPrefix(strings.TrimSpace(line), "|") && strings.HasSuffix(strings.TrimSpace(line), "|")
		if t.header == nil {
			if isRow {
				if cells := splitCells(line); colIndex(cells, "rule_id") >= 0 && colIndex(cells, "engine") >= 0 {
					t.header = cells
				}
			}
			continue
		}
		if !isRow {
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

// tiers maps each rule_id to its engine value.
func (t *registryTable) tiers() (map[string]string, error) {
	id, engine := colIndex(t.header, "rule_id"), colIndex(t.header, "engine")
	m := make(map[string]string, len(t.rows))
	for _, r := range t.rows {
		if _, dup := m[r[id]]; dup {
			return nil, fmt.Errorf("rule %q listed twice", r[id])
		}
		m[r[id]] = r[engine]
	}
	return m, nil
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

// checkRegistryText runs checkRules against the registry text.
func checkRegistryText(listing, text string) error {
	t, err := parseRegistry(text)
	if err != nil {
		return err
	}
	tiers, err := t.tiers()
	if err != nil {
		return err
	}
	return checkRules(listing, tiers)
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
func checkMutations(t *testing.T, listing, text string) {
	t.Helper()
	for line := range strings.SplitSeq(strings.TrimSuffix(listing, "\n"), "\n") {
		id, tier, _ := strings.Cut(line, "\t")
		other := map[string]string{"tool": "candidate", "candidate": "tool"}[tier]
		row := rowOf(t, text, id)
		for name, mutated := range map[string]string{
			"id renamed":      mutateCell(t, text, id, "rule_id", "`"+id+"-renamed`"),
			"other tier":      mutateCell(t, text, id, "engine", other),
			"llm tier":        mutateCell(t, text, id, "engine", "llm"),
			"row duplicated":  strings.Replace(text, "\n"+row, "\n"+row+"\n"+row, 1),
			"engine col gone": strings.Replace(text, "| `engine` |", "| `engines` |", 1),
		} {
			if err := checkRegistryText(listing, mutated); err == nil {
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
	"| `rule_id` | Carry | `engine` | `class` |\n" +
	"|-----------|-------|----------|---------|\n" +
	"| `a.tool` | never | tool | path |\n" +
	"| `b.candidate` | file | candidate | config-semantic |\n" +
	"| `c.llm` | never | llm | path |\n" +
	"| `d.unimplemented` | never | tool | path |\n" +
	"\ntext after the table\n"

func TestCheckRules(t *testing.T) {
	listing := "a.tool\ttool\nb.candidate\tcandidate\n"
	if err := checkRegistryText(listing, fixtureRegistry); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	checkMutations(t, listing, fixtureRegistry)
	for _, tt := range []struct {
		name, listing, text string
	}{
		{name: "llm rule listed", listing: "c.llm\tllm\n", text: fixtureRegistry},
		{name: "llm rule listed as tool", listing: "c.llm\ttool\n", text: fixtureRegistry},
		{name: "unknown rule", listing: "e.unknown\ttool\n", text: fixtureRegistry},
		{name: "empty listing", listing: "", text: fixtureRegistry},
		{name: "no tab", listing: "a.tool tool\n", text: fixtureRegistry},
		{name: "no table", listing: listing, text: "# Rules\n"},
		{name: "no rows", listing: listing, text: "| `rule_id` | `engine` |\n|---|---|\n"},
		{name: "short row", listing: listing, text: strings.Replace(fixtureRegistry, "| tool | path |", "| tool |", 1)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := checkRegistryText(tt.listing, tt.text); err == nil {
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
	listing, text := rulesListing(t), string(data)
	if err := checkRegistryText(listing, text); err != nil {
		t.Fatalf("mtf-facts rules does not match the registry:\n%v", err)
	}
	checkMutations(t, listing, text)
}
