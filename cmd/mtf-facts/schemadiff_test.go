package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modulestf/facts/internal/schemadiff"
)

func TestSchemaDiff(t *testing.T) {
	t.Parallel()
	td := filepath.Join("..", "..", "testdata", "schema-diff")
	var out, errb bytes.Buffer
	code := run([]string{"schema-diff",
		"--from", filepath.Join(td, "from.json"),
		"--to", filepath.Join(td, "to.json"),
		"--types-from", filepath.Join(td, "module"),
	}, &out, &errb)
	if code != 0 || errb.Len() != 0 {
		t.Fatalf("exit %d, stderr %q", code, errb.String())
	}
	if strings.Contains(out.String(), "testdata") {
		t.Errorf("output records a host path: %s", out.String())
	}
	var r schemadiff.Report
	if err := json.Unmarshal(out.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if r.SchemaVersion != schemadiff.SchemaVersion || r.Tool.Name != "mtf-facts" || len(r.Tool.BinarySHA256) != 64 ||
		r.From.Path != "from.json" || r.To.Path != "to.json" || len(r.Changes) == 0 {
		t.Errorf("report %+v", r)
	}
}

func TestSchemaDiffBadArgs(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{},
		{"--from", "a", "--to", "b"},
		{"--from", "a", "--to", "b", "--types-from", "c", "extra"},
		{"--unknown"},
	} {
		var out, errb bytes.Buffer
		if code := run(append([]string{"schema-diff"}, args...), &out, &errb); code != exitError || out.Len() != 0 || errb.Len() == 0 {
			t.Errorf("%v: exit %d, stdout %q, stderr %q", args, code, out.String(), errb.String())
		}
	}
	var out, errb bytes.Buffer
	if code := run([]string{"schema-diff", "--from", "none.json", "--to", "none.json", "--types-from", "."}, &out, &errb); code != exitError ||
		!strings.HasPrefix(errb.String(), "mtf-facts: schema-diff: --from:") {
		t.Errorf("missing file: exit %d, stderr %q", code, errb.String())
	}
}
