package registry

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// registryEnv names a local copy of the reviewer skill's rule-facts.md. CI
// fetches it from the public skills repository at a pinned commit.
const registryEnv = "MTF_FACTS_REGISTRY"

const ruleHeader = "| `rule_id` | Carry | `engine` | `class` | `scope` | `cross_area` | `area_kinds` | `bundle_sections` | `severities` | `confirm_class` | `proposed_confirm_class` |\n" +
	"|---|---|---|---|---|---|---|---|---|---|---|\n"

const fixture = "# Rules\n\n" + ruleHeader +
	"| `a.tool` | never | tool | path | any-file-in-area | no | example | tool-findings, file-index | MEDIUM | - | - |\n" +
	"| `b.candidate` | file | candidate | config-semantic | touched-block | yes | root, submodule | candidates | CRITICAL, HIGH | lead | mid |\n" +
	"| `b.llm` | never | llm | config-semantic | newly-introduced | no | root | none | none | - | - |\n" +
	"| `c.llm-two` | never | llm | path | touched-expression | no | root, submodule, example | interface-diff | LOW | - | - |\n" +
	"\ntext between\n\n" +
	"## Dedup Overlap\n\nIntro.\n\n" +
	"| Pair | Kept | Dropped | Same defect when | From |\n" +
	"|------|------|---------|------------------|------|\n" +
	"| 1 | `b.candidate` | `c.llm-two` | Both on one subject. | Final Step |\n" +
	"| 2 | The `b.*` finding for the break | `a.tool` | The title misreports it. | owner decision |\n" +
	"\nNot deduplicated:\n\n## Carry Audit\n\n- text\n"

func TestParseFixture(t *testing.T) {
	reg, err := Parse(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Rules) != 4 {
		t.Fatalf("%d rules", len(reg.Rules))
	}
	b, ok := reg.Rule("b.candidate")
	if !ok {
		t.Fatal("b.candidate missing")
	}
	want := Rule{
		ID: "b.candidate", Engine: EngineCandidate, Class: "config-semantic", Scope: "touched-block", CrossArea: true,
		AreaKinds: []string{"root", "submodule"}, BundleSections: []string{"candidates"}, Severities: []string{"CRITICAL", "HIGH"},
		ConfirmClass: "lead", ProposedConfirmClass: "mid",
	}
	if !equalRule(b, want) {
		t.Errorf("b.candidate = %+v, want %+v", b, want)
	}
	l, _ := reg.Rule("b.llm")
	if len(l.BundleSections) != 0 || len(l.Severities) != 0 || l.ConfirmClass != "" || l.CrossArea {
		t.Errorf("b.llm = %+v", l)
	}
	if _, ok := reg.Rule("z.none"); ok {
		t.Error("unknown rule found")
	}
	wantOv := []Overlap{
		{Pair: 1, Kept: "b.candidate", Dropped: "c.llm-two", When: "Both on one subject.", From: "Final Step"},
		{Pair: 2, Kept: "b.*", Dropped: "a.tool", When: "The title misreports it.", From: "owner decision"},
	}
	if !slices.Equal(reg.Overlaps, wantOv) {
		t.Errorf("overlaps = %+v, want %+v", reg.Overlaps, wantOv)
	}
}

func equalRule(a, b Rule) bool {
	return a.ID == b.ID && a.Engine == b.Engine && a.Class == b.Class && a.Scope == b.Scope && a.CrossArea == b.CrossArea &&
		slices.Equal(a.AreaKinds, b.AreaKinds) && slices.Equal(a.BundleSections, b.BundleSections) &&
		slices.Equal(a.Severities, b.Severities) && a.ConfirmClass == b.ConfirmClass && a.ProposedConfirmClass == b.ProposedConfirmClass
}

func TestMatch(t *testing.T) {
	for _, tt := range []struct {
		pattern, id string
		want        bool
	}{
		{"compat.*", "compat.default-changed", true},
		{"compat.*", "compatx.a", false},
		{"compat.*", "structure.flag-polarity", false},
		{"a.b", "a.b", true},
		{"a.b", "a.bc", false},
	} {
		if got := Match(tt.pattern, tt.id); got != tt.want {
			t.Errorf("Match(%q, %q) = %v", tt.pattern, tt.id, got)
		}
	}
}

