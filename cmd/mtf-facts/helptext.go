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
change still decides which lines are judged. With no change, a rule that
compares the change with its base is not_applicable with no_change_given.

A cross-area rule has one manifest entry under . for its pass, and one under
each other area that holds one of its findings or candidates. With a change,
the report lists each module whose versions or inputs it touches, with every
area that calls it and the version floors each declares, and says which
areas it checked, reached only as callers, or left out.

It prints the findings grouped by rule, one line that counts the candidates by
rule and one that counts the rules not evaluated by reason (--candidates and
--not-evaluated list each), a line saying how to pass a profile or a schema
when none was given, and a summary, or with --json the full report: the
envelope, the manifest, the findings and the candidates. --ai with --json
keeps what a reviewer decides on: the manifest keeps its finding, candidate
and not_evaluated entries and the not_applicable entries with no_change_given,
and manifest_summary counts the rest, each tree keeps its hash and a file
count in place of its file list, and the index, the interface diff and the
schema coverage are left out, on one line. bundle needs the full report.
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
	lintCandidatesUsage    = "list each candidate in the text report, not only the count per rule"
	lintNotEvaluatedUsage  = "list each rule and area not evaluated in the text report, not only the count per reason"
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
  2  an error: bad input, a limit or the timeout`
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
uses. The three file flags are required. It writes the report as JSON and records
each schema file by base name and SHA-256 only.

Exit codes:
  0  the report, with changes or none
  2  an error: a provider the tree uses that either file lacks, bad input,
     a limit or the timeout`
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
curated --resource type (aws_rds_cluster or aws_db_instance) and a
provider "aws" block that must
set region, fake credentials, every skip_* flag to true and endpoints at
http://127.0.0.1:1 or http://[::1]:1, the service under test included.
L2 runs module logic under terraform test: exactly one .tftest.hcl file, no
provider block anywhere, resources and data sources of any pinned provider
that has a mock_provider (terraform_remote_state refused), and moved blocks.
In the test file: mock_provider with literal mock defaults, override_resource
and override_data with literal values (each may set override_during = plan),
variables of literals, and run blocks that set command = plan, with assert,
plan_options mode = normal only and expect_failures into the tree; no
module, providers or mock source.
Every data source needs a mock_data, or an override_data outside run blocks,
whose literal values cover every computed attribute in the --schema file
(terraform providers schema -json); with override_during = plan, so does
every resource. Without --schema such a tree fails (test.schema-required).
A mock still starts the pinned provider plugin
(schema and validation calls only), so run L2 under the provider sandbox.
"override_during_plan": true marks a test that makes computed values known
at plan: an L2 run never evidences an unknown value or length.
Resource arguments that name a local file or source are refused.
Every function call must be on the pure-function allowlist and every reference
start with var, local, each, count, self or an allowed resource type; a heredoc
must be a plain string. Nothing is evaluated, so the host bounds what plan may
allocate.

--expect takes the manifest the trusted template wrote:
{"files":{"main.tf":{"blocks":[{"type","labels"}],"single_expressions":
[{"block","labels","attribute"}]}}}. Each file's top-level blocks must then be
exactly that set (type and labels, any order) and no other .tf file may exist;
the manifest binds .tf files only.
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
  2  an error: bad flags, a bad pins file, a directory that cannot be read,
     the output cap or the timeout`
	fixtureCheckExample = `  mtf-facts fixture-check scratch/head --json
  mtf-facts fixture-check scratch/l2 --level L2 --pins pins.json \
    --schema schema.json --json
  mtf-facts fixture-check scratch/l3 --level L3 --resource aws_rds_cluster \
    --pins pins.json --json`
	fixtureCheckLevelUsage    = "grammar `level` of the tree"
	fixtureCheckResourceUsage = "resource `type` under test, at L3 (aws_rds_cluster or aws_db_instance)"
	fixtureCheckPinsUsage     = "pins `file`, {\"providers\":[{\"source\",\"version\"}]}, of the provider versions the host installed"
	fixtureCheckJSONUsage     = "write the result as JSON"
	fixtureCheckExpectUsage   = "manifest `file` of the block set and single expressions the template wrote"
	fixtureCheckSchemaUsage   = "provider schemas `file`, from terraform providers schema -json, at L2"
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
  2  an error: bad input, a pin mismatch, a refused report or the timeout`
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
  2  an error: bad input, a limit or the timeout`
	mergeCheckExample = `  mtf-facts merge check bundle.txt reply.json`

	mergeFinalShort = "Merge a reply with the bundle into the final findings"
	mergeFinalLong  = `Print the merged findings and exit 0. It merges the first reply when that one
is valid, and otherwise the repair reply, which must then be given: an empty
file when the repair turn returned nothing. --registry is required.

Exit codes:
  0  the merged findings
  2  an error: bad input, a limit or the timeout`
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
  2  an error: bad input, a limit or the timeout`
	mergeCountsExample = `  mtf-facts merge counts bundle.txt reply.json repair.json`
)

const (
	rulesShort       = "List the implemented rules and their tier"
	rulesLong        = "List the implemented rules, one per line with its tier (tool or candidate),\nsorted by id."
	versionShort     = "Print the binary version"
	versionLong      = "Print the binary version. With --json, print one line of JSON instead: the\nversion, the commit, the schema_version of each report and the feature names,\nso a script can check what this binary supports (schema/version.schema.json)."
	versionJSONUsage = "print the version, the commit, the report schema versions and the features as JSON"
	helpShort        = "Help about any command"
	helpLong         = "Print the help of the root command or of the named command."
	completionShort  = "Generate a shell completion script"
)

