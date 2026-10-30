package main

// User-facing help text: command descriptions, examples, flag usages and the
// usage template. A word in backquotes in a flag usage is the value's name in
// the help.

// Command groups of the root help.
const (
	groupCheckTitle    = "Check a module:"
	groupPipelineTitle = "Review pipeline (run by hosts):"
	groupOtherTitle    = "Other commands:"
)

// usageTemplate renders the usage of every command.
const usageTemplate = `Usage:
  {{.UseLine}}{{if .HasAvailableSubCommands}}{{$cmds := .Commands}}{{range $group := .Groups}}

{{.Title}}{{range $cmds}}{{if (and (eq .GroupID $group.ID) (or .IsAvailableCommand (eq .Name "help")))}}
  {{rpad .Name .NamePadding}}  {{.Short}}{{end}}{{end}}{{end}}{{if not .AllChildCommandsHaveGroup}}

Commands:{{range $cmds}}{{if (and (eq .GroupID "") (or .IsAvailableCommand (eq .Name "help")))}}
  {{rpad .Name .NamePadding}}  {{.Short}}{{end}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

Flags:
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasExample}}

Examples:
{{.Example}}{{end}}{{if .HasAvailableSubCommands}}

Run '{{.CommandPath}} <command> --help' for its flags and exit codes.{{end}}
`

const (
	rootShort = "Report deterministic facts about Terraform module trees"
	rootLong  = `mtf-facts reports deterministic facts about Terraform module trees.
It reads files only: no network, no Terraform, no other program.`
	rootExample = `  mtf-facts lint ./my-module
  mtf-facts lint ./my-module --base ../base-tree --json > report.json
  mtf-facts ledger ./my-module --schema-files schema.json --check ledger.json`
	rootVersionUsage = "print the binary version"
)

const (
	lintShort = "Check a module tree, or only the areas a change touched"
	lintLong  = `Check a module tree. With a change, check only the areas the change touched,
against the tree the change started from: give that tree with --base, or the
change itself with --diff, a GitHub pull request files list or a unified diff
that the base tree is rebuilt from. A file the diff gives no patch for, such
as a binary or very large one, has no known base content: the rules that read
it are not_evaluated with no_base. --full-tree checks every area, and the
change still decides which lines are judged.

A cross-area rule has one manifest entry under . for its pass, and one under
each other area that holds one of its findings or candidates. With a change,
the report lists each module whose versions or inputs it touches, with every
area that calls it and the version floors each declares, and says which
areas it checked, reached only as callers, or left out.

It prints the findings, the candidates, the rules not evaluated and a summary,
or with --json the full report: the envelope, the manifest, the findings and
the candidates.
Candidates are unconfirmed and never change the exit code. The envelope
records the hash of --registry and the commits of --head-sha and
--merge-base-sha, each null when its flag is absent. A missing, oversized or
malformed schema input exits 2.

Exit codes:
  0  no findings, and every rule was decided in every area
  1  at least one finding
  2  an error: bad input, a limit, the timeout or a failed integrity check;
     stdout is not a report, and a timeout or an error before output
     writes nothing to stdout
  3  no findings, but at least one rule was not_evaluated in some area`
	lintExample = `  mtf-facts lint path/to/module
  mtf-facts lint path/to/module --base path/to/base-tree --json
  mtf-facts lint path/to/module --diff files.json --json`
	lintBaseUsage          = "the `tree` the change started from; only the areas the change touched are checked"
	lintDiffUsage          = "the change as a `file`, a GitHub pull request files list or a unified diff, to rebuild the base tree from; not with --base"
	lintFullTreeUsage      = "check every area, not only those the change touched"
	lintJSONUsage          = "write the full report as JSON"
	lintTimeoutUsage       = "stop with an error after this long; must be greater than zero"
	lintRegistryUsage      = "rule registry `file` (rule-facts.md) whose hash the report records"
	lintProfileDirUsage    = "module profile `dir`; must exist and not be a link (links in parent components resolve)"
	lintSchemaFilesUsage   = "a terraform providers schema -json `file`; repeat for more; two with one base name exit 2"
	lintSchemaHandoffUsage = "schema handoff `file` from the schema job, gzipped or not"
	lintHeadSHAUsage       = "commit `sha` the head tree came from"
	lintMergeBaseSHAUsage  = "commit `sha` the base tree came from"
)

const (
	ledgerShort = "List the provider fields of each type, or check a coverage ledger"
	ledgerLong  = `List, as JSON, the provider fields of each resource and data source type the
module in dir declares, nested fields included. It reads only dir, not
modules/* or examples/*. At least one --schema-files is required.

With --check, also list each problem of the coverage ledger, such as a leaf
field with no disposition or more than one. Without --check, problems is
null; without --registry, registry_sha256 is null.

Exit codes:
  0  the inventory, and with --check a complete ledger
  1  with --check: at least one problem, listed in problems
  2  an error: bad input or a limit`
	ledgerExample = `  mtf-facts ledger path/to/module --schema-files schema.json
  mtf-facts ledger path/to/module --schema-files schema.json \
    --check coverage-ledger.json`
	ledgerSchemaFilesUsage = "a terraform providers schema -json `file`; repeat for more"
	ledgerCheckUsage       = "coverage ledger `file` to check against the inventory"
	ledgerRegistryUsage    = "rule registry `file` (rule-facts.md) whose hash the output records"
)

