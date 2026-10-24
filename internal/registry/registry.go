// Package registry parses the rule registry: the reviewer skill's
// rule-facts.md, its rule table and its Dedup Overlap table.
//
// The parse is strict. A cut-off download must not pass as a shorter table,
// so a line that starts with a pipe but does not end with one is refused, and
// so is a table with no line after it. Every registry column value must come
// from its column's list.
//
// The skills-side policy checks of the registry, such as lead on every
// candidate rule and no small confirm class on a rule that allows CRITICAL,
// live in the skills repository's tests, not here.
package registry

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Engine tiers, the engine column.
const (
	EngineTool      = "tool"
	EngineCandidate = "candidate"
	EngineLLM       = "llm"
)

// Severities, in rank order: CRITICAL ranks first.
const (
	SeverityCritical = "CRITICAL"
	SeverityHigh     = "HIGH"
	SeverityMedium   = "MEDIUM"
	SeverityLow      = "LOW"
)

// Bundle section names, the bundle_sections column, in the spec's order.
const (
	SectionToolFindings   = "tool-findings"
	SectionCandidates     = "candidates"
	SectionAreaRules      = "area-rules"
	SectionInterfaceDiff  = "interface-diff"
	SectionSchemaCoverage = "schema-coverage"
	SectionFileIndex      = "file-index"
)

// SeverityRank returns the rank of a severity, 0 for CRITICAL up to 3 for
// LOW, and -1 for a value that is not a severity.
func SeverityRank(severity string) int { return slices.Index(severities, severity) }

// overlapHeading is the heading of the section that holds the overlap table.
const overlapHeading = "## Dedup Overlap"

// Column value lists, in the order rule-facts.md gives them.
var (
	engines        = []string{EngineTool, EngineCandidate, EngineLLM}
	classes        = []string{"path", "config-semantic"}
	scopes         = []string{"touched-block", "touched-expression", "newly-introduced", "any-file-in-area"}
	areaKinds      = []string{"root", "submodule", "example"}
	bundleSections = []string{SectionToolFindings, SectionCandidates, SectionAreaRules, SectionInterfaceDiff, SectionSchemaCoverage, SectionFileIndex}
	severities     = []string{SeverityCritical, SeverityHigh, SeverityMedium, SeverityLow}
	confirmClasses = []string{"small", "mid", "lead"}
)

var (
	ruleIDPattern = regexp.MustCompile(`^[a-z]+\.[a-z0-9]+(-[a-z0-9]+)*$`)
	familyPattern = regexp.MustCompile(`^[a-z]+$`)
)

// Rule is one row of the rule table, its registry columns.
type Rule struct {
	ID        string
	Engine    string // EngineTool, EngineCandidate or EngineLLM
	Class     string // "path" or "config-semantic"
	Scope     string
	CrossArea bool
	AreaKinds []string
	// BundleSections and Severities are empty for the registry's none.
	BundleSections []string
	Severities     []string
	// ConfirmClass and ProposedConfirmClass are set on candidate rules only,
	// and empty for the registry's - on every other rule.
	ConfirmClass         string
	ProposedConfirmClass string
}

// Overlap is one row of the Dedup Overlap table: when both findings are the
// same defect, the Kept finding stays and the Dropped one goes. Kept and
// Dropped are each the one code span of their cell: a rule id or a family
// pattern such as "compat.*"; see Match. A family pattern can be wider than
// its row's prose, so the merge must read KeptCell, DroppedCell and When, not
// the pattern alone. Pair 2's "The `compat.*` finding for the break" matches
// compat.claim-contradicts-analysis and compat.break-unexplained too, which
// the code-over-description rule excludes.
type Overlap struct {
	Pair        int
	Kept        string
	Dropped     string
	KeptCell    string // the Kept cell as written
	DroppedCell string // the Dropped cell as written
	When        string // the Same defect when condition, as text
	From        string
}

