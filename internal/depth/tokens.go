// Package depth decides how deep a review of a change must go: light,
// focused or deep. It reads the head tree and the merge base tree, the pull
// request files list and the classifier and affected-examples reports, and
// never runs Terraform. Every rule fails closed: anything it cannot prove
// makes the change deep.
package depth

import "slices"

// Format is the format of the depth report.
const Format = 1

// Depths.
const (
	Light   = "light"
	Focused = "focused"
	Deep    = "deep"
)

// Reason tokens. Reasons lists them in their canonical order: a report lists
// its reasons in this order. README.md lists the same tokens; a test checks.
const (
	ReasonLightDocsOnly               = "light-docs-only"
	ReasonFocusedWithinLimits         = "focused-within-limits"
	ReasonTerraformBlock              = "deep-terraform-block"
	ReasonLockFile                    = "deep-lock-file"
	ReasonCLIConfig                   = "deep-cli-config"
	ReasonCIConfig                    = "deep-ci-config"
	ReasonProviderConfig              = "deep-provider-config"
	ReasonDependsOn                   = "deep-depends-on"
	ReasonNewType                     = "deep-new-type"
	ReasonBlockRemovedOrRenamed       = "deep-block-removed-or-renamed"
	ReasonVariableContract            = "deep-variable-contract"
	ReasonOutputSensitive             = "deep-output-sensitive"
	ReasonLifecycleMovedRemovedImport = "deep-lifecycle-moved-removed-import"
	ReasonCountForEach                = "deep-count-for-each"
	ReasonProvisioner                 = "deep-provisioner"
	ReasonPolicy                      = "deep-policy"
	ReasonModuleSourceVersion         = "deep-module-source-version"
	ReasonDynamicStructure            = "deep-dynamic-structure"
	ReasonWrappers                    = "deep-wrappers"
	ReasonLimits                      = "deep-limits"
	ReasonClassifierCategory          = "deep-classifier-category"
	ReasonClassifierUnavailable       = "deep-classifier-unavailable"
	ReasonAffectedFallback            = "deep-affected-fallback"
	ReasonParseFailure                = "deep-parse-failure"
	ReasonUnwalked                    = "deep-unwalked"
	ReasonUnresolvedConsumer          = "deep-unresolved-consumer"
	ReasonUnresolvedFileDependency    = "deep-unresolved-file-dependency"
	ReasonPathUnsafe                  = "deep-path-unsafe"
	ReasonTooManyExamples             = "deep-too-many-examples"
	ReasonTooManyFiles                = "deep-too-many-files"
	ReasonTestOrMock                  = "deep-test-or-mock"
	ReasonFilesListMismatch           = "deep-files-list-mismatch"
	ReasonNonTerraformChange          = "deep-non-terraform-change"
)

// Reasons are the tokens mtf-facts depth writes, in canonical order.
var Reasons = []string{
	ReasonLightDocsOnly,
	ReasonFocusedWithinLimits,
	ReasonTerraformBlock,
	ReasonLockFile,
	ReasonCLIConfig,
	ReasonCIConfig,
	ReasonProviderConfig,
	ReasonDependsOn,
	ReasonNewType,
	ReasonBlockRemovedOrRenamed,
	ReasonVariableContract,
	ReasonOutputSensitive,
	ReasonLifecycleMovedRemovedImport,
	ReasonCountForEach,
	ReasonProvisioner,
	ReasonPolicy,
	ReasonModuleSourceVersion,
	ReasonDynamicStructure,
	ReasonWrappers,
	ReasonLimits,
	ReasonClassifierCategory,
	ReasonClassifierUnavailable,
	ReasonAffectedFallback,
	ReasonParseFailure,
	ReasonUnwalked,
	ReasonUnresolvedConsumer,
	ReasonUnresolvedFileDependency,
	ReasonPathUnsafe,
	ReasonTooManyExamples,
	ReasonTooManyFiles,
	ReasonTestOrMock,
	ReasonFilesListMismatch,
	ReasonNonTerraformChange,
}

// HostReasons are tokens a host writes when it raises a report to deep;
// mtf-facts never writes them. A host may also write HostSchemaPrefix
// followed by a schema-pass reason matching ^[a-z0-9-]{1,64}$.
var HostReasons = []string{"deep-owner-raise", "deep-validate-failed"}

// HostSchemaPrefix starts the token of a schema-pass failure a host
// escalates to deep.
const HostSchemaPrefix = "deep-schema-"

// Checks a depth can leave out of scope. GLadder is Check G without the
// claim reading: the ladder.
const (
	CheckA       = "A"
	CheckB       = "B"
	CheckD       = "D"
	CheckGLadder = "G-ladder"
)

// reasonRank orders reasons canonically.
func reasonRank(r string) int { return slices.Index(Reasons, r) }

// Limits of a focused depth (D-4) and of a light one (D-1).
const (
	maxTFFiles         = 6
	maxChangedLines    = 200
	maxChangedBlocks   = 4
	maxSelectedExample = 12
	maxLightFiles      = 50
)
