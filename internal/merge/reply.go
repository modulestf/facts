// Package merge checks the lead model's reply and merges it with the facts
// bundle into the merged findings, as the facts engine spec's Model output
// and merge section says.
//
// The reply is untrusted. It is parsed with exact keys, no duplicate key, no
// trailing data and a size cap, and a reply that fails any of that is
// invalid, never an error: an invalid reply is an expected outcome that
// starts the repair turn. Problem texts are fixed, with entry indexes and
// pattern-checked candidate ids only, so no reply text reaches them.
package merge

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/modulestf/facts/internal/registry"
)

// MaxReplyBytes is the largest reply the merge reads. A larger reply is
// invalid as a whole.
const MaxReplyBytes = 1 << 20

// maxReasonRunes is the model-output schema's maxLength of a reason.
const maxReasonRunes = 200

// maxCount is the model-output schema's maximum of a count, the largest
// integer a JSON reader holds exactly.
const maxCount = 1<<53 - 1

// Count keys of the envelope's optional counts object: the summary-line
// counts of the full variant.
const (
	countInstructionLike  = "instruction-like-text-from-others"
	countClaimsUnread     = "conversation-claims-unread"
	countQuotedUnverified = "review.quoted-claim-unverifiable"
)

var (
	countPattern       = regexp.MustCompile(`^(0|[1-9][0-9]*)$`)
	candidateIDPattern = regexp.MustCompile(`^C-[0-9]{3,}$`)
	findingIDPattern   = regexp.MustCompile(`^F-[0-9]{3,}$`)
	ruleIDPattern      = regexp.MustCompile(`^(compat|coverage|structure|examples|profile|scope|review|claim)\.[a-z0-9]+(-[a-z0-9]+)*$`)
	differenceID       = regexp.MustCompile(`^T-[0-9]{3}\|L[1-4]\|[A-Za-z0-9_.:-]{1,64}\|[a-z_]{1,32}$`)
	digestPattern      = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// maxExplains caps the difference ids of one finding's explains.
const maxExplains = 256

// explainsRule reports a rule whose findings may carry explains and
// evidence_digest.
func explainsRule(rule string) bool {
	return rule == "compat.expression-behaviour-changed" || strings.HasPrefix(rule, "claim.")
}

// Decision is one valid entry of candidate_decisions. Summary and
// SuggestedFix are empty when the entry leaves them out.
type Decision struct {
	CandidateID  string
	Confirm      bool
	Severity     string // set on a confirm only
	Summary      string
	SuggestedFix string
}

// Counts are the three summary-line counts a reply may carry. A reply
// without them has none: no count is ever read as 0 for its absence.
type Counts struct {
	InstructionLikeTextFromOthers uint64 `json:"instruction-like-text-from-others"`
	ConversationClaimsUnread      uint64 `json:"conversation-claims-unread"`
	QuotedClaimUnverifiable       uint64 `json:"review.quoted-claim-unverifiable"`
}

// Reply is a parsed lead reply. Parsing never fails: what does not match the
// model-output schema is recorded and makes the reply invalid.
type Reply struct {
	tooLarge bool
	// topOK is set when the reply is one JSON object with exactly the keys
	// candidate_decisions and findings, each an array, and optionally counts.
	topOK     bool
	decisions []decisionEntry
	findings  []findingEntry
	// counts is nil when the reply has no counts or malformed ones;
	// countsBad marks the malformed case.
	counts    *Counts
	countsBad bool
}

// decisionEntry is one entry of candidate_decisions. id is set whenever the
// entry carries a well-formed candidate_id, even if the rest is malformed,
// so an answer counts against its id either way.
type decisionEntry struct {
	id string
	ok bool
	d  Decision
}

type findingEntry struct {
	ok bool
	f  Finding
}

// ParseReply parses a lead reply.
func ParseReply(data []byte) *Reply {
	r := &Reply{}
	if len(data) > MaxReplyBytes {
		r.tooLarge = true
		return r
	}
	fields, err := objectFields(data)
	if err != nil {
		return r
	}
	rawCounts, hasCounts := fields["counts"]
	r.topOK = len(fields) == 2 || hasCounts && len(fields) == 3
	if hasCounts {
		r.counts = parseCounts(rawCounts)
		r.countsBad = r.counts == nil
	}
	for key, parse := range map[string]func(json.RawMessage){
		"candidate_decisions": func(raw json.RawMessage) { r.decisions = append(r.decisions, parseDecision(raw)) },
		"findings":            func(raw json.RawMessage) { r.findings = append(r.findings, parseFinding(raw)) },
	} {
		raw, ok := fields[key]
		var items []json.RawMessage
		if !ok || json.Unmarshal(raw, &items) != nil || items == nil {
			r.topOK = false
			continue
		}
		for _, it := range items {
			parse(it)
		}
	}
	return r
}

// objectFields reads one JSON object and nothing after it, and returns its
// members. It refuses a duplicate key, which encoding/json would resolve
// silently to the last value.
func objectFields(data []byte) (map[string]json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, errors.New("not a JSON object")
	}
	fields := map[string]json.RawMessage{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, _ := tok.(string) // a key is always a string token
		if _, dup := fields[key]; dup {
			return nil, errors.New("duplicate key")
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		fields[key] = v
	}
	if _, err := dec.Token(); err != nil { // the closing brace
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("data after the object")
	}
	return fields, nil
}

// exactKeys reports whether fields has every required key and no key outside
// required and optional.
func exactKeys(fields map[string]json.RawMessage, required, optional []string) bool {
	for _, k := range required {
		if _, ok := fields[k]; !ok {
			return false
		}
	}
	for k := range fields {
		if !slices.Contains(required, k) && !slices.Contains(optional, k) {
			return false
		}
	}
	return true
}

// str reads a JSON string. A value of any other type fails.
func str(raw json.RawMessage) (string, bool) {
	var s string
	if raw == nil || json.Unmarshal(raw, &s) != nil {
		return "", false
	}
	return s, true
}

// parseCounts reads the counts object: exactly the three keys, each a
// natural number written as decimal digits, at most maxCount. It returns
// nil for anything else.
func parseCounts(raw json.RawMessage) *Counts {
	fields, err := objectFields(raw)
	keys := []string{countInstructionLike, countClaimsUnread, countQuotedUnverified}
	if err != nil || !exactKeys(fields, keys, nil) {
		return nil
	}
	var n [3]uint64
	for i, k := range keys {
		v := string(fields[k])
		if !countPattern.MatchString(v) {
			return nil
		}
		if n[i], err = strconv.ParseUint(v, 10, 64); err != nil || n[i] > maxCount {
			return nil
		}
	}
	return &Counts{InstructionLikeTextFromOthers: n[0], ConversationClaimsUnread: n[1], QuotedClaimUnverifiable: n[2]}
}

func parseDecision(raw json.RawMessage) decisionEntry {
	var e decisionEntry
	fields, err := objectFields(raw)
	if err != nil {
		return e
	}
	if id, ok := str(fields["candidate_id"]); ok && candidateIDPattern.MatchString(id) {
		e.id = id
	}
	decision, _ := str(fields["decision"])
	reason, ok := str(fields["reason"])
	if e.id == "" || !ok || reason == "" || utf8.RuneCountInString(reason) > maxReasonRunes {
		return e
	}
	e.d.CandidateID = e.id
	switch decision {
	case "reject":
		e.ok = exactKeys(fields, []string{"candidate_id", "decision", "reason"}, nil)
	case "confirm":
		if !exactKeys(fields, []string{"candidate_id", "decision", "reason", "severity"}, []string{"summary", "suggested_fix"}) {
			return e
		}
		e.d.Confirm = true
		e.d.Severity, ok = str(fields["severity"])
		if !ok || registry.SeverityRank(e.d.Severity) < 0 {
			return e
		}
		for key, dst := range map[string]*string{"summary": &e.d.Summary, "suggested_fix": &e.d.SuggestedFix} {
			if raw, present := fields[key]; present {
				if *dst, ok = str(raw); !ok || *dst == "" {
					return e
				}
			}
		}
		e.ok = true
	}
	return e
}

func parseFinding(raw json.RawMessage) findingEntry {
	var e findingEntry
	fields, err := objectFields(raw)
	if err != nil || !exactKeys(fields, []string{"id", "rule_id", "severity", "file", "line", "summary", "suggested_fix"},
		[]string{"explains", "evidence_digest"}) {
		return e
	}
	f := Finding{Source: SourceLLM}
	for key, dst := range map[string]*string{
		"id": &f.ID, "rule_id": &f.RuleID, "severity": &f.Severity, "file": &f.File,
		"summary": &f.Summary, "suggested_fix": &f.SuggestedFix,
	} {
		var ok bool
		if *dst, ok = str(fields[key]); !ok || *dst == "" {
			return e
		}
	}
	line, ok := parseLine(fields["line"])
	if !ok || !findingIDPattern.MatchString(f.ID) || !ruleIDPattern.MatchString(f.RuleID) ||
		registry.SeverityRank(f.Severity) < 0 || !validPath(f.File) {
		return e
	}
	f.Line = line
	if raw, has := fields["explains"]; has {
		if json.Unmarshal(raw, &f.Explains) != nil || len(f.Explains) == 0 || len(f.Explains) > maxExplains ||
			slices.ContainsFunc(f.Explains, func(id string) bool { return !differenceID.MatchString(id) }) {
			return e
		}
	}
	if raw, has := fields["evidence_digest"]; has {
		if f.EvidenceDigest, ok = str(raw); !ok || !digestPattern.MatchString(f.EvidenceDigest) {
			return e
		}
	}
	if (f.Explains != nil || f.EvidenceDigest != "") && !explainsRule(f.RuleID) {
		return e
	}
	return findingEntry{ok: true, f: f}
}

// parseLine reads null or an integer of at least 1.
func parseLine(raw json.RawMessage) (*int, bool) {
	if string(raw) == "null" {
		return nil, true
	}
	var n int
	if json.Unmarshal(raw, &n) != nil || n < 1 {
		return nil, false
	}
	return &n, true
}

// validPath is the finding schema's path: "." or a relative path with
// forward slashes, no backslash and no ".." segment. It also refuses the
// line terminators the schema's "." does not match (line feed, carriage
// return, U+2028 and U+2029) and, stricter than the schema, every other C0
// and C1 control.
func validPath(p string) bool {
	if p == "." {
		return true
	}
	if p == "" || strings.HasPrefix(p, "/") || strings.ContainsRune(p, '\\') || strings.ContainsFunc(p, badPathRune) {
		return false
	}
	return !slices.Contains(strings.Split(p, "/"), "..")
}

// answerState is how the reply answered one candidate id of the bundle.
type answerState int

const (
	answerValid answerState = iota
	answerMissing
	answerRepeated
	answerMalformed
	answerRejectedNeeded // a reject for a severity_needed entry
	answerNoText         // a confirm without text for a candidate
)

// answer returns how the reply answered id, and the answer when it is valid.
// needed is set for an id from severity_needed.
func (r *Reply) answer(id string, needed bool) (Decision, answerState) {
	var got []decisionEntry
	for _, e := range r.decisions {
		if e.id == id {
			got = append(got, e)
		}
	}
	switch {
	case len(got) == 0:
		return Decision{}, answerMissing
	case len(got) > 1:
		return Decision{}, answerRepeated
	case !got[0].ok:
		return Decision{}, answerMalformed
	}
	d := got[0].d
	switch {
	case needed && !d.Confirm:
		return Decision{}, answerRejectedNeeded
	case !needed && d.Confirm && (d.Summary == "" || d.SuggestedFix == ""):
		return Decision{}, answerNoText
	}
	return d, answerValid
}

// Problems returns why the reply is invalid for the bundle, one fixed line
// each, or nothing when it is valid. A finding with a rule the area does not
// allow, or a severity outside its rule's list, is no problem: the merge
// drops or adjusts it and keeps the rest.
func Problems(b *Bundle, r *Reply) []string {
	if r.tooLarge {
		return []string{fmt.Sprintf("the reply is larger than %d bytes", MaxReplyBytes)}
	}
	var out []string
	if !r.topOK {
		out = append(out, "the reply is not one JSON object with exactly the arrays candidate_decisions and findings and an optional counts object")
	}
	if r.countsBad {
		out = append(out, "counts does not match the counts shape")
	}
	for i, e := range r.decisions {
		if !e.ok {
			out = append(out, fmt.Sprintf("candidate_decisions entry %d does not match the decision shape", i))
		}
	}
	for i, e := range r.findings {
		if !e.ok {
			out = append(out, fmt.Sprintf("findings entry %d does not match the finding shape", i))
		}
	}
	known := map[string]bool{}
	for _, id := range b.answerIDs() {
		known[id.id] = true
		_, st := r.answer(id.id, id.needed)
		if msg := stateProblem(st); msg != "" {
			out = append(out, fmt.Sprintf("candidate %s %s", id.id, msg))
		}
	}
	for i, e := range r.decisions {
		if e.id != "" && !known[e.id] {
			out = append(out, fmt.Sprintf("candidate_decisions entry %d answers an id the bundle does not have", i))
		}
	}
	return out
}

func stateProblem(st answerState) string {
	switch st {
	case answerMissing:
		return "has no answer"
	case answerRepeated:
		return "is answered more than once"
	case answerMalformed:
		return "has an answer that does not match the decision shape"
	case answerRejectedNeeded:
		return "is in severity_needed and takes confirm only"
	case answerNoText:
		return "is confirmed without both summary and suggested_fix"
	}
	return ""
}

// badPathRune reports a control character or a Unicode line or paragraph
// separator.
func badPathRune(r rune) bool {
	return r < 0x20 || (r >= 0x7f && r <= 0x9f) || r == '\u2028' || r == '\u2029'
}
