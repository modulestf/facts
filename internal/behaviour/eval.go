package behaviour

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"path"
	"reflect"
	"regexp"
	"slices"
	"strings"
)

// Trigger and claim states of claims-eval.
const (
	StateInert        = "inert"
	StateVerified     = "verified"
	StateCovered      = "covered"
	StateRefuted      = "refuted"
	StateUnverifiable = "unverifiable"
)

// Ladder is the ladder record the skills write.
type Ladder struct {
	SchemaVersion    int         `json:"schema_version"`
	HeadSHA          *string     `json:"head_sha"`
	BaseSHA          *string     `json:"base_sha"`
	TerraformVersion *string     `json:"terraform_version"`
	Pins             []Pin       `json:"pins"`
	Runs             []LadderRun `json:"runs"`
	Coverage         Coverage    `json:"coverage"`
}

// Pin is one provider the ladder pinned.
type Pin struct {
	Source  string `json:"source"`
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
}

// LadderRun is one level run for one trigger.
type LadderRun struct {
	TriggerID     string        `json:"trigger_id"`
	Level         string        `json:"level"`
	State         string        `json:"state"`
	Reason        *string       `json:"reason"`
	FixtureSHA256 *string       `json:"fixture_sha256"`
	Control       *string       `json:"control"`
	Observations  *Observations `json:"observations"`
}

// Observations are the allowlisted projection of a run, per revision.
type Observations struct {
	Base []Observation `json:"base"`
	Head []Observation `json:"head"`
	// Fixture and Sandbox are what the ladder records about the scratch
	// trees and the sandbox; claims-eval does not read them.
	Fixture json.RawMessage `json:"fixture,omitempty"`
	Sandbox json.RawMessage `json:"sandbox,omitempty"`
}

// Observation is one evaluated case: an expression for some inputs at L1,
// or the planned attribute at L3 and L4.
type Observation struct {
	Case  string `json:"case"`
	Known *bool  `json:"known"`
	// WhollyUnknown is set when the whole value is unknown, not only a part.
	WhollyUnknown *bool                      `json:"wholly_unknown,omitempty"`
	UnknownInputs []string                   `json:"unknown_inputs,omitempty"`
	Inputs        map[string]json.RawMessage `json:"inputs,omitempty"`
	Length        *int64                     `json:"length,omitempty"`
	// Value is the value, with null at the unknown parts of a partly
	// unknown one; absent when wholly unknown, sensitive or redacted.
	Value            json.RawMessage `json:"value,omitempty"`
	Sensitive        bool            `json:"sensitive,omitempty"`
	Redacted         bool            `json:"redacted,omitempty"`
	Action           *string         `json:"action,omitempty"`
	UnchangedVsPrior *bool           `json:"unchanged_vs_prior,omitempty"`
	// UnknownMask is the after_unknown projection: true for a wholly
	// unknown value, else the value's shape with true where it is unknown.
	UnknownMask json.RawMessage `json:"unknown_mask,omitempty"`
	// Error is set when the plan of the case failed in the subject; such
	// a case has no output.
	Error *bool `json:"error,omitempty"`
}

// Coverage is the plan coverage of the credentialed example plans.
type Coverage struct {
	Class    string            `json:"class"`
	Examples []CoverageExample `json:"examples"`
}

// CoverageExample is one planned example.
type CoverageExample struct {
	Dir        string              `json:"dir"`
	Head       string              `json:"head"`
	Base       *string             `json:"base"`
	Addresses  []string            `json:"addresses"`
	Attributes map[string][]string `json:"attributes,omitempty"`
	// ModuleCalls maps each module call of the example whose local source
	// resolves into the repository to the directory it resolves to, "." for
	// the root module.
	ModuleCalls map[string]string `json:"module_calls,omitempty"`
}

// EvalReport is the output of mtf-facts claims-eval.
type EvalReport struct {
	SchemaVersion int            `json:"schema_version"`
	Triggers      []TriggerState `json:"triggers"`
	// Capped echoes the behaviour report's capped: triggers past the cap
	// have no state and are unverifiable.
	Capped          int          `json:"capped"`
	Claims          []ClaimState `json:"claims"`
	DiscardedClaims []Discarded  `json:"discarded_claims"`
}

// TriggerState is the state of one trigger.
type TriggerState struct {
	ID     string `json:"id"`
	State  string `json:"state"`
	Reason string `json:"reason"`
	// Changes are the (case, attribute) pairs of the sweep that differ
	// between base and head, for a trigger the sweep decided.
	Changes []Change `json:"changes,omitempty"`
}