// Registry is the parsed rule-facts.md. It is read-only: Parse builds the
// index that Rule reads, so a change to Rules after Parse is not seen there.
type Registry struct {
	Rules    []Rule // in file order; do not modify
	Overlaps []Overlap
	byID     map[string]int
}

// Rule returns the rule with the given id.
func (r *Registry) Rule(id string) (Rule, bool) {
	i, ok := r.byID[id]
	if !ok {
		return Rule{}, false
	}
	return r.Rules[i], true
}

// Match reports whether a rule id matches an overlap pattern: the id itself,
// or "<family>.*" for every rule of that family. A family pattern matches the
// whole family, which can be wider than its overlap row means; see Overlap.
func Match(pattern, id string) bool {
	if family, ok := strings.CutSuffix(pattern, ".*"); ok {
		return strings.HasPrefix(id, family+".")
	}
	return pattern == id
}

// Parse reads the rule table and the Dedup Overlap table from text.
func Parse(text string) (*Registry, error) {
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	rt, err := readTable(lines, 0, len(lines), func(h []string) bool {
		return slices.Contains(h, "rule_id") && slices.Contains(h, "engine")
	})
	if err != nil {
		return nil, fmt.Errorf("rule table: %w", err)
	}
	reg := &Registry{byID: make(map[string]int, len(rt.rows))}
	for i, row := range rt.rows {
		r, err := parseRule(rt, row)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", rt.lines[i], err)
		}
		if _, dup := reg.byID[r.ID]; dup {
			return nil, fmt.Errorf("line %d: rule %q listed twice", rt.lines[i], r.ID)
		}
		reg.byID[r.ID] = len(reg.Rules)
		reg.Rules = append(reg.Rules, r)
	}
	if reg.Overlaps, err = parseOverlaps(lines, reg); err != nil {
		return nil, fmt.Errorf("dedup overlap table: %w", err)
	}
	return reg, nil
}

func parseRule(t *table, row []string) (Rule, error) {
	var r Rule
	var errs []error
	get := func(col string) string {
		c, err := t.cell(row, col)
		if err != nil {
			errs = append(errs, err)
		}
		return strings.Trim(c, "`")
	}
	one := func(col string, allowed []string) string {
		v := get(col)
		if !slices.Contains(allowed, v) {
			errs = append(errs, fmt.Errorf("%s %q is not one of %v", col, v, allowed))
		}
		return v
	}
	r.ID = get("rule_id")
	if !ruleIDPattern.MatchString(r.ID) {
		errs = append(errs, fmt.Errorf("rule_id %q is not a rule id", r.ID))
	}
	r.Engine = one("engine", engines)
	r.Class = one("class", classes)
	r.Scope = one("scope", scopes)
	r.CrossArea = one("cross_area", []string{"yes", "no"}) == "yes"
	var err error
	if r.AreaKinds, err = list(get("area_kinds"), areaKinds, false); err != nil {
		errs = append(errs, fmt.Errorf("area_kinds: %w", err))
	}
	if r.BundleSections, err = list(get("bundle_sections"), bundleSections, true); err != nil {
		errs = append(errs, fmt.Errorf("bundle_sections: %w", err))
	}
	if r.Severities, err = list(get("severities"), severities, true); err != nil {
		errs = append(errs, fmt.Errorf("severities: %w", err))
	}
	r.ConfirmClass = confirm(get("confirm_class"))
	r.ProposedConfirmClass = confirm(get("proposed_confirm_class"))
	if len(errs) > 0 {
		return Rule{}, fmt.Errorf("rule %q: %w", r.ID, errors.Join(errs...))
	}
	for _, c := range []struct{ col, v string }{{"confirm_class", r.ConfirmClass}, {"proposed_confirm_class", r.ProposedConfirmClass}} {
		col, v := c.col, c.v
		switch {
		case r.Engine == EngineCandidate && !slices.Contains(confirmClasses, v):
			errs = append(errs, fmt.Errorf("%s %q on a candidate rule is not one of %v", col, v, confirmClasses))
		case r.Engine != EngineCandidate && v != "":
			errs = append(errs, fmt.Errorf("%s %q on a %s rule is not -", col, v, r.Engine))
		}
	}
	if len(errs) > 0 {
		return Rule{}, fmt.Errorf("rule %q: %w", r.ID, errors.Join(errs...))
	}
	return r, nil
}

