package merge

import (
	"slices"
	"strings"
	"testing"

	"github.com/modulestf/facts/internal/registry"
)

// unitBundle is a versioned bundle whose module area infra/modules/network
// is listed for the in-repo downgrade and infra/modules/vpc is not. Each of
// the three areas has compat.variable-removed not_evaluated, so a lead may
// file it there; the root also has scope.cross, which is cross-area.
const unitBundle = `{"schema_version":1,"tool_findings":[],"candidates":[
 {"candidate_id":"C-001","rule_id":"compat.variable-removed","file":"infra/modules/network/variables.tf","line":5,"subject":"infra/modules/network:var.name","evidence":{},"evidence_complete":true,"downgrade":"in-repo","severities":["MEDIUM"]},
 {"candidate_id":"C-002","rule_id":"compat.variable-removed","file":"infra/modules/vpc/variables.tf","line":5,"subject":"infra/modules/vpc:var.name","evidence":{},"evidence_complete":true},
 {"candidate_id":"C-003","rule_id":"compat.variable-removed","file":"variables.tf","line":5,"subject":".:var.name","evidence":{},"evidence_complete":true}],
 "area_rules":[
  {"area":".","rules":[{"rule_id":"compat.variable-removed","status":"candidate"},{"rule_id":"scope.cross","status":"not_evaluated","reason":"parse_error"},{"rule_id":"structure.output-no-try","status":"not_evaluated","reason":"parse_error"}]},
  {"area":"examples/complete","rules":[{"rule_id":"examples.versions-missing","status":"not_evaluated","reason":"parse_error"}]},
  {"area":"infra/modules/network","rules":[{"rule_id":"compat.variable-removed","status":"candidate"},{"rule_id":"compat.default-changed","status":"not_evaluated","reason":"parse_error"}]},
  {"area":"infra/modules/vpc","rules":[{"rule_id":"compat.default-changed","status":"not_evaluated","reason":"parse_error"}]}],
 "module_areas":["infra/modules/network","infra/modules/vpc"],
 "in_repo_downgrade":["infra/modules/network"],
 "severity_needed":[]}`

// legacy returns text as a legacy bundle: no schema_version, module_areas
// or in_repo_downgrade, and the candidates without the marker.
func legacy(text string) string {
	for _, cut := range []string{`"schema_version":1,`, `,"downgrade":"in-repo","severities":["MEDIUM"]`,
		"\n \"module_areas\":[\"infra/modules/network\",\"infra/modules/vpc\"],", "\n \"in_repo_downgrade\":[\"infra/modules/network\"],"} {
		text = strings.Replace(text, cut, "", 1)
	}
	return text
}

// shed returns text with the whole ownership unit shed.
func shed(text string) string {
	text = legacy(text)
	i := strings.Index(text, "\n \"area_rules\":")
	j := strings.Index(text, "\n \"severity_needed\"")
	return text[:i] + text[j:]
}

func TestParseBundleVersions(t *testing.T) {
	b := testBundle(t, unitBundle)
	if !b.Versioned || !slices.Equal(b.ModuleAreas, []string{"infra/modules/network", "infra/modules/vpc"}) || !slices.Equal(b.InRepoDowngrade, []string{"infra/modules/network"}) {
		t.Errorf("versioned bundle read as %+v", b)
	}
	if b := testBundle(t, legacy(unitBundle)); b.Versioned || b.ModuleAreas != nil || len(b.AreaRules) == 0 {
		t.Errorf("legacy bundle read as versioned %v, module areas %q", b.Versioned, b.ModuleAreas)
	}
	if b := testBundle(t, shed(unitBundle)); b.Versioned || b.AreaRules != nil {
		t.Errorf("shed bundle read as versioned %v, area rules %v", b.Versioned, b.AreaRules)
	}
	for name, text := range map[string]string{
		"version 2":                  strings.Replace(unitBundle, `"schema_version":1`, `"schema_version":2`, 1),
		"version 0":                  strings.Replace(unitBundle, `"schema_version":1`, `"schema_version":0`, 1),
		"no module_areas":            strings.Replace(unitBundle, "\n \"module_areas\":[\"infra/modules/network\",\"infra/modules/vpc\"],", "", 1),
		"no in_repo_downgrade":       strings.Replace(unitBundle, "\n \"in_repo_downgrade\":[\"infra/modules/network\"],", "", 1),
		"no area_rules":              shed(unitBundle)[:0] + strings.Replace(shed(unitBundle), `{"tool_findings"`, `{"schema_version":1,"module_areas":[],"in_repo_downgrade":[],"tool_findings"`, 1),
		"unit without version":       strings.Replace(unitBundle, `"schema_version":1,`, "", 1),
		"module area escapes":        strings.Replace(unitBundle, `"module_areas":["infra/modules/network"`, `"module_areas":["../network"`, 1),
		"module area is the root":    strings.Replace(unitBundle, `"module_areas":["infra/modules/network"`, `"module_areas":[".","infra/modules/network"`, 1),
		"module areas unsorted":      strings.Replace(unitBundle, `"infra/modules/network","infra/modules/vpc"]`, `"infra/modules/vpc","infra/modules/network"]`, 1),
		"module area twice":          strings.Replace(unitBundle, `"infra/modules/network","infra/modules/vpc"]`, `"infra/modules/network","infra/modules/network"]`, 1),
		"downgrade area absolute":    strings.Replace(unitBundle, `"in_repo_downgrade":["infra/modules/network"]`, `"in_repo_downgrade":["/infra"]`, 1),
		"downgrade area backslashed": strings.Replace(unitBundle, `"in_repo_downgrade":["infra/modules/network"]`, `"in_repo_downgrade":["infra\\modules"]`, 1),
		"downgrade area null":        strings.Replace(unitBundle, `"in_repo_downgrade":["infra/modules/network"]`, `"in_repo_downgrade":null`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseBundle(regionForm(t, []byte(text))); err == nil {
				t.Error("no error")
			}
		})
	}
}

