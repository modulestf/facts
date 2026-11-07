# Terraform Modules Facts

`mtf-facts` is an offline Go CLI that parses Terraform module trees and reports deterministic facts: rule results, candidate findings with evidence, interface and schema diffs.

It serves two uses:

- Module authoring: `lint`, a provider coverage `ledger` check, and `schema-diff` for provider upgrades.
- Module review: a facts bundle that lets a model reviewer skip the rules a parser can decide, and `fixture-check`, the allowlist a verification ladder scratch tree passes before Terraform runs on it.

It reads files only. It makes no network call and runs no Terraform or other program.

## Install

With Homebrew, on macOS or Linux:

```sh
brew install modulestf/tap/mtf-facts
```

Or with Go:

```sh
go install github.com/modulestf/facts/cmd/mtf-facts@latest
```

Pin a release with `@v0.4.0` in place of `@latest`, or download a release binary and check it as in [Verify a release](#verify-a-release).

Or build from a checkout:

```sh
go build -trimpath -o bin/mtf-facts ./cmd/mtf-facts
```

## Quick start

Check a whole module tree:

```sh
mtf-facts lint path/to/module
```

Check only the areas a change touched, against the tree the change started from:

```sh
mtf-facts lint path/to/module --base path/to/base-tree
```

Or give the change itself, a GitHub pull request files list or a unified diff, when you have no base tree:

```sh
mtf-facts lint path/to/module --diff files.json
```

Write the full report as JSON:

```sh
mtf-facts lint path/to/module --base path/to/base-tree --json > report.json
```

Or the AI report a model reviewer reads, which leaves out what a reviewer never decides on (see [AI report](#ai-report)):

```sh
mtf-facts lint path/to/module --base path/to/base-tree --json --ai > report.json
```

`lint` exits with one of these codes:

| Code | Meaning |
|------|---------|
| 0 | No findings, and every rule was decided in every area. |
| 1 | At least one finding. |
| 2 | An error: bad input, a limit, the timeout, or a failed integrity check. |
| 3 | No findings, but at least one rule was `not_evaluated` in some area. |

Candidates are unconfirmed: the author confirms or rejects each one, and they never change the exit code.

`mtf-facts --help` lists the commands, and `mtf-facts <command> --help` gives the flags and exit codes of one. Help goes to stdout and exits 0. Long options take two dashes (`--json`); the single-dash form (`-json`) is not accepted. Every command exits 2 on an error. Ignore stdout on exit 2: a failed write to stdout can leave partial output.

## Example: review a pull request

These commands check pull request [#780](https://github.com/terraform-aws-modules/terraform-aws-lambda/pull/780) of `terraform-aws-modules/terraform-aws-lambda`. To check a change to your own module, put your repository and branch in place of the pull request.

Get the rule registry and the `terraform-aws-modules` module profile from [modulestf/modulestf](https://github.com/modulestf/modulestf), at the commit this repository's CI pins:

```sh
git clone https://github.com/modulestf/modulestf.git skills
git -C skills checkout 3bd9cf8bb175f2db92f82409544b8fda9efa90bd
REGISTRY=skills/.agents/skills/terraform-module-reviewer/references/rule-facts.md
PROFILE=skills/.agents/skills/terraform-module-maintainer/profiles/terraform-aws-modules
```

Check out the head of the pull request and the commit it branched from, side by side:

```sh
git clone https://github.com/terraform-aws-modules/terraform-aws-lambda.git lambda
git -C lambda fetch origin pull/780/head
HEAD_SHA=$(git -C lambda rev-parse FETCH_HEAD)
BASE_SHA=$(git -C lambda merge-base origin/master FETCH_HEAD)
git -C lambda worktree add ../head "$HEAD_SHA"
git -C lambda worktree add ../base "$BASE_SHA"
```

Write the provider schema, so the `coverage.*` rules run. This step runs Terraform and downloads the providers; `mtf-facts` does neither:

```sh
(cd head && terraform init -backend=false >/dev/null && terraform providers schema -json) > schema.json
```

Lint the areas the pull request touched:

```sh
mtf-facts lint head --base base \
  --registry "$REGISTRY" --profile-dir "$PROFILE" --schema-files schema.json
```

With `v0.4.0-rc.1` it prints this and exits 1 (the `not evaluated` lines are cut):

```text
examples/fixtures: MEDIUM examples.versions-missing: Example "examples/fixtures" has no versions.tf, so its Terraform and provider constraints are not in the file the profile and its sibling examples use.
  fix: Add `versions.tf` to `examples/fixtures`, copied from a sibling example, or rename the file that holds its `terraform` block to `versions.tf`.
examples/function-url/main.tf:45: unconfirmed structure.version-floor-understated: examples/function-url:required_providers.external, confirm or reject
examples/function-url/main.tf:45: unconfirmed structure.version-floor-understated: examples/function-url:required_providers.local, confirm or reject
examples/function-url/main.tf:45: unconfirmed structure.version-floor-understated: examples/function-url:required_providers.null, confirm or reject
scope: change from base; 4 areas checked, 0 calling a changed module, 18 left out: examples/alias, examples/async, examples/build-package, examples/code-signing, examples/complete, examples/container-image, examples/deploy, examples/event-source-mapping, examples/multiple-regions, examples/runtimes, examples/simple, examples/simple-cicd, examples/triggers, examples/with-efs, examp... (--full-tree checks every area)
1 findings, 3 candidates; manifest: 1 finding, 1 candidate, 76 clean, 2 not_evaluated, 68 not_applicable
```

Fix each finding, and confirm or reject each candidate. The two rules left `not_evaluated` need the declared minimum provider version, which a raw schema file does not record.

For the JSON report a hosted review writes for the same two commits, add `--json` and the commits:

```sh
mtf-facts lint head --base base --json \
  --registry "$REGISTRY" --profile-dir "$PROFILE" --schema-files schema.json \
  --head-sha "$HEAD_SHA" --merge-base-sha "$BASE_SHA" > report.json
jq '.findings[].summary, .candidates[].subject' report.json
```

## Common tasks

### Lint a module you are writing

Pass the rule registry and the module profile so the report records their hashes and the profile rules run, and the provider schema files so the `coverage.*` rules run:

```sh
mtf-facts lint path/to/module --base path/to/base-tree --json \
  --registry rule-facts.md --profile-dir path/to/profile \
  --schema-files schema.json > report.json
```

Without `--profile-dir`, the rules that need the profile are `not_evaluated` with `profile_unavailable`. Without a schema, a `coverage.*` rule is `not_evaluated(schema_unavailable)` in an area where the change adds or edits a resource or data block. See [lint](#lint) for each flag.

### Match a hosted review report

Add the commits the two trees came from, so the report records them as a hosted review does:

```sh
mtf-facts lint path/to/head --base path/to/base --json \
  --registry rule-facts.md --profile-dir path/to/profile \
  --head-sha <sha> --merge-base-sha <sha> > report.json
```

Each of `--registry`, `--head-sha` and `--merge-base-sha` is recorded as `null` when its flag is absent, and a host that checks a report against its pins must treat `null` as invalid.

### Write a provider coverage ledger

List the provider fields of each type the module declares, then check the ledger you wrote against that inventory:

```sh
mtf-facts ledger path/to/module --schema-files schema.json
mtf-facts ledger path/to/module --schema-files schema.json --check coverage-ledger.json
```

When the ledger is incomplete, the check exits 1 and lists each problem in `problems`. See [ledger](#ledger).

### Upgrade a provider

Compare the provider schema of the current and the target version, for the types the module tree uses:

```sh
mtf-facts schema-diff --from current.json --to target.json --types-from path/to/module
```

Each schema file is a `terraform providers schema -json` output. See [schema-diff](#schema-diff).

## Run in a review pipeline

A hosted review runs the commands in this order:

1. `lint --json`, without `--ai`, on the head tree with `--base`, `--registry`, `--profile-dir`, `--schema-handoff`, `--head-sha` and `--merge-base-sha`.
2. `bundle` on that report, with `--head-sha`, `--merge-base-sha`, `--binary-sha256` and the schema and profile flags the lint run had. It prints `judgment` and writes the facts bundle and the injected rule text, or prints `full` and writes no file. Steps 3 to 6 run only after `judgment`.
3. The model reviewer reads the bundle and the injected text and writes its reply.
4. `merge check` on the bundle and the reply. Exit 1 means the reply needs the repair turn.
5. `merge final --registry` on the bundle, the reply and, after a repair turn, the repair reply.
6. `merge counts` on the bundle and the same replies, read only after `merge final` succeeds.

Host contract:

- Exit code 2 means stdout is not a report. Ignore stdout on exit 2: a failed write to stdout can leave partial output.
- A timeout, or any error before output, writes nothing to stdout.
- Drain both stdout and stderr while the process runs, and keep your own wall-clock kill. `--timeout` does not replace it.
- Pin one version and its SHA-256 per platform, and check the attestation. See [Update a consumer pin](#update-a-consumer-pin).

## Command reference

### lint

```sh
mtf-facts lint <dir> [--base <dir> | --diff <file>] [--full-tree] [--json [--ai]] [--timeout <duration>]
  [--registry <rule-facts.md>] [--profile-dir <dir>]
  [--schema-files <file>]... [--schema-handoff <file>]
  [--head-sha <sha>] [--merge-base-sha <sha>]
```

| Flag | Meaning |
|------|---------|
| `--base <dir>` | The tree the change started from. Only the areas the change touched are checked. |
| `--diff <file>` | The change as a file, to rebuild the base tree from, when there is no base tree. Not with `--base`. See [Change as a diff](#change-as-a-diff). |
| `--full-tree` | Check every area, not only those the change touched. The change still decides which lines are judged. |
| `--json` | Write the full report as JSON. |
| `--ai` | With `--json`, write the AI report on one line. See [AI report](#ai-report). The text report is the same with or without it. |
| `--timeout <duration>` | Stop a run that takes longer. Default `60s`. |
| `--registry <rule-facts.md>` | Record the registry hash. |
| `--profile-dir <dir>` | The module profile directory. |
| `--schema-files <file>` | A `terraform providers schema -json` output. Repeatable. |
| `--schema-handoff <file>` | The schema handoff the schema job writes, gzipped or not. |
| `--head-sha <sha>` | Record the commit the head tree came from. |
| `--merge-base-sha <sha>` | Record the commit the base tree came from. |

The `--json` report holds the envelope, one manifest entry per rule and area (see [Areas](#areas) for a cross-area rule), findings in the reviewer's findings schema, and candidates. A `structure.argument-order` candidate carries `predates_change: true` when every item out of order in the head block, by name, is also out of order in the base block of the same address, so the same subject: the defect is older than the change. A change that puts a new item out of order does not predate it. No other rule sets it today; it is for a rule whose check already reads the base block. It also holds:

- `scope`: how the change was given (`none`, `base` or `diff`), `full_tree`, and three disjoint lists of areas. `areas_checked` have their own manifest entries. `areas_calling` are the other areas that call a module listed in `callers`. `areas_left_out` are the rest, so no area is dropped without a trace. `module_areas` lists every discovered area of kind `module`, at head or at base, sorted: the set that decides which area owns a file (see [Module ownership](#module-ownership)).
- `callers`, with a change only: each root or submodule area the change touches whose `versions.tf` or `terraform` block, or `variables.tf` or a `variable` block, changed, with its version floors at base (`null` for a new module) and at head, and every area that calls it by a local path: the call's file and line, whether that area is checked, the floors it declares, and the ids of the `structure.version-floor-understated` candidates filed in it. A floor is an `address` (`required_version` or `required_providers.<name>`), the provider `source` and the `constraint` as written. Callers under `wrappers/` are not listed: no area reads that directory.

- `module_callers`: always written, `[]` without a change. See [Module ownership](#module-ownership).
- `in_repo_downgrade`: always written, sorted, often `[]`. See [In-repo downgrade](#in-repo-downgrade).
- On a candidate, `downgrade: "in-repo"` and `severities: ["MEDIUM"]` when the in-repo downgrade covers it; absent otherwise.
- A manifest entry for a rule the profile's options gate is `not_applicable` with `reason_code` `profile_option` and a reason text. See [Profile options](#profile-options).

The text report prints a `scope:` line, and with a change one line per changed module with the floors the change moves, and one line per caller with its own floor at those addresses. The JSON schemas are in [schema/](schema/).

#### AI report

`--json --ai` writes the parts of the report a reviewer decides on, as one line of JSON. The exit code is the same as without `--ai`. Compared with the full report:

- `manifest` keeps only its `finding`, `candidate` and `not_evaluated` entries, in the same order and shape.
- `manifest_summary` is added. `status` counts every manifest entry of the full report by status. `omitted_by_family` counts the `clean` and `not_applicable` entries left out, per rule family: the rule id up to its first dot, such as `structure`. `areas` lists every area with a manifest entry, sorted. A rule with no entry in a listed area was `clean` or `not_applicable` there, or has no entry there in the full report either.
- `envelope.inputs.head` and `envelope.inputs.base` keep `tree_sha256` and give `file_count` in place of the `files` list. Every other envelope key is unchanged, `schema_inputs` with its handoff hash, commits and tree hashes included.
- `index`, `interface_diff` and `schema_coverage` are left out.
- `findings`, `candidates`, `scope`, `callers`, `module_callers` and `in_repo_downgrade` are unchanged.

The report keeps `schema_version` 2 and the keys `envelope`, `manifest`, `findings` and `candidates` with their types, and `manifest_summary` is present only in the AI form. `bundle` and `merge final --report` need the index and the file lists, so they refuse a AI report and exit 2.

#### Change as a diff

`--diff` reads a GitHub pull request files list, the JSON array `gh api repos/<owner>/<repo>/pulls/<number>/files --paginate --slurp | jq add` writes, or a unified diff as `git diff` writes it, with or without `diff --git` lines. The format is detected from the first byte. Paths must be relative to `<dir>`. The base tree is the head tree with each changed file rebuilt by reversing its hunks: a removed file comes back from its removed lines, an added file is left out, and a renamed file moves back to its old path. Every context and added line must match the head file, line ending included, or the run exits 2, and so does a path that is not clean and relative, a path listed twice, or a status the tool does not know.

A file the diff does not list is unchanged, even when it is too large to read or cannot be read: its base entry is the head's own. With `--base`, a file too large to read is compared by a SHA-256 of its bytes, read in a stream and never kept, up to 64 MiB a file and 256 MiB a tree; past those, or unreadable, it counts as changed. A file the diff gives no patch for, as GitHub does for a binary or very large file, has no known base content. It is in `inputs.base` with kind `no_patch`, and the configuration rules of its area are `not_evaluated` with `no_base` and a reason that says the diff has no patch for it. A GitHub rename with no patch and no added or removed line is a pure rename when the head file is text, as git judges it (no NUL byte in the first 8000 bytes), and a binary one is `no_patch`. A modified entry with no patch and no added or removed line is, for a text file, a change of its exec bit only: its base content is the head's. A removed `.tf` file with no patch and no removed line was empty, or binary and so no configuration: its base content is empty, unless a `.gitattributes` file of either tree names `binary` or `-diff`, which may hide the diff of a text file, and then it is `no_patch`. A unified diff's `old mode` and `deleted file mode` lines set the base exec bit, and a changed exec bit alone makes a path changed. The envelope records the diff in `inputs.diff`: its `format`, `github-files` or `unified`, and its SHA-256. A report from `--diff` has a base tree, so `bundle` accepts it.

Two schema files with one base name exit 2. The schema handoff and the schema files are read offline under size caps; a missing, oversized or malformed one exits 2. The handoff is read only when `--head-sha`, `--merge-base-sha` and the `.tf` tree hashes of the head and `--base` trees equal the ones it records; otherwise every lookup in it is `schema_unavailable`.

The envelope's `schema_inputs` records the handoff's hash, commits and tree hashes, and the hash of each schema file, named by its base name. With schema files and no handoff, it records the hash of each schema file and the handoff keys are null. `handoff_sha256` hashes the file's bytes as read, so a gzipped and a plain copy of the same handoff hash differently: a host that checks it must hash the form it passed.

The `coverage.*` rules read the bound handoff when one is given, and never fall back to the schema files. With schema files and no handoff, as in local `lint`, they resolve each block's provider source from `required_providers` and read the files as the latest schema; a provider no file has stays `schema_unavailable`. A schema file records no provider version, so its schema facts carry the version `schema-file`, and `coverage.argument-above-min-version`, which needs the declared minimum, stays `not_evaluated(schema_unavailable)`.

`--profile-dir` reads the directory's `PROFILE.md` profile-constants blocks and the templates they name, without following a link and with size caps. The output envelope records its `profile_sha256`, and `profile_tree_sha256`: the SHA-256 of sorted `<sha256>  <path>` lines over every file of the directory, `null` when an entry is a link, special or unreadable, or when a name is not UTF-8 or holds a control character or DEL, which could forge a line of the hash input. `profile-snapshot` prints the same value as `tree_sha256`. A path that is missing, not a directory or itself a link exits 2; a link in a parent component of the path is resolved. A profile that cannot be read or parsed is no error: the rules that need it are `not_evaluated` with `profile_unavailable`, as they are without `--profile-dir`.

`--timeout` must be greater than zero. A run that takes longer writes `mtf-facts: timeout` to stderr and exits with code 2.

The exit codes are in [Quick start](#quick-start).

### affected

`mtf-facts affected` lists the examples a change can affect, most likely first, so a host can plan them in that order within its time budget.

```sh
mtf-facts affected <dir> [--base <dir> | --diff <file>] [--json] [--timeout <duration>]
```

`--base` and `--diff` read the change as `lint` does (see [Change as a diff](#change-as-a-diff)). With `--json` it writes one JSON document in the shape of [affected.schema.json](schema/affected.schema.json):

```json
{"schema_version": 1, "inert": false, "fallback": null,
 "examples": [{"dir": "examples/c", "reason": "touched"}, {"dir": "examples/a", "reason": "caller"}, {"dir": "examples/b", "reason": "all"}],
 "changed": ["examples/c/main.tf", "modules/log/main.tf"]}
```

- `examples` holds every example: each first-level directory under `examples/` with a `.tf` file in it. First those with a changed path under them (`touched`), then those whose local module sources reach a changed module directory, transitively and the root module included, as with `source = "../../"` (`caller`), then the rest (`all`), each group in path order. Registry and git sources are not callers of the local tree; a local source written with backslashes, as on Windows, is read with slashes. A changed file Terraform loads by its directory (`.tf`, `.tf.json`, `.tfvars`, `.tftest.hcl`, `.terraform.lock.hcl`, or an entry that is not a regular file) is a change of that directory. Any other changed file, such as a script, a template or a README, is a change of every module whose read sites reach it, and of none when no read site does.
- `inert` is true only when every changed path is a documentation or metadata path (Markdown and other docs files, `LICENSE`, `.gitignore`, anything under `docs/` or `.github/` that is not configuration), its content changed but not its exec bit, and no HCL read site in the whole tree reaches it. Read sites are the first argument of `file`, `templatefile`, `fileset` and the other file functions, the file calls inside every template a `templatefile` call reaches, the file attributes of `local_file`, `archive_file` and the provisioners, and any other string that is a path from `path.module`, `path.root` or `path.cwd`, or a plain literal resolved against the repository root and each example. A read site with a dynamic path, a shell command (`local-exec` `command` and `interpreter`, `external` `program`), a template that does not parse, and a path that leaves the tree, such as `${path.module}/../x`, reach every file. A docs file a read site reaches is not inert, so a README a module reads with `file()` makes its callers `caller`.
- A change of the exec bit alone is a change. The exec bit comes from the file mode with `--base`, from `old mode` and `deleted file mode` lines in a unified diff, and for a GitHub files list entry that is modified with no patch and no added or removed line, from the head file with its bit flipped when the file is text. Such an entry for a binary file, like any file the diff gives no patch for, is never inert. The tree hashes a `lint` report records do not include the exec bit, so they stay as they were.
- `fallback` says why the examples could not be ranked, and then every example has reason `all` and `inert` is false: `unparsed` when a `.tf` file of the head tree does not parse or a `.tf.json` or override file is present, `dynamic-source` when a module call has no literal source, `no-change-input` when neither `--base` nor `--diff` is given.
- `changed` lists the changed paths, both paths of a rename.

It exits 0 with the list, or 2 on bad input, a limit, an example directory whose name is outside `[A-Za-z0-9._-]`, or the timeout, with nothing on stdout. Without `--json` it prints one `<reason> <dir>` line per example and a summary line.

### depth

`mtf-facts depth` decides how deep a review of a change must go: `light`, `focused` or `deep`. It reads the head tree and the merge base tree, the pull request files list, and the `behaviour --json` and `affected --json` reports of the same change, and never runs Terraform. Every rule fails closed: anything it cannot prove makes the change deep.

```sh
mtf-facts depth --head <dir> --base <dir> --files <files.json> [--classify <behaviour.json>] [--affected <affected.json>] [--examples <dir,dir>] [--timeout <duration>]
```

`--files` is GitHub's `pulls/<n>/files` list (`filename`, `status`, `previous_filename`, `additions`, `deletions`). `--examples` names example directories the host knows of besides those of the affected report; each is selected unless proven out of scope. It prints one JSON object:

```json
{"format": 1, "depth": "focused", "reasons": ["focused-within-limits"],
 "checks_out_of_scope": [], "examples_selected": ["examples/complete"], "examples_out_of_scope": [],
 "touched_types": ["aws_rds_cluster"], "walk": {"complete": true, "unwalked": 0},
 "limits": {"tf_files": 1, "changed_lines": 2, "changed_blocks": 1, "selected_examples": 1},
 "schema_providers": ["base:.:aws=hashicorp/aws:>=5.0", "head:.:aws=hashicorp/aws:>=5.0"],
 "schema_types": [".:resource:aws_rds_cluster:hashicorp/aws"], "schema_locks": []}
```

- The changed paths are every path whose entry differs between the trees and every path the files list names; a list that disagrees with the trees is `deep-files-list-mismatch`. Terraform-read files are `.tf`, `.tf.json`, `.tfvars`, `.tfvars.json`, any `.hcl` (`.tftest.hcl` and `.terraform.lock.hcl` included), `.tftest.json`, `.tfmock.json`, `.terraformrc`, `terraform.rc` and `*.tfrc`, matched case-insensitively. Both trees are parsed, HCL and JSON forms; a Terraform-read file that does not parse, is not a regular file, or is an override file is `deep-parse-failure`, and `walk` counts it.
- `light` needs all of: every changed path is Markdown (`*.md`), `CHANGELOG*`, under a `docs` directory at any level, `LICENSE`, `.gitignore` or `.editorconfig`; none is Terraform-read; no file sink string of the head (below) resolves to a changed path or a directory holding one, and every one resolves; the head calls only Terraform's built-in functions (a provider-defined function blocks light); the classifier report is valid, of this change, and `inert`; at most 50 changed files, else `deep-too-many-files`. Out of scope at light: checks A, B and D and the Check G ladder (`G-ladder`); every example is listed in `examples_selected`, for validate.
- `focused` needs a Terraform change with no deep class, at most 6 changed Terraform-read files, 200 changed lines (`additions` plus `deletions` of every listed file), 4 changed resource, data and module blocks, and 12 selected examples, and every classifier trigger on the allow-list: an expression or literal change in a resource or data argument, a local, an output value or a module call argument; an added output; an unknown propagation candidate; an example `.tfvars` change. Selected examples are those the affected report lists as `touched` or `caller`, those it does not list, those holding a changed path, and those whose local module calls reach a changed directory or the closure; the rest, reason `all` in a report with no fallback, are out of scope. B is out of scope with no touched type; D is always in scope at focused, and with no example selected and none changed the reasons add `focused-no-example`.
- `deep` is everything else, with every check and example in scope.
- Changed values: every changed argument of a resource or data block, local, variable, output, module call or variables file assignment, and every argument whose file sink string resolves to a changed path. Each is followed to every consumer, through outputs to the calling module and through module call arguments into the callee's variables. A changed argument `X` of a managed resource `R` affects references to `R.X` and to `R` as a whole (`R`, `R[*]`, `R[k]`, a `for` expression over it); references to other attributes are not affected (a changed `dynamic "X"` block of `R` is the argument `X`), and every reference is when `R` is added or its `count`, `for_each` or `provider` changes, or when `X` defines `R`'s identity: its whole name is `name`, `name_prefix`, `bucket`, `bucket_prefix`, `identifier`, `identifier_prefix`, `cluster_identifier`, `cluster_name`, `function_name`, `family`, or ends in `_name` or `_identifier`. That rule is a heuristic for the computed attributes (`id`, `arn` and the like) a replacement changes; the plan of every selected example still shows a replacement. A changed variables file assignment is a changed value of that variable in its directory; one in a directory with no module, or naming a variable no module there declares, is `deep-unresolved-consumer`. A data source is a query: a changed argument affects every reference to it. A reference inside `depends_on` carries ordering, not a value. A consumer the parser does not model (a variable validation, a precondition or postcondition, `check`, `import`, `removed`, an unknown block, a registry or git module, a module call whose source does not resolve) is `deep-unresolved-consumer`.
- File sinks: the path argument of the file functions (`file*`, `templatefile`, `fileset`); the provisioner arguments that read files (the file provisioner's `source`, remote-exec's `script` and `scripts`, local-exec's `working_dir` and `interpreter`); and any argument whose whole name is one of `path`, `paths`, `file`, `files`, `filename`, `dir`, `directory`, `source`, `source_dir`, `source_file`, `chart`, `config`, `config_path`, `config_paths`, `template`, `template_dir`, `program`, `script`, `scripts`, `content_dir`, `output_path`, or ends in `_path`, `_paths`, `_file`, `_files`, `_filename`, `_dir`, `_dirs`, `_directory`, `_template`, `_script` or `_scripts` (such as `source_path` or `iam_role_path`), at any nesting, in any block. A module call's arguments are sinks only for a registry or git module, since `file()` resolves against the working directory; a local module's sinks trace the value where it lands. Every string a sink can read must be a literal, or `path.module` or `path.root` joined with literals; anything else is `deep-unresolved-file-dependency`. References are traced to their sources: a local to its expression; a variable to its default and every in-repo assignment (callers' arguments and the variables files of its directory; module calls under a `wrappers` directory add none); a module output to its value in a local callee; `each.key` and `each.value`, a dynamic block's iterator and a `for` expression's variables to the collection they iterate, attribute by attribute through objects, `merge`, `flatten`, `concat` and the other collection functions that pass elements on; `try` and `coalesce` to their arguments, `lookup` with a literal key to the map's value at that key and its default, an index or a splat to the collection's elements, `chomp` and `trimspace` to their argument trimmed. An encoding, hash, number, bool or time (`jsonencode`, `md5`, `length` and the like) is no path and passes. A variable with no default and no in-repo assignment is an external input and passes, as does a null, a number or a bool. An attribute of a managed resource or data source passes, except one of `local_file`, `local_sensitive_file`, `archive_file`, `template_file`, `template_dir` or `external`, which is traced to the argument of the same name. A string built in the repository (a template with any part that is not a path, or any other function) does not resolve. A local-exec `command` is not resolved, but its literal text is searched for the name of every changed path and of every directory holding one, case-folded; a match is `deep-unresolved-file-dependency`. A relative path resolves against every configuration directory, segment by segment as the operating system does: each symbolic link of the head is replaced by its target before a following `..` applies, and a link that is absolute, leads out of the tree or loops does not resolve. Paths are compared with the changed paths case-folded. A source of several expressions (a JSON list of strings) and an object with a computed key do not resolve. Tracing stops after 2,000,000 steps or at the deadline, and the change is then `deep-unwalked`. A template a `templatefile` call reads is scanned for file function calls too. Strings outside sinks are not resolved. Tracing into registry and git modules is a later improvement: a changed value that reaches one is `deep-unresolved-consumer`.
- Paths: a changed path that is not printable ASCII, has an empty, `.` or `..` segment, or a backslash; a changed link or special file; and two paths of either tree equal when case-folded are `deep-path-unsafe`. A change under `.github` or `.circleci`, to `action.yml` or `action.yaml`, `.pre-commit-config.yaml`, `.pre-commit-hooks.yaml`, `.releaserc*`, `release.config.*` or `.gitlab-ci.yml` at any path is `deep-ci-config`.
- `touched_types` are the types of resource and data blocks with changed lines and those the closure reaches. `limits` gives the counts the focused limits compare.
- `schema_providers`, `schema_types` and `schema_locks` are the schema pass's tokens, in the grammar of its Inputs section: `<rev>:<dir>:<local>=<namespace>/<name>:<constraints>` per revision and configuration directory from `required_providers` (`<rev>:<dir>:!constraint_unparsable` when it cannot be read or gives a local name two sources, `<rev>:<dir>:<local>=!registry_host` for another registry); `<dir>:<resource|data>:<type>:<namespace>/<name>` for every head resource and data block, bound by its `provider` argument, else its type's prefix, through that local name's source, else `hashicorp/<local>`, none for the builtin `terraform` provider; `<dir>:<namespace>/<name>=<version>` from each head `.terraform.lock.hcl`. A token that fails its pattern is left out. They are written at every depth.

Reasons are fixed tokens, written in this order. A host that raises a report to deep writes one of the host tokens after them, or `deep-schema-<reason>` for a schema pass failure; `mtf-facts` never writes those.

<!-- depth-reasons -->
| Token | When |
|---|---|
| `light-docs-only` | the change is light |
| `focused-within-limits` | the change is focused |
| `focused-no-example` | focused, with no example selected and none changed: Check D stays in scope to report the missing example |
| `deep-terraform-block` | a `terraform` block changed, or a changed value reaches one |
| `deep-lock-file` | a `.terraform.lock.hcl` changed |
| `deep-cli-config` | a CLI configuration file changed |
| `deep-ci-config` | a CI, hook or release configuration file changed |
| `deep-provider-config` | a provider block, alias, `provider` or `providers` argument changed or is reached |
| `deep-depends-on` | a `depends_on` changed |
| `deep-new-type` | a resource or data type new to its directory |
| `deep-block-removed-or-renamed` | a resource, data, module, output or variable block removed or renamed |
| `deep-variable-contract` | a variable added, or anything but its description changed |
| `deep-output-sensitive` | an output's `sensitive` or `ephemeral` changed |
| `deep-lifecycle-moved-removed-import` | a `lifecycle`, `moved`, `removed` or `import` block, a precondition or postcondition, changed or reached |
| `deep-count-for-each` | a `count` or `for_each` changed or reached |
| `deep-provisioner` | a provisioner or connection changed or reached |
| `deep-policy` | an argument whose name contains `policy`, an object key or block named `Statement` or `Principal` (any case), an IAM resource or data source (a type with `_iam_`), a resource type ending in `_policy` or `_permission`, or a `*_policy_document` data source, changed or reached; a changed block counts only when such a name inside it, or what it holds, differs between the revisions, and the keys of a variable's `type` are no such names |
| `deep-module-source-version` | a module call's `source` or `version` changed, or a module call added |
| `deep-dynamic-structure` | a dynamic block changed or reached |
| `deep-wrappers` | a file under a `wrappers` directory changed, or a changed value reaches an argument there other than a pass-through output |
| `deep-limits` | over a focused limit, or a files list entry with no line counts |
| `deep-classifier-category` | a classifier trigger outside the allow-list, or capped triggers |
| `deep-classifier-unavailable` | no classifier report |
| `deep-classifier-invalid` | a classifier report that does not decode as `behaviour --json` (an unknown key, a wrapper object, another `schema_version`) |
| `deep-classifier-mismatch` | a classifier report with no entry for a changed path: of another change, or with paths relative to another directory than the head tree's root |
| `deep-affected-fallback` | no affected report, or one with a fallback |
| `deep-affected-invalid` | an affected report that does not decode as `affected --json` (such as `{}`) or lists an unknown reason |
| `deep-parse-failure` | a Terraform-read file does not parse |
| `deep-unwalked` | a file, block or expression the parser cannot walk, or a limit of the walk |
| `deep-unresolved-consumer` | a consumer the parser does not model |
| `deep-unresolved-file-dependency` | a file sink string does not resolve, or a local-exec command names a changed path |
| `deep-path-unsafe` | an unsafe path, a changed link, or a case-folded collision |
| `deep-too-many-examples` | more than 12 selected examples |
| `deep-too-many-files` | a light change of more than 50 files |
| `deep-test-or-mock` | a test or mock file changed |
| `deep-files-list-mismatch` | the files list and the trees disagree |
| `deep-non-terraform-change` | not light, and no Terraform-read file changed |
| `deep-owner-raise` | host only: the owner asked for `depth: deep` |
| `deep-validate-failed` | host only: validate failed at light |
<!-- /depth-reasons -->

Paths of the files list, the classifier report and the affected report are relative to the head tree's root, the trees `--head` and `--base` name. A `.git` file or directory (a linked worktree's, a submodule's) is not part of either tree. For each reason a bad input gave, a line `mtf-facts: depth: <why>` goes to stderr, such as the paths a files list mismatch is about.

It exits 0 with the report, a tree over a read limit and the timeout included (deep, `deep-unwalked`), or 2 on a missing tree, a files list that does not read or has a line count over 10,000,000, or an unsafe `--examples` entry, with nothing on stdout.

### behaviour

`mtf-facts behaviour` lists every changed tree entry and decomposes the change into triggers: one per AST-level change, each with one category and the verification levels it needs. It never runs Terraform.

```sh
mtf-facts behaviour <dir> (--base <dir> | --diff <file>) [--schema <file>] --json [--timeout <duration>]
```

The change is read as `lint` reads it, and the report is the same for a base tree and for every diff form and context size. It is one JSON document in the shape of [behaviour.schema.json](schema/behaviour.schema.json):

```json
{"schema_version": 1, "inert": false,
 "entries": [{"path": "main.tf", "change_kind": "modified", "inert": false, "trigger_ids": ["T-001", "T-002"]}],
 "triggers": [{"id": "T-001", "file": "main.tf", "line": 10, "category": "wrapper-change", "change_kind": "modified",
   "address": "aws_rds_cluster.this", "attribute": "vpc_security_group_ids",
   "base_expr": "compact(concat(aws_security_group.this[*].id, var.vpc_security_group_ids))",
   "head_expr": "concat(aws_security_group.this[*].id, var.vpc_security_group_ids)",
   "referenced_inputs": [{"name": "vpc_security_group_ids", "type": "list(string)"}],
   "contextual_refs": ["aws_security_group.this"], "contextual_ref_details": [...],
   "pure": true, "levels": ["L1"], "reason": null}, ...],
 "capped": 0}
```

- A documentation or metadata path (as for `affected`) is inert when the read-site scan of both trees is complete and no read site reaches it. A `.tf` file is inert when both revisions of its directory decompose and no trigger lands in it: comments, layout, a block moved unchanged between files of one directory, or a variable or output `description`; not when a read site can read the file itself. A mode-only change, a lock file, a script and every other path are triggers.
- Each module directory is compared block by block: resources, data sources, module calls, variables, outputs, each local, providers, the `terraform` block's `required_version` and each required provider, `moved` blocks, and any other block by its source. A directory with a `.tf` file that does not parse, is not a regular file, or is an override file gets one `unclassified-change` trigger per changed `.tf` file.
- Categories and levels: `wrapper-change` (one revision wraps the other in function calls, or calls another function on the same arguments), `default-change`, `count-for_each-conditional-change`, `type-validation-change` and `locals-outputs-change` select `L1`; `literal-argument-change`, `resource-added-or-removed`, `lifecycle-change`, `dynamic-block-change`, `unclassified-change` and `non-hcl-dependency` select `L4`; `provider-version-floor-change` selects `compat`; a `count` or `for_each` change of a resource, data source or module selects `L1` and `L2` (`terraform test` with mock providers), and a dynamic block whose `for_each` alone changed (same labels, iterator and content) selects `L1` on its `for_each` and is marked `for_each_only`; a change inside nested dynamic blocks that lies in one inner dynamic block alone, with the blocks around it the same but for that one, is placed at the innermost such block, its `attribute` the path of group keys such as `dynamic.origin.dynamic.custom_origin_config`; `moved-or-renamed-address` and provisioner or connection changes select none and say why.
- A changed resource argument whose expression is not a literal on both sides, or that is removed, also yields an `unknown-propagation-candidate` trigger (`L1`, `L3`) when the provider schema given with `--schema` (a `terraform providers schema -json` file) marks it Optional and Computed, or when there is no schema, the schema lacks the provider, the type or the attribute. A data source argument is not planned at `L3`: a changed data source argument whose expression is not a literal on both sides yields an `unknown-propagation-candidate` trigger with `L1` only, since an unknown argument defers the read to apply whatever the schema says. A deferral by `depends_on` or by a pending change of a dependency is not modelled, so no claim that a read is not deferred can verify.
- An argument added to, or removed from, a resource or data source block that is present at both revisions reads as `null` at the revision without it, when the schema given with `--schema`, at each revision's provider source, marks the name as an attribute that is not write-only, reached through nested attribute types only. One schema stands for both revisions, so there is no `null` when the provider's `required_providers` entry (its source or version constraint) or its local name differs between the revisions in that directory. A revision that writes the name in block syntax, as a nested block or a `dynamic` block of that name, is not absent and gets no `null`. That side's expression is then `null` and the trigger carries `base_absent` or `head_absent`; the flags change no category and no level. There is no `null` with no schema, a disagreement, a nested block on the path, or a name that is a meta-argument or a block Terraform reads (`count`, `for_each`, `depends_on`, `provider`, `lifecycle`, `provisioner`, `connection`, `dynamic`, `moved`, `import`, `removed`, `check`); arguments of provider, module, output, locals, variable and terraform blocks never get one.
- A variable the change adds, whose default is a bare literal (a quoted string with no `${` or `%{`, `true`, `false` or `null`), gives the triggers that read it a `default` case with that value, unless a case of that value is already there.
- `pure` is set when every function the expressions call is on the built-in pure-function allowlist and neither reads `self`. A variable's `type` expression reads nothing: its keywords, such as `string`, are no references. `contextual_ref_details` gives each other reference as written, its kind, its type from the schema and whether its block has `count` or `for_each`.
- A trigger that selects `L1` lists its required `cases`. The inputs are its variables, its resource, data, `count.index` and `each.key` references, and for a default or type change the variable itself. Each input has states: a list or set is typical, empty, an empty element, a null element, an unknown element, wholly unknown and null; a map the same with key `k`; a string, number or bool typical, zero, unknown and null; every string, number and bool literal of either expression (at most 8 of a kind) is one more state of each input of its type, and one more element of each collection of it; a variable whose default changed is also omitted (unset, so each revision's default applies); a splat reference has one unknown instance, none, a null attribute or one known, and no null attribute when it reads the `id` of a managed resource (`<type>.<name>[*].id`), which may be unknown but is never null; any other resource or data reference is unknown or known; `count.index` is 0 or 1 and `each.key` `a` or empty. A list or set input, and a splat reference, also take order states: two distinct elements and a duplicate. The cases are the first that fits 32: every combination of all states; every combination of the other states plus one case per order state with the rest typical; for a `count` or `for_each` trigger that selects `L2`, or a dynamic block, no cases and a reason; with no order states, every combination of the states; one typical case and one case per other state of each input, order states included; past 32, or with a `local`, `module`, `self` or `each.value` reference, the trigger has no cases and says why. Each input of a case sets exactly one of `value`, `unknown`, `null` and `omitted`, with `unknown_elements` for the unknown elements of a value.
- Triggers of one file and line whose base and head expressions are the same, and neither null, are one change seen by several categories, and are merged into one trigger, verified once: the first keeps its `category` and its place, `also_categories` lists the others' categories, `levels` is the union in `L1`, `L2`, `L3`, `L4`, `compat` order, and `cases` the union by case id; `address` and `attribute` come from whichever has them. A merge is not made when either trigger selects no level, when both name an address or an attribute and they differ, when the union of the cases would be over 32, when one case id has other inputs in each, or when one trigger could not get the required cases it needs while the other has some. The ids are numbered after the merge, and each entry's `trigger_ids` lists the merged ids.
- At most 40 triggers are listed, ordered by file, line and category; `capped` counts the rest.

It exits 0 with the report, or 2 on bad input, a limit or the timeout, with nothing on stdout.

### claims-eval

`mtf-facts claims-eval` validates the claims a reviewing model wrote, and computes the state of every trigger and every claim from a ladder record.

```sh
mtf-facts claims-eval --behaviour <file> --ladder <file> [--claims <file>] [--head-sha <sha>] [--base-sha <sha>] --json
```

- The claims file has the shape of [claims.schema.json](schema/claims.schema.json): each claim names a closed predicate (`expression-output-unknown`, `expression-output-length-known`, `expression-output-equals-for-inputs`, `plan-attribute-unknown`, `plan-attribute-unchanged-vs-prior`, `plan-action`, `input-filtered`) and a trigger by id in `params.node`, never HCL text. A claim that does not validate is listed in `discarded_claims` with the reason.
- The ladder record has the shape of [ladder-record.schema.json](schema/ladder-record.schema.json); unknown fields are refused, and a record with runs must name its `head_sha` and `base_sha`. A record with runs needs `--head-sha` and `--base-sha`, and a record of other commits is refused. `capped` echoes the behaviour report's.
- `input-filtered` names a case input (a variable or a contextual reference) and a marker (`empty-string` or `null`); with `"filtered": false` it states that the input's marker elements reach the output instead of being removed.
- `expression-output-unknown`, `expression-output-length-known` and `input-filtered` take an optional `params.cases_where`: a non-empty object of the trigger's case inputs to states (`wholly_unknown`, `unknown_element`, `empty`, `known` for any other known non-null value, `single` and `multiple` for a known collection of one or more elements, which `known` also matches, `null`, `omitted`); the claim is judged only on the required cases whose inputs are all in those states, and a scope that matches no case leaves it unverifiable.
- A claim reads the runs of its trigger at its levels (`L1` for expression predicates and `input-filtered`, `L3` or `L4` for plan predicates) that ran with a valid control. It is verified when some observation is about it and all agree, refuted when one contradicts it, and unverifiable otherwise; an unknown, sensitive or redacted value leaves a value predicate unverifiable.
- A trigger is refuted when one of its claims is. A trigger that selects `L1` or `L3` is decided by the tool's own sweep, never by the claims alone. Every selected level needs base and head observations from runs with a valid control and a fixture hash (and none with an invalid control): at `L1` and `L2` of every required case the trigger lists (the same cases at both), each observed with exactly the case's `inputs`, other cases ignored; at `L3` the same case ids at both revisions; at most 32 cases at each level. A case whose plan fails in the subject (`error`) has no output; a failure at one side only is a difference, and a sweep whose cases all fail at both, or whose first case does, is unverifiable. Each case is compared attribute by attribute: `known`, `length`, `value` (the value, or the known parts of a partly unknown one), `sensitive`, `redacted`, `unknown_inputs`, `action`, `unchanged_vs_prior`, and when neither side is known `wholly_unknown` and `unknown_mask`. A side not known with no `unknown_mask`, a partly known side without its known parts, or a case known at both revisions whose value is sensitive or redacted leaves the trigger unverifiable. With no difference the trigger is verified, claims or not. Otherwise it is verified only when verified claims state both sides of every difference exactly, each on that case's observation at its own revision: an unknown side by `expression-output-unknown` (`L1`) or `plan-attribute-unknown` (`L3`), a known side by `expression-output-equals-for-inputs` (`L1`) or `plan-attribute-unchanged-vs-prior` (`L3`), a length by `expression-output-length-known` or `expression-output-equals-for-inputs`, a value by `expression-output-equals-for-inputs` at both revisions, an action by `plan-action` at both. At `L1` a `wholly_unknown` or `unknown_mask` difference from wholly unknown at the base to partly known at the head (a definite length, not failed, sensitive or redacted) is explained by a scoped `expression-output-unknown` at the base and a scoped `expression-output-length-known` whose `length` equals the observed one at the head; the reverse, a known length becoming unknown, is never explained; any other `wholly_unknown` or `unknown_mask` difference, and every `sensitive`, `redacted`, `unknown_inputs` and `error` difference, is never explained. At `L1` a `value` or `length` difference where one revision drops an input's marker elements that the other keeps is also explained by a verified `input-filtered` claim at each side (filtered at one, `"filtered": false` at the other, same input and marker) when the keeping side's value with those markers removed is the other side's value and it holds no more marker elements than the claimed inputs do. An `L2` difference (an `L2` observation is the instance count for `count`, or an object of instance key to planned input for `for_each`) is listed and never explained. An `L2` run, whose mock providers make computed values known, never evidences `expression-output-unknown`, `expression-output-length-known` or `plan-attribute-unknown` and explains no unknown-ness difference. A trigger whose expressions compare an input with an input-free function call, such as `var.cidr == lower("EVIL")`, has no cases. Each difference is listed in the trigger's `changes`, with `explained`, the claims that explained it in `explained_by`, and the case inputs.
- A trigger that selects levels of more than one family, the sweep (`L1`, `L2`, `L3`), `L4` and `compat`, which only a merged trigger does, needs each family to decide it, and is then covered; one family never stands in for another.
- A trigger the sweep reads that is not inert or refuted carries `complete`, `outstanding` and, when `outstanding` is not empty, `evidence_digest`. `complete` is true when every level it selects ran with a valid control, every required case was observed at both revisions, every observation could be compared, and an `L4` or `compat` level it also selects decided it. `outstanding` lists the differences no verified claim explains as `<trigger id>|<level>|<case id>|<attribute>`, sorted, `[]` when none; a complete trigger with an empty set is verified. Past 256 unexplained differences `complete` is false, `outstanding` and `evidence_digest` are left out, and the reason is `more than 256 unexplained differences`: no finding can list them. `evidence_digest` is the sha256, in hex, of canonical JSON (keys sorted, no whitespace, numbers as written) of `head_sha`, `base_sha`, the outstanding ids, the base and head observations of every case with an outstanding difference, and the sorted fixture hashes of the trigger's valid runs at those levels: the same ids over other observations give another digest. A finding that explains the differences carries the ids in `explains` and the digest in `evidence_digest`, so a host can tell it judged this evidence.
- A trigger that selects `L4` is covered when no claim of it is unverifiable and its address and changed attribute (a `dynamic` block by the blocks it generates; never a `lifecycle` argument) are in a planned example: in the example's own directory, or under a module call the record's `module_calls` ties to the trigger's directory. An inert entry's triggers are inert; anything else is unverifiable. Refuted outranks covered.

The output has the shape of [claims-eval.schema.json](schema/claims-eval.schema.json). It exits 0 with the report, or 2 on bad input.

### bundle

`mtf-facts bundle` turns a `lint --json` report into the facts bundle a model reviewer reads, and the injected rule text for the rules the report left `not_evaluated`.

```sh
mtf-facts bundle --report lint.json --skills path/to/terraform-module-reviewer \
  --out bundle.txt --injection injection.md \
  --head-sha <sha> --merge-base-sha <sha> --binary-sha256 <sha256> \
  [--schema-handoff <file>] [--schema-files <file>]... [--profile-dir <dir>]
```

`--skills` is the reviewer skill directory at its pinned commit. From it, the bundle reads the rule registry, `references/rule-facts.md`, and the rule text under `references/engine/`. `--out` and `--injection` must be different files. `--head-sha` and `--merge-base-sha` take 40 or 64 lowercase hex characters, and `--binary-sha256` the pinned SHA-256 of the binary that wrote the report. Give `--schema-handoff`, `--schema-files` and `--profile-dir` exactly as the lint run had them.

These envelope fields of the report must equal their pins: the schema version, both commits, the binary SHA-256, the SHA-256 of the registry, the profile SHA-256 and the schema inputs. The report must also come from a run with `--base`, name only tool and candidate rules of the registry, give every `not_evaluated` entry a reason code, give each candidate a well-formed id that no other candidate has, and carry schema coverage only when it read a schema handoff. Any mismatch exits 2.

It prints the mode to stdout and exits 0:

- `judgment`: it wrote `--out` and `--injection`. Both files must not exist yet; when the second write fails, the first file is removed.
- `full`: the tool findings and candidates alone are over the 48 KiB cap, or the injected rule text is over its 24 KiB budget. It writes no file and gives the reason on stderr.

`--out` is not plain JSON. It is a framed region:

```text
FACTS-BUNDLE-BYTES <n>
FACTS-BUNDLE BEGIN
<json-string>
FACTS-BUNDLE END
```

`<json-string>` is one line holding the bundle as a JSON string with every byte outside printable ASCII escaped. The bundle inside that string has the shape of [facts-bundle.schema.json](schema/facts-bundle.schema.json), and `<n>` is the byte length of the string line. The 48 KiB cap counts that line. Over the cap, sections are shed lowest priority first (`file_index`, `schema_coverage`, `interface_diff`, `area_rules`), never `tool_findings` or `candidates`.

The bundle carries `schema_version: 1`, `module_areas` (the report's `scope.module_areas`) and `in_repo_downgrade` together with `area_rules`, as one ownership unit: they are shed together, last, as `area-rules`. A report without `scope.module_areas`, from an older release, or with a module or downgrade area longer than 1024 bytes sheds the unit from the start. A bundle without `schema_version` is a legacy bundle.

### merge

`mtf-facts merge` reads the bundle file `mtf-facts bundle` writes. It checks a model reviewer's reply to that bundle and merges the two into the final findings. The reply is untrusted: it is read with exact keys, no duplicate key, no trailing data and a 1 MiB cap.

```sh
mtf-facts merge check bundle.txt reply.json
mtf-facts merge final --registry rule-facts.md [--report lint.json] [--templates templates.json] \
  bundle.txt reply.json [repair.json]
mtf-facts merge counts bundle.txt reply.json [repair.json]
```

The reply is one JSON object with the arrays `candidate_decisions` and `findings` and, optionally, a `counts` object, in the shape of [model-output.schema.json](schema/model-output.schema.json). `counts` holds the three counts a reviewer's summary line carries: `instruction-like-text-from-others`, `conversation-claims-unread` and `review.quoted-claim-unverifiable`. When present, it has exactly those three keys, each a natural number written as decimal digits, 0 included and at most 9007199254740991; anything else makes the reply invalid.

`merge check` prints `{"result":"valid"}` and exits 0, or `{"result":"repair","reasons":[...]}` and exits 1 when the reply needs the repair turn. Its reasons are fixed text and quote nothing from the reply.

`merge final` prints the merged findings, in the shape of [merged-findings.schema.json](schema/merged-findings.schema.json), and exits 0. It merges the first reply when that one is valid, and otherwise the repair reply, which must then be given: an empty file when the repair turn returned nothing. A repair reply that is still invalid keeps only its valid entries; a candidate it leaves undecided becomes `review.check-not-run`. `--report` takes a `lint --json` report, whose index derives the subject a lead finding dedups by. `--templates` takes the per-rule host text for confirmed entries the model gave no text for. A tool finding keeps the `predates_change` its bundle entry carries, which the host copies from the confirmed candidate; an `llm` or `host` finding never has it. A lead finding of `compat.expression-behaviour-changed` or a `claim.*` rule may carry `explains`, up to 256 difference ids as claims-eval lists them in `outstanding`, and `evidence_digest`; on any other rule, or malformed, they make the finding invalid. Lead findings that dedup into one and carry the same digest explain the union of their ids.

A lead finding on a rule that is not an `llm` rule is kept only where the bundle's `area_rules` lists that rule `not_evaluated` for the finding's area, or under `.` for a cross-area rule. In a bundle with `schema_version` 1 the area is the one [`facts.AreaOf`](#module-ownership) gives with the bundle's `module_areas`, after the two-segment `examples/<n>` and `modules/<m>` directory of a path rule, which is its own area when the bundle lists it. A legacy bundle, without `schema_version`, maps paths as before module areas and has no downgrade. A `schema_version` other than 1, or one without all of `area_rules`, `module_areas` and `in_repo_downgrade`, or with an area that is not a clean relative path, exits 2.

A model severity off its rule's list is clamped to the nearest permitted one, as before, with one exception: exactly `MEDIUM` on `compat.variable-removed`, `compat.variable-now-required`, `compat.type-narrowed`, `compat.default-changed` or `compat.generated-policy-narrowed`, in an area of the bundle's `in_repo_downgrade`, is kept. `merge final` writes the number of clamped severities to stderr when there are any.

`merge counts` takes the replies as `merge final` does, picks the same reply, and prints its counts as `{"counts":{...}}`, or `{"counts":null}` when that reply has none, and exits 0. A reply without counts has none: no count is ever read as 0 for its absence. A repair reply that is still invalid keeps its counts when they are well formed. `merge counts` does not load the registry, the report or the templates, so it can succeed where `merge final` fails: a host reads the counts only after `merge final` succeeds.

### profile-check

```sh
mtf-facts profile-check --profile-dir <dir> [--gates] [--downgrade-ids]
```

First reads the directory with the walker `profile-snapshot` uses and exits 2 on anything it refuses (a link, a special file, a bad name, another device or a bound), so a profile cannot pass the check and then fail the snapshot. Hosts run it on the snapshot directory. It then reads the profile as `lint --profile-dir` does and checks it, options included. It prints one JSON object: `valid`, `problems`, and for a valid profile `options` and `relaxing`. `--gates` adds `gates`, the gate table, and `gated_rules`, the rules this profile gates; `--downgrade-ids` adds `downgrade_ids`. Exit 0 for a valid profile, 1 for an invalid one, 2 when the directory is missing, a link or cannot be read.

### profile-snapshot

```sh
mtf-facts profile-snapshot --profile-dir <dir>
```

Copies the profile directory into a new 0700 temporary directory and prints `{"dir", "tree_sha256", "profile_sha256"}` for the host. Each directory is opened relative to its parent and each file with `O_NOFOLLOW|O_NONBLOCK`, checked with `fstat` as the regular file the listing saw and on the source's `st_dev`, and copied from that descriptor. A link, a special file, another device, a name that is not UTF-8 or holds a control character or DEL, more than 256 entries, more than 8 MiB or a path more than 8 levels deep exits 2, and so does a copy that does not read back the same. The host reads options and gates only from the copy and removes it when done. `profile_sha256` is `null` when the copy is not a valid profile.

### ledger

`mtf-facts ledger` lists the provider fields of each resource and data source type the module in the given directory declares, nested fields included, from one or more `terraform providers schema -json` files:

```sh
mtf-facts ledger path/to/module --schema-files schema.json [--schema-files more.json]... \
  [--check coverage-ledger.json] [--registry rule-facts.md]
```

It reads only that directory, not `modules/*` or `examples/*`. At least one `--schema-files` is required. The run exits 2, with no inventory, when:

- a provider the module uses is in no schema file;
- a type is not in its provider's schema;
- the directory has no `.tf` file or no resource or data block;
- a block is an ephemeral resource;
- a file is one the parser cannot model.

The JSON output has an `envelope`, the `types` and their fields, and `problems`. The envelope holds the tool, `registry_sha256` (`null` without `--registry`), and the hash of each schema file and of the ledger, each named by its base name.

`--check` reads the author's coverage ledger, in the shape of [ledger.schema.json](schema/ledger.schema.json), and lists each problem it finds:

- a leaf field with no disposition or more than one;
- a field not in the schema;
- a type the module does not declare, or one listed twice;
- an input or output the module lacks;
- an input on a computed-only field;
- a disposition other than `intentional_omission` or `not_applicable` on a block or nested attribute that has fields.

Without `--check`, `problems` is `null`.

| Code | Meaning |
|------|---------|
| 0 | The inventory, and with `--check` a complete ledger. |
| 1 | With `--check`: at least one problem, listed in `problems`. |
| 2 | An error: bad input or a limit. |

### schema-diff

`mtf-facts schema-diff` compares two `terraform providers schema -json` files, the current and the target provider version, for the resource and data source types a module tree uses:

```sh
mtf-facts schema-diff --from current.json --to target.json --types-from path/to/module
```

All three flags are required. It writes the report as JSON, in the shape of [schema-diff.schema.json](schema/schema-diff.schema.json), and exits 0, with changes or none. Each schema file is recorded by base name and SHA-256 only. A provider the tree uses that either file lacks, bad input or a limit exits 2.

### fixture-check

`mtf-facts fixture-check` checks the scratch tree a verification ladder level will run against a default-deny allowlist, before any Terraform command runs on it, and hashes the tree:

```sh
mtf-facts fixture-check scratch/head --json
mtf-facts fixture-check scratch/l2 --level L2 --pins pins.json --schema schema.json --json
mtf-facts fixture-check scratch/l3 --level L3 --resource aws_rds_cluster --pins pins.json --json
```

The output, in the shape of [fixture-check.schema.json](schema/fixture-check.schema.json), is `{"schema_version":1,"ok":...,"tree_sha256":"...","violations":[{"file","line","rule","detail"}]}`. `line` is 0 for a whole file and `file` is `.` for the whole tree. `detail` names the refused function, block type or argument when that name is a plain identifier; it is left out otherwise, so no text from the tree is echoed. At most 200 violations are listed, then one `limit.violations` entry.

`tree_sha256` is the SHA-256 of one `name NUL type NUL content-sha256 LF` line per directory entry, sorted by name. The type is `file`, `file+x` (any exec bit set), `dir`, `symlink` or `special`, so a umask does not change the hash. It identifies a tree that passes; on a violation it covers what was read.

`--expect` takes the manifest the trusted template wrote, in the shape of [fixture-manifest.schema.json](schema/fixture-manifest.schema.json):

```json
{"schema_version": 1, "files": {
  "main.tf": {"blocks": [{"type": "resource", "labels": ["terraform_data", "ref_0"]}]},
  "head.tf": {"blocks": [{"type": "output", "labels": ["head"]}],
    "single_expressions": [{"block": "output", "labels": ["head"], "attribute": "value"}]}}}
```

With it, each file's top-level blocks must be exactly the listed set, by type and labels in any order, and no other `.tf` file may exist (`manifest.block-set`). The manifest binds `.tf` files only; at L2 the test file is checked by its grammar and never by the manifest. A file has at most one single expression, so each splice (base and head) goes in its own file. Its attribute must exist, its block must be the last block of the file, the attribute the last argument or block of its block, and no comment may start at or after the attribute (`manifest.expression`). A spliced expression that closes its block to add another, ends its attribute to add an argument, or opens a comment to swallow template text fails one of the two.

`--pins` takes `{"providers":[{"source","version"}]}`, optionally with a `sha256` per entry: the provider versions the host installed, each version exact and each source once.

The grammar, by level (`--level`, default `L1`):

- Files: only names matching `^[A-Za-z0-9][A-Za-z0-9_.-]*\.tf$` directly in the directory, no `override.tf` or `*_override.tf`. Any subdirectory (`.terraform` included), symbolic or hard link, special, hidden, `.tf.json`, `.tfvars` or lock file is a violation. At most 64 entries, 256 KiB a file and 1 MiB in all.
- Top-level blocks: `terraform`, `variable`, `locals`, `output`, `provider` and `resource`. `data`, `module`, `moved`, `import`, `removed`, `check`, `ephemeral`, `action` and every other block are refused.
- `terraform`: only `required_providers`, each entry `{ source, version }` as literal strings, each key once, equal to a pin, with the local name equal to the last part of the source and a provider of the level. `backend`, `cloud`, `provider_meta` and `required_version` are refused.
- `variable`: `type`, `default`, `description`, `sensitive`, `nullable` and `validation`. `output`: `value`, `description`, `sensitive`, `depends_on` and `precondition`.
- `resource`: `terraform_data` at every level. L3 adds exactly one resource of the `--resource` type, which must be a curated type: `aws_rds_cluster` or `aws_db_instance`. Adding a curated type takes an audit of every argument in the pinned provider schema, nested ones included, for any that reads a local file or other local source; the audit is recorded in the type's test. A type with such an argument is not curated. No resource argument, nested ones included, may be named `filename`, `source`, `file`, `path`, `content_base64` or end in `_file`, `_filename` or `_path`. `provisioner` and `connection` blocks are refused, also as `dynamic` labels; `lifecycle` takes `create_before_destroy`, `prevent_destroy`, `ignore_changes`, `replace_triggered_by`, `precondition` and `postcondition`. The `provider` argument names a provider of the level.
- `provider`: `terraform` with no argument, at every level. At L3, `aws`, which must be set: `region` as a literal matching `^[a-z]{2}(-[a-z]+)+-[0-9]$`, `access_key` and `secret_key` as nonempty literal strings, `skip_credentials_validation`, `skip_requesting_account_id`, `skip_metadata_api_check` and `skip_region_validation` as literal `true`, and an `endpoints` block that has the key of the service under test (`rds` for both curated types) and whose every entry is `http://127.0.0.1:1` or `http://[::1]:1`. Every other argument or block, `alias` included, is refused.
- Expressions: every function call is on a pinned pure-function list (no `file*`, `templatefile`, `templatestring`, `abspath`, `pathexpand`, `dirname`, `basename`, `timestamp`, `plantimestamp`, `uuid`, `uuidv5`, `bcrypt`, `rsadecrypt`, `yamldecode`, `ephemeralasnull` or `provider::` function). Every reference starts with `var`, `local`, `each`, `count`, `self`, `terraform_data` or the type under test, and a `for` expression, template `for` directive or `dynamic` iterator may not bind one of those names or `path`, `terraform`, `data`, `module`, `ephemeral`, `provider` or `resource`. A heredoc may only be a plain string.

L2 runs module logic under `terraform test` with every provider mocked. Its `.tf` files follow the grammar above, with these changes:

- Files: exactly one `*.tftest.hcl` file besides the `.tf` files (`test.file-count`). `.tftest.json`, `*.tfmock.hcl` and a `tests/` directory are refused like any other file or directory.
- No `provider` block, in a `.tf` file or the test file: every provider is a `mock_provider`. A mock still starts the pinned provider plugin: terraform calls its GetProviderSchema, GetResourceIdentitySchemas and ValidateResourceConfig on the tree's configuration, never Configure or plan. So the runner must start L2 under the same provider sandbox profile as L3, and the file-argument denylist stays. Resources and data sources of any type are allowed. The provider of each one (its `provider` argument, or the prefix of its type) must be declared in `required_providers` as a pin and have a `mock_provider` (`test.provider-not-mocked`). So must every declared provider. The built-in provider needs no mock, and its `terraform_remote_state` data source is refused.
- `data` and `moved` blocks are allowed. `moved` takes `from` and `to`, each a static resource address, never a module or data address. References may also start with `data` or any resource type name (`^[a-z][a-z0-9]*_[a-z0-9_]+$`).

The test file holds only these blocks:

- `mock_provider "<name>"`, with no argument (no `source` of mock files, no `alias`), holding `mock_resource` and `mock_data` blocks with only `defaults`, and `override_resource` and `override_data` blocks.
- `override_resource` and `override_data` with `target`, a static address of a resource or data source in the tree, and `values`.
- Each mock and override block, `mock_provider` included, may also set `override_during = plan`, so a plan-only run sees the mock values. `apply`, the default, is refused. The result then carries `"override_during_plan": true`.
- Every data source a run reads must have fixed values. A mock generates a random value, on each run, for every computed attribute no mock value covers. So a `mock_data` block of its type in its provider's mock, or an `override_data` targeting the whole block (no instance key) at the top level or in the mock, must give a non-null literal to every computed attribute of the type (`test.data-not-mocked`). That includes optional and computed ones, nested attributes, and every nested block that holds a computed attribute (with at least one element for a list, set or map). An override inside one `run` does not count.
- When the test file sets `override_during = plan`, every resource of a mocked provider needs the same cover, from a `mock_resource` or a whole-block `override_resource` (`test.resource-not-mocked`). Without it, computed resource values stay unknown at plan and need nothing.
- Which attributes are computed comes from `--schema`: the `terraform providers schema -json` file the acquisition phase wrote for the pinned providers. A type it lacks is not covered. Without `--schema`, a tree with a data source of a mocked provider, or a mocked resource under `override_during = plan`, fails with `test.schema-required`. `--schema` is refused at L1 and L3.
- `variables`.
- `run`, which must set `command = plan` (`test.command-not-plan`, also when the command is left out, since the default is apply), with `assert` (`condition`, `error_message`), `variables`, `override_resource`, `override_data`, `plan_options` with only `mode = normal` (`test.plan-options-denied`), and `expect_failures` naming `var`, `output`, data or resource addresses. `module`, `override_module`, `providers`, `state_key` and every other argument or block are refused.

Mock values are not provider behaviour, and a mock can make computed attributes known at plan. So an L2 run never evidences `expression-output-unknown`, `expression-output-length-known` or `plan-attribute-unknown`, with or without `override_during_plan`. It evidences module logic only: keys, counts, addresses and known values.

Mock defaults, override values and variables must be literals: strings without interpolation, numbers, bools, `null`, and lists and objects of them with bare or quoted keys (`test.value-not-literal`). Asserts use the pure functions and references from `var`, `local`, `output`, `run`, `data` or a resource type. A test file `provider`, `test` or other block is refused.

The check reads syntax only and evaluates nothing. It does not bound what a passing tree can make Terraform allocate, such as a large `count` or `range`: the host runs plan under its own memory and time limits.

| Code | Meaning |
|------|---------|
| 0 | The tree passes. |
| 1 | At least one violation. |
| 2 | An error: bad flags, a bad pins file or a directory that cannot be read. |

### rules, version and completion

```sh
mtf-facts rules
mtf-facts version
mtf-facts completion bash
```

`rules` lists the implemented rules, one per line with its tier (`tool` or `candidate`), sorted by id. `version` prints the binary version, as does `mtf-facts --version`. `completion` prints a shell completion script for `bash`, `zsh`, `fish` or `powershell` to stdout.

## How rules are evaluated

All 42 tool and candidate rules of the reviewer's rule registry are implemented. CI checks that the `mtf-facts rules` listing equals the registry's tool and candidate rules, tier for tier, against a registry pinned by commit and SHA-256.

### Areas

Areas are the root module, each `modules/<name>` and each `examples/<name>`, parsed one directory at a time. A change under `.github/` or to a root profile file such as `.editorconfig` counts as the root. Candidate subjects are scoped to their area, `<area>:<address>`, such as `.:var.name`.

A tree outside that layout, such as a monorepo with `infra/modules/network` called from `infra/live/prod`, gets one more kind of area, `module`: each other directory that directly holds a `.tf` or `.tf.json` file, at head or at base. A file belongs to the nearest such directory above it, whatever its extension, so a change to `infra/modules/network/main.tf` puts that module in scope, not the root. Directories with a `tests`, `wrappers` or `examples` segment, a dot segment or a top-level `modules/` are not discovered, and their files stay the root's as before, so a module under such a directory is read as part of the root. A module area takes only the `compat.*` and `coverage.*` rules; the convention, profile and layout rules (`structure.*`, `profile.*`, `scope.*`, `examples.*`) are `not_applicable` there, `scope.tooling` included, so a scanner config file added under a module is not judged. A local `source` that resolves to a module area counts as a call in `callers` and `scope.areas_calling`. A directory that held configuration at base and is a link at head stays an area and is `not_evaluated` for every rule that applies; a link that never held configuration is not an area, and links are never followed. A tree with more than 512 discovered areas is a limit: exit 2, `area_limit_exceeded`, no report. Reports of a tree with no discovered area are unchanged.

With a base tree, the root is in scope only when the change touches it. A cross-area rule has one entry under `.` for its pass, whether or not the root is in scope, and one more under each other area that holds one of its findings or candidates: the deepest area directory that holds the item's `file`. The `.` entry is `not_evaluated` when the pass is, and otherwise has the status of the items in the root area, `clean` when there are none. A `structure.version-floor-understated` candidate points at the caller's own floor, or at the call when the caller declares none, so it is filed under the caller's area; it is in the change when the caller's floor, the callee's floor or the call is a changed line. A cross-area pass reads every area of the module, in scope or not, and keeps only the findings and candidates of the areas in the change. When any area it reads cannot be evaluated, untouched areas included, that entry is `not_evaluated` with one reason code (`parse_error`, `unsupported_file`, `symlink` or `limit_exceeded`) and no findings or candidates.

Files Terraform ignores (a leading `.`, a trailing `~`, `#...#`) are skipped. A directory gets `not_evaluated` for its configuration rules, never `clean`, when it has a parse error, a `.tf.json` file, an override file, a `.tf` file that is a link or cannot be read, or a file over the token, nesting or unary operator limits. Path rules still run: a link where a path rule looks for a file counts as present and is never followed. An area directory that is a link or cannot be read gets `not_evaluated` for every rule.

### Module ownership

`facts.AreaOf(file, module_areas)` is the one ownership function, shared by the engine and the merge: the top-level `modules/<m>` or `examples/<n>`; a module area whose own path the file is; the root for a file under a `tests`, `wrappers`, `examples` or dot segment; the nearest module area above the file; the root. It refuses a path that is empty, absolute, holds a backslash or a drive letter, is not clean or has a `..` segment.

`module_callers` has one entry per head area of kind root, submodule or module (never example) with a file the change adds, changes or removes, sorted by `module`: `{module, complete, callers: [{area, file, line, file_changed}]}`, callers sorted by area, file and line. A module the change removes gets no entry. Callers come from every head `.tf` file, those under `tests/`, `wrappers/` and `examples/` included; `.git` and `.terraform` are never read. A literal source starting `./` or `../` is joined to the calling file's directory and must name exactly a discovered area path: `.`, a submodule or a module. Registry addresses, `git::`, `hg::`, `s3::`, `gcs::`, `http(s)://`, `github.com/` and `bitbucket.org/` sources are ignored. `file_changed` is true when the change adds the caller's file, changes its bytes or, with `--diff`, lists it. More than 4096 caller records exit 2.

`complete` is one value for the whole tree, repeated on every entry. It is false when a local source escapes the tree or names no area (no ancestor is tried), a source is empty, not a literal, absolute, a drive letter or of no known form, a `.tf` or `.tf.json` file cannot be parsed or read, any entry is a link, special or unreadable, the tree holds a file that may call a module but that the engine does not parse (a `.tftest.hcl` or `.tftest.json` test file, a `terragrunt.hcl` or any other `.hcl` file except `.terraform.lock.hcl`, or an OpenTofu `.tofu` or `.tofu.json` file), or a change path is not a clean relative path. A changed or unchanged such file anywhere in the head tree is enough, so a Terragrunt caller beside the `.tf` ones blocks the downgrade.

### Profile options

A profile may hold a `profile-options` constants block: `format: 1`, `consumers` (`public` or `in-repo`), `partitions` (`portable` or `fixed`), `examples` (`required` or `none`) and `release` (`registry` or `none`), every key required. Without it the options are the defaults `public`, `portable`, `required`, `registry`; any other value relaxes the review. An unknown format, key or value, or another unknown block, makes the profile invalid (`profile_unavailable`). A key written as `<key>:` with nothing after the colon has the empty value: an empty list, or a single value that is not set; `<key>: ` with a trailing space stays invalid. Only these keys may be empty: `required-files` `example` and `example-dirs` (and only with `examples: none`, which needs both empty), `profile-owned-files` `verbatim` and `workflow-dir`, `tooling` `files` and `dirs`, and `banner` `line`. `template-destinations` may hold no key at all. An empty value judges nothing: no verbatim file and no workflow directory for `scope.profile-file`, no banner for `scope.readme-banner`, no scanner file or directory for `scope.tooling`, and no template for `profile.template-drift`.

The gate table is held in the binary; `profile-check --gates` prints it. A listed value makes only the listed rules `not_applicable` with reason code `profile_option`:

| Option | Rules |
|--------|-------|
| `partitions: fixed` | `structure.arn-partition-hardcoded` |
| `examples: none` | `examples.argument-undemonstrated`, `examples.disabled-call-missing`, `examples.feature-undemonstrated`, `examples.submodule-missing`, `examples.versions-missing` |
| `release: none` | `profile.changelog-handwritten`, `profile.commit-type-mismatch`, `profile.migration-notes-misplaced`, `profile.owner-identifier`, `profile.provider-meta-missing`, `profile.release-config-changed`, `profile.template-drift`, `profile.title-misspelling`, `profile.title-uninformative`, `profile.unrelated-change-bundled` |

`profile.required-file-missing` is never gated; a profile with `examples: none` has no example entries for it to require. `consumers: in-repo` gates nothing: it only feeds the downgrade below.

### In-repo downgrade

`in_repo_downgrade` lists the modules of `module_callers` that have at least one caller and every caller's `file_changed` set, when the profile sets `consumers: in-repo`, the change is `base` or `diff`, the run is not `--full-tree`, and `complete` is true. The candidates of `compat.variable-removed`, `compat.variable-now-required`, `compat.type-narrowed`, `compat.default-changed` and `compat.generated-policy-narrowed` in those areas carry `downgrade: "in-repo"` and `severities: ["MEDIUM"]`.

Depth limitation: `depth` does not read module areas. Examples nested under a module area, such as `infra/modules/network/examples/basic`, are out of its scope.

### Scope rules at area boundaries

No area reads `tests/` or a directory more than one level under `modules/<name>/`, though both hold boundary directories for the scope rules. So these rules are `not_evaluated(unsupported_file)` in these cases:

- `scope.module-call` and `scope.provider` at the root, when the change adds or edits a `.tf` or `.tf.json` file, or any link, special or unreadable entry, under `tests/`, or when `tests` itself is such an entry.
- `scope.module-call` and `scope.provider` at `modules/<name>`, when the change adds or edits a `.tf` or `.tf.json` file, or any link, special or unreadable entry, in a nested directory there, or adds a link, special or unreadable entry directly in `modules/<name>/` that is not a `.tf` or `.tf.json` file (one that is keeps its own reason code).
- `scope.new-submodule` at `modules/<name>`, on the same entries when they lie in a nested directory the base lacks, or are a link, special or unreadable entry.

The `modules/<name>` cases exempt a regular `.tf` file in a nested directory that parses with no issue and holds only `variable`, `output`, `locals`, `moved` and `removed` blocks. For a new `modules/<name>`, a candidate from its own top level comes first, and the nested entries give `not_evaluated` only when that top level gives none.

### Platform

The build supports Unix only: the tree reader opens files with `O_NOFOLLOW`.

## Releases

Each release publishes one binary per platform, `mtf-facts_<tag>_darwin_amd64`, `mtf-facts_<tag>_darwin_arm64`, `mtf-facts_<tag>_linux_amd64` and `mtf-facts_<tag>_linux_arm64`, a `SHA256SUMS` file, and a build provenance attestation for each binary. The build is reproducible: the same tag and the Go version in `.go-version` give the same bytes on any host. [scripts/build-release.sh](scripts/build-release.sh) fixes the build settings known to change the output: `-trimpath`, an empty build ID, the toolchain, `CGO_ENABLED=0`, `GOFLAGS=-mod=readonly`, `GOWORK=off`, an empty `GOEXPERIMENT`, `GOFIPS140=off` and the architecture levels. Go embeds no build time; the module version comes from the tag.

### Cut a release

Releases follow the [Conventional Commits](https://www.conventionalcommits.org/) in the titles of merged pull requests, which become the commit subjects on `master`. `pr-title-lint` checks each title.

1. A merge to `master` that holds a `feat`, `fix`, `perf` or `revert` commit or a breaking change starts `automated-release`. It bumps `version.json`, adds the new section to [CHANGELOG.md](CHANGELOG.md) and opens and merges `chore(release): vX.Y.Z`. Other commit types wait for the next releasable one.
2. `tag-release` tags that merged commit with the version.
3. The tag starts the `release` workflow. It builds every binary, attests them and creates the GitHub release with the changelog section as its notes. The build job holds no token that can sign or write. The attestation runs in the `release` environment, so it waits for approval once that environment has reviewers. Only the job that creates the release can write to the repository. A last job asks [modulestf/homebrew-tap](https://github.com/modulestf/homebrew-tap) to update its formula.

The first two steps need the secret `RELEASE_TOKEN` in the `release-automation` environment, which only `master` may use: a fine-grained token of an administrator with contents and pull requests write. A tag or pull request made with the default `GITHUB_TOKEN` starts no workflow, and only administrators may create `v*` tags. The last job needs `HOMEBREW_TAP_TOKEN`, which may run workflows in the tap; without it the tap picks up the release on its daily schedule.

To cut a release by hand, as before, tag a commit on `master` whose `ci` run passed and push the tag. The workflow refuses a tag whose commit is not on `master`. A pre-release tag such as `v0.5.0-rc.1` is published as a pre-release.

```sh
git tag -a v0.5.0 -m v0.5.0 <commit>
git push origin v0.5.0
```

A pushed tag publishes a public release. Do not reuse or move a tag: delete a bad release and cut the next version.

### Verify a release

Check the attestation of a downloaded binary. `-R` alone accepts an attestation from any workflow on any ref of the repository, so also name the release workflow, the tag and GitHub-hosted runners:

```sh
gh attestation verify mtf-facts_v0.1.0_linux_amd64 -R modulestf/facts \
  --signer-workflow modulestf/facts/.github/workflows/release.yml \
  --source-ref refs/tags/v0.1.0 \
  --deny-self-hosted-runners
```

Check its SHA-256 against the release:

```sh
sha256sum --ignore-missing -c SHA256SUMS          # Linux
shasum -a 256 --ignore-missing -c SHA256SUMS      # macOS
```

Rebuild the release from source and compare every SHA-256 with the published one. This needs `git`, `curl` and Go; a different local Go downloads the release's toolchain.

```sh
make release-verify TAG=v0.1.0
```

### Update a consumer pin

A consumer, such as the verify job of a hosted review workflow, pins one version and one SHA-256 per platform next to the step that downloads `mtf-facts`. To move the pin:

1. Verify the new release as above: the attestation and the rebuild.
2. Read the SHA-256 of each platform the consumer runs on from that release's `SHA256SUMS`.
3. Change the version and every SHA-256 in one commit. A version without its matching SHA-256 values fails the download check, as intended.
4. Keep the attestation check in the download step, with the same flags as above and the pinned tag, so the pin rests on both the hash and the provenance:

   ```sh
   gh attestation verify "mtf-facts_${VERSION}_linux_amd64" -R modulestf/facts \
     --signer-workflow modulestf/facts/.github/workflows/release.yml \
     --source-ref "refs/tags/${VERSION}" \
     --deny-self-hosted-runners
   ```

## License

Apache-2.0. See [LICENSE](LICENSE).
