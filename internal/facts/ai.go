package facts

import (
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"strings"
)

// AIReport is the report lint --json --ai writes: the parts a
// reviewer decides on. The manifest keeps only its finding, candidate and
// not_evaluated entries, ManifestSummary counts the rest, the envelope gives
// each tree its hash and file count in place of its file list, and the index,
// the interface diff and the schema coverage are left out.
type AIReport struct {
	Envelope        AIEnvelope       `json:"envelope"`
	Manifest        []ManifestEntry  `json:"manifest"`
	ManifestSummary ManifestSummary  `json:"manifest_summary"`
	Findings        []Finding        `json:"findings"`
	Candidates      []Candidate      `json:"candidates"`
	Scope           *Scope           `json:"scope,omitempty"`
	Callers         *[]ChangedModule `json:"callers,omitempty"`
}

// ErrAIReport refuses a AI report where the full one is needed.
var ErrAIReport = errors.New("a lint --ai report: give the report lint --json writes without --ai")

// IsAIReport reports whether a lint --json document is the AI form,
// which alone has manifest_summary. A document that does not decode is not.
func IsAIReport(data []byte) bool {
	var probe struct {
		ManifestSummary json.RawMessage `json:"manifest_summary"`
	}
	return json.Unmarshal(data, &probe) == nil && probe.ManifestSummary != nil
}

// AIEnvelope is Envelope with AIInputs.
type AIEnvelope struct {
	SchemaVersion  int           `json:"schema_version"`
	Tool           Tool          `json:"tool"`
	HeadSHA        *string       `json:"head_sha"`
	MergeBaseSHA   *string       `json:"merge_base_sha"`
	RegistrySHA256 *string       `json:"registry_sha256"`
	Inputs         AIInputs      `json:"inputs"`
	SchemaInputs   *SchemaInputs `json:"schema_inputs"`
	ProfileSHA256  *string       `json:"profile_sha256"`
}

// AIInputs is Inputs with each tree's file list replaced by its count.
type AIInputs struct {
	Head AITree     `json:"head"`
	Base *AITree    `json:"base"`
	Diff *DiffInput `json:"diff,omitempty"`
}

// AITree is a tree's hash over its file list, and the number of files.
type AITree struct {
	TreeSHA256 string `json:"tree_sha256"`
	FileCount  int    `json:"file_count"`
}

// ManifestSummary describes the whole manifest: the entries per status, the
// clean and not_applicable entries a AI report leaves out per rule
// family (the rule id up to its first dot), and every area with an entry.
type ManifestSummary struct {
	Status  StatusCounts             `json:"status"`
	Omitted map[string]OmittedCounts `json:"omitted_by_family"`
	Areas   []string                 `json:"areas"`
}

// StatusCounts counts manifest entries per status.
type StatusCounts struct {
	Finding       int `json:"finding"`
	Candidate     int `json:"candidate"`
	NotEvaluated  int `json:"not_evaluated"`
	Clean         int `json:"clean"`
	NotApplicable int `json:"not_applicable"`
}

// OmittedCounts counts the entries of one rule family a AI report
// leaves out.
type OmittedCounts struct {
	Clean         int `json:"clean"`
	NotApplicable int `json:"not_applicable"`
}

// AIReport returns the AI form of r. It shares r's slices and pointers.
func (r *Report) AIReport() *AIReport {
	e := r.Envelope
	c := &AIReport{
		Envelope: AIEnvelope{SchemaVersion: e.SchemaVersion, Tool: e.Tool, HeadSHA: e.HeadSHA, MergeBaseSHA: e.MergeBaseSHA,
			RegistrySHA256: e.RegistrySHA256, SchemaInputs: e.SchemaInputs, ProfileSHA256: e.ProfileSHA256,
			Inputs: AIInputs{Head: aiTree(e.Inputs.Head), Diff: e.Inputs.Diff}},
		Manifest:        []ManifestEntry{},
		ManifestSummary: ManifestSummary{Omitted: map[string]OmittedCounts{}, Areas: []string{}},
		Findings:        r.Findings,
		Candidates:      r.Candidates,
		Scope:           r.Scope,
		Callers:         r.Callers,
	}
	if e.Inputs.Base != nil {
		b := aiTree(*e.Inputs.Base)
		c.Envelope.Inputs.Base = &b
	}
	s := &c.ManifestSummary
	areas := map[string]bool{}
	for _, m := range r.Manifest {
		areas[m.Area] = true
		switch m.Status {
		case StatusClean, StatusNotApplicable:
			family, _, _ := strings.Cut(m.RuleID, ".")
			o := s.Omitted[family]
			if m.Status == StatusClean {
				s.Status.Clean++
				o.Clean++
			} else {
				s.Status.NotApplicable++
				o.NotApplicable++
			}
			s.Omitted[family] = o
			continue
		case StatusFinding:
			s.Status.Finding++
		case StatusCandidate:
			s.Status.Candidate++
		case StatusNotEvaluated:
			s.Status.NotEvaluated++
		}
		c.Manifest = append(c.Manifest, m)
	}
	s.Areas = append(s.Areas, slices.Sorted(maps.Keys(areas))...)
	return c
}

func aiTree(t TreeInput) AITree {
	return AITree{TreeSHA256: t.TreeSHA256, FileCount: len(t.Files)}
}