// setCell returns text with the cell under col of the first row that starts
// with prefix set to value. The header is the last line before that row that
// holds col as a cell.
func setCell(t *testing.T, text, prefix, col, value string) string {
	t.Helper()
	lines := strings.Split(text, "\n")
	c := -1
	for i, l := range lines {
		if strings.HasPrefix(l, "|") {
			for j, h := range strings.Split(l, "|") {
				if strings.Trim(strings.TrimSpace(h), "`") == col {
					c = j
				}
			}
		}
		if strings.HasPrefix(l, prefix) {
			if c < 0 {
				t.Fatalf("no %s column above %q", col, prefix)
			}
			cells := strings.Split(l, "|")
			cells[c] = " " + value + " "
			lines[i] = strings.Join(cells, "|")
			return strings.Join(lines, "\n")
		}
	}
	t.Fatalf("no row starts with %q", prefix)
	return ""
}

func lineOf(t *testing.T, text, prefix string) string {
	t.Helper()
	for l := range strings.SplitSeq(text, "\n") {
		if strings.HasPrefix(l, prefix) {
			return l
		}
	}
	t.Fatalf("no line starts with %q", prefix)
	return ""
}

// overlapMutations are the negative self-checks of the overlap table, for a
// registry text that has at least one overlap row.
func overlapMutations(t *testing.T, text string) map[string]string {
	t.Helper()
	row1 := lineOf(t, text, "| 1 |")
	cut := text[:strings.Index(text, row1)+len(row1)]
	reg, err := Parse(text)
	if err != nil {
		t.Fatal(err)
	}
	id := "`" + reg.Rules[0].ID + "`"
	overlapRow := regexp.MustCompile(`^\| [0-9]+ \|`)
	var rowsGone []string
	for l := range strings.SplitSeq(text, "\n") {
		if !overlapRow.MatchString(l) {
			rowsGone = append(rowsGone, l)
		}
	}
	return map[string]string{
		"heading gone":         strings.Replace(text, "## Dedup Overlap\n", "## Dedup\n", 1),
		"second heading":       strings.Replace(text, "## Dedup Overlap\n", "## Dedup Overlap\n\n## Dedup Overlap\n", 1),
		"table after section":  strings.Replace(text, "## Dedup Overlap\n", "## Dedup Overlap\n\n## Other\n", 1),
		"pair col renamed":     strings.Replace(text, "| Pair |", "| Pairs |", 1),
		"kept col renamed":     strings.Replace(text, "| Kept |", "| Keep |", 1),
		"dropped col renamed":  strings.Replace(text, "| Dropped |", "| Drop |", 1),
		"when col renamed":     strings.Replace(text, "| Same defect when |", "| When |", 1),
		"pair renumbered":      setCell(t, text, "| 1 |", "Pair", "3"),
		"pair not a number":    setCell(t, text, "| 1 |", "Pair", "one"),
		"kept unknown":         setCell(t, text, "| 1 |", "Kept", "`zz.unknown`"),
		"kept no code span":    setCell(t, text, "| 1 |", "Kept", "a compat finding"),
		"kept two code spans":  setCell(t, text, "| 1 |", "Kept", "`compat.*` or `scope.*`"),
		"kept family unknown":  setCell(t, text, "| 1 |", "Kept", "The `zz.*` finding"),
		"kept bad family":      setCell(t, text, "| 1 |", "Kept", "`Compat.*`"),
		"dropped unknown":      setCell(t, text, "| 1 |", "Dropped", "`zz.unknown`"),
		"dropped is kept":      setCell(t, setCell(t, text, "| 1 |", "Kept", id), "| 1 |", "Dropped", id),
		"when empty":           setCell(t, text, "| 1 |", "Same defect when", ""),
		"from empty":           setCell(t, text, "| 1 |", "From", ""),
		"cell missing":         strings.Replace(text, row1, row1[:strings.LastIndex(row1[:len(row1)-1], "|")+1], 1),
		"row without pipe end": strings.Replace(text, row1, strings.TrimSuffix(row1, "|"), 1),
		"rows gone":            strings.Join(rowsGone, "\n"),
		"cut after row 1":      cut,
		"cut after row 1, nl":  cut + "\n",
		"cut inside row 2":     cut + "\n| 2 | `compat",
	}
}