// confirm maps the registry's - to empty.
func confirm(v string) string {
	if v == "-" {
		return ""
	}
	return v
}

// list splits a comma-separated cell. Every item must be allowed and appear
// once. With noneOK, the cell may be "none" alone, which gives an empty list.
func list(cell string, allowed []string, noneOK bool) ([]string, error) {
	if noneOK && cell == "none" {
		return []string{}, nil
	}
	items := strings.Split(cell, ",")
	for i, it := range items {
		it = strings.TrimSpace(it)
		if !slices.Contains(allowed, it) {
			return nil, fmt.Errorf("%q is not one of %v", it, allowed)
		}
		if slices.Contains(items[:i], it) {
			return nil, fmt.Errorf("%q listed twice", it)
		}
		items[i] = it
	}
	return items, nil
}

// parseOverlaps reads the table under the Dedup Overlap heading, before the
// next level-2 heading. Each Kept and Dropped cell holds one code span, a rule
// id of the registry or a family pattern that matches one.
func parseOverlaps(lines []string, reg *Registry) ([]Overlap, error) {
	start, end := -1, len(lines)
	for i, l := range lines {
		switch {
		case strings.TrimSpace(l) == overlapHeading:
			if start >= 0 {
				return nil, fmt.Errorf("line %d: second %q heading", i+1, overlapHeading)
			}
			start = i + 1
		case start >= 0 && end == len(lines) && strings.HasPrefix(l, "## "):
			end = i
		}
	}
	if start < 0 {
		return nil, fmt.Errorf("no %q heading", overlapHeading)
	}
	t, err := readTable(lines, start, end, func(h []string) bool {
		return len(h) > 0 && h[0] == "Pair"
	})
	if err != nil {
		return nil, err
	}
	out := make([]Overlap, 0, len(t.rows))
	for i, row := range t.rows {
		o, err := parseOverlap(t, row, reg)
		switch {
		case err != nil:
		case o.Pair != i+1:
			err = fmt.Errorf("pair %d, want %d", o.Pair, i+1)
		case slices.ContainsFunc(out, func(p Overlap) bool { return p.Kept == o.Kept && p.Dropped == o.Dropped }):
			err = fmt.Errorf("kept %q and dropped %q repeat an earlier pair", o.Kept, o.Dropped)
		}
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", t.lines[i], err)
		}
		out = append(out, o)
	}
	return out, nil
}

func parseOverlap(t *table, row []string, reg *Registry) (Overlap, error) {
	var o Overlap
	cells := map[string]string{}
	for _, col := range []string{"Pair", "Kept", "Dropped", "Same defect when", "From"} {
		c, err := t.cell(row, col)
		if err != nil {
			return o, err
		}
		if c == "" {
			return o, fmt.Errorf("%s is empty", col)
		}
		cells[col] = c
	}
	pair, err := strconv.Atoi(cells["Pair"])
	if err != nil || pair < 1 {
		return o, fmt.Errorf("pair %q is not a positive number", cells["Pair"])
	}
	o.Pair, o.When, o.From = pair, cells["Same defect when"], cells["From"]
	o.KeptCell, o.DroppedCell = cells["Kept"], cells["Dropped"]
	if o.Kept, err = pattern(cells["Kept"], reg); err != nil {
		return o, fmt.Errorf("kept: %w", err)
	}
	if o.Dropped, err = pattern(cells["Dropped"], reg); err != nil {
		return o, fmt.Errorf("dropped: %w", err)
	}
	if o.Kept == o.Dropped {
		return o, fmt.Errorf("kept and dropped are both %q", o.Kept)
	}
	return o, nil
}

