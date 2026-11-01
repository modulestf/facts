package merge

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/modulestf/facts/internal/registry"
)

// testRegistryHeader is the rule table header of rule-facts.md.
const testRegistryHeader = "| `rule_id` | Provider facts | Page facts | Page entry when | Carry | `engine` | `class` | `scope` | `cross_area` | `area_kinds` | `bundle_sections` | `severities` | `confirm_class` | `proposed_confirm_class` |\n" +
	"|---|---|---|---|---|---|---|---|---|---|---|---|---|---|\n"

// testOverlap is the Dedup Overlap section of rule-facts.md, both rows.
const testOverlap = "\n## Dedup Overlap\n\n" +
	"| Pair | Kept | Dropped | Same defect when | From |\n" +
	"|------|------|---------|------------------|------|\n" +
	"| 1 | `compat.default-changed` | `structure.flag-polarity` | Both are about one new input. | Final Step |\n" +
	"| 2 | The `compat.*` finding for the break | `profile.commit-type-mismatch` | The title misreports that same break. | Final Step |\n" +
	"\n## Carry Audit\n\nNone.\n"

// testRows are the rules the unit tests use: id, engine, class, cross_area,
// severities.
var testRows = [][5]string{
	{"compat.break-unexplained", "llm", "config-semantic", "yes", "MEDIUM"},
	{"compat.claim-contradicts-analysis", "llm", "config-semantic", "yes", "HIGH, MEDIUM, LOW"},
	{"compat.default-changed", "candidate", "config-semantic", "no", "CRITICAL, HIGH, MEDIUM"},
	{"compat.expression-behaviour-changed", "llm", "config-semantic", "no", "CRITICAL, HIGH, LOW"},
	{"compat.variable-removed", "candidate", "config-semantic", "no", "CRITICAL, HIGH"},
	{"examples.versions-missing", "tool", "path", "no", "MEDIUM"},
	{"profile.commit-type-mismatch", "llm", "config-semantic", "yes", "MEDIUM, LOW"},
	{"profile.nonstandard-config-file", "tool", "path", "no", "HIGH"},
	{"review.check-not-run", "llm", "path", "yes", "MEDIUM"},
	{"review.tool-disputed", "llm", "path", "no", "LOW"},
	{"scope.cross", "tool", "config-semantic", "yes", "HIGH"},
	{"structure.flag-polarity", "llm", "config-semantic", "no", "MEDIUM"},
	{"structure.grouping", "llm", "config-semantic", "no", "LOW"},
	{"structure.output-no-try", "candidate", "config-semantic", "no", "HIGH"},
	{"structure.wrong-file", "candidate", "config-semantic", "no", "MEDIUM"},
	{"structure.never-emitted", "candidate", "config-semantic", "no", "none"},
}

// testRegistryText builds a rule-facts.md with testRows and the overlap
// rows in overlap.
func testRegistryText(overlap string) string {
	var b strings.Builder
	b.WriteString("# Rule facts\n\n" + testRegistryHeader)
	for _, r := range testRows {
		confirm := "-"
		if r[1] == "candidate" {
			confirm = "lead"
		}
		b.WriteString("| `" + r[0] + "` | none | - | - | never | " + r[1] + " | " + r[2] + " | any-file-in-area | " + r[3] +
			" | root, submodule, example | none | " + r[4] + " | " + confirm + " | " + confirm + " |\n")
	}
	b.WriteString(overlap)
	return b.String()
}

func testRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	reg, err := registry.Parse(testRegistryText(testOverlap))
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// regionForm wraps bundle JSON in the region form mtf-facts bundle writes:
// the byte-count header line, then one region whose one payload line is
// the ai JSON as a JSON string.
func regionForm(t testing.TB, text []byte) []byte {
	t.Helper()
	var ai bytes.Buffer
	if err := json.Compact(&ai, text); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(ai.String())
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Appendf(nil, "FACTS-BUNDLE-BYTES %d\nFACTS-BUNDLE BEGIN\n%s\nFACTS-BUNDLE END\n", len(payload), payload)
}

func testBundle(t *testing.T, text string) *Bundle {
	t.Helper()
	b, err := ParseBundle(regionForm(t, []byte(text)))
	if err != nil {
		t.Fatal(err)
	}
	return b
}
