package bundle

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/modulestf/facts/internal/facts"
	"github.com/modulestf/facts/internal/registry"
)

// Defaults of the cap, the injection budget and the length of one string
// from the pull request. The cap and the budget are the spec's starting
// values, to be measured.
const (
	DefaultCapBytes        = 48 << 10
	DefaultInjectionBudget = 24 << 10
	maxPRString            = 1024
)

// Modes. A run uses one from start to end.
const (
	ModeJudgment = "judgment"
	ModeFull     = "full"
)

// Reason codes for a (rule, area) the bundle leaves not_evaluated because a
// section it relies on was shed to fit the cap, or because the report does
// not carry that section.
const (
	ReasonSectionShed        = "section_shed"
	ReasonSectionUnavailable = "section_unavailable"
)

// Region markers. The header line, outside the region, gives the payload's
// byte count; the payload is one line, a JSON string with every byte
// outside printable ASCII escaped, so no text in it can end the region.
const (
	headerPrefix = "FACTS-BUNDLE-BYTES "
	regionBegin  = "FACTS-BUNDLE BEGIN"
	regionEnd    = "FACTS-BUNDLE END"
)

// shedOrder lists the sections that may be shed, lowest priority first.
// tool-findings and candidates are never shed.
var shedOrder = []string{
	registry.SectionFileIndex,
	registry.SectionSchemaCoverage,
	registry.SectionInterfaceDiff,
	registry.SectionAreaRules,
}

var (
	candidateIDPattern = regexp.MustCompile(`^C-[0-9]{3,}$`)
	reasonPattern      = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
)

// Options set the cap and the budget, both in bytes.
type Options struct {
	CapBytes        int // payload bytes the header counts
	InjectionBudget int // bytes of injected rule text
}

// Result is the outcome of Build. In ModeFull, Bundle and Injection are nil
// and Reason says why.
type Result struct {
	Mode      string
	Reason    string
	Bundle    []byte // header line and region
	Injection []byte // injected rule text, empty when nothing is injected
	Shed      []string
}

// toolFinding is a finding with the fields the bundle adds.
type toolFinding struct {
	facts.Finding
	Source  string `json:"source"`
	Subject string `json:"subject"`
}