// ClaimState is the state of one valid claim.
type ClaimState struct {
	ClaimID   string `json:"claim_id"`
	State     string `json:"state"`
	Reason    string `json:"reason"`
	TriggerID string `json:"trigger_id"`
	Revision  string `json:"revision"`
}

// plannedClasses are the plan classes of an example that produced a plan.
var plannedClasses = []string{"planned", "planned-no-changes"}

var (
	shaPattern     = regexp.MustCompile(`^[0-9a-f]{40}$`)
	levelPattern   = regexp.MustCompile(`^(L[0-9]|compat)$`)
	coverageAddrRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	casePattern    = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,64}$`)
)

// ReadBehaviour reads a behaviour report.
func ReadBehaviour(file string) (*Report, error) {
	data, err := readCapped(file)
	if err != nil {
		return nil, fmt.Errorf("behaviour: %w", err)
	}
	var r Report
	if err := decodeStrict(data, &r); err != nil {
		return nil, fmt.Errorf("behaviour: %w", err)
	}
	if r.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("behaviour: want schema_version %d", SchemaVersion)
	}
	seen := map[string]bool{}
	for _, t := range r.Triggers {
		if !triggerIDPattern.MatchString(t.ID) || seen[t.ID] {
			return nil, fmt.Errorf("behaviour: trigger id %q is malformed or used twice", t.ID)
		}
		seen[t.ID] = true
	}
	return &r, nil
}

// ReadLadder reads a ladder record. Unknown fields are refused.
func ReadLadder(file string) (*Ladder, error) {
	data, err := readCapped(file)
	if err != nil {
		return nil, fmt.Errorf("ladder: %w", err)
	}
	var l Ladder
	if err := decodeStrict(data, &l); err != nil {
		return nil, fmt.Errorf("ladder: %w", err)
	}
	if why := l.check(); why != "" {
		return nil, fmt.Errorf("ladder: %s", why)
	}
	return &l, nil
}

// Bind refuses a record that does not name the given commits. A record
// with runs needs both; an empty record may be read without them.
func (l *Ladder) Bind(headSHA, baseSHA string) error {
	if len(l.Runs) > 0 && (headSHA == "" || baseSHA == "") {
		return errors.New("ladder: a record with runs needs --head-sha and --base-sha")
	}
	if headSHA != "" && deref(l.HeadSHA) != headSHA {
		return fmt.Errorf("ladder: head_sha %q is not the head commit %q", deref(l.HeadSHA), headSHA)
	}
	if baseSHA != "" && deref(l.BaseSHA) != baseSHA {
		return fmt.Errorf("ladder: base_sha %q is not the base commit %q", deref(l.BaseSHA), baseSHA)
	}
	return nil
}

func (l *Ladder) check() string {
	switch {
	case l.SchemaVersion != SchemaVersion:
		return fmt.Sprintf("want schema_version %d", SchemaVersion)
	case l.HeadSHA != nil && !shaPattern.MatchString(*l.HeadSHA), l.BaseSHA != nil && !shaPattern.MatchString(*l.BaseSHA):
		return "head_sha and base_sha are 40 lower-case hex digits or null"
	case l.Pins == nil || l.Runs == nil || l.Coverage.Examples == nil:
		return "pins, runs and coverage.examples must be arrays"
	case len(l.Runs) > 0 && (l.HeadSHA == nil || l.BaseSHA == nil):
		return "a record with runs must name its head_sha and base_sha"
	case !slices.Contains([]string{"complete", "partial", "none"}, l.Coverage.Class):
		return "coverage.class is not complete, partial or none"
	}
	for _, r := range l.Runs {
		switch {
		case !triggerIDPattern.MatchString(r.TriggerID):
			return fmt.Sprintf("run trigger_id %q is not a trigger id", r.TriggerID)
		case !levelPattern.MatchString(r.Level):
			return fmt.Sprintf("run level %q is not a level", r.Level)
		case r.State != "ran" && r.State != "unverifiable":
			return fmt.Sprintf("run state %q is not ran or unverifiable", r.State)
		case r.Control != nil && *r.Control != "valid" && *r.Control != "invalid":
			return fmt.Sprintf("run control %q is not valid, invalid or null", *r.Control)
		}
		if r.Observations != nil {
			for _, o := range slices.Concat(r.Observations.Base, r.Observations.Head) {
				if o.Known == nil {
					return "an observation has no known field"
				}
				if !casePattern.MatchString(o.Case) {
					return fmt.Sprintf("observation case %q does not match %s", o.Case, casePattern)
				}
				if o.Length != nil && *o.Length < 0 {
					return "an observation has a negative length"
				}
				if o.Action != nil && !slices.Contains(planActions, *o.Action) {
					return fmt.Sprintf("observation action %q is not a plan action", *o.Action)
				}
			}
		}
	}
	for _, e := range l.Coverage.Examples {
		for _, a := range e.Addresses {
			if !coverageAddrRe.MatchString(a) {
				return fmt.Sprintf("coverage address %q is malformed", a)
			}
		}
		for call, dir := range e.ModuleCalls {
			if !coverageAddrRe.MatchString(call) || dir != "." && !safeRelPath(dir) {
				return fmt.Sprintf("coverage module call %q -> %q is malformed", call, dir)
			}
		}
	}
	return ""
}

// Evaluate computes the state of every trigger and every valid claim. A
// claim is decided at its own revision. A trigger is refuted when one of
// its claims is. A trigger that selects L1 or L3 is decided by the tool's
// own sweep (see sweep): verified when no case differs between base and
// head, or every difference is explained by verified claims. A trigger
// that selects L4 is covered when its address, and changed attribute, are
// in a planned example and none of its claims is unverifiable. Anything
// else is unverifiable.
func Evaluate(b *Report, l *Ladder, claims []Claim, discarded []Discarded) *EvalReport {
	r := &EvalReport{SchemaVersion: SchemaVersion, Triggers: []TriggerState{}, Claims: []ClaimState{},
		DiscardedClaims: append([]Discarded{}, discarded...), Capped: b.Capped}
	byTrigger := map[string][]ClaimState{}
	claimsOf := map[string][]Claim{}
	verified := map[string]bool{}
	for _, c := range claims {
		state, why := evalClaim(c, l)
		cs := ClaimState{ClaimID: c.ClaimID, State: state, Reason: why, TriggerID: c.Node, Revision: c.Revision}
		r.Claims = append(r.Claims, cs)
		byTrigger[c.Node] = append(byTrigger[c.Node], cs)
		claimsOf[c.Node] = append(claimsOf[c.Node], c)
		verified[c.ClaimID] = state == StateVerified
	}
	inert := map[string]bool{}
	for _, e := range b.Entries {
		if e.Inert {
			for _, id := range e.TriggerIDs {
				inert[id] = true
			}
		}
	}
	for _, t := range b.Triggers {
		r.Triggers = append(r.Triggers, evalTrigger(t, l, byTrigger[t.ID], claimsOf[t.ID], verified, inert[t.ID]))
	}
	return r
}

func evalTrigger(t Trigger, l *Ladder, states []ClaimState, claims []Claim, verified map[string]bool, inert bool) TriggerState {
	ts := TriggerState{ID: t.ID, State: StateUnverifiable}
	if inert {
		ts.State, ts.Reason = StateInert, "the entry is inert"
		return ts
	}
	for _, c := range states {
		if c.State == StateRefuted {
			ts.State, ts.Reason = StateRefuted, "claim "+c.ClaimID+" is refuted: "+c.Reason
			return ts
		}
	}
	if len(t.Levels) == 0 {
		ts.Reason = cmp.Or(deref(t.Reason), "the trigger selects no level")
		return ts
	}
	var why []string
	if slices.Contains(t.Levels, LevelL1) || slices.Contains(t.Levels, LevelL2) || slices.Contains(t.Levels, LevelL3) {
		w, changes := sweep(t, l, claims, verified)
		ts.Changes = changes
		if w == "" {
			ts.State, ts.Reason = StateVerified, "no case of the sweep differs between base and head"
			if len(changes) > 0 {
				ts.Reason = fmt.Sprintf("verified claims state both sides of all %d differences", len(changes))
			}
			return ts
		}
		why = append(why, w)
	}
	unchecked := slices.ContainsFunc(states, func(c ClaimState) bool { return c.State == StateUnverifiable })
	if slices.Contains(t.Levels, LevelL4) {
		ok, cov := covered(t, l)
		switch {
		case ok && !unchecked:
			ts.State, ts.Reason = StateCovered, cov
			return ts
		case ok:
			why = append(why, "a claim of it is unverifiable")
		default:
			why = append(why, cov)
		}
	}
	if slices.Contains(t.Levels, LevelCompat) {
		why = append(why, "the compat rules decide a version floor change")
	}
	ts.Reason = strings.Join(why, "; ")
	return ts
}

// covered reports whether the trigger's address, and changed attribute,
// are in a planned example.
func covered(t Trigger, l *Ladder) (bool, string) {
	if t.Address == nil {
		return false, "the trigger has no address a plan can show"
	}
	addr := *t.Address
	dir := path.Dir(t.File)
	for _, e := range l.Coverage.Examples {
		if !slices.Contains(plannedClasses, e.Head) {
			continue
		}
		for _, a := range e.Addresses {
			if !addressMatch(a, addr, dir, e) {
				continue
			}
			if t.Attribute == nil {
				return true, fmt.Sprintf("%s is planned in %s", a, e.Dir)
			}
			if strings.HasPrefix(addr, "module.") {
				continue // a plan shows no module call argument
			}
			// A dynamic block plans as the blocks it generates. A lifecycle
			// argument is no planned attribute, so it is never covered.
			if strings.HasPrefix(*t.Attribute, "lifecycle.") {
				continue
			}
			top, _, _ := strings.Cut(strings.TrimPrefix(*t.Attribute, "dynamic."), ".")
			if slices.Contains(e.Attributes[a], top) {
				return true, fmt.Sprintf("%s.%s is planned in %s", a, top, e.Dir)
			}
		}
	}
	return false, "the address and changed attribute are in no planned example"
}

// addressMatch matches a plan config address to a trigger address of a
// block in dir. In the example's own directory the address must be equal.
// Elsewhere the plan address must be under exactly one module call of the
// example, and that call must resolve to dir: a same-named block of any
// other module never matches. A module call trigger matches every address
// under that module call.
func addressMatch(plan, trig, dir string, e CoverageExample) bool {
	if e.Dir == dir {
		return plan == trig || strings.HasPrefix(trig, "module.") && strings.HasPrefix(plan, trig+".")
	}
	rest, ok := strings.CutPrefix(plan, "module.")
	if !ok {
		return false
	}
	call, rest, ok := strings.Cut(rest, ".")
	if to, known := e.ModuleCalls[call]; !ok || !known || to != dir {
		return false
	}
	if strings.HasPrefix(trig, "module.") {
		return strings.HasPrefix(rest, trig+".")
	}
	return rest == trig
}

// validRuns returns the runs of a trigger at the levels that ran with a
// valid control.
func validRuns(l *Ladder, id string, levels []string) []LadderRun {
	var out []LadderRun
	for _, r := range l.Runs {
		// An L1, L2 or L3 run counts only with the hash of the fixture it ran.
		fixture := r.Level != LevelL1 && r.Level != LevelL2 && r.Level != LevelL3 || r.FixtureSHA256 != nil && *r.FixtureSHA256 != ""
		if r.TriggerID == id && slices.Contains(levels, r.Level) && r.State == "ran" &&
			r.Control != nil && *r.Control == "valid" && fixture {
			out = append(out, r)
		}
	}
	return out
}

// verdict of one observation for a claim.
type verdict int

const (
	vSkip verdict = iota // the observation is not about the claim
	vSupport
	vContradict
	vUnknown // about the claim, but its value cannot decide it
)

// evalClaim decides one claim from the valid runs at its levels.
func evalClaim(c Claim, l *Ladder) (string, string) {
	levels := predicateLevels[c.PredicateType]
	if slices.Contains(neverFromL2, c.PredicateType) {
		levels = slices.DeleteFunc(slices.Clone(levels), func(lv string) bool { return lv == LevelL2 })
	}
	runs := validRuns(l, c.Node, levels)
	if len(runs) == 0 {
		return StateUnverifiable, fmt.Sprintf("no %s run of %s with a valid control", strings.Join(levels, " or "), c.Node)
	}
	support, unknown := 0, 0
	for _, r := range runs {
		if r.Observations == nil {
			continue
		}
		obs := r.Observations.Head
		if c.Revision == "base" {
			obs = r.Observations.Base
		}
		for _, o := range obs {
			switch judge(c, o) {
			case vContradict:
				return StateRefuted, fmt.Sprintf("%s %s case %q contradicts it", r.Level, c.Revision, o.Case)
			case vSupport:
				support++
			case vUnknown:
				unknown++
			}
		}
	}
	switch {
	case unknown > 0:
		return StateUnverifiable, fmt.Sprintf("%d observations are unknown, sensitive or redacted", unknown)
	case support == 0:
		return StateUnverifiable, "no observation is about the claim"
	}
	return StateVerified, fmt.Sprintf("%d observations support it", support)
}

// judge is the verdict of one observation on a claim.
func judge(c Claim, o Observation) verdict {
	if failed(o) || !c.inScope(o.Inputs) {
		return vSkip // a failed plan has no output; a case out of scope is not judged
	}
	known := o.Known != nil && *o.Known
	hidden := o.Sensitive || o.Redacted || !known || o.Value == nil
	switch c.PredicateType {
	case PredExprUnknown:
		if len(o.UnknownInputs) == 0 {
			return vSkip
		}
		// Only a wholly unknown value counts: a list of known length with
		// unknown elements is not.
		if o.WhollyUnknown != nil {
			return pick(*o.WhollyUnknown)
		}
		return pick(!known && o.Length == nil)
	case PredExprLengthKnown:
		if len(o.UnknownInputs) == 0 {
			return vSkip
		}
		if !known && o.Length == nil {
			return vContradict
		}
		if c.Length == nil {
			return vSupport
		}
		if o.Length == nil {
			return vUnknown
		}
		return pick(*o.Length == *c.Length)
	case PredExprEquals:
		if len(o.UnknownInputs) > 0 || !sameInputs(o.Inputs, c.Inputs) {
			return vSkip
		}
		if hidden {
			return vUnknown
		}
		return pick(jsonEqual(o.Value, c.Expected))
	case PredInputFiltered:
		if len(o.UnknownInputs) > 0 || !holds(o.Inputs[c.Input], c.Value) {
			return vSkip
		}
		if hidden {
			return vUnknown
		}
		var out []json.RawMessage
		if json.Unmarshal(o.Value, &out) != nil {
			return vUnknown
		}
		return pick(holdsAny(out, c.Value) == c.Kept)
	case PredPlanUnknown:
		return pick(!known)
	case PredPlanUnchanged:
		if o.UnchangedVsPrior == nil && known {
			return vSkip
		}
		if !known || o.Sensitive || o.Redacted {
			return vUnknown
		}
		return pick(*o.UnchangedVsPrior)
	case PredPlanAction:
		if o.Action == nil {
			return vSkip
		}
		return pick(*o.Action == c.Action)
	}
	return vSkip
}

func pick(ok bool) verdict {
	if ok {
		return vSupport
	}
	return vContradict
}

// sameInputs reports whether an observation's case sets every input the
// claim names to the claim's known value. Inputs the claim does not name
// may take any state.
func sameInputs(obs, claim map[string]json.RawMessage) bool {
	for k, v := range claim {
		w, ok := caseValue(obs[k])
		if !ok || !jsonEqual(v, w) {
			return false
		}
	}
	return true
}

// caseValue is the known value of a case input as an observation records
// it, a CaseInput object; ok is false for an unknown or omitted input, one
// with unknown elements, or anything else.
func caseValue(raw json.RawMessage) (json.RawMessage, bool) {
	if raw == nil {
		return nil, false
	}
	var in CaseInput
	if err := decodeStrict(raw, &in); err != nil || in.Unknown || in.Omitted || len(in.UnknownElements) > 0 {
		return nil, false
	}
	if in.Null {
		return json.RawMessage("null"), true
	}
	return in.Value, in.Value != nil
}

// holds reports whether a list input holds the marker value.
func holds(raw json.RawMessage, marker string) bool {
	var list []json.RawMessage
	v, ok := caseValue(raw)
	if !ok || json.Unmarshal(v, &list) != nil {
		return false
	}
	return holdsAny(list, marker)
}

func holdsAny(list []json.RawMessage, marker string) bool {
	want := `""`
	if marker == "null" {
		want = "null"
	}
	for _, el := range list {
		if string(bytes.TrimSpace(el)) == want {
			return true
		}
	}
	return false
}

// jsonEqual compares two JSON values, numbers by value.
func jsonEqual(a, b json.RawMessage) bool {
	x, err1 := canonical(a)
	y, err2 := canonical(b)
	return err1 == nil && err2 == nil && reflect.DeepEqual(x, y)
}

func canonical(raw json.RawMessage) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return canon(v), nil
}

func canon(v any) any {
	switch x := v.(type) {
	case json.Number:
		// big.Float keeps a huge exponent cheap, where big.Rat would expand it.
		if f, ok := new(big.Float).SetPrec(256).SetString(x.String()); ok {
			return number(f.Text('g', 70))
		}
		return number(x.String())
	case []any:
		for i := range x {
			x[i] = canon(x[i])
		}
	case map[string]any:
		for k := range x {
			x[k] = canon(x[k])
		}
	}
	return v
}

// number is a canonical JSON number, apart from any string.
type number string