func checkFails(t *testing.T, name, text string) {
	t.Helper()
	if _, err := Parse(text); err == nil {
		t.Errorf("%s: mutated registry passed", name)
	}
}

func TestParseRefusesMutations(t *testing.T) {
	for name, text := range overlapMutations(t, fixture) {
		checkFails(t, "overlap "+name, text)
	}
	for _, tt := range []struct{ name, prefix, col, value string }{
		{"engine word", "| `a.tool`", "engine", "tools"},
		{"class word", "| `a.tool`", "class", "paths"},
		{"scope word", "| `a.tool`", "scope", "touched block"},
		{"scope empty", "| `a.tool`", "scope", ""},
		{"cross_area word", "| `a.tool`", "cross_area", "maybe"},
		{"area kind unknown", "| `a.tool`", "area_kinds", "root, module"},
		{"area kind twice", "| `a.tool`", "area_kinds", "root, root"},
		{"area kinds none", "| `a.tool`", "area_kinds", "none"},
		{"area kinds empty", "| `a.tool`", "area_kinds", ""},
		{"section unknown", "| `a.tool`", "bundle_sections", "tool-findings, facts"},
		{"section none and more", "| `a.tool`", "bundle_sections", "none, candidates"},
		{"section twice", "| `a.tool`", "bundle_sections", "candidates, candidates"},
		{"severity lower case", "| `a.tool`", "severities", "medium"},
		{"severity unknown", "| `a.tool`", "severities", "INFO"},
		{"severity twice", "| `a.tool`", "severities", "HIGH, HIGH"},
		{"severity none and more", "| `a.tool`", "severities", "none, HIGH"},
		{"confirm on tool", "| `a.tool`", "confirm_class", "lead"},
		{"proposed on llm", "| `b.llm`", "proposed_confirm_class", "small"},
		{"confirm missing on candidate", "| `b.candidate`", "confirm_class", "-"},
		{"confirm word on candidate", "| `b.candidate`", "confirm_class", "large"},
		{"proposed missing on candidate", "| `b.candidate`", "proposed_confirm_class", "-"},
		{"rule id bad", "| `a.tool`", "rule_id", "`A.Tool`"},
		{"rule id no family", "| `a.tool`", "rule_id", "`tool`"},
		{"rule id duplicated", "| `a.tool`", "rule_id", "`b.llm`"},
	} {
		checkFails(t, tt.name, setCell(t, fixture, tt.prefix, tt.col, tt.value))
	}
	for _, col := range []string{"rule_id", "engine", "class", "scope", "cross_area", "area_kinds", "bundle_sections", "severities", "confirm_class", "proposed_confirm_class"} {
		checkFails(t, col+" col gone", strings.Replace(fixture, "| `"+col+"` |", "| `"+col+"x` |", 1))
	}
	row := lineOf(t, fixture, "| `b.llm`")
	cut := fixture[:strings.Index(fixture, row)+len(row)]
	for name, text := range map[string]string{
		"no rule table":       "# Rules\n",
		"rule rows gone":      "# Rules\n\n" + ruleHeader + fixture[strings.Index(fixture, "\ntext between"):],
		"rule row short":      strings.Replace(fixture, row, row[:strings.LastIndex(row[:len(row)-1], "|")+1], 1),
		"rule row cut":        strings.Replace(fixture, row, row[:20], 1),
		"rule table cut":      cut,
		"rule table cut, nl":  cut + "\n",
		"empty":               "",
		"rule table only":     fixture[:strings.Index(fixture, "## Dedup")],
		"rule row duplicated": strings.Replace(fixture, row, row+"\n"+row, 1),
	} {
		checkFails(t, name, text)
	}
}

// TestPinnedRegistry parses the pinned registry and runs the overlap and
// scope mutations against it. It skips without one, except in CI, where a
// skip would hide the check.
func TestPinnedRegistry(t *testing.T) {
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
	text := string(data)
	reg, err := Parse(text)
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Overlaps) == 0 {
		t.Fatal("no overlap rows")
	}
	for name, mutated := range overlapMutations(t, text) {
		checkFails(t, "overlap "+name, mutated)
	}
	for _, r := range reg.Rules {
		checkFails(t, r.ID+" scope word", setCell(t, text, "| `"+r.ID+"` |", "scope", "touched"))
	}
}