// Messages of usage errors a handler finds.
const (
	usageHint           = "Run '%s --help' for usage.\n"
	timeoutNotPositive  = "--timeout must be greater than zero"
	schemaDiffRequired  = "--from, --to and --types-from are required"
	registryRequired    = "--registry is required"
	changesBaseRequired = "exactly one of --base and --diff is required"
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
that is a path, except the IAM path "/" of an aws_iam_* resource or data
block that no expression reads whole or by path or path_prefix. A shell
command, a dynamic path, a template that does not parse and a path that
leaves the tree reach every file.

When the examples cannot be ranked, fallback says why and every example has
reason all: unparsed when an HCL file cannot be parsed, dynamic-source when
a module call has no literal source, no-change-input when neither --base
nor --diff is given.

With --module-root, <dir> and --base are repository roots, the change is
repository-relative and the module under review is the directory the flag
names. Every path in the report is then repository-relative and
module_root names it. The examples are those of the module root,
<module-root>/examples/<name>, and the repository-root examples whose
local module calls reach the module root or a directory below it,
transitively. A repository-root example whose calls pass a directory that
does not parse or a call with no literal source is listed with reason all
and unresolved; so is every repository-root example when they hold more
than 1000 .tf files or 8 MiB. An unresolved example makes the change not
inert. Only the changed paths in the module root's scope count: under the
module root or a listed example, in a directory they reach on the way to
the module root, or read by a read site there. A fallback lists the module
root's examples and every repository-root example.

Exit codes:
  0  the list was written
  2  an error: bad input, a limit, an example name outside [A-Za-z0-9._-],
     a module root that is not a directory of the tree under a clean
     relative path, or the timeout; stdout is empty`
	affectedExample = `  mtf-facts affected path/to/module --diff files.json --json
  mtf-facts affected path/to/module --base path/to/base-tree
  mtf-facts affected path/to/repo --module-root modules/foo --diff files.json --json`
	affectedBaseUsage       = "the `tree` the change started from"
	affectedDiffUsage       = "the change as a `file`, a GitHub pull request files list or a unified diff, to rebuild the base tree from; not with --base"
	affectedJSONUsage       = "write the list as JSON (schema/affected.schema.json)"
	affectedModuleRootUsage = "the repository-relative `dir` of the module under review; <dir> is then the repository root"
)

const (
	fixtureBuildShort = "Build the fixture pair a ladder level runs for one trigger"
	fixtureBuildLong  = `Build the scratch trees a verification ladder level runs for one trigger of a
behaviour report that carries the classifier's cases, from trusted templates.

<trigger.json> is one trigger object: base_expr and head_expr, the
referenced_inputs with their type constraints, contextual_ref_details, category,
address and cases. --template is the L1 and L2 template and, at L3,
--resource-template the resource template; each holds sections that start at a
line "#-- <name>", with __NAME__ placeholders filled in one pass.

Under --out, an existing empty directory, it writes for each side with an
expression <side>/main.tf (a variable, a terraform_data stand-in whose output is
a typed unknown and a canary per input, and a local per input whose state a
case var file picks), <side>/subject.tf (the expression with each input renamed
to its local), at L2 <side>/ladder.tftest.hcl, and expect/<side>.json, the
manifest fixture-check --expect reads; and vars/<case id>.tfvars.json per case.

A type constraint is string, number, bool, any, list, set or map of one type,
tuple, or object with optional attributes, at most 400 tokens and 20 levels
deep. A trigger the builder cannot model, such as a type outside that grammar,
a local or module reference, over 32 cases, an expression over 16 KiB, two
case ids that differ only in letter case, or a count, each or default context
at L2 or L3, is refused with a fixed reason and no file is written.

It prints one JSON object, sorted keys: {"schema_version":1,"ok":true,"wrap",
"sides","canaries","cases":[{"id","inputs","unknown_inputs","addr"}]} or
{"schema_version":1,"ok":false,"reason"}.

Exit codes:
  0  the fixture is written
  1  the trigger is refused, with its reason
  2  an error: bad flags, an input over its size, a template without a
     section or value, an --out that is not an empty directory, or a timeout`
	fixtureBuildExample = `  mtf-facts fixture-build trigger.json --template l1.tf.tmpl --out scratch
  mtf-facts fixture-build trigger.json --template l1.tf.tmpl --out scratch \
    --level L2 --attribute for_each
  mtf-facts fixture-build trigger.json --template l1.tf.tmpl --out scratch \
    --level L3 --attribute vpc_security_group_ids --resource aws_rds_cluster \
    --resource-template template.tf.tmpl`
	fixtureBuildTemplateUsage         = "L1 and L2 template `file`, required"
	fixtureBuildOutUsage              = "existing empty `dir` to write the fixture to, required"
	fixtureBuildLevelUsage            = "ladder `level`: L1, L2 or L3"
	fixtureBuildAttributeUsage        = "`attribute` the expression sets: count or for_each at L2, the attribute under test at L3"
	fixtureBuildResourceUsage         = "resource `type` under test, at L3"
	fixtureBuildResourceTemplateUsage = "resource template `file`, at L3"
)
