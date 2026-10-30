// Package bundle builds the facts bundle a judgment-variant review reads:
// the lint report checked against the workflow's pins, cut down to six
// sections under a byte cap, embedded as one JSON string in a fixed region,
// and the operative rule text injected for every (rule, area) the bundle
// leaves not_evaluated. When the bundle or the injection does not fit, the
// run uses the full variant instead.
package bundle

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/modulestf/facts/internal/facts"
)

// Input caps. A lint report is at most facts.DefaultLimits.MaxOutputBytes.
const (
	maxReportBytes   = 16 << 20
	maxRegistryBytes = 1 << 20
	maxEngineBytes   = 256 << 10
	maxEngineFiles   = 64
)

// Report is the lint report the bundle is built from. InterfaceDiff and
// SchemaCoverage stay raw: the bundle only caps their strings. Scope and
// Callers are read so a report that has them decodes; no bundle section
// carries them.
type Report struct {
	Envelope       facts.Envelope        `json:"envelope"`
	Manifest       []facts.ManifestEntry `json:"manifest"`
	Findings       []facts.Finding       `json:"findings"`
	Candidates     []facts.Candidate     `json:"candidates"`
	Index          []facts.IndexEntry    `json:"index"`
	InterfaceDiff  json.RawMessage       `json:"interface_diff"`
	SchemaCoverage json.RawMessage       `json:"schema_coverage"`
	Scope          json.RawMessage       `json:"scope"`
	Callers        json.RawMessage       `json:"callers"`
}

// ReadReport reads and decodes a lint --json report. An unknown field, a
// second JSON value or a report over the cap is an error.
func ReadReport(p string) (*Report, error) {
	data, err := readRegular(p, maxReportBytes)
	if err != nil {
		return nil, fmt.Errorf("report: %w", err)
	}
	if facts.IsAIReport(data) {
		return nil, fmt.Errorf("report: %w", facts.ErrAIReport)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var r Report
	if err := dec.Decode(&r); err != nil {
		return nil, fmt.Errorf("report: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("report: more than one JSON value")
	}
	if string(r.InterfaceDiff) == "null" {
		r.InterfaceDiff = nil
	}
	if string(r.SchemaCoverage) == "null" {
		r.SchemaCoverage = nil
	}
	return &r, nil
}

// ReadRegistry reads the rule registry file, rule-facts.md.
func ReadRegistry(p string) ([]byte, error) {
	data, err := readRegular(p, maxRegistryBytes)
	if err != nil {
		return nil, fmt.Errorf("registry: %w", err)
	}
	return data, nil
}

// readRegular reads a regular file, not a link, of at most limit bytes.
func readRegular(p string, limit int64) ([]byte, error) {
	if st, err := os.Lstat(p); err != nil || !st.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", p)
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return readCapped(f, p, limit)
}

// readCapped reads f, which must be a regular file of at most limit bytes.
func readCapped(f interface {
	io.Reader
	Stat() (os.FileInfo, error)
}, name string, limit int64) ([]byte, error) {
	if st, err := f.Stat(); err != nil || !st.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", name)
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s is over %d bytes", name, limit)
	}
	return data, nil
}
