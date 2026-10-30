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
the candidates. --ai with --json keeps what a reviewer decides on: the
manifest keeps its finding, candidate and not_evaluated entries and
manifest_summary counts the rest, each tree keeps its hash and a file count
in place of its file list, and the index, the interface diff and the schema
coverage are left out, on one line. bundle needs the full report.
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
  mtf-facts lint path/to/module --diff files.json --json
  mtf-facts lint path/to/module --diff files.json --json --ai`
	lintBaseUsage          = "the `tree` the change started from; only the areas the change touched are checked"
	lintDiffUsage          = "the change as a `file`, a GitHub pull request files list or a unified diff, to rebuild the base tree from; not with --base"
	lintFullTreeUsage      = "check every area, not only those the change touched"
	lintJSONUsage          = "write the full report as JSON"
	lintAIUsage            = "with --json, write the AI report a reviewer reads; bundle needs the full one"
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
	fixtureCheckShort = "Check a ladder scratch tree against its allowlist"
	fixtureCheckLong  = `Check the scratch tree a verification ladder level will run against a
default-deny allowlist, before any Terraform runs on it, and hash the tree.

Only .tf files directly in <dir> pass: no subdirectory, link, hard link,
special, hidden, override or JSON file, at most 64 entries, 256 KiB a file and
1 MiB in all.
Top-level blocks: terraform (required_providers entries equal to a pin of
--pins, nothing else), variable, locals, output, provider (the arguments its
allowlist names, literal values only) and resource of type terraform_data.
L1 uses the built-in provider only. L3 adds exactly one resource of the
curated --resource type (aws_rds_cluster) and a provider "aws" block that must
set region, fake credentials, every skip_* flag to true and endpoints at
http://127.0.0.1:1 or http://[::1]:1, the service under test included.
Resource arguments that name a local file or source are refused.
Every function call must be on the pure-function allowlist and every reference
start with var, local, each, count, self or an allowed resource type; a heredoc
must be a plain string. Nothing is evaluated, so the host bounds what plan may
allocate.

--expect takes the manifest the trusted template wrote:
{"files":{"main.tf":{"blocks":[{"type","labels"}],"single_expressions":
[{"block","labels","attribute"}]}}}. Each file's top-level blocks must then be
exactly that set (type and labels, any order) and no other .tf file may exist.
A file has at most one single expression: its block must be the last of the
file, the attribute the last item of its block, with no comment from its
start on, so a splice cannot add a block or argument or swallow template text.

tree_sha256 is the SHA-256 of one "name NUL type NUL content-sha256 LF" line
per directory entry, sorted by name, the type file, file+x, dir, symlink or
special; it identifies a tree that passes.

Exit codes:
  0  the tree passes
  1  violations, each with its file, line (0 for the whole file, file "."
     for the whole tree) and rule
  2  an error: bad flags, a bad pins file or a directory that cannot be read`
	fixtureCheckExample = `  mtf-facts fixture-check scratch/head --json
  mtf-facts fixture-check scratch/l3 --level L3 --resource aws_rds_cluster \
    --pins pins.json --json`
	fixtureCheckLevelUsage    = "grammar `level` of the tree"
	fixtureCheckResourceUsage = "resource `type` under test, at L3"
	fixtureCheckPinsUsage     = "pins `file`, {\"providers\":[{\"source\",\"version\"}]}, of the provider versions the host installed"
	fixtureCheckJSONUsage     = "write the result as JSON"
	fixtureCheckExpectUsage   = "manifest `file` of the block set and single expressions the template wrote"
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

const (
	affectedShort = "List the examples a change can affect, most likely first"
	affectedLong  = `List the examples a change can affect, for a host that plans them. Give
the tree the change started from with --base, or the change itself with
--diff, a GitHub pull request files list or a unified diff that the base
tree is rebuilt from.

Every example is listed: a first-level directory under examples/ that holds
a .tf file. First those the change touches (touched), then those that call a
module it touches through local module sources, transitively, the root
module included (caller), then every other example (all), each group in
path order. Registry and git sources are not callers of the local tree;
a local source written with backslashes is read as one with slashes. A
changed .tf, .tf.json, .tfvars, .tftest.hcl or lock file is a change of its
directory; any other changed file is a change of every module whose read
sites reach it.

inert is true only when every changed path is a documentation or metadata
path, such as a Markdown file or one under .github/, that no HCL read site
reaches, and whose exec bit did not change. Read sites are file function
arguments, file calls inside the templates templatefile reads, local_file,
archive_file and external attributes, provisioner scripts, and any string
that is a path. A shell command, a dynamic path, a template that does not
parse and a path that leaves the tree reach every file.

When the examples cannot be ranked, fallback says why and every example has
reason all: unparsed when an HCL file cannot be parsed, dynamic-source when
a module call has no literal source, no-change-input when neither --base
nor --diff is given.

Exit codes:
  0  the list was written
  2  an error: bad input, a limit, an example name outside [A-Za-z0-9._-]
     or the timeout; stdout is empty`
	affectedExample = `  mtf-facts affected path/to/module --diff files.json --json
  mtf-facts affected path/to/module --base path/to/base-tree`
	affectedBaseUsage = "the `tree` the change started from"
	affectedDiffUsage = "the change as a `file`, a GitHub pull request files list or a unified diff, to rebuild the base tree from; not with --base"
	affectedJSONUsage = "write the list as JSON (schema/affected.schema.json)"
)
