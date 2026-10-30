package behaviour

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path"
	"regexp"
	"slices"
	"strings"
)

// Predicate types of a claim.
const (
	PredExprUnknown     = "expression-output-unknown"
	PredExprLengthKnown = "expression-output-length-known"
	PredExprEquals      = "expression-output-equals-for-inputs"
	PredPlanUnknown     = "plan-attribute-unknown"
	PredPlanUnchanged   = "plan-attribute-unchanged-vs-prior"
	PredPlanAction      = "plan-action"
	PredInputFiltered   = "input-filtered"
)

// Caps of the claims file.
const (
	MaxClaims        = 200
	maxClaimText     = 2000
	maxInputFileSize = 16 << 20
)

// predicateKeys are the params keys of each predicate, required ones first;
// optional keys are listed in optionalKeys.
var predicateKeys = map[string][]string{
	PredExprUnknown:     {"node", "revision"},
	PredExprLengthKnown: {"node", "revision"},
	PredExprEquals:      {"node", "revision", "inputs", "expected"},
	PredPlanUnknown:     {"node", "revision"},
	PredPlanUnchanged:   {"node", "revision"},
	PredPlanAction:      {"node", "revision", "action"},
	PredInputFiltered:   {"node", "revision", "input", "value"},
}

var optionalKeys = map[string][]string{
	PredExprUnknown:     {"cases_where"},
	PredExprLengthKnown: {"length", "cases_where"},
	PredInputFiltered:   {"filtered", "cases_where"},
}

// neverFromL2 are the predicates about unknown values. A mock provider
// makes computed values known at plan, so an L2 run never evidences them.
var neverFromL2 = []string{PredExprUnknown, PredExprLengthKnown, PredPlanUnknown}

// scopeStates are the states a cases_where entry can name.
var scopeStates = []string{"wholly_unknown", "unknown_element", "empty", "known", "null", "omitted"}

// predicateLevels are the ladder levels whose runs a predicate reads.
var predicateLevels = map[string][]string{
	PredExprUnknown: {LevelL1}, PredExprLengthKnown: {LevelL1}, PredExprEquals: {LevelL1},
	PredInputFiltered: {LevelL1}, PredPlanUnknown: {LevelL3, LevelL4},
	PredPlanUnchanged: {LevelL3, LevelL4}, PredPlanAction: {LevelL3, LevelL4},
}

var (
	claimIDPattern   = regexp.MustCompile(`^C-[0-9]{1,6}$`)
	triggerIDPattern = regexp.MustCompile(`^T-[0-9]{3}$`)
	planActions      = []string{"no-op", "create", "update", "replace", "delete"}
)

// Claim is one valid claim.
type Claim struct {
	ClaimID       string
	PredicateType string
	Node          string
	Revision      string
	Length        *int64
	Inputs        map[string]json.RawMessage
	Expected      json.RawMessage
	Action        string
	Input         string
	Value         string // empty-string or null
	// Scope limits an unknown-ness claim to the required cases whose inputs
	// are all in the named states.
	Scope map[string]string
	// Kept inverts input-filtered: the marker elements of the input reach
	// the output (params.filtered false).
	Kept bool
}

// Discarded is a claim the validation refused.
type Discarded struct {
	ClaimID string `json:"claim_id"`
	Reason  string `json:"reason"`
}

// rawClaim is the shape of one claim in the file.
type rawClaim struct {
	ClaimID       string          `json:"claim_id"`
	Text          string          `json:"text"`
	PredicateType string          `json:"predicate_type"`
	Params        json.RawMessage `json:"params"`
	TriggerID     *string         `json:"trigger_id"`
	File          *string         `json:"file"`
	Line          *int64          `json:"line"`
}