type areaRule struct {
	RuleID string `json:"rule_id"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

type areaRules struct {
	Area  string     `json:"area"`
	Rules []areaRule `json:"rules"`
}

type fileChange struct {
	Path   string `json:"path"`
	Change string `json:"change"`
}

// doc is the bundle, its keys in the schema's order. A nil section is left
// out. schema_coverage has no field: the report carries no schema coverage
// delta yet, so Build always leaves it out.
type doc struct {
	ToolFindings   []toolFinding     `json:"tool_findings"`
	Candidates     []facts.Candidate `json:"candidates"`
	AreaRules      *[]areaRules      `json:"area_rules,omitempty"`
	InterfaceDiff  json.RawMessage   `json:"interface_diff,omitempty"`
	FileIndex      *[]fileChange     `json:"file_index,omitempty"`
	SeverityNeeded []struct{}        `json:"severity_needed"`
}

// Build checks the report against the registry and builds the bundle and
// the injection. A report that does not match the registry is an error. A
// bundle whose first two sections alone are over the cap, or an injection
// over the budget, gives ModeFull.
func Build(r *Report, reg *registry.Registry, text EngineText, o Options) (*Result, error) {
	if err := checkReport(r, reg); err != nil {
		return nil, err
	}
	base, err := baseDoc(r, reg)
	if err != nil {
		return nil, err
	}
	// The report carries no schema coverage delta yet, so that section is
	// always left out; an absent interface diff is left out too.
	missing := []string{registry.SectionSchemaCoverage}
	if base.InterfaceDiff == nil {
		missing = append(missing, registry.SectionInterfaceDiff)
	}
	var shed []string
	for {
		out := map[string]string{}
		for _, s := range missing {
			out[s] = ReasonSectionUnavailable
		}
		for _, s := range shed {
			out[s] = ReasonSectionShed
		}
		manifest := applyOut(r.Manifest, reg, out)
		payload, err := encode(withSections(base, manifest, out))
		if err != nil {
			return nil, err
		}
		if len(payload) <= o.CapBytes {
			return finish(payload, manifest, text, o, shed)
		}
		next := slices.IndexFunc(shedOrder, func(s string) bool { _, gone := out[s]; return !gone })
		if next < 0 {
			return &Result{Mode: ModeFull, Reason: fmt.Sprintf("tool findings and candidates alone are %d bytes, over the %d byte cap", len(payload), o.CapBytes), Shed: shed}, nil
		}
		shed = append(shed, shedOrder[next])
	}
}

// finish renders the region and the injection, or gives ModeFull when the
// injection is over the budget.
func finish(payload []byte, manifest []facts.ManifestEntry, text EngineText, o Options, shed []string) (*Result, error) {
	var ne []notEvaluated
	for _, e := range manifest {
		if e.Status == facts.StatusNotEvaluated {
			ne = append(ne, notEvaluated{rule: e.RuleID, area: e.Area, reason: e.ReasonCode})
		}
	}
	slices.SortFunc(ne, func(a, b notEvaluated) int {
		return cmp.Or(strings.Compare(a.rule, b.rule), strings.Compare(a.area, b.area))
	})
	inj, err := injection(ne, text)
	if err != nil {
		return nil, err
	}
	if len(inj) > o.InjectionBudget {
		return &Result{Mode: ModeFull, Reason: fmt.Sprintf("injected rule text is %d bytes, over the %d byte budget", len(inj), o.InjectionBudget), Shed: shed}, nil
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, "%s%d\n%s\n%s\n%s\n", headerPrefix, len(payload), regionBegin, payload, regionEnd)
	return &Result{Mode: ModeJudgment, Bundle: b.Bytes(), Injection: inj, Shed: shed}, nil
}

// checkReport refuses a report the registry does not describe: a manifest,
// finding or candidate rule that is not a registry tool or candidate rule, a
// not_evaluated entry without a reason code, or a malformed candidate id.
func checkReport(r *Report, reg *registry.Registry) error {
	var errs []error
	known := func(id, what string) {
		if rule, ok := reg.Rule(id); !ok || rule.Engine == registry.EngineLLM {
			errs = append(errs, fmt.Errorf("%s rule %q is not a tool or candidate rule of the registry", what, id))
		}
	}
	for _, e := range r.Manifest {
		known(e.RuleID, "manifest")
		if e.Status == facts.StatusNotEvaluated && !reasonPattern.MatchString(e.ReasonCode) {
			errs = append(errs, fmt.Errorf("manifest: %s in %q is not_evaluated with no reason code", e.RuleID, e.Area))
		}
	}
	for _, f := range r.Findings {
		known(f.RuleID, "finding")
	}
	seen := map[string]bool{}
	for _, c := range r.Candidates {
		known(c.RuleID, "candidate")
		if !candidateIDPattern.MatchString(c.CandidateID) || seen[c.CandidateID] {
			errs = append(errs, fmt.Errorf("candidate id %q is malformed or repeated", c.CandidateID))
		}
		seen[c.CandidateID] = true
	}
	if r.Envelope.Inputs.Base == nil {
		errs = append(errs, errors.New("report has no base tree"))
	}
	if len(errs) > 0 {
		return fmt.Errorf("report does not match the registry: %w", errors.Join(errs...))
	}
	return nil
}

// baseDoc builds every section from the report, with the strings from the
// pull request capped. Area rules are filled in per attempt by withSections.
func baseDoc(r *Report, reg *registry.Registry) (doc, error) {
	d := doc{ToolFindings: []toolFinding{}, Candidates: []facts.Candidate{}, SeverityNeeded: []struct{}{}}
	for _, f := range r.Findings {
		f.Summary, f.SuggestedFix = capMarked(f.Summary), capMarked(f.SuggestedFix)
		d.ToolFindings = append(d.ToolFindings, toolFinding{Finding: f, Source: "tool", Subject: findingSubject(f, r.Index, reg)})
	}
	for _, c := range r.Candidates {
		d.Candidates = append(d.Candidates, capCandidate(c))
	}
	if r.InterfaceDiff != nil {
		raw, err := capStrings(r.InterfaceDiff)
		if err != nil {
			return doc{}, fmt.Errorf("interface diff: %w", err)
		}
		d.InterfaceDiff = raw
	}
	idx := fileIndex(r.Envelope.Inputs)
	d.FileIndex = &idx
	return d, nil
}

// findingSubject derives a tool finding's subject: a path rule's is the file
// itself, any other's the innermost index entry at its line, or the file when
// none holds it.
func findingSubject(f facts.Finding, index []facts.IndexEntry, reg *registry.Registry) string {
	if rule, _ := reg.Rule(f.RuleID); rule.Class == facts.ClassPath || f.Line == nil {
		return f.File
	}
	if s := facts.SubjectAt(index, f.File, *f.Line); s != "" {
		return s
	}
	return f.File
}

// withSections returns d with the area rules of manifest and without the
// sections in out.
func withSections(d doc, manifest []facts.ManifestEntry, out map[string]string) doc {
	if _, gone := out[registry.SectionAreaRules]; !gone {
		ar := areaRulesOf(manifest)
		d.AreaRules = &ar
	}
	if _, gone := out[registry.SectionInterfaceDiff]; gone {
		d.InterfaceDiff = nil
	}
	if _, gone := out[registry.SectionFileIndex]; gone {
		d.FileIndex = nil
	}
	return d
}

// applyOut returns the manifest with every entry of a rule that relies on a
// section in out made not_evaluated with that section's reason, unless it is
// not_applicable or already not_evaluated.
func applyOut(manifest []facts.ManifestEntry, reg *registry.Registry, out map[string]string) []facts.ManifestEntry {
	res := slices.Clone(manifest)
	for i, e := range res {
		if e.Status == facts.StatusNotApplicable || e.Status == facts.StatusNotEvaluated {
			continue
		}
		rule, _ := reg.Rule(e.RuleID)
		for _, s := range rule.BundleSections {
			if reason, gone := out[s]; gone {
				res[i] = facts.ManifestEntry{RuleID: e.RuleID, Area: e.Area, Status: facts.StatusNotEvaluated,
					Reason: "bundle section " + s + " left out", ReasonCode: reason}
				break
			}
		}
	}
	return res
}

// areaRulesOf groups the entries that are not clean or not_applicable by
// area, areas and rules sorted.
func areaRulesOf(manifest []facts.ManifestEntry) []areaRules {
	by := map[string][]areaRule{}
	for _, e := range manifest {
		switch e.Status {
		case facts.StatusFinding, facts.StatusCandidate:
			by[e.Area] = append(by[e.Area], areaRule{RuleID: e.RuleID, Status: e.Status})
		case facts.StatusNotEvaluated:
			by[e.Area] = append(by[e.Area], areaRule{RuleID: e.RuleID, Status: e.Status, Reason: e.ReasonCode})
		}
	}
	out := []areaRules{}
	for _, a := range sortedKeys(by) {
		rules := by[a]
		slices.SortStableFunc(rules, func(x, y areaRule) int { return strings.Compare(x.RuleID, y.RuleID) })
		out = append(out, areaRules{Area: a, Rules: rules})
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// fileIndex lists the files the change adds, modifies or removes.
func fileIndex(in facts.Inputs) []fileChange {
	base := map[string]facts.FileInput{}
	if in.Base != nil {
		for _, f := range in.Base.Files {
			base[f.Path] = f
		}
	}
	out := []fileChange{}
	for _, f := range in.Head.Files {
		b, ok := base[f.Path]
		switch {
		case !ok:
			out = append(out, fileChange{Path: f.Path, Change: "added"})
		case b != f:
			out = append(out, fileChange{Path: f.Path, Change: "modified"})
		}
		delete(base, f.Path)
	}
	for _, p := range sortedKeys(base) {
		out = append(out, fileChange{Path: p, Change: "removed"})
	}
	slices.SortStableFunc(out, func(x, y fileChange) int { return strings.Compare(x.Path, y.Path) })
	return out
}

// capCandidate caps the evidence text of one candidate. A cut sets
// evidence_complete to false.
func capCandidate(c facts.Candidate) facts.Candidate {
	ev := c.Evidence
	lines := make([]facts.SourceLine, len(ev.SourceLines))
	for i, l := range ev.SourceLines {
		var cut bool
		l.Text, cut = capString(l.Text)
		c.EvidenceComplete = c.EvidenceComplete && !cut
		lines[i] = l
	}
	ev.SourceLines = lines
	for _, v := range []**string{&ev.Base, &ev.Head} {
		if *v != nil {
			s, cut := capString(**v)
			c.EvidenceComplete = c.EvidenceComplete && !cut
			*v = &s
		}
	}
	if ev.SchemaFacts == nil {
		ev.SchemaFacts = []facts.SchemaFact{}
	}
	c.Evidence = ev
	return c
}

// capString cuts s to maxPRString bytes on a character boundary.
func capString(s string) (string, bool) {
	if len(s) <= maxPRString {
		return s, false
	}
	n := maxPRString
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n], true
}

// capMarked caps s and marks a cut with the number of bytes omitted.
func capMarked(s string) string {
	t, cut := capString(s)
	if cut {
		t += fmt.Sprintf(" [truncated: %d bytes omitted]", len(s)-len(t))
	}
	return t
}

// capStrings caps every string value, not object keys, in a JSON document,
// keeping its structure and key order.
func capStrings(raw json.RawMessage) (json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var b bytes.Buffer
	type frame struct {
		object bool
		n      int
	}
	var stack []frame
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) && len(stack) == 0 && b.Len() > 0 {
			break
		}
		if err != nil {
			return nil, err
		}
		if len(stack) == 0 && b.Len() > 0 {
			return nil, errors.New("more than one JSON value")
		}
		if d, ok := tok.(json.Delim); ok && (d == '}' || d == ']') {
			stack = stack[:len(stack)-1]
			b.WriteString(d.String())
			continue
		}
		key := false
		if len(stack) > 0 {
			top := &stack[len(stack)-1]
			key = top.object && top.n%2 == 0
			switch {
			case top.n > 0 && (!top.object || key):
				b.WriteByte(',')
			case top.object && !key:
				b.WriteByte(':')
			}
			top.n++
		}
		switch v := tok.(type) {
		case json.Delim:
			b.WriteString(v.String())
			stack = append(stack, frame{object: v == '{'})
		case string:
			if !key {
				v = capMarked(v)
			}
			enc, err := json.Marshal(v)
			if err != nil {
				return nil, err
			}
			b.Write(enc)
		default:
			enc, err := json.Marshal(v)
			if err != nil {
				return nil, err
			}
			b.Write(enc)
		}
	}
	return b.Bytes(), nil
}

// encode renders d as compact JSON, then as one JSON string value with every
// byte outside printable ASCII escaped: the payload the header counts.
func encode(d doc) ([]byte, error) {
	inner, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}
	outer, err := json.Marshal(string(inner))
	if err != nil {
		return nil, err
	}
	return asciiOnly(outer), nil
}

// asciiOnly escapes DEL and every non-ASCII character of a JSON string
// literal as \uXXXX, with a surrogate pair above U+FFFF. json.Marshal has
// already escaped the other control characters and made the text valid UTF-8.
func asciiOnly(s []byte) []byte {
	var b bytes.Buffer
	for _, r := range string(s) {
		switch {
		case r == 0x7f:
			b.WriteString(`\u007f`)
		case r < utf8.RuneSelf:
			b.WriteRune(r)
		case r > 0xffff:
			r -= 0x10000
			fmt.Fprintf(&b, `\u%04x\u%04x`, 0xd800+(r>>10), 0xdc00+(r&0x3ff))
		default:
			fmt.Fprintf(&b, `\u%04x`, r)
		}
	}
	return b.Bytes()
}

// quote writes s as an ASCII JSON string.
func quote(s string) string {
	q, _ := json.Marshal(s) // a string always marshals
	return string(asciiOnly(q))
}
