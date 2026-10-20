package facts

import "fmt"

// Rules are the implemented rules, in the order the manifest lists them.
// Each one follows the Fails when and Severity text of its reviewer check file.
var Rules = []Rule{
	{ID: "examples.versions-missing", Tier: "tool", Class: ClassPath, Kinds: []string{AreaExample}, Check: checkVersionsMissing},
	{ID: "structure.missing-description", Tier: "tool", Class: ClassConfig, Kinds: []string{AreaRoot, AreaSubmodule, AreaExample}, Check: checkMissingDescription},
	{ID: "structure.variable-untyped", Tier: "candidate", Class: ClassConfig, Kinds: []string{AreaRoot, AreaSubmodule, AreaExample}, Check: checkVariableUntyped},
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
func newVariable(a *Area, name string) bool {
	if a.Base == nil {
		return true
	}
	for _, v := range a.Base.Variables {
		if v.Name == name {
			return false
		}
	}
	return true
}

func newOutput(a *Area, name string) bool {
	if a.Base == nil {
		return true
	}
	for _, o := range a.Base.Outputs {
		if o.Name == name {
			return false
		}
	}
	return true
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
		ev := VariableEvidence{Variable: v.Name, Comments: []string{}}
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
			Subject: fmt.Sprintf("variable %q", v.Name), Evidence: ev, EvidenceComplete: complete,
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
				fmt.Sprintf("Variable %q has no description, so its documentation row says nothing about what it controls.", v.Name),
				fmt.Sprintf("Add a `description` to `variable %q` in `%s` stating what the input controls, per Variable Descriptions in %s.",
					v.Name, areaFile(a.Path, v.File), moduleStructureRef)))
		}
	}
	for _, o := range a.Head.Outputs {
		if newOutput(a, o.Name) && !o.HasDescription {
			out = append(out, finding(id, a.Path, o.File, o.Line,
				fmt.Sprintf("Output %q has no description, so its documentation row says nothing about what it returns.", o.Name),
				fmt.Sprintf("Add a `description` to `output %q` in `%s` stating what the value is.", o.Name, areaFile(a.Path, o.File))))
		}
	}
	return Result{Findings: out}
}

// examples.versions-missing: an example with no versions.tf. A version file
// under another name is the same finding, since versions.tf is the name the
// profile and its sibling examples use.
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
	case e.Kind != KindFile:
		return Result{NotEvaluated: "versions.tf is " + kindText(e.Kind)}
	}
	return Result{}
}
