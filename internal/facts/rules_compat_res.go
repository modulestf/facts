package facts

import (
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

// compatResRules is the compat_res rule family: the Check A rules about
// resource addresses, generated policy documents and version floors. Each
// is a candidate rule that needs the base. A change the detector cannot
// decide, such as a moved block with an expression key or a constraint it
// cannot parse, is a candidate for the confirmer, never clean.
var compatResRules = []Rule{
	{ID: "compat.generated-policy-narrowed", Tier: "candidate", Class: ClassConfig, Scope: ScopeTouchedBlock, Kinds: compatResKinds, NeedsBase: true, Check: checkPolicyNarrowed},
	{ID: "compat.provider-floor-raised", Tier: "candidate", Class: ClassConfig, Scope: ScopeTouchedExpression, Kinds: compatResKinds, NeedsBase: true, Check: checkProviderFloorRaised},
	{ID: "compat.resource-address-changed", Tier: "candidate", Class: ClassConfig, Scope: ScopeTouchedBlock, Kinds: compatResKinds, CrossArea: true, NeedsBase: true, Check: checkResourceAddressChanged},
	{ID: "compat.version-constraint-raised", Tier: "candidate", Class: ClassConfig, Scope: ScopeTouchedExpression, Kinds: compatResKinds, NeedsBase: true, Check: checkVersionConstraintRaised},
}

var compatResKinds = []string{AreaRoot, AreaSubmodule}

// Caps on one candidate's evidence. A cap that bites sets evidence_complete
// to false.
const (
	maxResSourceLines = 8
	maxResSchemaFacts = 64
)

// resEvidence builds one candidate's evidence under the caps.
type resEvidence struct {
	a        *Area
	ev       Evidence
	complete bool
}

func newResEvidence(a *Area) *resEvidence {
	return &resEvidence{a: a, ev: Evidence{SourceLines: []SourceLine{}, SchemaFacts: []SchemaFact{}}, complete: true}
}

func (e *resEvidence) text(s string) string {
	if len(s) > maxEvidenceText {
		e.complete = false
		return s[:maxEvidenceText]
	}
	return s
}

// line adds line n of an area file at a revision, "head" or "merge_base".
// A file that is not a regular file there adds nothing.
func (e *resEvidence) line(rev, file string, n int) {
	t := e.a.headTree
	if rev == "merge_base" {
		t = e.a.baseTree
	}
	full := areaFile(e.a.Path, file)
	if t == nil || n < 1 {
		return
	}
	en := t.Entries[full]
	if en == nil || en.Kind != KindFile {
		return
	}
	ls := splitLines(en.Data)
	if n > len(ls) {
		return
	}
	if len(e.ev.SourceLines) == maxResSourceLines {
		e.complete = false
		return
	}
	e.ev.SourceLines = append(e.ev.SourceLines, SourceLine{Revision: rev, File: full, Line: n, Text: e.text(strings.TrimSuffix(string(ls[n-1]), "\r"))})
}

func (e *resEvidence) values(base, head *string) {
	if base != nil {
		b := e.text(*base)
		e.ev.Base = &b
	}
	if head != nil {
		h := e.text(*head)
		e.ev.Head = &h
	}
}

// candidate is the candidate of a rule in the area. A nil line is a subject
// with no head line, judged by its file.
func (e *resEvidence) candidate(rule, file string, line int, address string) Candidate {
	c := Candidate{RuleID: rule, File: areaFile(e.a.Path, file), Subject: subject(e.a.Path, e.text(address)),
		Evidence: e.ev, EvidenceComplete: e.complete}
	if line > 0 {
		c.Line = &line
	}
	return c
}

// parseExprAt parses an expression's source text at its own position, so
// the ranges it returns are those of the file.
func parseExprAt(x Expr) (hclsyntax.Expression, bool) {
	e, diags := hclsyntax.ParseExpression([]byte(x.Text), x.Range.Filename, x.Range.Start)
	return e, !diags.HasErrors()
}

// literalValue is the value of an expression that needs no variable or
// function, or false when it has none or is not wholly known.
func literalValue(e hclsyntax.Expression) (cty.Value, bool) {
	v, diags := e.Value(nil)
	if diags.HasErrors() || !v.IsWhollyKnown() {
		return cty.NilVal, false
	}
	return v, true
}

func bodyAttr(b Body, name string) *Attribute {
	for i := range b.Attributes {
		if b.Attributes[i].Name == name {
			return &b.Attributes[i]
		}
	}
	return nil
}

// baseHasFiles reports an area the base has any path in: the module existed
// before the change. An empty base is a new module, not a missing one.
func baseHasFiles(a *Area) bool {
	if !a.BaseExists || a.baseTree == nil {
		return false
	}
	return slices.ContainsFunc(a.baseTree.Paths, func(p string) bool {
		return a.Path == rootArea || strings.HasPrefix(p, a.Path+"/")
	})
}