// TestAllowedModuleArea checks a lead finding in a module area is allowed by
// that area's not_evaluated entry and by no other area's, both ways, and the
// two-segment directory of a path rule keeps its own area.
func TestAllowedModuleArea(t *testing.T) {
	in := Inputs{Registry: testRegistry(t), Bundle: testBundle(t, unitBundle)}
	for _, c := range []struct {
		rule, file string
		want       bool
	}{
		{"compat.default-changed", "infra/modules/network/variables.tf", true},
		{"compat.default-changed", "infra/modules/network/deeper/x.tf", true},
		{"compat.default-changed", "infra/modules/vpc/variables.tf", true},
		{"compat.default-changed", "variables.tf", false},
		{"compat.default-changed", "infra/other/x.tf", false},
		{"structure.output-no-try", "infra/modules/network/outputs.tf", false},
		{"structure.output-no-try", "outputs.tf", true},
		{"structure.output-no-try", "infra/other/outputs.tf", true},
		{"scope.cross", "infra/modules/vpc/main.tf", true},
		{"examples.versions-missing", "examples/complete", true},
		{"examples.versions-missing", "examples/complete/main.tf", true},
		{"compat.default-changed", "../escape.tf", false},
	} {
		if _, ok := in.allowed(Finding{RuleID: c.rule, File: c.file}); ok != c.want {
			t.Errorf("%s in %s: allowed %v, want %v", c.rule, c.file, ok, c.want)
		}
	}
	// The legacy reading of the same bundle owns a module area's files by
	// the root.
	lin := Inputs{Registry: testRegistry(t), Bundle: testBundle(t, legacy(unitBundle))}
	if _, ok := lin.allowed(Finding{RuleID: "structure.output-no-try", File: "infra/modules/network/outputs.tf"}); !ok {
		t.Error("legacy bundle: root entry does not allow a module area file")
	}
}

// TestDowngradeSeverity checks exactly MEDIUM is kept for the downgrade
// rules in a listed area, and clamping is unchanged elsewhere, in a legacy
// bundle and in a shed one, with the off-list count.
func TestDowngradeSeverity(t *testing.T) {
	reply := `{"candidate_decisions":[
 {"candidate_id":"C-001","decision":"confirm","reason":"r","severity":"MEDIUM","summary":"network","suggested_fix":"f"},
 {"candidate_id":"C-002","decision":"confirm","reason":"r","severity":"MEDIUM","summary":"vpc","suggested_fix":"f"},
 {"candidate_id":"C-003","decision":"confirm","reason":"r","severity":"MEDIUM","summary":"root","suggested_fix":"f"}],
 "findings":[
 {"id":"F-001","rule_id":"compat.default-changed","severity":"MEDIUM","file":"infra/modules/network/variables.tf","line":9,"summary":"lead network","suggested_fix":"f"},
 {"id":"F-002","rule_id":"compat.default-changed","severity":"LOW","file":"infra/modules/network/variables.tf","line":12,"summary":"lead low","suggested_fix":"f"}]}`
	for name, c := range map[string]struct {
		text    string
		want    []string
		offList int
	}{
		"versioned": {unitBundle, []string{"HIGH root", "HIGH vpc", "MEDIUM lead network", "MEDIUM network", "MEDIUM lead low"}, 3},
		"legacy":    {legacy(unitBundle), []string{"HIGH network", "HIGH root", "HIGH vpc"}, 3},
		"shed":      {shed(unitBundle), []string{"HIGH network", "HIGH root", "HIGH vpc"}, 3},
	} {
		t.Run(name, func(t *testing.T) {
			got, stats, err := FinalStats(Inputs{Registry: testRegistry(t), Bundle: testBundle(t, c.text)}, []byte(reply), nil)
			if err != nil {
				t.Fatal(err)
			}
			var lines []string
			for _, f := range got {
				lines = append(lines, f.Severity+" "+f.Summary)
			}
			slices.Sort(lines)
			want := slices.Sorted(slices.Values(c.want))
			if !slices.Equal(lines, want) {
				t.Errorf("got %q, want %q", lines, want)
			}
			if stats.OffList != c.offList {
				t.Errorf("off-list count %d, want %d", stats.OffList, c.offList)
			}
		})
	}
	_ = registry.SeverityMedium
}

// TestOffListCountedOncePerID checks an empty severity is not counted and a
// finding id is counted once however often its severity is clamped.
func TestOffListCountedOncePerID(t *testing.T) {
	in := Inputs{Registry: testRegistry(t), Bundle: testBundle(t, unitBundle), stats: &Stats{}}
	high := []string{"CRITICAL", "HIGH"}
	in.severity("F-001", "compat.variable-removed", "variables.tf", "", high)
	in.severity("F-002", "compat.variable-removed", "variables.tf", "HIGH", high)
	in.severity("F-003", "compat.variable-removed", "variables.tf", "LOW", high)
	in.severity("F-003", "compat.variable-removed", "variables.tf", "LOW", high)
	in.severity("C-001", "compat.variable-removed", "infra/modules/network/variables.tf", "MEDIUM", high)
	if in.stats.OffList != 1 {
		t.Errorf("off-list count %d, want 1", in.stats.OffList)
	}
}
