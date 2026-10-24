package merge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestModelOutputExamples runs every schema example of the model reply
// through ParseReply: the valid ones parse in shape with no bad counts, and
// the counts examples the schema refuses set countsBad.
func TestModelOutputExamples(t *testing.T) {
	paths, err := filepath.Glob("../../schema/examples/model-output.*.json")
	if err != nil || len(paths) == 0 {
		t.Fatalf("no model-output examples: %v", err)
	}
	badCounts := map[string]bool{
		"model-output.invalid.required.counts-missing-key.json":           true,
		"model-output.invalid.minimum.negative-count.json":                true,
		"model-output.invalid.type.fractional-count.json":                 true,
		"model-output.invalid.additionalProperties.counts-extra-key.json": true,
	}
	seen := 0
	for _, p := range paths {
		name := filepath.Base(p)
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(p) //nolint:gosec // G304: a schema example of this repository
			if err != nil {
				t.Fatal(err)
			}
			r := ParseReply(data)
			switch {
			case strings.HasPrefix(name, "model-output.valid."):
				if !r.topOK || r.countsBad {
					t.Errorf("topOK %v, countsBad %v", r.topOK, r.countsBad)
				}
				if (r.counts != nil) != strings.Contains(string(data), `"counts"`) {
					t.Errorf("counts %+v", r.counts)
				}
			case badCounts[name]:
				seen++
				if !r.countsBad || r.counts != nil {
					t.Errorf("countsBad %v, counts %+v", r.countsBad, r.counts)
				}
			default:
				if r.countsBad || r.counts != nil {
					t.Errorf("an example without counts has countsBad %v, counts %+v", r.countsBad, r.counts)
				}
			}
		})
	}
	if seen != len(badCounts) {
		t.Errorf("ran %d of the %d counts examples", seen, len(badCounts))
	}
}
