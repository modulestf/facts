package ledger

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
)

// Version is the ledger format version in schema/ledger.schema.json.
const Version = 1

// Dispositions of a field, the categories of the maintainer skill's coverage
// ledger.
const (
	DispInput               = "input"
	DispOutput              = "output"
	DispSensitiveOmission   = "sensitive_omission"
	DispIntentionalOmission = "intentional_omission"
	DispNotApplicable       = "not_applicable"
)

var namePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)

// Ledger is the author's coverage ledger: one disposition per field of each
// type the module uses.
type Ledger struct {
	LedgerVersion int          `json:"ledger_version"`
	Types         []LedgerType `json:"types"`
}

// LedgerType holds the dispositions of one type's fields.
type LedgerType struct {
	Mode   string        `json:"mode"`
	Type   string        `json:"type"`
	Fields []Disposition `json:"fields"`
}

// Disposition maps one field: Variable is set for an input, Output for an
// output, Reason for every omission.
type Disposition struct {
	Name        string `json:"name"`
	Disposition string `json:"disposition"`
	Variable    string `json:"variable,omitempty"`
	Output      string `json:"output,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

// Parse decodes a ledger strictly: one JSON value, no unknown key, and every
// entry in the shape the schema states. Completeness is Check's job.
func Parse(data []byte) (*Ledger, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var l Ledger
	if err := dec.Decode(&l); err != nil {
		return nil, fmt.Errorf("ledger: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("ledger: more than one JSON value")
	}
	if l.LedgerVersion != Version {
		return nil, fmt.Errorf("ledger: ledger_version must be %d", Version)
	}
	if l.Types == nil {
		return nil, errors.New("ledger: types is required")
	}
	for _, t := range l.Types {
		if (t.Mode != "resource" && t.Mode != "data") || !namePattern.MatchString(t.Type) || t.Fields == nil {
			return nil, fmt.Errorf("ledger: type %q: needs mode resource or data, a type name and fields", t.Type)
		}
		for _, d := range t.Fields {
			if err := d.validate(); err != nil {
				return nil, fmt.Errorf("ledger: %s %s: field %q: %w", t.Mode, t.Type, d.Name, err)
			}
		}
	}
	return &l, nil
}

func (d Disposition) validate() error {
	if !namePattern.MatchString(d.Name) {
		return errors.New("bad name")
	}
	switch d.Disposition {
	case DispInput:
		if !namePattern.MatchString(d.Variable) || d.Output != "" || d.Reason != "" {
			return errors.New("an input needs a variable name and no output or reason")
		}
	case DispOutput:
		if !namePattern.MatchString(d.Output) || d.Variable != "" || d.Reason != "" {
			return errors.New("an output needs an output name and no variable or reason")
		}
	case DispSensitiveOmission, DispIntentionalOmission, DispNotApplicable:
		if d.Reason == "" || d.Variable != "" || d.Output != "" {
			return errors.New("an omission needs a reason and no variable or output")
		}
	default:
		return fmt.Errorf("unknown disposition %q", d.Disposition)
	}
	return nil
}

// Problem is one way a ledger fails the inventory.
type Problem struct {
	Mode    string `json:"mode"`
	Type    string `json:"type"`
	Field   string `json:"field"` // "" for a problem with the whole type
	Problem string `json:"problem"`
}

// Problem texts.
const (
	ProblemUnmapped        = "unmapped: no disposition"
	ProblemDuplicate       = "duplicate: more than one disposition"
	ProblemUnknownField    = "unknown field: not in the schema"
	ProblemUnusedType      = "unknown type: the module does not declare it"
	ProblemDuplicateType   = "duplicate type"
	ProblemMissingVariable = "input: no such variable in the module"
	ProblemMissingOutput   = "output: no such output in the module"
)

// Check reports every field of the inventory without exactly one
// disposition, every ledger entry the inventory lacks, and every input or
// output the module does not declare. An empty result means the ledger is
// complete; it says nothing of whether each disposition is right.
func Check(inv *Inventory, l *Ledger) []Problem {
	problems := []Problem{}
	byType := map[string]LedgerType{}
	for _, t := range l.Types {
		k := t.Mode + " " + t.Type
		if _, dup := byType[k]; dup {
			problems = append(problems, Problem{Mode: t.Mode, Type: t.Type, Problem: ProblemDuplicateType})
			continue
		}
		byType[k] = t
	}
	used := map[string]bool{}
	for _, it := range inv.Types {
		k := it.Mode + " " + it.Type
		used[k] = true
		p := func(field, text string) {
			problems = append(problems, Problem{Mode: it.Mode, Type: it.Type, Field: field, Problem: text})
		}
		known := map[string]bool{}
		for _, f := range it.Fields {
			known[f.Name] = true
		}
		count := map[string]int{}
		for _, d := range byType[k].Fields {
			count[d.Name]++
			switch {
			case !known[d.Name]:
				if count[d.Name] == 1 {
					p(d.Name, ProblemUnknownField)
				}
			case d.Disposition == DispInput && !inv.variables[d.Variable]:
				p(d.Name, ProblemMissingVariable)
			case d.Disposition == DispOutput && !inv.outputs[d.Output]:
				p(d.Name, ProblemMissingOutput)
			}
		}
		for _, f := range it.Fields {
			switch {
			case count[f.Name] == 0:
				p(f.Name, ProblemUnmapped)
			case count[f.Name] > 1:
				p(f.Name, ProblemDuplicate)
			}
		}
	}
	for _, t := range l.Types {
		if k := t.Mode + " " + t.Type; !used[k] {
			used[k] = true // one problem per type
			problems = append(problems, Problem{Mode: t.Mode, Type: t.Type, Problem: ProblemUnusedType})
		}
	}
	return problems
}

// ReadFile reads the regular file at p, refusing a link, a special file and
// a file over limit bytes.
func ReadFile(p string, limit int64) ([]byte, error) {
	if st, err := os.Lstat(p); err != nil || !st.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", p)
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if st, err := f.Stat(); err != nil || !st.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", p)
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s is over %d bytes", p, limit)
	}
	return data, nil
}
