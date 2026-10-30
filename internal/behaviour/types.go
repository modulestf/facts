// Package behaviour classifies a change to a Terraform module tree into
// triggers, one per AST-level change, and selects the verification levels
// each needs. It also validates the claims a reviewing model writes and
// evaluates them, with the triggers, against a ladder record. It reads files
// only and never runs Terraform.
package behaviour

// SchemaVersion is the schema_version of every document this package reads
// or writes.
const SchemaVersion = 1

// MaxTriggers caps the triggers of one report; the rest are counted in
// Report.Capped and stay unverifiable.
const MaxTriggers = 40

// Change kinds of an entry or a trigger.
const (
	KindAdded    = "added"
	KindRemoved  = "removed"
	KindModified = "modified"
	KindRenamed  = "renamed"
	KindMode     = "mode"
)

// Trigger categories, in the order of the design's list.
const (
	CatWrapper            = "wrapper-change"
	CatDefault            = "default-change"
	CatCountForEach       = "count-for_each-conditional-change"
	CatTypeValidation     = "type-validation-change"
	CatDynamic            = "dynamic-block-change"
	CatLocalsOutputs      = "locals-outputs-change"
	CatMoved              = "moved-or-renamed-address"
	CatVersionFloor       = "provider-version-floor-change"
	CatUnknownPropagation = "unknown-propagation-candidate"
	CatLiteral            = "literal-argument-change"
	CatResourceAddRemove  = "resource-added-or-removed"
	CatLifecycle          = "lifecycle-change"
	CatUnclassified       = "unclassified-change"
	CatNonHCL             = "non-hcl-dependency"
)

// Verification levels a trigger can select.
const (
	LevelL1     = "L1"
	LevelL2     = "L2" // terraform test with mock providers: computed values are known at plan
	LevelL3     = "L3"
	LevelL4     = "L4"
	LevelCompat = "compat"
)

// levelsOf is the selection of design D. A category with no level is
// unverifiable by design and its trigger carries a reason.
var levelsOf = map[string][]string{
	CatWrapper:            {LevelL1},
	CatDefault:            {LevelL1},
	CatCountForEach:       {LevelL1},
	CatTypeValidation:     {LevelL1},
	CatLocalsOutputs:      {LevelL1},
	CatUnknownPropagation: {LevelL1, LevelL3},
	CatDynamic:            {LevelL4},
	CatLiteral:            {LevelL4},
	CatResourceAddRemove:  {LevelL4},
	CatLifecycle:          {LevelL4},
	CatUnclassified:       {LevelL4},
	CatNonHCL:             {LevelL4},
	CatVersionFloor:       {LevelCompat},
	CatMoved:              {},
}

// catRank orders triggers at one location: the design's list order, with
// the unknown propagation candidate, which rides on another change, last.
var catRank = map[string]int{
	CatWrapper: 0, CatDefault: 1, CatCountForEach: 2, CatTypeValidation: 3, CatDynamic: 4,
	CatLocalsOutputs: 5, CatMoved: 6, CatVersionFloor: 7, CatLiteral: 8, CatResourceAddRemove: 9,
	CatLifecycle: 10, CatUnclassified: 11, CatNonHCL: 12, CatUnknownPropagation: 13,
}

// Report is the output of mtf-facts behaviour.
type Report struct {
	SchemaVersion int `json:"schema_version"`
	// Inert is set when every changed entry is inert.
	Inert    bool      `json:"inert"`
	Entries  []Entry   `json:"entries"`
	Triggers []Trigger `json:"triggers"`
	// Capped counts the triggers past MaxTriggers, left out of Triggers.
	Capped int `json:"capped"`
}

// Entry is one changed tree entry.
type Entry struct {
	Path string `json:"path"`
	// PreviousPath is the base path of a renamed entry.
	PreviousPath *string  `json:"previous_path,omitempty"`
	ChangeKind   string   `json:"change_kind"`
	Inert        bool     `json:"inert"`
	TriggerIDs   []string `json:"trigger_ids"`
}

// Trigger is one AST-level change with one category.
type Trigger struct {
	ID   string `json:"id"`
	File string `json:"file"`
	// Line is the head line, or the base line of something the change
	// removes; null for a change to a file as a whole.
	Line       *int    `json:"line"`
	Category   string  `json:"category"`
	ChangeKind string  `json:"change_kind"`
	Address    *string `json:"address"`
	Attribute  *string `json:"attribute"`
	BaseExpr   *string `json:"base_expr"`
	HeadExpr   *string `json:"head_expr"`
	// ReferencedInputs are the variables the expressions read, with their
	// declared types.
	ReferencedInputs []Input `json:"referenced_inputs"`
	// ContextualRefs are the other references the expressions read, which
	// an evaluation must stand in for.
	ContextualRefs []string `json:"contextual_refs"`
	// ContextualRefDetails describe each contextual reference as written,
	// for the stand-ins of an L1 evaluation. An optional field.
	ContextualRefDetails []RefDetail `json:"contextual_ref_details"`
	// Pure is set when every function the expressions call is on the pure
	// function allowlist and neither reads self.
	Pure bool `json:"pure"`
	// Cases are the required cases of the L1 sweep, for a trigger that
	// selects L1. An optional field.
	Cases  []Case   `json:"cases,omitempty"`
	Levels []string `json:"levels"`
	Reason *string  `json:"reason"`
	// ForEachOnly marks a dynamic-block-change whose for_each alone changed,
	// routed to L1 on that for_each. An optional field.
	ForEachOnly bool `json:"for_each_only,omitempty"`
}

// Input is one variable a trigger reads.
type Input struct {
	Name string `json:"name"`
	Type string `json:"type"`
}