// pattern returns the one code span of a cell, which must be a registry rule
// id or "<family>.*" for a family with a registry rule.
func pattern(cell string, reg *Registry) (string, error) {
	parts := strings.Split(cell, "`")
	if len(parts) != 3 {
		return "", fmt.Errorf("%q does not hold exactly one code span", cell)
	}
	p := parts[1]
	if family, ok := strings.CutSuffix(p, ".*"); ok {
		if !familyPattern.MatchString(family) {
			return "", fmt.Errorf("%q is not a rule family", p)
		}
		for _, r := range reg.Rules {
			if Match(p, r.ID) {
				return p, nil
			}
		}
		return "", fmt.Errorf("no registry rule matches %q", p)
	}
	if _, ok := reg.Rule(p); !ok {
		return "", fmt.Errorf("%q is not a registry rule", p)
	}
	return p, nil
}

// table is one markdown table: its header cells, with spaces and backticks
// trimmed, and its rows, with spaces trimmed.
type table struct {
	header []string
	rows   [][]string
	lines  []int // the 1-based line of each row
}

func (t *table) cell(row []string, col string) (string, error) {
	i := slices.Index(t.header, col)
	if i < 0 {
		return "", fmt.Errorf("no %s column", col)
	}
	return row[i], nil
}

func splitCells(line string) []string {
	cells := strings.Split(strings.TrimSpace(line), "|")
	cells = cells[1 : len(cells)-1] // outside the leading and trailing pipes
	for i, c := range cells {
		cells[i] = strings.TrimSpace(c)
	}
	return cells
}

// readTable reads the first table in lines[from:to] whose header satisfies
// match. The separator row must follow the header with as many cells, and no
// other row may be empty or dashes only. The table must end with a line that is not a row
// before to.
func readTable(lines []string, from, to int, match func([]string) bool) (*table, error) {
	t := &table{}
	closed, sepSeen := false, false
	for i := from; i < to; i++ {
		trimmed := strings.TrimSpace(lines[i])
		startsRow := strings.HasPrefix(trimmed, "|")
		if startsRow && (len(trimmed) < 2 || !strings.HasSuffix(trimmed, "|")) {
			return nil, fmt.Errorf("line %d: table row does not end with a pipe", i+1)
		}
		if t.header == nil {
			if startsRow {
				cells := splitCells(trimmed)
				for j, c := range cells {
					cells[j] = strings.Trim(c, "`")
				}
				if match(cells) {
					t.header = cells
				}
			}
			continue
		}
		if !startsRow {
			closed = true
			break
		}
		cells := splitCells(trimmed)
		isSep := strings.Trim(strings.Join(cells, ""), "-:") == ""
		switch {
		case !sepSeen && !isSep:
			return nil, fmt.Errorf("line %d: no separator row after the header", i+1)
		case !sepSeen && len(cells) != len(t.header):
			return nil, fmt.Errorf("line %d: separator row has %d cells, header has %d", i+1, len(cells), len(t.header))
		case !sepSeen:
			sepSeen = true
			continue
		case isSep:
			return nil, fmt.Errorf("line %d: empty row", i+1)
		}
		if len(cells) != len(t.header) {
			return nil, fmt.Errorf("line %d: %d cells, header has %d", i+1, len(cells), len(t.header))
		}
		t.rows = append(t.rows, cells)
		t.lines = append(t.lines, i+1)
	}
	switch {
	case t.header == nil:
		return nil, errors.New("no table found")
	case !closed:
		return nil, errors.New("table runs to the end of its section")
	case len(t.rows) == 0:
		return nil, errors.New("table has no rows")
	}
	return t, nil
}
