package merge

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the merged findings under testdata/merge-golden")

// goldenDir holds merged findings from hostile replies. TestMergedFindingsSchema
// in internal/facts validates them against merged-findings.schema.json with
// the schema validator there, which this package cannot import.
const goldenDir = "../../testdata/merge-golden"

// goldenBundle has a tool finding, two candidates, a severity_needed entry
// and an injected path rule, so the outputs carry every source.
const goldenBundle = `{"tool_findings":[
 {"id":"F-001","rule_id":"profile.nonstandard-config-file","severity":"HIGH","file":"override.tf","line":null,"summary":"tool","suggested_fix":"f","source":"tool","subject":"override.tf"}],
 "candidates":[
 {"candidate_id":"C-001","rule_id":"structure.output-no-try","file":"outputs.tf","line":3,"subject":".:output.arn","evidence":{},"evidence_complete":true},
 {"candidate_id":"C-002","rule_id":"structure.wrong-file","file":"examples/a/main.tf","line":7,"subject":"examples/a:output.url","evidence":{},"evidence_complete":true}],
 "area_rules":[{"area":"examples/a","rules":[{"rule_id":"examples.versions-missing","status":"not_evaluated","reason":"parse_error"}]}],
 "severity_needed":[
 {"candidate_id":"C-003","rule_id":"compat.variable-removed","file":"variables.tf","line":null,"subject":".:var.x","severities":["CRITICAL","HIGH"],"confirmed_by":"mid"}]}`

// hostileFindings are lead findings a hostile model could write: paths with
// line terminators, C1 controls, a parent segment, an absolute path and a
// backslash, smuggled provenance, review.* at CRITICAL, and severities
// outside their rule's list. Only the well-formed ones may survive.
const hostileFindings = `[
 {"id":"F-001","rule_id":"structure.grouping","severity":"CRITICAL","file":"main.tf","line":1,"summary":"capped to LOW","suggested_fix":"f"},
 {"id":"F-002","rule_id":"structure.grouping","severity":"LOW","file":"main.tf\u2028x","line":1,"summary":"line separator","suggested_fix":"f"},
 {"id":"F-003","rule_id":"structure.grouping","severity":"LOW","file":"main.tf\u2029x","line":1,"summary":"paragraph separator","suggested_fix":"f"},
 {"id":"F-004","rule_id":"structure.grouping","severity":"LOW","file":"main\u0085.tf","line":1,"summary":"C1 control","suggested_fix":"f"},
 {"id":"F-005","rule_id":"structure.grouping","severity":"LOW","file":"main.tf\n::error::x","line":1,"summary":"line feed","suggested_fix":"f"},
 {"id":"F-006","rule_id":"structure.grouping","severity":"LOW","file":"modules/../../etc/x","line":1,"summary":"parent","suggested_fix":"f"},
 {"id":"F-007","rule_id":"structure.grouping","severity":"LOW","file":"/etc/passwd","line":1,"summary":"absolute","suggested_fix":"f"},
 {"id":"F-008","rule_id":"structure.grouping","severity":"LOW","file":"a\\b.tf","line":1,"summary":"backslash","suggested_fix":"f"},
 {"id":"F-009","rule_id":"structure.grouping","severity":"LOW","file":"main.tf","line":2,"summary":"smuggled","suggested_fix":"f","source":"tool","confirmed_by":"small"},
 {"id":"F-010","rule_id":"review.check-not-run","severity":"CRITICAL","file":"main.tf","line":4,"summary":"review capped to MEDIUM","suggested_fix":"f"},
 {"id":"F-011","rule_id":"review.tool-disputed","severity":"HIGH","file":"override.tf","line":null,"summary":"disputed capped to LOW","suggested_fix":"f"},
 {"id":"F-012","rule_id":"profile.nonstandard-config-file","severity":"LOW","file":"override.tf","line":null,"summary":"restates the tool","suggested_fix":"f"},
 {"id":"F-013","rule_id":"examples.versions-missing","severity":"CRITICAL","file":"examples/a","line":null,"summary":"injected path rule on the area directory","suggested_fix":"f"},
 {"id":"F-014","rule_id":"structure.output-no-try","severity":"HIGH","file":"outputs.tf","line":3,"summary":"candidate rule, not injected","suggested_fix":"f"}
]`

func TestHostileGolden(t *testing.T) {
	// The first case answers every candidate once, but its bad findings are
	// shape errors, so the reply is invalid; the repair turn returns the
	// same reply, which is merged by its valid entries.
	hostileReply := `{"candidate_decisions":[
 {"candidate_id":"C-001","decision":"confirm","reason":"r","severity":"CRITICAL","summary":"capped to HIGH","suggested_fix":"f"},
 {"candidate_id":"C-002","decision":"reject","reason":"r"},
 {"candidate_id":"C-003","decision":"confirm","reason":"r","severity":"LOW"}],
 "findings":` + hostileFindings + `}`
	cases := []struct {
		name          string
		first, repair string
	}{
		{name: "hostile-findings", first: hostileReply, repair: hostileReply},
		{
			name:  "hostile-repair-still-invalid",
			first: `not JSON`,
			repair: `{"candidate_decisions":[
 {"candidate_id":"C-001","decision":"confirm","reason":"r","severity":"HIGH","summary":"s","suggested_fix":"f","source":"llm"},
 {"candidate_id":"C-002","decision":"confirm","reason":"r","severity":"MEDIUM","summary":"kept","suggested_fix":"f"},
 {"candidate_id":"C-003","decision":"reject","reason":"may not reject"},
 {"candidate_id":"C-009","decision":"reject","reason":"unknown"}],
 "findings":` + hostileFindings + `,"verdict":"approve"}`,
		},
		{
			name:   "hostile-repair-empty",
			first:  `{"candidate_decisions":[],"candidate_decisions":[],"findings":[]}`,
			repair: ``,
		},
	}
	in := Inputs{Registry: testRegistry(t), Bundle: testBundle(t, goldenBundle), Templates: map[string]Template{
		"compat.variable-removed": {Summary: "{subject} is removed.", SuggestedFix: "Restore {subject}."},
	}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Final(in, []byte(tc.first), []byte(tc.repair))
			if err != nil {
				t.Fatal(err)
			}
			for _, f := range got {
				if err := checkMerged(f); err != nil {
					t.Errorf("%s: %v", f.ID, err)
				}
			}
			out, err := Write(got)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(goldenDir, tc.name+".json")
			if *update {
				if err := os.WriteFile(path, out, 0o600); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path) //nolint:gosec // G304: a golden file of this repository
			if err != nil {
				t.Fatal(err, " (run with -update to write it)")
			}
			if !bytes.Equal(out, want) {
				t.Errorf("output differs from %s (run with -update)\ngot:\n%s", path, out)
			}
		})
	}
}
