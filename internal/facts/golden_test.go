package facts

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var update = flag.Bool("update", false, "rewrite testdata/*/expected.json")

// TestGolden runs every case under testdata/. A case has head/, an optional
// base/, and expected.json with the manifest, findings and candidates a person checked.
func TestGolden(t *testing.T) {
	cases, err := filepath.Glob("../../testdata/*/head")
	if err != nil || len(cases) == 0 {
		t.Fatalf("no golden cases: %v", err)
	}
	for _, head := range cases {
		dir := filepath.Dir(head)
		t.Run(filepath.Base(dir), func(t *testing.T) {
			o := Options{Head: head, Limits: DefaultLimits}
			if _, err := os.Stat(filepath.Join(dir, "base")); err == nil {
				o.Base = filepath.Join(dir, "base")
			}
			r, err := Run(o)
			if err != nil {
				t.Fatal(err)
			}
			got, err := json.MarshalIndent(struct {
				Manifest   []ManifestEntry `json:"manifest"`
				Findings   []Finding       `json:"findings"`
				Candidates []Candidate     `json:"candidates"`
			}{r.Manifest, r.Findings, r.Candidates}, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, '\n')
			file := filepath.Join(dir, "expected.json")
			if *update {
				if err := os.WriteFile(file, got, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("output differs from %s:\n%s", file, got)
			}
		})
	}
}
