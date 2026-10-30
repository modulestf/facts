package fixture

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
)

// Pin is one provider version the host installed.
type Pin struct {
	Source  string `json:"source"`
	Version string `json:"version"`
	SHA256  string `json:"sha256,omitempty"` // informational; the acquisition phase checks it
}

const maxPinsBytes = 1 << 20

var (
	pinSource  = regexp.MustCompile(`^([a-z0-9.-]+/)?[a-z0-9-]+/[a-z0-9-]+$`)
	pinVersion = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$`)
	hex64      = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// ReadPins reads a pins file, {"providers":[{"source","version"}]}, each
// version exact and each source once.
func ReadPins(name string) ([]Pin, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxPinsBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxPinsBytes {
		return nil, fmt.Errorf("pins: over %d bytes", maxPinsBytes)
	}
	var doc struct {
		Providers []Pin `json:"providers"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("pins: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("pins: data after the document")
	}
	seen := map[string]bool{}
	for i, p := range doc.Providers {
		switch {
		case !pinSource.MatchString(p.Source):
			return nil, fmt.Errorf("pins: provider %d: source %q is not a provider source", i, p.Source)
		case !pinVersion.MatchString(p.Version):
			return nil, fmt.Errorf("pins: provider %d: version %q is not an exact version", i, p.Version)
		case p.SHA256 != "" && !hex64.MatchString(p.SHA256):
			return nil, fmt.Errorf("pins: provider %d: sha256 is not 64 hex digits", i)
		case seen[p.Source]:
			return nil, fmt.Errorf("pins: source %q pinned twice", p.Source)
		}
		seen[p.Source] = true
	}
	return doc.Providers, nil
}