const (
	schemaDiffShort = "Compare two provider schemas for the types a module uses"
	schemaDiffLong  = `Compare two terraform providers schema -json files, the current and the
target provider version, for the resource and data source types a module tree
uses. All three flags are required. It writes the report as JSON and records
each schema file by base name and SHA-256 only.

Exit codes:
  0  the report, with changes or none
  2  an error: a provider the tree uses that either file lacks, bad input or
     a limit`
	schemaDiffExample = `  mtf-facts schema-diff --from current.json --to target.json \
    --types-from path/to/module`
	schemaDiffFromUsage      = "schema `file` of the current provider version"
	schemaDiffToUsage        = "schema `file` of the target provider version"
	schemaDiffTypesFromUsage = "module `tree` whose resource and data source types are compared"
)

const (
	bundleShort = "Build the facts bundle a model reviewer reads"
	bundleLong  = `Turn a lint --json report into the facts bundle a model reviewer reads, and
the injected rule text for the rules the report left not_evaluated.
--report, --skills, --out and --injection are required. Give
--schema-handoff, --schema-files and --profile-dir exactly as the lint run had
them. The report's envelope must equal its pins, --head-sha, --merge-base-sha
and --binary-sha256 among them; any mismatch exits 2.

It prints the mode to stdout and exits 0:
  judgment  it wrote --out and --injection, which must not exist yet
  full      the tool findings and candidates alone are over the 48 KiB cap, or
            the injected rule text over its 24 KiB budget; it writes no file and
            gives the reason on stderr

Exit codes:
  0  the mode on stdout
  2  an error: bad input, a pin mismatch or a refused report`
	bundleExample = `  mtf-facts bundle --report lint.json \
    --skills path/to/terraform-module-reviewer \
    --out bundle.txt --injection injection.md \
    --head-sha <sha> --merge-base-sha <sha> --binary-sha256 <sha256>`
	bundleReportUsage        = "lint --json report `file`"
	bundleSkillsUsage        = "reviewer skill `dir` at its pinned commit: references/rule-facts.md and references/engine/"
	bundleOutUsage           = "bundle `file` to create; must not exist"
	bundleInjectionUsage     = "injected rule text `file` to create; must not exist"
	bundleHeadSHAUsage       = "commit `sha` the head tree came from, 40 or 64 lowercase hex characters"
	bundleMergeBaseSHAUsage  = "commit `sha` the base tree came from, 40 or 64 lowercase hex characters"
	bundleBinarySHA256Usage  = "pinned SHA-256 (`hex`) of the mtf-facts binary that wrote the report"
	bundleSchemaHandoffUsage = "schema handoff `file` the lint run was given"
	bundleSchemaFilesUsage   = "raw schema `file` the lint run was given; repeat for more"
	bundleProfileDirUsage    = "module profile `dir` the lint run was given"
)

const (
	mergeShort = "Check a model reply and merge it into the final findings"
	mergeLong  = `Check a model reviewer's reply to the bundle mtf-facts bundle writes, and
merge the two into the final findings. The reply is untrusted: it is read with
exact keys, no duplicate key, no trailing data and a 1 MiB cap.`

	mergeCheckShort = "Check a reply; exit 1 when it needs the repair turn"
	mergeCheckLong  = `Print {"result":"valid"} and exit 0, or {"result":"repair","reasons":[...]}
and exit 1 when the reply needs the repair turn. The reasons are fixed text
and quote nothing from the reply.

Exit codes:
  0  the reply is valid
  1  the reply needs the repair turn
  2  an error: bad input or a limit`
	mergeCheckExample = `  mtf-facts merge check bundle.txt reply.json`

	mergeFinalShort = "Merge a reply with the bundle into the final findings"
	mergeFinalLong  = `Print the merged findings and exit 0. It merges the first reply when that one
is valid, and otherwise the repair reply, which must then be given: an empty
file when the repair turn returned nothing. --registry is required.

Exit codes:
  0  the merged findings
  2  an error: bad input or a limit`
	mergeFinalExample = `  mtf-facts merge final --registry rule-facts.md --report lint.json \
    bundle.txt reply.json repair.json`
	mergeFinalRegistryUsage  = "rule registry `file` (rule-facts.md)"
	mergeFinalReportUsage    = "lint --json report `file` whose index derives the subject a lead finding dedups by"
	mergeFinalTemplatesUsage = "per-rule host text `file` for confirmed entries the model gave no text for"

	mergeCountsShort = "Print the summary-line counts of the reply merge final reads"
	mergeCountsLong  = `Take the replies as merge final does, pick the same reply, and print its
counts as {"counts":{...}}, or {"counts":null} when that reply has none.
No count is read as 0 for its absence.

Exit codes:
  0  the counts
  2  an error: bad input or a limit`
	mergeCountsExample = `  mtf-facts merge counts bundle.txt reply.json repair.json`
)

const (
	rulesShort      = "List the implemented rules and their tier"
	rulesLong       = "List the implemented rules, one per line with its tier (tool or candidate),\nsorted by id."
	versionShort    = "Print the binary version"
	helpShort       = "Help about any command"
	helpLong        = "Print the help of the root command or of the named command."
	completionShort = "Generate a shell completion script"
)

// Messages of usage errors a handler finds.
const (
	usageHint          = "Run '%s --help' for usage.\n"
	timeoutNotPositive = "--timeout must be greater than zero"
	schemaDiffRequired = "--from, --to and --types-from are required"
	registryRequired   = "--registry is required"
)
