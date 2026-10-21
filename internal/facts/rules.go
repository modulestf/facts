package facts

import "fmt"

// Rules are the implemented rules, in the order the manifest lists them.
// Each one follows the Fails when and Severity text of its reviewer check file.
// Kinds are the registry's area_kinds; any other area is not_applicable.
var Rules = []Rule{
	{ID: "examples.versions-missing", Tier: "tool", Class: ClassPath, Kinds: []string{AreaExample}, Check: checkVersionsMissing},
	{ID: "structure.missing-description", Tier: "tool", Class: ClassConfig, Kinds: []string{AreaRoot, AreaSubmodule}, Check: checkMissingDescription},
	{ID: "structure.variable-untyped", Tier: "candidate", Class: ClassConfig, Kinds: []string{AreaRoot, AreaSubmodule}, Check: checkVariableUntyped},
}

// Caps on pull request text copied into candidate evidence. A cap that bites
// sets evidence_complete to false.
const (
	maxEvidenceText     = 1024
	maxEvidenceComments = 8
)

const moduleStructureRef = "the maintainer skill's module-structure reference"

// newVariable reports whether a head variable is new: it has no variable of
// the same name at base. Without a base tree every block counts as new.
func newVariable(a *Area, name string) bool { return a.Base == nil || !a.Base.varNames[name] }

func newOutput(a *Area, name string) bool { return a.Base == nil || !a.Base.outNames[name] }

// subject is a candidate's dedup key, scoped to its area: "<area>:<address>",
// such as ".:var.name".
func subject(area, address string) string { return area + ":" + address }

// short caps a block name before it goes into a finding's text.
func short(name string) string {
	if len(name) > maxEvidenceText {
		return name[:maxEvidenceText]
	}
	return name
}

func finding(rule, area, file string, line int, summary, fix string) Finding {
	l := line
	return Finding{RuleID: rule, Severity: "MEDIUM", File: areaFile(area, file), Line: &l, Summary: summary, SuggestedFix: fix}
}

// VariableEvidence is the evidence of a structure.variable-untyped candidate.
type VariableEvidence struct {
	Variable string   `json:"variable"`
	Type     *string  `json:"type"`     // source text of the type, null when absent
	Comments []string `json:"comments"` // comments inside the block or on the line above it
}

// structure.variable-untyped is a candidate rule: whether a comment gives a
// reason for `any` is prose, which the engine does not judge. A new variable
// with no type, or whose type uses `any`, is a candidate with its evidence.
func checkVariableUntyped(a *Area) Result {
	var res Result
	for _, v := range a.Head.Variables {
		if !newVariable(a, v.Name) || (v.HasType && !v.TypeUsesAny) {
			continue
		}
		complete := true
		capText := func(s string) string {
			if len(s) > maxEvidenceText {
				complete = false
				return s[:maxEvidenceText]
			}
			return s
		}
		name := capText(v.Name)
		ev := VariableEvidence{Variable: name, Comments: []string{}}
		if v.HasType {
			t := capText(v.TypeText)
			ev.Type = &t
		}
		for i, c := range v.Comments {
			if i == maxEvidenceComments {
				complete = false
				break
			}
			ev.Comments = append(ev.Comments, capText(c))
		}
		line := v.Line
		res.Candidates = append(res.Candidates, Candidate{
			RuleID: "structure.variable-untyped", File: areaFile(a.Path, v.File), Line: &line,
			Subject: subject(a.Path, "var."+name), Evidence: ev, EvidenceComplete: complete,
		})
	}
	return res
}

// structure.missing-description: a new variable or output with no description.
func checkMissingDescription(a *Area) Result {
	const id = "structure.missing-description"
	var out []Finding
	for _, v := range a.Head.Variables {
		if newVariable(a, v.Name) && !v.HasDescription {
			out = append(out, finding(id, a.Path, v.File, v.Line,
				fmt.Sprintf("Variable %q has no description, so its documentation row says nothing about what it controls.", short(v.Name)),
				fmt.Sprintf("Add a `description` to `variable %q` in `%s` stating what the input controls, per Variable Descriptions in %s.",
					short(v.Name), areaFile(a.Path, v.File), moduleStructureRef)))
		}
	}
	for _, o := range a.Head.Outputs {
		if newOutput(a, o.Name) && !o.HasDescription {
			out = append(out, finding(id, a.Path, o.File, o.Line,
				fmt.Sprintf("Output %q has no description, so its documentation row says nothing about what it returns.", short(o.Name)),
				fmt.Sprintf("Add a `description` to `output %q` in `%s` stating what the value is.", short(o.Name), areaFile(a.Path, o.File))))
		}
	}
	return Result{Findings: out}
}

// examples.versions-missing: an example with no versions.tf. A version file
// under another name is the same finding, since versions.tf is the name the
// profile and its sibling examples use. A path rule reads only whether its
// path exists, so a symbolic link at versions.tf counts as present and is
// never followed.
func checkVersionsMissing(a *Area) Result {
	if !a.HeadExists {
		return Result{NotEvaluated: StatusNotApplicable}
	}
	e := a.headEntry("versions.tf")
	switch {
	case e == nil:
		return Result{Findings: []Finding{{
			RuleID: "examples.versions-missing", Severity: "MEDIUM", File: a.Path,
			Summary:      fmt.Sprintf("Example %q has no versions.tf, so its Terraform and provider constraints are not in the file the profile and its sibling examples use.", a.Path),
			SuggestedFix: fmt.Sprintf("Add `versions.tf` to `%s`, copied from a sibling example, or rename the file that holds its `terraform` block to `versions.tf`.", a.Path),
		}}}
	case e.Kind != KindFile && e.Kind != KindSymlink:
		return Result{NotEvaluated: "versions.tf is " + kindText(e.Kind), Code: kindCode(e.Kind)}
	}
	return Result{}
}
