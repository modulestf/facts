package ledger

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strings"
)

// Version is the ledger format version in schema/ledger.schema.json.
const Version = 1

// Dispositions of a field: the five categories of the maintainer skill's
// coverage ledger (Rule 4), in its order.
const (
	DispInput               = "input"                // Exposed input: a variable in variables.tf
	DispOutput              = "output"               // Computed-only output: an output in outputs.tf
	DispSensitiveOmission   = "sensitive_omission"   // Sensitive/write-only omission
	DispIntentionalOmission = "intentional_omission" // Intentional omission, with its reason
	DispNotApplicable       = "not_applicable"       // Not applicable to the module's scope
)

var (
	// namePattern is a Terraform identifier: a type, variable or output name.
	namePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)
	// pathPattern is a field path: names joined by dots, down nested blocks
	// and nested attributes.
	pathPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*(\.[A-Za-z_][A-Za-z0-9_-]*)*$`)
)

// extraKey is the one key each disposition takes besides name and disposition.
var extraKey = map[string]string{
	DispInput:               "variable",
	DispOutput:              "output",
	DispSensitiveOmission:   "reason",
	DispIntentionalOmission: "reason",
	DispNotApplicable:       "reason",
}

// Ledger is the author's coverage ledger: one disposition per field of each
// type the module uses.
type Ledger struct {
	LedgerVersion int
	Types         []LedgerType
}

// LedgerType holds the dispositions of one type's fields.
type LedgerType struct {
	Mode   string
	Type   string
	Fields []Disposition
}

// Disposition maps one field, named by its dotted path: Variable is set for
// an input, Output for an output, Reason for every omission.
type Disposition struct {
	Name        string
	Disposition string
	Variable    string
	Output      string
	Reason      string
}

// Parse decodes a ledger as strictly as schema/ledger.schema.json states it:
// one JSON value, keys matched exactly (no case folding), no unknown,
// missing or duplicate key at any level. Completeness is Check's job.
func Parse(data []byte) (*Ledger, error) {
	if err := uniqueKeys(data); err != nil {
		return nil, fmt.Errorf("ledger: %w", err)
	}
	top, err := object(data, "ledger_version", "types")
	if err != nil {
		return nil, fmt.Errorf("ledger: %w", err)
	}
	l := &Ledger{}
	if err := json.Unmarshal(top["ledger_version"], &l.LedgerVersion); err != nil || l.LedgerVersion != Version {
		return nil, fmt.Errorf("ledger: ledger_version must be %d", Version)
	}
	types, err := array(top["types"])
	if err != nil {
		return nil, fmt.Errorf("ledger: types: %w", err)
	}
	for i, raw := range types {
		t, err := parseType(raw)
		if err != nil {
			return nil, fmt.Errorf("ledger: types[%d]: %w", i, err)
		}
		l.Types = append(l.Types, t)
	}
	return l, nil
}

func parseType(raw json.RawMessage) (LedgerType, error) {
	obj, err := object(raw, "mode", "type", "fields")
	if err != nil {
		return LedgerType{}, err
	}
	var t LedgerType
	if str(obj["mode"], &t.Mode) != nil || (t.Mode != "resource" && t.Mode != "data") {
		return LedgerType{}, errors.New("mode must be resource or data")
	}
	if str(obj["type"], &t.Type) != nil || !namePattern.MatchString(t.Type) {
		return LedgerType{}, errors.New("bad type name")
	}
	fields, err := array(obj["fields"])
	if err != nil {
		return LedgerType{}, fmt.Errorf("fields: %w", err)
	}
	for i, f := range fields {
		d, err := parseDisposition(f)
		if err != nil {
			return LedgerType{}, fmt.Errorf("fields[%d]: %w", i, err)
		}
		t.Fields = append(t.Fields, d)
	}
	return t, nil
}

func parseDisposition(raw json.RawMessage) (Disposition, error) {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil || probe == nil {
		return Disposition{}, errors.New("not an object")
	}
	var d Disposition
	if str(probe["disposition"], &d.Disposition) != nil || extraKey[d.Disposition] == "" {
		return Disposition{}, fmt.Errorf("unknown disposition %q", d.Disposition)
	}
	extra := extraKey[d.Disposition]
	obj, err := object(raw, "name", "disposition", extra)
	if err != nil {
		return Disposition{}, fmt.Errorf("%s: %w", d.Disposition, err)
	}
	if str(obj["name"], &d.Name) != nil || !pathPattern.MatchString(d.Name) {
		return Disposition{}, errors.New("bad field name")
	}
	var v string
	if str(obj[extra], &v) != nil {
		return Disposition{}, fmt.Errorf("%s must be a string", extra)
	}
	switch extra {
	case "variable":
		d.Variable = v
	case "output":
		d.Output = v
	case "reason":
		d.Reason = v
	}
	if (extra == "reason" && v == "") || (extra != "reason" && !namePattern.MatchString(v)) {
		return Disposition{}, fmt.Errorf("bad %s", extra)
	}
	return d, nil
}

// object decodes a JSON object whose keys are exactly keys: each present,
// no other, compared byte for byte.
func object(raw []byte, keys ...string) (map[string]json.RawMessage, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil || m == nil {
		return nil, errors.New("not an object")
	}
	for k := range m {
		if !slices.Contains(keys, k) {
			return nil, fmt.Errorf("unknown key %q", k)
		}
	}
	for _, k := range keys {
		if _, ok := m[k]; !ok {
			return nil, fmt.Errorf("missing key %q", k)
		}
	}
	return m, nil
}

// array decodes a JSON array, refusing null.
func array(raw json.RawMessage) ([]json.RawMessage, error) {
	var a []json.RawMessage
	if err := json.Unmarshal(raw, &a); err != nil || a == nil {
		return nil, errors.New("not an array")
	}
	return a, nil
}

// str decodes a JSON string, refusing null and every other type.
func str(raw json.RawMessage, s *string) error {
	if len(raw) == 0 || raw[0] != '"' {
		return errors.New("not a string")
	}
	return json.Unmarshal(raw, s)
}

// uniqueKeys refuses a document with a duplicate key in any object, which
// encoding/json would resolve silently by keeping the last, and a document
// with more than one JSON value.
func uniqueKeys(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := walk(dec); err != nil {
		return err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("more than one JSON value")
	}
	return nil
}

// walk reads one JSON value from dec, checking the keys of every object in it.
func walk(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	switch tok {
	case json.Delim('{'):
		seen := map[string]bool{}
		for dec.More() {
			k, err := dec.Token()
			if err != nil {
				return err
			}
			key, _ := k.(string)
			if seen[key] {
				return fmt.Errorf("duplicate key %q", key)
			}
			seen[key] = true
			if err := walk(dec); err != nil {
				return err
			}
		}
		_, err = dec.Token()
		return err
	case json.Delim('['):
		for dec.More() {
			if err := walk(dec); err != nil {
				return err
			}
		}
		_, err = dec.Token()
		return err
	}
	return nil
}

// ReadFile reads the regular file at p, refusing a link, a special file and
// a file over limit bytes.
func ReadFile(p string, limit int64) ([]byte, error) {
	if p == "" {
		return nil, errors.New("empty path")
	}
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

// parent is the path of the field that holds the field at p, "" at the top.
func parent(p string) string {
	i := strings.LastIndexByte(p, '.')
	if i < 0 {
		return ""
	}
	return p[:i]
}
