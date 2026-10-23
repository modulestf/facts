package bundle

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/modulestf/facts/internal/facts"
	"github.com/modulestf/facts/internal/registry"
)

// Region markers. The header line, outside the region, gives the payload's
// byte count; the payload is one line, a JSON string with every byte
// outside printable ASCII escaped, so no text in it can end the region.
const (
	headerPrefix = "FACTS-BUNDLE-BYTES "
	regionBegin  = "FACTS-BUNDLE BEGIN"
	regionEnd    = "FACTS-BUNDLE END"
)

// maxBundleBytes caps what Decode reads.
const maxBundleBytes = 16 << 20

// Bundle is the facts bundle of facts-bundle.schema.json, its keys in the
// schema's order. A nil section was left out; schema_coverage is also left
// out when the report has none, that is when no schema handoff bound.
type Bundle struct {
	ToolFindings   []ToolFinding     `json:"tool_findings"`
	Candidates     []facts.Candidate `json:"candidates"`
	AreaRules      *[]AreaRules      `json:"area_rules,omitempty"`
	InterfaceDiff  json.RawMessage   `json:"interface_diff,omitempty"`
	SchemaCoverage json.RawMessage   `json:"schema_coverage,omitempty"`
	FileIndex      *[]FileChange     `json:"file_index,omitempty"`
	SeverityNeeded []SeverityNeeded  `json:"severity_needed"`
}

// ToolFinding is a final finding with the fields the bundle adds. A finding
// from a confirmed candidate keeps its candidate_id and carries confirmed_by.
type ToolFinding struct {
	facts.Finding
	Source      string `json:"source"`
	Subject     string `json:"subject"`
	CandidateID string `json:"candidate_id,omitempty"`
	ConfirmedBy string `json:"confirmed_by,omitempty"`
}

// AreaRule is one rule of an area that is not clean or not_applicable.
type AreaRule struct {
	RuleID string `json:"rule_id"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

// AreaRules are the area rules of one area.
type AreaRules struct {
	Area  string     `json:"area"`
	Rules []AreaRule `json:"rules"`
}

// FileChange is one file the change adds, modifies or removes.
type FileChange struct {
	Path   string `json:"path"`
	Change string `json:"change"`
}

// SeverityNeeded is a candidate a cheaper model confirmed on a rule with
// several severities, which the lead answers with a severity.
type SeverityNeeded struct {
	CandidateID string   `json:"candidate_id"`
	RuleID      string   `json:"rule_id"`
	File        string   `json:"file"`
	Line        *int     `json:"line"`
	Subject     string   `json:"subject"`
	Severities  []string `json:"severities"`
	ConfirmedBy string   `json:"confirmed_by"`
}

// drop leaves out one sheddable section and reports whether it was there.
func (b *Bundle) drop(section string) bool {
	had := false
	switch section {
	case registry.SectionAreaRules:
		had, b.AreaRules = b.AreaRules != nil, nil
	case registry.SectionInterfaceDiff:
		had, b.InterfaceDiff = b.InterfaceDiff != nil, nil
	case registry.SectionSchemaCoverage:
		had, b.SchemaCoverage = b.SchemaCoverage != nil, nil
	case registry.SectionFileIndex:
		had, b.FileIndex = b.FileIndex != nil, nil
	}
	return had
}

// region renders the header line and the region around a payload.
func region(payload []byte) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "%s%d\n%s\n%s\n%s\n", headerPrefix, len(payload), regionBegin, payload, regionEnd)
	return b.Bytes()
}

// Decode reads a bundle file as Build writes it: the header line, then one
// region of exactly one payload line, whose length must equal the header's
// count, and nothing after the end line. It decodes the payload's JSON
// string once and the bundle in it, refusing unknown fields.
func Decode(r io.Reader) (*Bundle, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxBundleBytes+1))
	if err != nil {
		return nil, fmt.Errorf("bundle: %w", err)
	}
	if len(data) > maxBundleBytes {
		return nil, fmt.Errorf("bundle: over %d bytes", maxBundleBytes)
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) != 5 || lines[4] != "" {
		return nil, errors.New("bundle: not a header line and one region")
	}
	count, ok := strings.CutPrefix(lines[0], headerPrefix)
	n, err := strconv.Atoi(count)
	switch {
	case !ok || err != nil || n < 0 || strconv.Itoa(n) != count:
		return nil, errors.New("bundle: bad header line")
	case lines[1] != regionBegin || lines[3] != regionEnd:
		return nil, errors.New("bundle: region markers missing")
	case n != len(lines[2]):
		return nil, fmt.Errorf("bundle: header counts %d bytes, payload has %d", n, len(lines[2]))
	}
	var payload string
	if err := json.Unmarshal([]byte(lines[2]), &payload); err != nil {
		return nil, fmt.Errorf("bundle: payload is not one JSON string: %w", err)
	}
	dec := json.NewDecoder(strings.NewReader(payload))
	dec.DisallowUnknownFields()
	var b Bundle
	if err := dec.Decode(&b); err != nil {
		return nil, fmt.Errorf("bundle: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("bundle: more than one JSON value in the payload")
	}
	if b.ToolFindings == nil || b.Candidates == nil || b.SeverityNeeded == nil {
		return nil, errors.New("bundle: tool_findings, candidates or severity_needed missing")
	}
	return &b, nil
}