// ReadClaims reads and validates a claims file against the triggers of a
// behaviour report. A file that is not a claims document is an error; a
// claim that does not validate is discarded with a reason.
func ReadClaims(file string, b *Report) ([]Claim, []Discarded, error) {
	data, err := readCapped(file)
	if err != nil {
		return nil, nil, fmt.Errorf("claims: %w", err)
	}
	var doc struct {
		SchemaVersion *int              `json:"schema_version"`
		Claims        []json.RawMessage `json:"claims"`
	}
	if err := decodeStrict(data, &doc); err != nil {
		return nil, nil, fmt.Errorf("claims: %w", err)
	}
	if doc.SchemaVersion == nil || *doc.SchemaVersion != SchemaVersion || doc.Claims == nil {
		return nil, nil, fmt.Errorf("claims: want schema_version %d and a claims array", SchemaVersion)
	}
	triggers := map[string]*Trigger{}
	for i := range b.Triggers {
		triggers[b.Triggers[i].ID] = &b.Triggers[i]
	}
	var valid []Claim
	discarded := []Discarded{}
	seen := map[string]bool{}
	for i, raw := range doc.Claims {
		id := fmt.Sprintf("claims[%d]", i)
		var rc rawClaim
		if err := decodeStrict(raw, &rc); err == nil && claimIDPattern.MatchString(rc.ClaimID) && !seen[rc.ClaimID] {
			id = rc.ClaimID
		}
		if i >= MaxClaims {
			discarded = append(discarded, Discarded{id, fmt.Sprintf("over the cap of %d claims", MaxClaims)})
			continue
		}
		c, why := validateClaim(raw, triggers, seen)
		if why != "" {
			discarded = append(discarded, Discarded{id, why})
			continue
		}
		seen[c.ClaimID] = true
		valid = append(valid, c)
	}
	return valid, discarded, nil
}

// validateClaim checks one claim; why is set when it is refused.
func validateClaim(raw json.RawMessage, triggers map[string]*Trigger, seen map[string]bool) (Claim, string) {
	var rc rawClaim
	if err := decodeStrict(raw, &rc); err != nil {
		return Claim{}, "not a claim object: " + err.Error()
	}
	switch {
	case !claimIDPattern.MatchString(rc.ClaimID):
		return Claim{}, "claim_id does not match ^C-[0-9]{1,6}$"
	case seen[rc.ClaimID]:
		return Claim{}, "claim_id " + rc.ClaimID + " is used twice"
	case strings.TrimSpace(rc.Text) == "" || len(rc.Text) > maxClaimText:
		return Claim{}, fmt.Sprintf("text is empty or over %d bytes", maxClaimText)
	case predicateKeys[rc.PredicateType] == nil:
		return Claim{}, fmt.Sprintf("predicate_type %q is not in the closed predicate list", rc.PredicateType)
	case rc.File != nil && !safeRelPath(*rc.File):
		return Claim{}, "file is not a clean relative path"
	case rc.Line != nil && *rc.Line < 1:
		return Claim{}, "line is not a positive integer"
	}
	c := Claim{ClaimID: rc.ClaimID, PredicateType: rc.PredicateType}
	if why := c.params(rc.Params, triggers); why != "" {
		return Claim{}, why
	}
	if rc.TriggerID != nil && *rc.TriggerID != c.Node {
		return Claim{}, "trigger_id differs from params.node"
	}
	return c, ""
}

