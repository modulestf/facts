package behaviour

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/modulestf/facts/internal/facts"
)

// writeTree creates files under a new temp dir.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for p, body := range files {
		full := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// runPair runs the classifier on a base and a head tree given as file maps.
func runPair(t *testing.T, base, head map[string]string, schemaFile string) *Report {
	t.Helper()
	r, err := Run(Options{Head: writeTree(t, head), Base: writeTree(t, base), Schema: schemaFile, Limits: facts.DefaultLimits})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(b, '\n')
}

func str(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return *p
}

// categories lists the categories of r's triggers in order.
func categories(r *Report) []string {
	var out []string
	for _, tr := range r.Triggers {
		out = append(out, tr.Category)
	}
	return out
}