// params decodes and checks the params of a claim of a known predicate.
func (c *Claim) params(raw json.RawMessage, triggers map[string]*Trigger) string {
	var p map[string]json.RawMessage
	if err := decodeStrict(raw, &p); err != nil || p == nil {
		return "params is not an object"
	}
	required := predicateKeys[c.PredicateType]
	allowed := append(slices.Clone(required), optionalKeys[c.PredicateType]...)
	for _, k := range slices.Sorted(maps.Keys(p)) {
		if !slices.Contains(allowed, k) {
			return fmt.Sprintf("params key %q is not allowed for %s", k, c.PredicateType)
		}
	}
	for _, k := range required {
		if _, ok := p[k]; !ok {
			return fmt.Sprintf("params lacks %q", k)
		}
	}
	str := func(k string) (string, bool) {
		var s string
		return s, json.Unmarshal(p[k], &s) == nil
	}
	var ok bool
	if c.Node, ok = str("node"); !ok || !triggerIDPattern.MatchString(c.Node) {
		return "params.node is not a trigger id (T-nnn); expressions and attributes are named by trigger id, never by HCL text"
	}
	t := triggers[c.Node]
	if t == nil {
		return "params.node " + c.Node + " is not a trigger of the behaviour report"
	}
	if c.Revision, ok = str("revision"); !ok || c.Revision != "base" && c.Revision != "head" {
		return `params.revision is not "base" or "head"`
	}
	if (c.Revision == "base" && t.BaseExpr == nil || c.Revision == "head" && t.HeadExpr == nil) &&
		slices.Contains(predicateLevels[c.PredicateType], LevelL1) {
		return "trigger " + c.Node + " has no " + c.Revision + " expression"
	}
	inputs := map[string]bool{}
	for _, in := range t.ReferencedInputs {
		inputs[in.Name] = true
	}
	// The case inputs: variables and the contextual references the sweep
	// stands in for.
	caseInputs := maps.Clone(inputs)
	for _, cs := range t.Cases {
		for name := range cs.Inputs {
			caseInputs[name] = true
		}
	}
	if raw, has := p["cases_where"]; has {
		if why := c.scope(raw, t, caseInputs); why != "" {
			return why
		}
	}
	switch c.PredicateType {
	case PredExprLengthKnown:
		if raw, has := p["length"]; has {
			var n int64
			if json.Unmarshal(raw, &n) != nil || n < 0 {
				return "params.length is not a non-negative integer"
			}
			c.Length = &n
		}
	case PredExprEquals:
		if json.Unmarshal(p["inputs"], &c.Inputs) != nil || c.Inputs == nil {
			return "params.inputs is not an object"
		}
		for name := range c.Inputs {
			if !caseInputs[name] {
				return fmt.Sprintf("params.inputs names %q, which trigger %s does not read", name, c.Node)
			}
		}
		c.Expected = p["expected"]
	case PredPlanAction:
		if c.Action, ok = str("action"); !ok || !slices.Contains(planActions, c.Action) {
			return "params.action is not one of " + strings.Join(planActions, ", ")
		}
	case PredInputFiltered:
		if c.Input, ok = str("input"); !ok || !caseInputs[c.Input] {
			return "params.input is not an input of trigger " + c.Node
		}
		if raw, has := p["filtered"]; has {
			var f bool
			if json.Unmarshal(raw, &f) != nil {
				return "params.filtered is not a boolean"
			}
			c.Kept = !f
		}
		if c.Value, ok = str("value"); !ok || c.Value != "empty-string" && c.Value != "null" {
			return `params.value is not "empty-string" or "null"`
		}
	}
	return ""
}

// scope decodes and checks params.cases_where: a non-empty object of case
// input names to scope states.
func (c *Claim) scope(raw json.RawMessage, t *Trigger, caseInputs map[string]bool) string {
	if len(t.Cases) == 0 {
		return "params.cases_where needs a trigger with required cases"
	}
	if err := decodeStrict(raw, &c.Scope); err != nil || len(c.Scope) == 0 {
		return "params.cases_where is not a non-empty object of input names to states"
	}
	for name, st := range c.Scope {
		if !caseInputs[name] {
			return fmt.Sprintf("params.cases_where names %q, which is no input of trigger %s's cases", name, c.Node)
		}
		if !slices.Contains(scopeStates, st) {
			return fmt.Sprintf("params.cases_where state %q is not one of %s", st, strings.Join(scopeStates, ", "))
		}
	}
	return ""
}

// inScope reports whether a case's inputs are all in the scope's states.
// An unscoped claim is judged on every case.
func (c Claim) inScope(inputs map[string]json.RawMessage) bool {
	for name, want := range c.Scope {
		var in CaseInput
		if raw := inputs[name]; raw == nil || decodeStrict(raw, &in) != nil || scopeState(in) != want {
			return false
		}
	}
	return true
}

// scopeState is the scope state of a case input.
func scopeState(in CaseInput) string {
	switch {
	case in.Unknown:
		return "wholly_unknown"
	case in.Omitted:
		return "omitted"
	case in.Null:
		return "null"
	case len(in.UnknownElements) > 0:
		return "unknown_element"
	}
	switch string(bytes.TrimSpace(in.Value)) {
	case "[]", "{}", `""`:
		return "empty"
	}
	return "known"
}

// safeRelPath accepts a clean relative path with no control character.
func safeRelPath(p string) bool {
	if p == "" || len(p) > 512 || path.IsAbs(p) || path.Clean(p) != p || p == ".." || strings.HasPrefix(p, "../") {
		return false
	}
	return !strings.ContainsFunc(p, func(r rune) bool { return r < 0x20 || r == 0x7f })
}

// decodeStrict decodes one JSON value, refusing unknown fields and
// trailing data.
func decodeStrict(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("more than one JSON value")
	}
	return nil
}

// readCapped reads a regular file, not a link, of at most maxInputFileSize
// bytes.
func readCapped(p string) ([]byte, error) {
	if st, err := os.Lstat(p); err != nil || !st.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path.Base(p))
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxInputFileSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxInputFileSize {
		return nil, fmt.Errorf("%s is over %d bytes", path.Base(p), maxInputFileSize)
	}
	return data, nil
}
