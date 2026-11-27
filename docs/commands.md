# Command reference

Each command's flags, report and exit codes. `mtf-facts <command> --help` prints the same flags and exit codes.

Every command that reads files, all but `profile-snapshot`, `rules` and `version`, takes `--timeout <duration>`, default `60s`, which must be greater than zero, and refuses output over 16 MiB. A run past the timeout writes `mtf-facts: timeout` to stderr and exits 2 with nothing on stdout; `depth` alone fails closed instead, with its `deep-unwalked` report and exit 0. Output over the cap writes `mtf-facts: output over 16777216 bytes` and exits 2 with nothing on stdout. The cap holds each file `bundle` writes too, and a refused `bundle` run writes no file.

## lint

```sh
mtf-facts lint <dir> [--base <dir> | --diff <file>] [--full-tree] [--json [--ai]] [--candidates] [--not-evaluated] [--timeout <duration>]
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
| `--candidates` | In the text report, list each candidate, not only the count per rule. |
| `--not-evaluated` | In the text report, list each rule and area not evaluated, not only the count per reason. |
| `--timeout <duration>` | Stop a run that takes longer. Default `60s`. |
| `--stats-file <file>` | Also write a timing and memory record. Every command a review runs takes it; see [Timing and memory stats](usage.md#timing-and-memory-stats). |
| `--registry <rule-facts.md>` | Record the registry hash. |
| `--profile-dir <dir>` | The module profile directory. |
| `--schema-files <file>` | A `terraform providers schema -json` output. Repeatable. |
| `--schema-handoff <file>` | The schema handoff the schema job writes, gzipped or not. |
| `--head-sha <sha>` | Record the commit the head tree came from. |
| `--merge-base-sha <sha>` | Record the commit the base tree came from. |

The `--json` report holds the envelope, one manifest entry per rule and area (see [Areas](rules.md#areas) for a cross-area rule), findings in the reviewer's findings schema, and candidates. A `structure.argument-order` candidate carries `predates_change: true` when every item out of order in the head block, by name, is also out of order in the base block of the same address, so the same subject: the defect is older than the change. A change that puts a new item out of order does not predate it. No other rule sets it today; it is for a rule whose check already reads the base block. It also holds:

- `scope`: how the change was given (`none`, `base` or `diff`), `full_tree`, and three disjoint lists of areas. `areas_checked` have their own manifest entries. `areas_calling` are the other areas that call a module listed in `callers`. `areas_left_out` are the rest, so no area is dropped without a trace. `module_areas` lists every discovered area of kind `module`, at head or at base, sorted: the set that decides which area owns a file (see [Module ownership](rules.md#module-ownership)).
- `callers`, with a change only: each root or submodule area the change touches whose `versions.tf` or `terraform` block, or `variables.tf` or a `variable` block, changed, with its version floors at base (`null` for a new module) and at head, and every area that calls it by a local path: the call's file and line, whether that area is checked, the floors it declares, and the ids of the `structure.version-floor-understated` candidates filed in it. A floor is an `address` (`required_version` or `required_providers.<name>`), the provider `source` and the `constraint` as written. Callers under `wrappers/` are not listed: no area reads that directory.

- `module_callers`: always written, `[]` without a change. See [Module ownership](rules.md#module-ownership).
- `in_repo_downgrade`: always written, sorted, often `[]`. See [In-repo downgrade](rules.md#in-repo-downgrade).
- On a candidate, `downgrade: "in-repo"` and `severities: ["MEDIUM"]` when the in-repo downgrade covers it; absent otherwise.
- A manifest entry for a rule the profile's options gate is `not_applicable` with `reason_code` `profile_option` and a reason text. See [Profile options](rules.md#profile-options).
- A rule whose check fails inside the tool (a panic, a bug of the tool and never of the tree) is `not_evaluated` with `reason_code` `internal_error` and a fixed reason text, in each area where it ran; the other rules and the report are unaffected, and the rule must be judged another way.
- In a run with no change (no `--base`, no `--diff`), a rule that needs the base is `not_applicable` with `reason_code` `no_change_given` and a reason text in each area of its kinds: there is nothing to compare, which does not mean the rule never applies. A rule the profile's options gate keeps `profile_option`. With a change, a file whose base content is unknown still makes the rule `not_evaluated(no_base)`.

The text report prints the findings grouped by rule, each with its own summary and fix, one line that counts the candidates by rule and one that counts the rules not evaluated by reason (top five rules each, the rest counted), a line saying how to pass a profile when `--profile-dir` is not given (the default profile is `.agents/skills/terraform-module-maintainer/profiles/terraform-aws-modules` in [modulestf/modulestf](https://github.com/modulestf/modulestf)), and one saying how to pass a schema when schema rules are not evaluated for want of one. It prints a `scope:` line, and with a change one line per changed module with the floors the change moves, and one line per caller with its own floor at those addresses. The JSON schemas are in [schema/](../schema/).

### AI report

`--json --ai` writes the parts of the report a reviewer decides on, as one line of JSON. The exit code is the same as without `--ai`. Compared with the full report:

- `manifest` keeps only its `finding`, `candidate` and `not_evaluated` entries, and the `not_applicable` entries with `reason_code` `no_change_given`, in the same order and shape.
- `manifest_summary` is added. `status` counts every manifest entry of the full report by status. `omitted_by_family` counts the `clean` and `not_applicable` entries left out, per rule family: the rule id up to its first dot, such as `structure`. `areas` lists every area with a manifest entry, sorted. A rule with no entry in a listed area was `clean` or `not_applicable` there, or has no entry there in the full report either.
- `envelope.inputs.head` and `envelope.inputs.base` keep `tree_sha256` and give `file_count` in place of the `files` list. Every other envelope key is unchanged, `schema_inputs` with its handoff hash, commits and tree hashes included.
- `index`, `interface_diff` and `schema_coverage` are left out.
- `findings`, `candidates`, `scope`, `callers`, `module_callers` and `in_repo_downgrade` are unchanged.

The report keeps `schema_version` 2 and the keys `envelope`, `manifest`, `findings` and `candidates` with their types, and `manifest_summary` is present only in the AI form. `bundle` and `merge final --report` need the index and the file lists, so they refuse a AI report and exit 2.

### Change as a diff

`--diff` reads a GitHub pull request files list, the JSON array `gh api repos/<owner>/<repo>/pulls/<number>/files --paginate --slurp | jq add` writes, or a unified diff as `git diff` writes it, with or without `diff --git` lines. The format is detected from the first byte. Paths must be relative to `<dir>`. The base tree is the head tree with each changed file rebuilt by reversing its hunks: a removed file comes back from its removed lines, an added file is left out, and a renamed file moves back to its old path. Every context and added line must match the head file, line ending included, or the run exits 2, and so does a path that is not clean and relative, a path listed twice, or a status the tool does not know.

A `.git` directory is never read, and `--diff` refuses a path with a `.git` element, on either side of a rename, as git tracks none. Any other directory, `.terraform` included, is read and listed in the change like any other path, so a host reads a clean checkout, with no untracked `terraform init` output in it. A tree is read with caps: 10000 entries, 2 MiB a file, 262144 HCL tokens in a `.tf` file, and 64 MiB of file bytes kept for the rules. One run of lint, affected, changes, behaviour or depth parses at most 8388608 HCL tokens in all: each configuration and template parse of either tree counts, a file by its lexer tokens or, when it is parsed without lexing it first, by an upper bound of them. plan-gates, fixture-check and the per-file token re-scans are not counted. A file parsed after that budget is spent is refused as a file over the token cap is: lint reports `limit_exceeded`, behaviour an `unclassified-change`, depth `deep`, affected its `unparsed` fallback and changes the reason `limit`. A binary file (a NUL byte in its first 8000 bytes, as git judges it) whose name no rule reads, such as a build archive or a compiled cache, keeps its kind `file` and the SHA-256 of all its bytes in the envelope, but its bytes are not kept and do not count toward the 64 MiB; such files count toward their own 256 MiB a tree, and past that the run exits 2. A file of a name a rule reads (`.tf`, `.tf.json`, `.tofu`, `.tfvars`, `.hcl`, `.tfrc`, `.json`, `.yml`, `.yaml`, `.md` and a few fixed names) and every text file are kept, so a `templatefile` target of any name is read. A content reader treats a binary file it did not keep as one it cannot see: a rule that reads it by content is `not_evaluated` with `unsupported_file`, a template is unresolved, and the file's changed lines are unknown. With `--diff`, a patch for such a file is ignored and its base entry is `no_patch`, as for a binary file GitHub gives no patch for. The names kept also include `.editorconfig`, `.gitignore` and `LICENSE`, which profiles copy verbatim.

A file the diff does not list is unchanged, even when it is too large to read or cannot be read: its base entry is the head's own. With `--base`, a file too large to read is compared by a SHA-256 of its bytes, read in a stream and never kept, up to 64 MiB a file and 256 MiB a tree; past those, or unreadable, it counts as changed. A file the diff gives no patch for, as GitHub does for a binary or very large file, has no known base content. It is in `inputs.base` with kind `no_patch`, and the configuration rules of its area are `not_evaluated` with `no_base` and a reason that says the diff has no patch for it. A blank patch is no patch. A files list shows neither side's content, so in that form every entry with no patch other than an `added`, `copied` or `unchanged` one is `no_patch`, whatever its counts: a rename or a modified or changed entry with 0 added and 0 removed lines may have a binary base under a text head, and a removed file may have been empty or a file a host listed as removed because a rename moved it out of the tree. Behaviour gives such a file an `unclassified-change`, lint its base rules `no_base` and changes the reason `no_patch`. A status outside GitHub's set (`added`, `removed`, `modified`, `renamed`, `copied`, `changed`, `unchanged`) and a count that is not a number or null stop the run; a null or negative count is accepted, as counts are not read. Shapes git and GitHub never write, each of which would read as no change, stop the run too: in a files list, an entry with no `filename`, an `unchanged` entry with a patch or for a path the head tree does not hold, and a `previous_filename` on an entry that is not `renamed` or `copied`; in either form, a rename or copy whose old and new paths are the same, a hunk of no line (`@@ -0,0 +0,0 @@`) or of context lines alone, and a patch whose removed and added lines cancel out, at the same path or after a rename. A files list's patch is checked as whole hunks whatever its status, an `added` one included.

A unified diff is read as an allowlist of what `git diff` writes. Lines before the first file section are a preamble and skipped, as the message of a patch mail, and so are a last `-- ` line and the git version line after it, the signature `git format-patch` ends a mail with; non-blank input with no file section exits 2. In a diff with `diff --git` lines, each section is that line and git's extended header lines in git's order: an `old mode` and `new mode` pair of `100644` and `100755`, a change of the exec bit (git writes a change of type, such as a file to a link or a gitlink, as a removal and an addition of one path, which exits 2), or a `new file mode` or `deleted file mode` line; a `similarity index` or `dissimilarity index` line; a `rename from` and `rename to` or `copy from` and `copy to` pair, after a similarity index, whose paths are not empty, differ and match the `diff --git` line; an `index` line of two hashes of 4 to 64 hex digits and, only when no mode line is given, the mode of both sides; then a `---`/`+++` pair naming the section's paths (or `/dev/null` for an added or removed file) with at least one hunk, a `Binary files ... differ` line naming them, or a `GIT binary patch` block. A mode is `100644`, `100755`, `120000` or `160000`, and the head mode a section gives (`new mode`, `new file mode` or the `index` line's) must be the head entry's: a regular file, executable or not, a link, or a gitlink or nothing there. A section must show a change: a hunk, a binary marker, a mode pair, a new or deleted file, or a rename or copy. A section with no hunk and no binary marker whose `index` line gives two different hashes exits 2, unless it adds or removes an empty file (the empty blob `e69de29` on one side and all zeros on the other), as it would hide a change of content; so does a removed link with no hunk, as git writes its target. In a diff with no `diff --git` line, each section is a `---`/`+++` pair with at least one hunk, and the two forms do not mix. Any other line, order or combination, a carriage return included, exits 2. The fixtures under `testdata/diff-git-corpus` are real `git diff` output of one change (a mode change, an empty file added and removed, renames and a copy at 100%, a rename with a mode change or an edit, a link retarget, a gitlink bump and a binary file, with and without renames, `--binary`, `-U0` and `--full-index`, and as `git format-patch` writes it), and each must rebuild its base. A unified diff shows a rename with no hunk, an empty removed file and a mode change as such, and `--base` compares the trees, so both forms keep the content there. A unified diff's `old mode` and `deleted file mode` lines set the base exec bit, and a changed exec bit alone makes a path changed. The envelope records the diff in `inputs.diff`: its `format`, `github-files` or `unified`, and its SHA-256. A report from `--diff` has a base tree, so `bundle` accepts it.

Two schema files with one base name exit 2. The schema handoff and the schema files are read offline under size caps; a missing, oversized or malformed one exits 2. The handoff is read only when `--head-sha`, `--merge-base-sha` and the `.tf` tree hashes of the head and `--base` trees equal the ones it records; otherwise every lookup in it is `schema_unavailable`.

The envelope's `schema_inputs` records the handoff's hash, commits and tree hashes, and the hash of each schema file, named by its base name. With schema files and no handoff, it records the hash of each schema file and the handoff keys are null. `handoff_sha256` hashes the file's bytes as read, so a gzipped and a plain copy of the same handoff hash differently: a host that checks it must hash the form it passed.

The `coverage.*` rules read the bound handoff when one is given, and never fall back to the schema files. With schema files and no handoff, as in local `lint`, they resolve each block's provider source from `required_providers` and read the files as the latest schema; a provider no file has stays `schema_unavailable`. A schema file records no provider version, so its schema facts carry the version `schema-file`, and `coverage.argument-above-min-version`, which needs the declared minimum, stays `not_evaluated(schema_unavailable)`.

`--profile-dir` reads the directory's `PROFILE.md` profile-constants blocks and the templates they name, without following a link and with size caps. The output envelope records its `profile_sha256`, and `profile_tree_sha256`: the SHA-256 of sorted `<sha256>  <path>` lines over every file of the directory, `null` when an entry is a link, special or unreadable, or when a name is not UTF-8 or holds a control character or DEL, which could forge a line of the hash input. `profile-snapshot` prints the same value as `tree_sha256`. A path that is missing, not a directory or itself a link exits 2; a link in a parent component of the path is resolved. A profile that cannot be read or parsed is no error: the rules that need it are `not_evaluated` with `profile_unavailable`, as they are without `--profile-dir`.

`--timeout` must be greater than zero. A run that takes longer writes `mtf-facts: timeout` to stderr and exits with code 2.

The exit codes are in [Lint examples](usage.md#lint-examples).

## affected

`mtf-facts affected` lists the examples a change can affect, most likely first, so a host can plan them in that order within its time budget.

```sh
mtf-facts affected <dir> [--base <dir> | --diff <file>] [--module-root <dir>] [--json] [--timeout <duration>]
```

`--base` and `--diff` read the change as `lint` does (see [Change as a diff](#change-as-a-diff)). With `--json` it writes one JSON document in the shape of [affected.schema.json](../schema/affected.schema.json):

```json
{"schema_version": 1, "inert": false, "fallback": null,
 "examples": [{"dir": "examples/c", "reason": "touched"}, {"dir": "examples/a", "reason": "caller"}, {"dir": "examples/b", "reason": "all"}],
 "changed": ["examples/c/main.tf", "modules/log/main.tf"]}
```

- `examples` holds every example: each first-level directory under `examples/` with a `.tf` file in it. First those with a changed path under them (`touched`), then those whose local module sources reach a changed module directory, transitively and the root module included, as with `source = "../../"` (`caller`), then the rest (`all`), each group in path order. Registry and git sources are not callers of the local tree; a local source written with backslashes, as on Windows, is read with slashes. A changed file Terraform loads by its directory (`.tf`, `.tf.json`, `.tfvars`, `.tftest.hcl`, `.terraform.lock.hcl`, or an entry that is not a regular file) is a change of that directory. Any other changed file, such as a script, a template or a README, is a change of every module whose read sites reach it, and of none when no read site does.
- `inert` is true only when every changed path is a documentation or metadata path (Markdown and other docs files, `LICENSE`, `.gitignore`, anything under `docs/` or `.github/` that is not configuration), its content changed but not its exec bit, and no HCL read site in the whole tree reaches it. Read sites are the first argument of `file`, `templatefile`, `fileset` and the other file functions, the file calls inside every template a `templatefile` call reaches, the file attributes of `local_file`, `archive_file` and the provisioners, and any other string that is a path from `path.module`, `path.root` or `path.cwd`, or a plain literal resolved against the repository root and each example. The one plain literal that is no read site is `"/"` as the whole value of the `path` or `path_prefix` argument of an `aws_iam_*` resource or data block: an IAM path, not a file. Even that one is a read site when an expression in the tree reads the block by `path` or `path_prefix`, as in `join("", [path.module, aws_iam_role.r.path, "README.md"])`, or reads it whole: by its address alone, as in `local.role = aws_iam_role.r`, or through an index or splat that does not end in another attribute, as in `aws_iam_role.r[*].path` or `aws_iam_role.r[each.key].arn`. A reference written with the `resource.` prefix, as in `resource.aws_iam_role.r.path`, is the same reference. A reference to another attribute, such as `aws_iam_role.r.arn`, keeps it no read site. A `.tftest.hcl` file is not scanned for references to an `aws_iam_*` block, so a reference there, such as one that joins the block's `path` to `path.module`, does not restore the held `"/"`. A `"/"` anywhere else, such as a variable default, a module argument or a `join` separator, is a read site that reaches every file, since other code may join it to `path.module`. A read site with a dynamic path, a shell command (`local-exec` `command` and `interpreter`, `external` `program`), a template that does not parse, and a path that leaves the tree, such as `${path.module}/../x`, reach every file. A docs file a read site reaches is not inert, so a README a module reads with `file()` makes its callers `caller`.
- A change of the exec bit alone is a change. The exec bit comes from the file mode with `--base`, from `old mode` and `deleted file mode` lines in a unified diff, and not from a GitHub files list, whose entries with no patch are `no_patch` and so never inert. The tree hashes a `lint` report records do not include the exec bit, so they stay as they were.
- `fallback` says why the examples could not be ranked, and then every example has reason `all` and `inert` is false: `unparsed` when a `.tf` file of the head tree does not parse, a `.tf.json` or override file is present, or a submodule commit pointer (a gitlink, held in a checkout as an empty directory or one with only a `.git` file) is in the trees, since its commit is in neither (under a module root only a gitlink in scope counts, and a gitlink is listed in `changed`; a base tree read from a directory cannot tell a moved pointer from an unmoved one, so with `--base` every empty directory counts as a changed gitlink, which fails closed and only costs the ranking; with `--diff` a gitlink is changed when the diff shows mode 160000 for it, or a `Subproject commit` line for a side whose mode the diff does not state (a files list states none, and a unified diff that gives another mode is read as that mode), a removal included, and in a files list a change to or from a file; and a module call, an example or a read site that reaches a gitlink, changed or not, is also `unparsed`, since its files are a commit the tree does not hold), `dynamic-source` when a module call has no literal source, `no-change-input` when neither `--base` nor `--diff` is given.
- `changed` lists the changed paths, both paths of a rename.

#### Module root

`--module-root <dir>` reviews one module of a repository that holds several, such as a submodule `modules/foo` whose examples sit beside it at the repository root. `<dir>` and `--base` are then repository roots and the change is repository-relative, unstripped. The module root must be a clean relative path of names in `[A-Za-z0-9._-]`, a directory of the tree reached through no link and not under `examples/`; anything else exits 2.

```json
{"schema_version": 1, "module_root": "modules/foo", "inert": false, "fallback": null,
 "examples": [{"dir": "examples/touched", "reason": "touched"}, {"dir": "examples/calls", "reason": "caller"},
              {"dir": "modules/foo/examples/own", "reason": "caller"}, {"dir": "examples/odd", "reason": "all", "unresolved": true}],
 "changed": ["examples/touched/main.tf", "modules/foo/main.tf"]}
```

- Every path is repository-relative, and `module_root` names the module root. Without the flag the report has neither `module_root` nor `unresolved`.
- `examples` holds the module root's own examples, `<module-root>/examples/<name>`, and the repository-root examples, `examples/<name>`, whose local module calls reach the module root or a directory below it, transitively, through a wrapper module too, ranked together as above. The HCL parser reads the calls, so a source in a comment, a heredoc or a resource is no call. A source that leaves the tree, one that passes through a link, and a sibling whose name only starts with the module root (`modules/foobar`) do not reach it. Repository-root examples that do not reach it are left out; the examples of other modules are never listed.
- A repository-root example the engine cannot resolve is listed with reason `all` and `unresolved: true`: its calls pass a directory with a `.tf` file that does not parse, is a link, is over 2 MiB or past the token limit, or a module call with no literal source. So is every repository-root example, none of them parsed, when together they hold more than 1000 `.tf` files or 8 MiB. An unresolved example makes `inert` false. A problem in a directory only an unrelated part of the repository reaches is ignored.
- Only the changed paths in the module root's scope count, in `changed` and in `inert`: those under the module root or a listed example, those in a directory the module root or its own examples reach, or on the way from a listed repository-root example to the module root, and those a read site of these directories reaches. A change to another module nobody here calls, or under `.github/`, is left out; with no path in scope, `changed` is empty and `inert` true unless an example is unresolved.
- `fallback` is set when a directory the module root or its own examples reach cannot be resolved, a read site in scope cannot be read, or no change is given; it lists the module root's examples and every repository-root example, reason `all`.

It exits 0 with the list, or 2 on bad input, a limit, an example directory whose name is outside `[A-Za-z0-9._-]`, a module root it refuses, or the timeout, with nothing on stdout. Without `--json` it prints one `<reason> <dir>` line per example, with ` unresolved` after an unresolved one, and a summary line.

## changes

`mtf-facts changes` reports what a change does to the HCL of a module tree, read with the HCL parser: the changed direct attributes of each top-level block, whether the module reads each of them, and the data source types of each directory. A host uses it to decide whether a change is only a value change of resource or data arguments, and whether anything else depends on those values.

```sh
mtf-facts changes <dir> (--base <dir> | --diff <file>) [--json] [--timeout <duration>]
```

One of `--base` and `--diff` is required; they read the change as `lint` does (see [Change as a diff](#change-as-a-diff)). With `--json` it writes one JSON document in the shape of [changes.schema.json](../schema/changes.schema.json):

```json
{"schema_version": 1, "head_tree_sha256": "<64 hex>", "base": {"kind": "tree", "tree_sha256": "<64 hex>", "diff": null},
 "files": [{"path": "main.tf", "dir": ".", "status": "modified", "parsed": true, "reason": null, "outside": false,
   "blocks": [{"address": "aws_lb.this", "kind": "resource", "status": "edited", "structural": false,
     "attributes": [{"name": "subnets", "plain": {"head": true, "base": true}, "read": "read",
       "readers": [{"path": "data.tf", "line": 3, "how": "name"}], "readers_truncated": false}]}]}],
 "data_sources": [{"dir": "examples/sso", "types": ["aws_ssoadmin_instances"], "complete": true}]}
```

- `files` lists every `.tf`, `.tf.json`, `.tofu` and `.tofu.json` file whose entry differs, in path order. `parsed` is false for a `.tf.json` file, a `.tofu` or `.tofu.json` file, an override file, a file that does not parse or is over a parser limit, a file with two `resource`, `data`, `module`, `variable` or `output` blocks of one address, and an entry that is not a regular file; `reason` names which. Blocks of a type that may repeat, such as `locals`, `moved` or `terraform`, are matched in order. No other changed file is listed, so a template or `.tfvars` change gives `files: []`: the host classifies every other changed file itself. `outside` is set when anything changed besides the values of direct attributes of blocks present at both sides: a comment or blank line between blocks, a block added, removed or moved, or the file added, removed or not parsed.
- `blocks` lists each top-level block that was added, removed or edited. `structural` is set for an edit besides the values of its direct attributes: its header, a comment or blank line, a trailing comment, an attribute added, removed or moved, or any nested block, `dynamic` and `lifecycle` included, and in a `resource` or `data` block a change of `count`, `for_each`, `provider` or `depends_on`, which changes which instances exist or in what order rather than one value; such a change is never listed as a changed attribute. `attributes` lists the direct attributes present at both sides whose value bytes changed, so two changed attributes on adjacent lines are two entries.
- `plain` says, per side, whether a value is a plain expression: numbers, strings whose interpolations are plain and with no template directive, `true`, `false`, `null`, references with attribute, index and splat steps not rooted at `each`, `count` or `self`, tuples, objects with name or literal string keys, a negative number, and calls of only `concat`, `compact`, `flatten`, `distinct`, `join`, `format`, `length`, `toset`, `tolist`, `tomap`, `element`, `keys` and `values`. An operator, `?:`, `for`, `...`, parentheses, a heredoc and every other function are not plain.
- `read` is `read`, `unread` or `unknown` for an attribute of a resource or data block: whether an expression in a configuration file directly in the block's directory reads it at head. An address resolves only inside its module, so other directories are not read, and comments and string text are no reads. A reference reads the attribute it names after the address, past one index or splat that selects instances of a block with `count` or `for_each`. An index with a literal string key names an attribute. The whole object, a `for` expression over it, and any other index or splat read every attribute. `tags_all` reads `tags`, `self.<name>` inside the block reads `<name>`, and `resource.<type>.<name>`, Terraform's long form of an address, is read as `<type>.<name>`. Reads are by name or by the whole object only: a read of a derived (computed) attribute such as `id` or `arn`, known only after apply, or a `lifecycle` `ignore_changes` entry, does not mark the attribute it names or any other attribute read. A `.tf.json`, `.tofu`, `.tofu.json`, override, unparsed or non-regular file in that directory makes the state `unknown`, with that file as a reader of `how` `unknown` and line 0. Attributes of other blocks, a `resource` or `data` block without two labels among them, are `unknown`. Only `.tf` configuration files are scanned: the expressions of `.tftest.hcl` files, `assert` conditions among them, are not. `readers` lists at most 32 reads, each whole-object read of the block counted once for all its attributes; `readers_truncated` says when there were more, and the state does not depend on the cap.
- `data_sources` lists each head directory with a configuration file and the data source types its files declare, from the parsed blocks, inside `check` blocks too, so a type named only in a comment or a heredoc is not declared. `complete` is false when a file there is a `.tf.json`, `.tofu` or `.tofu.json` file, is not a regular file or does not parse, or a data block does not have two labels. A directory reached through a link is not listed.

It exits 0 with the report, or 2 on bad input, a limit or the timeout, with nothing on stdout. Without `--json` it prints one line per file, block, attribute and directory with data sources.

## iam-policy-docs

`mtf-facts iam-policy-docs` reports the IAM policy documents a module tree defines, each with its normalized content, and exactly which blocks use it, so a host can compare a document with a copy kept elsewhere, such as the published policy of the workload it serves. It reads every directory of the tree that holds configuration and evaluates nothing. What it cannot decide is `undecidable` with a reason code, never equal or absent.

```sh
mtf-facts iam-policy-docs <dir> [--base <dir>] [--timeout <duration>]
```

It writes one JSON document in the shape of [iam-policy-docs.schema.json](../schema/iam-policy-docs.schema.json), `head` for the tree and, with `--base`, `base`, `compare` and `precondition`:

```json
{"schema_version": 1,
 "head": {"directories": [{"dir": ".", "undecidable": null}],
          "documents": [{"dir": ".", "address": "data.aws_iam_policy_document.widget", "kind": "data_source",
                         "file": "main.tf", "lines": [1, 6], "gate": null, "instances": "always",
                         "content": {"version": "2012-10-17", "id": null, "statements": [{"sid": null, "effect": "Allow",
                           "actions": ["s3:GetObject"], "not_actions": null,
                           "resources": [{"template": ["arn:", {"ref": "local.partition"}, ":s3:::b/*"]}], "not_resources": null,
                           "principals": null, "not_principals": null, "conditions": null}]},
                         "content_undecidable": null,
                         "uses": [{"dir": ".", "address": "aws_iam_policy.this", "argument": "policy",
                                   "via": [{"dir": ".", "node": "local.docs", "role": "local"},
                                           {"dir": ".", "node": "data.aws_iam_policy_document.merged", "role": "source"}],
                                   "gates": [], "instances": "always"}],
                         "uses_undecidable": null}]},
 "base": null, "compare": null, "precondition": null}
```

- A document is a `data "aws_iam_policy_document"` block (`data_source`), a `jsonencode()` call of an object with a `Statement` key (`jsonencode`), a string or heredoc that holds a JSON policy (`json_string`), or a policy argument whose value no document explains, such as a variable or `file()` (`expression`, always undecidable): `policy`, a name ending in `_policy` or `policy_document`, or `access_policies` of a resource, data source, provider or module call, and the `content` of an `aws_organizations_policy`. Any other argument that holds a policy is found only when it reads a document. An inline document's address is that of the argument it sits in.
- `content` is the document in one normal form for all kinds: statements in source order, every list sorted with duplicates kept, repeated `principals` and `condition` blocks not merged, a number or bool as its text, a JSON `"Principal": "*"` as type `*` with identifier `*` (a principal object keyed by `*` is refused, so the two never read alike), and a data source's missing `effect` as `Allow` and missing `version` as `2012-10-17`, as the provider reads them. A string with interpolations is a template of literal parts and references, each a plain traversal with literal index keys; the host substitutes what it knows. A `dynamic` block, `source_policy_documents` or `override_policy_documents` (the provider merges those by statement id, which the reader does not repeat), a value other than a literal, a list written out or a plain reference, an unknown argument or key, and a JSON string with an interpolation make `content` null and `content_undecidable` say why.
- `uses` lists each block and argument that reads the document, with every hop on the way and the `count` or `for_each` of each block on the way (`gates`). A trace passes locals, `concat`, `flatten`, `compact` and `tolist`, a `[*]` splat or a literal instance key on the document, the merge arguments of a merging document (`source_policy_documents`, `override_policy_documents`, and the legacy `source_json` and `override_json`), and the inputs and outputs of module calls whose local source is in the tree. `uses` lists the blocks that read the document directly: a block that carries the value on, such as `terraform_data`, ends a trace and is the use. An output is always a use, since callers outside the tree read it, and its in-tree callers are traced on. Any other function, a conditional, a `for` expression, an index into a value, a module outside the tree, a dynamic block, a directory on the way that is undecidable, and an output when any directory of the tree is undecidable (a caller there is unseen, even when none is seen elsewhere), and a document that is a variable default (`variable_default`: every reader of the variable reads it) make `uses` null and `uses_undecidable` say why: a partial list is never written. A node reached on paths with other hops or gates is followed on each, so each path is its own use. `depends_on` and `lifecycle` read no value and are not uses. `instances` is `none` when a gate on the path is a literal `0` or an empty literal collection, `always` with no gate or only literal non-empty ones, and `conditional` otherwise. A use in a directory that calls in the tree reach is at most `conditional` unless every call up the chain has only literal non-zero gates; callers never make it `none`, since the directory may also be used directly. Callers outside the tree are not seen.
- Files Terraform ignores (names starting with `.`, ending in `~`, or `#...#`) are not read. A `.tf.json`, override or OpenTofu file, a link or a file that does not parse makes its directory undecidable, and every document in it.
- `compare` pairs the documents of both trees by directory and address. `presence` is `head_only` or `base_only` when one tree alone defines the document, and `undecidable` when either tree defines it twice or the precondition below fails. With `both`, `content` and `uses` follow this contract:
  1. Precondition: both trees hold only regular files read whole (no symbolic link, special, unreadable or oversize entry), no variables file of any name (`*.tfvars`, `*.tfvars.json`, hidden ones included), no undecidable directory, every local module source resolved to a directory of the tree, and the same `provider` and `terraform` blocks, with the closure of what they read, and `.terraform.lock.hcl` files. Otherwise every pair's `presence` is `undecidable`, and so are its `content` and `uses` (null). `precondition` says which, true or false, and is null without `--base`: a tree whose only directories are undecidable has no pair at all, so a host reads `precondition` and the `directories` of both trees, never an empty `compare` alone.
  2. A side's key is the normalized content (or the uses) and the text, read token by token with only comments and layout left out, of every definition it reads, transitively: locals, variables, resource and data blocks, the whole blocks that use the document, and every in-tree module call of the directories on the way, up the call chain (source, providers, arguments and gates). The text of every site on the way and of an output that uses the document is in its uses key; only a data source's own block is left out, since its content is compared apart, while an inline document's site and local stay in; content that reads `each.*` or `count.*` reads the document's `count` or `for_each`.
  3. Closed grammar: a definition read for a key may call only functions whose result depends on their arguments (not `file*`, `templatefile`, `templatestring`, the path, clock and random functions, `yamldecode`, or a provider-defined function; `try` and `can` read every argument) and may not reference `path.*`, a module output, a name that is no definition of the directory (an iterator symbol of a `for` expression, or a dynamic block's iterator outside the block that defines it, or an `ephemeral` value), a resource or data block of a type other than `aws_*`, or an `aws_*` block with an argument that names a file to read (`source`, `filename`, `source_file`, `source_dir`, `source_path`, or a name ending in `_filename` or `_file_path`). Anything else makes that key undecidable.
  4. `equal` when both keys match, `differs` when they do not, `undecidable` when either is undecided. Values from outside the tree (variables a run passes with `-var`, `-var-file` or `TF_VAR_`, callers outside the tree, provider APIs, credentials, workspace and state) are taken as the same for both trees.

It exits 0 with the report, or 2 on bad input, a limit or the timeout, with nothing on stdout.

## depth

`mtf-facts depth` decides how deep a review of a change must go: `light`, `focused` or `deep`. It reads the head tree and the merge base tree, the pull request files list, and the `behaviour --json` and `affected --json` reports of the same change, and never runs Terraform. Every rule fails closed: anything it cannot prove makes the change deep.

```sh
mtf-facts depth --head <dir> --base <dir> --files <files.json> [--classify <behaviour.json>] [--affected <affected.json>] [--examples <dir,dir>] [--timeout <duration>]
```

`--files` is GitHub's `pulls/<n>/files` list (`filename`, `status`, `previous_filename`, `additions`, `deletions`). An entry with no `filename`, or with a `status` outside GitHub's set, an empty one included, exits 2. `--examples` names example directories the host knows of besides those of the affected report; each is selected unless proven out of scope. It prints one JSON object in the shape of [depth.schema.json](../schema/depth.schema.json). `format` and `schema_version` hold the same version; `schema_version` is the field the other reports use, and `format` stays for hosts that read it:

```json
{"format": 1, "schema_version": 1, "depth": "focused", "reasons": ["focused-within-limits"],
 "checks_out_of_scope": [], "examples_selected": ["examples/complete"], "examples_out_of_scope": [],
 "touched_types": ["aws_rds_cluster"], "walk": {"complete": true, "unwalked": 0},
 "limits": {"tf_files": 1, "changed_lines": 2, "changed_blocks": 1, "selected_examples": 1},
 "schema_providers": ["base:.:aws=hashicorp/aws:>=5.0", "head:.:aws=hashicorp/aws:>=5.0"],
 "schema_types": [".:resource:aws_rds_cluster:hashicorp/aws"], "schema_locks": []}
```

- The changed paths are every path whose entry differs between the trees and every path the files list names; a list that disagrees with the trees is `deep-files-list-mismatch`. Terraform-read files are `.tf`, `.tf.json`, `.tfvars`, `.tfvars.json`, any `.hcl` (`.tftest.hcl` and `.terraform.lock.hcl` included), `.tftest.json`, `.tfmock.json`, `.terraformrc`, `terraform.rc` and `*.tfrc`, matched case-insensitively. Both trees are parsed, HCL and JSON forms; a Terraform-read file that does not parse, is not a regular file, or is an override file is `deep-parse-failure`, and `walk` counts it.
- `light` needs all of: every changed path is Markdown (`*.md`), `CHANGELOG*`, under a `docs` directory at any level, `LICENSE`, `.gitignore` or `.editorconfig`; none is Terraform-read; no file sink string of the head (below) resolves to a changed path or a directory holding one, and every one resolves; the head calls only Terraform's built-in functions (a provider-defined function blocks light); the classifier report is valid, of this change, and `inert`; at most 50 changed files, else `deep-too-many-files`. Out of scope at light: checks A, B and D and the Check G ladder (`G-ladder`); every example is listed in `examples_selected`, for validate.
- `focused` needs a Terraform change with no deep class, at most 6 changed Terraform-read files, 200 changed lines (`additions` plus `deletions` of every listed file), 4 changed resource, data and module blocks, and 12 selected examples, and every classifier trigger on the allow-list: an expression or literal change in a resource or data argument, a local, an output value or a module call argument; an added output; an unknown propagation candidate; an example `.tfvars` change. Selected examples are those the affected report lists as `touched` or `caller`, those it does not list, those holding a changed path, and those whose local module calls reach a changed directory or the closure; the rest, reason `all` in a report with no fallback, are out of scope. An example a module root report lists as `unresolved` is selected; one it leaves out that lies outside its `module_root` counts as reason `all`. B is out of scope with no touched type; D is always in scope at focused, and with no example selected and none changed the reasons add `focused-no-example`.
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
| `deep-policy` | an argument whose name contains `policy`, an object key or block named `Statement` or `Principal` (any case), an IAM resource or data source (a type with `_iam_`), a resource type ending in `_policy` or `_permission`, or a `*_policy_document` data source, changed or reached; a changed block counts only when such a name inside it, what it holds, or the `for_each` or `iterator` of a dynamic block around it differs between the revisions, and the keys of a variable's `type` are no such names |
| `deep-module-source-version` | a module call's `source` or `version` changed, or a module call added |
| `deep-dynamic-structure` | a dynamic block changed or reached |
| `deep-wrappers` | a file under a `wrappers` directory changed, or a changed value reaches an argument there other than a pass-through output |
| `deep-limits` | over a focused limit, or a files list entry with no line counts |
| `deep-classifier-category` | a classifier trigger outside the allow-list, or capped triggers |
| `deep-classifier-unavailable` | no classifier report |
| `deep-classifier-invalid` | a classifier report that does not decode as `behaviour --json` (an unknown key, a wrapper object, another `schema_version`) |
| `deep-classifier-mismatch` | a classifier report with no entry for a changed path: of another change, or with paths relative to another directory than the head tree's root |
| `deep-affected-fallback` | no affected report, or one with a fallback |
| `deep-affected-invalid` | an affected report that does not decode as `affected --json` (such as `{}`), lists an unknown reason, or has a `module_root` that is not a safe path outside `examples/` |
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

## behaviour

`mtf-facts behaviour` lists every changed tree entry and decomposes the change into triggers: one per AST-level change, each with one category and the verification levels it needs. It never runs Terraform.

```sh
mtf-facts behaviour <dir> (--base <dir> | --diff <file>) [--schema <file>] --json [--timeout <duration>]
```

The change is read as `lint` reads it, and the report is the same for a base tree and for every diff form and context size. It is one JSON document in the shape of [behaviour.schema.json](../schema/behaviour.schema.json):

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

- A documentation or metadata path (as for `affected`, with the same read sites, the IAM path `"/"` of an `aws_iam_*` block excluded) is inert when the read-site scan of both trees is complete and no read site reaches it. A `.tf` file is inert when both revisions of its directory decompose and no trigger lands in it: comments, layout, a block moved unchanged between files of one directory, or a variable or output `description`; not when a read site can read the file itself. A mode-only change, a lock file, a script and every other path are triggers.
- Each module directory is compared block by block: resources, data sources, module calls, variables, outputs, each local, providers, the `terraform` block's `required_version` and each required provider, `moved` blocks, and any other block by its source. A directory with a `.tf` file that does not parse, is not a regular file, or is an override file gets one `unclassified-change` trigger per changed `.tf` file.
- Categories and levels: `wrapper-change` (one revision wraps the other in function calls, or calls another function on the same arguments), `default-change`, `count-for_each-conditional-change`, `type-validation-change` and `locals-outputs-change` select `L1`; `literal-argument-change`, `resource-added-or-removed`, `lifecycle-change`, `dynamic-block-change`, `unclassified-change` and `non-hcl-dependency` select `L4`; `provider-version-floor-change` selects `compat`; a `count` or `for_each` change of a resource, data source or module selects `L1` and `L2` (`terraform test` with mock providers), and a dynamic block whose `for_each` alone changed (same labels, iterator and content) selects `L1` on its `for_each` and is marked `for_each_only`; a change inside nested dynamic blocks that lies in one inner dynamic block alone, with the blocks around it the same but for that one, is placed at the innermost such block, its `attribute` the path of group keys such as `dynamic.origin.dynamic.custom_origin_config`, with `L4` and never `for_each_only`, since an inner `for_each` reads the iterator around it; `moved-or-renamed-address` and provisioner or connection changes select none and say why.
- A changed resource argument whose expression is not a literal on both sides, or that is removed, also yields an `unknown-propagation-candidate` trigger (`L1`, `L3`) when the provider schema given with `--schema` (a `terraform providers schema -json` file) marks it Optional and Computed, or when there is no schema, the schema lacks the provider, the type or the attribute. A data source argument is not planned at `L3`: a changed data source argument whose expression is not a literal on both sides yields an `unknown-propagation-candidate` trigger with `L1` only, since an unknown argument defers the read to apply whatever the schema says. It yields none when both sides have the same set of subexpressions that decide what is known: a template, tuple or object is looked through, its literal parts never unknown, and every other part that is not a literal (a reference, a function call, an index, a conditional, a `for`, a splat) is compared by its text; a template with an `if` or `for` directive, or an object with a computed or repeated key, always yields one, so `"${local.p}-a"` to `"${local.p}-b"` yields none while `lookup(local.m, "a")` to `lookup(local.m, "b")` yields one; the trigger of the change itself still needs evidence. A deferral by `depends_on` or by a pending change of a dependency is not modelled, so no claim that a read is not deferred can verify.
- An argument added to, or removed from, a resource or data source block that is present at both revisions reads as `null` at the revision without it, when the schema given with `--schema`, at each revision's provider source, marks the name as an attribute that is not write-only, reached through nested attribute types only. One schema stands for both revisions, so there is no `null` when the provider's `required_providers` entry (its source or version constraint) or its local name differs between the revisions in that directory. A revision that writes the name in block syntax, as a nested block or a `dynamic` block of that name, is not absent and gets no `null`. That side's expression is then `null` and the trigger carries `base_absent` or `head_absent`; the flags change no category and no level. There is no `null` with no schema, a disagreement, a nested block on the path, or a name that is a meta-argument or a block Terraform reads (`count`, `for_each`, `depends_on`, `provider`, `lifecycle`, `provisioner`, `connection`, `dynamic`, `moved`, `import`, `removed`, `check`); arguments of provider, module, output, locals, variable and terraform blocks never get one.
- A variable the change adds, whose default is a bare literal (a quoted string with no `${` or `%{`, `true`, `false` or `null`), gives the triggers that read it a `default` case with that value, unless a case of that value is already there.
- Each `referenced_inputs` entry, and a trigger at `var.<name>`, carries `declaration`: the variable's block at the head in the trigger's module directory, read with the HCL parser, so comments, heredocs and nested types read as Terraform reads them. `added` is set when the directory has no variable of that name at the base (a new directory has none). `default` is `null` when the default evaluates to null with no reference or function call (`null`, `(null)`), `literal` for any other such constant (`true`, `3000`, `"a"`, `[null]`, `{ a = null }`), `other` for any other expression (a function call, a reference) and `none` with no `default` argument. `nullable` is `unset` with no `nullable` argument, `true` or `false` for that constant and `other` otherwise. There is no `declaration` when the directory does not decompose at either revision (a file that does not parse, such as one with two `default` arguments, an override file, a file that is a link or over the size limit, or a variable declared twice), when it holds a `.tf.json`, `.tofu` or `.tofu.json` file at either revision, which may declare or override the variable, or when it holds no such variable at the head, such as a removed one.
- With `--schema`, a trigger on an argument or nested block of a resource or data source carries `schema_required`: `required` when the schema, at the head's provider source, marks the attribute at that path required, at any block depth or inside a nested attribute type, or the block type has `min_items` above 0; `not_required` when it is neither; `unknown` when the schema lacks the provider, the type or the path, which includes meta-arguments. A `dynamic` segment of the path stands for the block it generates. There is no `schema_required` with no schema, on a trigger of any other block, or on a `lifecycle` trigger.
- A variable the change adds follows its use sites: it keeps its category, selects no level, and lists in `follows` the id of every trigger whose head source holds a `var.<name>` (or `var["<name>"]`) read of it in its own module directory. A read in another directory, `var.defaults.<name>`, and `"var.<name>"` inside a string are no reads of it. The source a trigger holds is its argument, its added block (resource, data source or module call), its local, its output `value`, an added nested block of a type the base block already has (a nested block of a type new to the block has no source, so a read in it declines), its `dynamic` block (only the `for_each` for `for_each_only`) or its validation `condition`. It follows only when all of these hold: it is new in the directory, not moved between its files; its default is `null`, a bool or number literal, a quoted string with no `${` or `%{`, `[]` or `{}`, and its type admits that value (an object type with a required attribute does not admit `{}`); it has no validation or other nested block, is not `sensitive` or `ephemeral`, is not `nullable = false`, and sets no other argument; both revisions of the directory decompose and it holds no `.tf.json`, `.tofu` or `.tofu.json` file; every reference to `var` names a variable; no read lies inside the arguments of a function that reads files (`file`, `templatefile`, `fileset`, `fileexists`, `filebase64` and the file hash functions); and every read lies inside another trigger's source. A variable nothing reads follows nothing (`follows` is `[]`), and then only when the directory calls no such function. Otherwise it is `unclassified-change` at `L4`, as before.
- An argument added to `module "wrapper"` in `wrappers/main.tf` or `wrappers/<one directory>/main.tf`, whose `source` is unchanged and a relative path to a module directory, whose `for_each` is `var.items`, whose own `variable "defaults"` and `variable "items"` set a `default` of exactly `{}`, no `type` or `type = any`, and nothing but a `description` at both revisions, in a wrapper directory with no `.tf.json`, `.tofu` or `.tofu.json` file, and whose expression is, token for token with comments and layout aside, `try(each.value.<name>, var.defaults.<name>, <default>)`, where the module adds the variable `<name>` with that default and the variable follows its use sites, follows that variable's trigger and selects no level. Every other form (no fallback, another fallback, another order, `lookup`, an extra argument, `each.key`, an index, a heredoc), an argument of any other module call, a wrapper two directories deep, and a line that changes rather than adds an argument stay as before.
- A follower whose followed trigger is past the cap, or follows such a follower, selects its category's levels instead, with the reason that says so.
- `pure` is set when every function the expressions call is on the built-in pure-function allowlist and neither reads `self`. A variable's `type` expression reads nothing: its keywords, such as `string`, are no references. `contextual_ref_details` gives each other reference as written, its kind, its type from the schema and whether its block has `count` or `for_each`.
- A trigger that selects `L1` lists its required `cases`. The inputs are its variables, its resource, data, `count.index` and `each.key` references, and for a default or type change the variable itself. Each input has states: a list or set is typical, empty, an empty element, a null element, an unknown element, wholly unknown and null; a map the same with key `k`; a string, number or bool typical, zero, unknown and null; every string, number and bool literal of either expression (at most 8 of a kind) is one more state of each input of its type, and one more element of each collection of it; a variable whose default changed is also omitted (unset, so each revision's default applies); a splat reference has one unknown instance, none, a null attribute or one known, and no null attribute when it reads the `id` of a managed resource (`<type>.<name>[*].id`), which may be unknown but is never null; any other resource or data reference is unknown or known; `count.index` is 0 or 1 and `each.key` `a` or empty. A list or set input, and a splat reference, also take order states: two distinct elements and a duplicate. The cases are the first that fits 32: every combination of all states; every combination of the other states plus one case per order state with the rest typical; for a `count` or `for_each` trigger that selects `L2`, or a dynamic block, no cases and a reason; with no order states, every combination of the states; one typical case and one case per other state of each input, order states included; past 32, or with a `local`, `module`, `self` or `each.value` reference, the trigger has no cases and says why. Each input of a case sets exactly one of `value`, `unknown`, `null` and `omitted`, with `unknown_elements` for the unknown elements of a value.
- Triggers of one file and line whose base and head expressions are the same, and neither null, are one change seen by several categories, and are merged into one trigger, verified once: the first keeps its `category` and its place, `also_categories` lists the others' categories, `levels` is the union in `L1`, `L2`, `L3`, `L4`, `compat` order, and `cases` the union by case id; `address` and `attribute` come from whichever has them. A merge is not made when either trigger selects no level, when both name an address or an attribute and they differ, when the union of the cases would be over 32, when one case id has other inputs in each, or when one trigger could not get the required cases it needs while the other has some. The ids are numbered after the merge, and each entry's `trigger_ids` lists the merged ids.
- At most 40 triggers that may need evidence are listed, and apart from them at most 40 that no ladder level can verify: a `provider-version-floor-change` whose `levels` are `compat` alone and a `moved-or-renamed-address`, which selects no level, when neither is merged with another category or follows another trigger. Each cap keeps the first triggers of its kind in file, line and category order, so version floors that sort first never push a change past the cap. The kept triggers stay in that order and are numbered after the cut; a report lists at most 80. These stay unverifiable to `claims-eval`, as before; only the cap they count toward differs. `capped` counts the rest of both kinds. `version --json` lists the feature `behaviour.off-ladder-cap`. 80 triggers whose expressions are each near the size limit can make a report larger than a host's output cap (1 MiB in the review skill); the host then refuses the output.

It exits 0 with the report, or 2 on bad input, a limit or the timeout, with nothing on stdout.

## claims-eval

`mtf-facts claims-eval` validates the claims a reviewing model wrote, and computes the state of every trigger and every claim from a ladder record.

```sh
mtf-facts claims-eval --behaviour <file> --ladder <file> [--claims <file>] [--head-sha <sha>] [--base-sha <sha>] --json [--timeout <duration>]
```

- The claims file has the shape of [claims.schema.json](../schema/claims.schema.json): each claim names a closed predicate (`expression-output-unknown`, `expression-output-length-known`, `expression-output-equals-for-inputs`, `plan-attribute-unknown`, `plan-attribute-unchanged-vs-prior`, `plan-action`, `input-filtered`) and a trigger by id in `params.node`, never HCL text. A claim that does not validate is listed in `discarded_claims` with the reason.
- The ladder record has the shape of [ladder-record.schema.json](../schema/ladder-record.schema.json); unknown fields are refused, and a record with runs must name its `head_sha` and `base_sha`. A record with runs needs `--head-sha` and `--base-sha`, and a record of other commits is refused. `capped` echoes the behaviour report's.
- `input-filtered` names a case input (a variable or a contextual reference) and a marker (`empty-string` or `null`); with `"filtered": false` it states that the input's marker elements reach the output instead of being removed.
- `expression-output-unknown`, `expression-output-length-known` and `input-filtered` take an optional `params.cases_where`: a non-empty object of the trigger's case inputs to states (`wholly_unknown`, `unknown_element`, `empty`, `known` for any other known non-null value, `single` and `multiple` for a known collection of one or more elements, which `known` also matches, `null`, `omitted`); the claim is judged only on the required cases whose inputs are all in those states, and a scope that matches no case leaves it unverifiable.
- A claim reads the runs of its trigger at its levels (`L1` for expression predicates and `input-filtered`, `L3` or `L4` for plan predicates) that ran with a valid control. It is verified when some observation is about it and all agree, refuted when one contradicts it, and unverifiable otherwise; an unknown, sensitive or redacted value leaves a value predicate unverifiable.
- A trigger is refuted when one of its claims is. A trigger that selects `L1` or `L3` is decided by the tool's own sweep, never by the claims alone. Every selected level needs base and head observations from runs with a valid control and a fixture hash (and none with an invalid control): at `L1` and `L2` of every required case the trigger lists (the same cases at both), each observed with exactly the case's `inputs`, other cases ignored; at `L3` the same case ids at both revisions; at most 32 cases at each level. A case whose plan fails in the subject (`error`) has no output; a failure at one side only is a difference, and a sweep whose cases all fail at both, or whose first case does, is unverifiable. Each case is compared attribute by attribute: `known`, `length`, `value` (the value, or the known parts of a partly unknown one), `sensitive`, `redacted`, `unknown_inputs`, `action`, `unchanged_vs_prior`, and when neither side is known `wholly_unknown` and `unknown_mask`. A side not known with no `unknown_mask`, a partly known side without its known parts, or a case known at both revisions whose value is sensitive or redacted leaves the trigger unverifiable. With no difference the trigger is verified, claims or not. Otherwise it is verified only when verified claims state both sides of every difference exactly, each on that case's observation at its own revision: an unknown side by `expression-output-unknown` (`L1`) or `plan-attribute-unknown` (`L3`), a known side by `expression-output-equals-for-inputs` (`L1`) or `plan-attribute-unchanged-vs-prior` (`L3`), a length by `expression-output-length-known` or `expression-output-equals-for-inputs`, a value by `expression-output-equals-for-inputs` at both revisions, an action by `plan-action` at both. At `L1` a `wholly_unknown` or `unknown_mask` difference from wholly unknown at the base to partly known at the head (a definite length, not failed, sensitive or redacted) is explained by a scoped `expression-output-unknown` at the base and a scoped `expression-output-length-known` whose `length` equals the observed one at the head; the reverse, a known length becoming unknown, is never explained; any other `wholly_unknown` or `unknown_mask` difference, and every `sensitive`, `redacted`, `unknown_inputs` and `error` difference, is never explained. At `L1` a `value` or `length` difference where one revision drops an input's marker elements that the other keeps is also explained by a verified `input-filtered` claim at each side (filtered at one, `"filtered": false` at the other, same input and marker) when the keeping side's value with those markers removed is the other side's value and it holds no more marker elements than the claimed inputs do. An `L2` difference (an `L2` observation is the instance count for `count`, or an object of instance key to planned input for `for_each`) is listed and never explained. An `L2` run, whose mock providers make computed values known, never evidences `expression-output-unknown`, `expression-output-length-known` or `plan-attribute-unknown` and explains no unknown-ness difference. A trigger whose expressions compare an input with an input-free function call, such as `var.cidr == lower("EVIL")`, has no cases. Each difference is listed in the trigger's `changes`, with `explained`, the claims that explained it in `explained_by`, and the case inputs.
- A trigger that selects levels of more than one family, the sweep (`L1`, `L2`, `L3`), `L4` and `compat`, which only a merged trigger does, needs each family to decide it, and is then covered; one family never stands in for another.
- A trigger the sweep reads that is not inert or refuted carries `complete`, `outstanding` and, when `outstanding` is not empty, `evidence_digest`. `complete` is true when every level it selects ran with a valid control, every required case was observed at both revisions, every observation could be compared, and an `L4` or `compat` level it also selects decided it. `outstanding` lists the differences no verified claim explains as `<trigger id>|<level>|<case id>|<attribute>`, sorted, `[]` when none; a complete trigger with an empty set is verified. Past 256 unexplained differences `complete` is false, `outstanding` and `evidence_digest` are left out, and the reason is `more than 256 unexplained differences`: no finding can list them. `evidence_digest` is the sha256, in hex, of canonical JSON (keys sorted, no whitespace, numbers as written) of `head_sha`, `base_sha`, the outstanding ids, the base and head observations of every case with an outstanding difference, and the sorted fixture hashes of the trigger's valid runs at those levels: the same ids over other observations give another digest. A finding that explains the differences carries the ids in `explains` and the digest in `evidence_digest`, so a host can tell it judged this evidence.
- A trigger that selects `L4` is covered when no claim of it is unverifiable and its address and changed attribute (a `dynamic` block by the blocks it generates; never a `lifecycle` argument) are in a planned example: in the example's own directory, or under a module call the record's `module_calls` ties to the trigger's directory. An inert entry's triggers are inert; anything else is unverifiable. Refuted outranks covered.
- A trigger with `follows` is decided after the others. It is refuted when one of its claims is, and unverifiable when any other claim of it is not verified. Otherwise it is covered when every trigger it follows is verified or covered and one is covered, verified when all are verified, and verified when it follows none. A followed trigger that is unverifiable, refuted or inert, an id that names no trigger of the report, and a cycle of follows leave it unverifiable. A behaviour report whose follower selects a level, or follows something other than a trigger id, is refused.

The output has the shape of [claims-eval.schema.json](../schema/claims-eval.schema.json). It exits 0 with the report, or 2 on bad input.

## bundle

`mtf-facts bundle` turns a `lint --json` report into the facts bundle a model reviewer reads, and the injected rule text for the rules the report left `not_evaluated`.

```sh
mtf-facts bundle --report lint.json --skills path/to/terraform-module-reviewer \
  --out bundle.txt --injection injection.md \
  --head-sha <sha> --merge-base-sha <sha> --binary-sha256 <sha256> \
  [--schema-handoff <file>] [--schema-files <file>]... [--profile-dir <dir>] [--timeout <duration>]
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

`<json-string>` is one line holding the bundle as a JSON string with every byte outside printable ASCII escaped. The bundle inside that string has the shape of [facts-bundle.schema.json](../schema/facts-bundle.schema.json), and `<n>` is the byte length of the string line. The 48 KiB cap counts that line. Over the cap, sections are shed lowest priority first (`file_index`, `schema_coverage`, `interface_diff`, `area_rules`), never `tool_findings` or `candidates`.

The bundle carries `schema_version: 1`, `module_areas` (the report's `scope.module_areas`) and `in_repo_downgrade` together with `area_rules`, as one ownership unit: they are shed together, last, as `area-rules`. A report without `scope.module_areas`, from an older release, or with a module or downgrade area longer than 1024 bytes sheds the unit from the start. A bundle without `schema_version` is a legacy bundle.

## merge

`mtf-facts merge` reads the bundle file `mtf-facts bundle` writes. It checks a model reviewer's reply to that bundle and merges the two into the final findings. The reply is untrusted: it is read with exact keys, no duplicate key, no trailing data and a 1 MiB cap.

```sh
mtf-facts merge check bundle.txt reply.json [--timeout <duration>]
mtf-facts merge final --registry rule-facts.md [--report lint.json] [--templates templates.json] \
  bundle.txt reply.json [repair.json] [--timeout <duration>]
mtf-facts merge counts bundle.txt reply.json [repair.json] [--timeout <duration>]
```

The reply is one JSON object with the arrays `candidate_decisions` and `findings` and, optionally, a `counts` object, in the shape of [model-output.schema.json](../schema/model-output.schema.json). `counts` holds the three counts a reviewer's summary line carries: `instruction-like-text-from-others`, `conversation-claims-unread` and `review.quoted-claim-unverifiable`. When present, it has exactly those three keys, each a natural number written as decimal digits, 0 included and at most 9007199254740991; anything else makes the reply invalid.

`merge check` prints, in the shape of [merge-check.schema.json](../schema/merge-check.schema.json), `{"result":"valid"}` and exits 0, or `{"result":"repair","reasons":[...]}` and exits 1 when the reply needs the repair turn. Its reasons are fixed text and quote nothing from the reply.

`merge final` prints the merged findings, in the shape of [merged-findings.schema.json](../schema/merged-findings.schema.json), and exits 0. It merges the first reply when that one is valid, and otherwise the repair reply, which must then be given: an empty file when the repair turn returned nothing. A repair reply that is still invalid keeps only its valid entries; a candidate it leaves undecided becomes `review.check-not-run`. `--report` takes a `lint --json` report, whose index derives the subject a lead finding dedups by. `--templates` takes the per-rule host text for confirmed entries the model gave no text for. A tool finding keeps the `predates_change` its bundle entry carries, which the host copies from the confirmed candidate; an `llm` or `host` finding never has it. A lead finding of `compat.expression-behaviour-changed` or a `claim.*` rule may carry `explains`, up to 256 difference ids as claims-eval lists them in `outstanding`, and `evidence_digest`; on any other rule, or malformed, they make the finding invalid. Lead findings that dedup into one and carry the same digest explain the union of their ids.

A lead finding on a rule that is not an `llm` rule is kept only where the bundle's `area_rules` lists that rule `not_evaluated` for the finding's area, or under `.` for a cross-area rule. In a bundle with `schema_version` 1 the area is the one [`facts.AreaOf`](rules.md#module-ownership) gives with the bundle's `module_areas`, after the two-segment `examples/<n>` and `modules/<m>` directory of a path rule, which is its own area when the bundle lists it. A legacy bundle, without `schema_version`, maps paths as before module areas and has no downgrade. A `schema_version` other than 1, or one without all of `area_rules`, `module_areas` and `in_repo_downgrade`, or with an area that is not a clean relative path, exits 2.

A model severity off its rule's list is clamped to the nearest permitted one, as before, with one exception: exactly `MEDIUM` on `compat.variable-removed`, `compat.variable-now-required`, `compat.type-narrowed`, `compat.default-changed` or `compat.generated-policy-narrowed`, in an area of the bundle's `in_repo_downgrade`, is kept. `merge final` writes the number of clamped severities to stderr when there are any.

`merge counts` takes the replies as `merge final` does, picks the same reply, and prints its counts as `{"counts":{...}}`, or `{"counts":null}` when that reply has none, in the shape of [merge-counts.schema.json](../schema/merge-counts.schema.json), and exits 0. A reply without counts has none: no count is ever read as 0 for its absence. A repair reply that is still invalid keeps its counts when they are well formed. `merge counts` does not load the registry, the report or the templates, so it can succeed where `merge final` fails: a host reads the counts only after `merge final` succeeds.

## profile-check

```sh
mtf-facts profile-check --profile-dir <dir> [--gates] [--downgrade-ids] [--timeout <duration>]
```

First reads the directory with the walker `profile-snapshot` uses and exits 2 on anything it refuses (a link, a special file, a bad name, another device or a bound), so a profile cannot pass the check and then fail the snapshot. Hosts run it on the snapshot directory. It then reads the profile as `lint --profile-dir` does and checks it, options included. It prints one JSON object, in the shape of [profile-check.schema.json](../schema/profile-check.schema.json): `valid`, `problems`, and for a valid profile `options` and `relaxing`. `--gates` adds `gates`, the gate table, and `gated_rules`, the rules this profile gates; `--downgrade-ids` adds `downgrade_ids`. Exit 0 for a valid profile, 1 for an invalid one, 2 when the directory is missing, a link or cannot be read.

## profile-snapshot

```sh
mtf-facts profile-snapshot --profile-dir <dir>
```

Copies the profile directory into a new 0700 temporary directory and prints `{"dir", "tree_sha256", "profile_sha256"}` for the host, in the shape of [profile-snapshot.schema.json](../schema/profile-snapshot.schema.json). Each directory is opened relative to its parent and each file with `O_NOFOLLOW|O_NONBLOCK`, checked with `fstat` as the regular file the listing saw and on the source's `st_dev`, and copied from that descriptor. A link, a special file, another device, a name that is not UTF-8 or holds a control character or DEL, more than 256 entries, more than 8 MiB or a path more than 8 levels deep exits 2, and so does a copy that does not read back the same. The host reads options and gates only from the copy and removes it when done. `profile_sha256` is `null` when the copy is not a valid profile.

## ledger

`mtf-facts ledger` lists the provider fields of each resource and data source type the module in the given directory declares, nested fields included, from one or more `terraform providers schema -json` files:

```sh
mtf-facts ledger path/to/module --schema-files schema.json [--schema-files more.json]... \
  [--check coverage-ledger.json] [--registry rule-facts.md] [--timeout <duration>]
```

It reads only that directory, not `modules/*` or `examples/*`. At least one `--schema-files` is required. The run exits 2, with no inventory, when:

- a provider the module uses is in no schema file;
- a type is not in its provider's schema;
- the directory has no `.tf` file or no resource or data block;
- a block is an ephemeral resource;
- a file is one the parser cannot model.

The JSON output, in the shape of [ledger-report.schema.json](../schema/ledger-report.schema.json), has an `envelope`, the `types` and their fields, and `problems`. The envelope holds the tool, `registry_sha256` (`null` without `--registry`), and the hash of each schema file and of the ledger, each named by its base name.

`--check` reads the author's coverage ledger, in the shape of [ledger.schema.json](../schema/ledger.schema.json), and lists each problem it finds:

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

## schema-diff

`mtf-facts schema-diff` compares two `terraform providers schema -json` files, the current and the target provider version, for the resource and data source types a module tree uses:

```sh
mtf-facts schema-diff --from current.json --to target.json --types-from path/to/module [--timeout <duration>]
```

The three file flags are required. It writes the report as JSON, in the shape of [schema-diff.schema.json](../schema/schema-diff.schema.json), and exits 0, with changes or none. Each schema file is recorded by base name and SHA-256 only. A provider the tree uses that either file lacks, bad input or a limit exits 2.

## fixture-build

`mtf-facts fixture-build` builds the scratch trees a verification ladder level runs for one trigger of a behaviour report, from the trusted templates of the host. It reads Terraform type constraints without evaluating anything, and it never starts Terraform or reads the network:

```sh
mtf-facts fixture-build trigger.json --template l1.tf.tmpl --out scratch [--timeout <duration>] [--stats-file <file>]
mtf-facts fixture-build trigger.json --template l1.tf.tmpl --out scratch --level L2 --attribute for_each
mtf-facts fixture-build trigger.json --template l1.tf.tmpl --out scratch --level L3 \
  --attribute vpc_security_group_ids --resource aws_rds_cluster --resource-template template.tf.tmpl
```

`trigger.json` is one trigger object with the cases of the classifier: `base_expr` and `head_expr`, `referenced_inputs` with each type constraint, `contextual_ref_details`, `category`, `address` and `cases`, at most 4 MiB. Each template, at most 256 KiB, holds sections that start at a line `#-- <name>`, with `__NAME__` placeholders filled in one pass; at L3 the sections of `--resource-template` replace or add to those of `--template`.

`--out` must be an existing empty directory. For each side with an expression it writes `<side>/main.tf` (a variable, a `terraform_data` stand-in whose output is a typed unknown and a canary per input, and a local per input whose state a case var file picks), `<side>/subject.tf` (the expression with each input renamed to its local), at L2 `<side>/ladder.tftest.hcl`, and `expect/<side>.json`, the manifest `fixture-check --expect` reads; and `vars/<case id>.tfvars.json` per case. The files are written only after the build is done, so a refusal, a timeout or the output cap leaves `--out` empty, and a write that fails part way removes what it made. `--attribute` must match `^[a-z][a-z0-9_]{0,63}$` and `--resource` `^[a-z][a-z0-9]*_[a-z0-9_]{1,63}$`.

A type constraint is `string`, `number`, `bool`, `any`, `list`, `set` or `map` of one type, `tuple`, or `object` with `optional` attributes, at most 400 tokens and nested at most 20 deep. An input is renamed where the expression's own tokens name it, so the text of a quoted string, a heredoc or a comment stays as it is. A trigger the builder cannot model is refused with a fixed reason: a type outside the grammar, a local, module, `path` or `terraform` reference, an `each.value` reference, more than 32 cases, a case that does not name exactly the inputs, an expression over 16 KiB, two case ids that differ only in letter case (they would share one var file on a case-insensitive file system), or a `count`, `each` or default context at L2 or L3.

The trigger's JSON values are copied as the ladder pass's earlier Perl builder copied them, so the two build the same files: an integer of up to 20 characters, sign included, that fits neither a signed nor an unsigned 64-bit integer is written as a float with 15 significant digits, a longer one as a string; a whole float in the 64-bit range is written as its digits when its text has an exponent and no point, or both and a value past 2^53, and with 15 significant digits otherwise; and a lone surrogate escape (`\ud800` with no low half) is an error, exit 2. The builder differs on purpose where the earlier one built a wrong or invalid file or refused valid HCL: a type with a vertical tab, a form feed, or a lone carriage return or no-break space, two object attributes on one line with no comma, a missing comma between tuple elements, or a line break before a comma in an object type is refused, where the old builder built an invalid fixture; `optional(string,)` is taken; a trigger with invalid UTF-8 anywhere in it is an error, exit 2; a trigger that is JSON `null` is an error, exit 2, where the old builder printed `ok:false` and exited 0; raw UTF-8 and escapes are both written as UTF-8, and an escape below U+0100 is written as UTF-8 too, where the old builder wrote it as one Latin-1 byte unless a wider character was in the same output; a number past the float range is an error, not `Inf`; two case ids that differ only in letter case are refused; and a refusal leaves no file.

The output, in the shape of [fixture-build.schema.json](../schema/fixture-build.schema.json), is `{"schema_version":1,"ok":true,"wrap","sides","canaries","cases":[{"id","inputs","unknown_inputs","addr"}]}`, or `{"schema_version":1,"ok":false,"reason"}`, one line with sorted keys.

| Code | Meaning |
|------|---------|
| 0 | The fixture is written. |
| 1 | The trigger is refused, with its reason. |
| 2 | An error: bad flags, an input that is not a regular file or is over its size, a template without a section or value, an `--out` that is not an empty directory, the output cap or the timeout. |

## fixture-check

`mtf-facts fixture-check` checks the scratch tree a verification ladder level will run against a default-deny allowlist, before any Terraform command runs on it, and hashes the tree:

```sh
mtf-facts fixture-check scratch/head --json [--timeout <duration>]
mtf-facts fixture-check scratch/l2 --level L2 --pins pins.json --schema schema.json --json
mtf-facts fixture-check scratch/l3 --level L3 --resource aws_rds_cluster --pins pins.json --json
```

The output, in the shape of [fixture-check.schema.json](../schema/fixture-check.schema.json), is `{"schema_version":1,"ok":...,"tree_sha256":"...","violations":[{"file","line","rule","detail"}]}`. `line` is 0 for a whole file and `file` is `.` for the whole tree. `detail` names the refused function, block type or argument when that name is a plain identifier; it is left out otherwise, so no text from the tree is echoed. At most 200 violations are listed, then one `limit.violations` entry.

`tree_sha256` is the SHA-256 of one `name NUL type NUL content-sha256 LF` line per directory entry, sorted by name. The type is `file`, `file+x` (any exec bit set), `dir`, `symlink` or `special`, so a umask does not change the hash. It identifies a tree that passes; on a violation it covers what was read.

`--expect` takes the manifest the trusted template wrote, in the shape of [fixture-manifest.schema.json](../schema/fixture-manifest.schema.json):

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

## plan-gates

`mtf-facts plan-gates` runs the plan pass's gates G0 to G3 over the `.tf` files listed on standard input, one path a line, before any Terraform command runs on them with credentials:

```sh
<file list> | mtf-facts plan-gates --scope clone --mode laptop --root <clone> [--example <example dir>] [--timeout <duration>]
<file list> | mtf-facts plan-gates --scope downloaded --mode runner --root <clone> --provider compliancetf/*
```

It prints one line per hit, `<gate> <path>:<line>`, with line 0 for a whole file, in the order the rules find them and with duplicates kept. The line `UNREADABLE` in the list stands for a file the caller could not list and is a G0 hit. The lines are byte for byte those of the Perl gates the plan pass of the pull request review skill ran before, apart from the G2 lines the second source read below adds; the rules read text with the same patterns, so a keyword in a comment or a string counts as it did there.

- G0: a file that is unreadable, not a regular file once links are followed, or over 8 MiB; an override file; a carriage return, form feed or vertical tab; a byte over 0x7f in code or in a block label; a block comment; in runner mode, a string, comment or heredoc that does not end.
- G1: a `required_providers` entry outside the allowlist, in clone scope: laptop mode allows hashicorp `aws`, `random`, `time`, `tls` and `cloudinit`; runner mode those, `null`, `local`, `external` and each `--provider`. In runner mode, in either scope, also a local name that is not its source's type.
- G2: a module source outside `--root` or the terraform-aws-modules registry namespace (runner mode also takes github.com). A source that holds a NUL byte is a G2 hit; the old Perl gates truncated the source at the NUL and read the part before it. Clone scope only. The rule reads the source from the text, as the Perl did: the first `source = "..."` in the block, even in a comment, a nested object or a heredoc. A second read parses the file as Terraform does and adds a G2 hit for a module block whose `source` attribute is missing, is not a constant string, or differs from the one the text read passed, and, in a file that does not parse, for each module block the text read passed and each module block of the partial body it did not see: terraform init still installs the module blocks of the partial body it reads, blocks after the parse error included when the parser recovers. A block the text read did not see at all, such as `module"m"{`, gets the rule on the source Terraform reads. The second read only adds lines: a block the text read refused keeps its one line, and a block both reads agree on prints nothing new. `version --json` lists it as `plan-gates.structural-source`.
- G3 in laptop mode: a block or expression that runs code or reads an account: provisioners, `file*` and `templatefile` calls, provider functions, `backend` and `cloud`, an unknown top-level block, a resource or data source of another provider, a data source outside the allowed list, a data source `region` that is not a literal region or a variable every call from the example sets to one, and a provider argument other than `region` and `alias`. It allows `default_tags`, the documented skip flags, a data `aws_iam_policy` of one AWS managed policy ARN, and a data `aws_cloudwatch_event_bus` whose name and region the example sets to literals, unless a banned file name (`override.tf`, `*.tfvars`, `*.tf.json` and the like) is in a directory of the scope. `--example` names the example root those rules read; without it the event bus is a hit.
- G3 in runner mode: a keyword written against its label, a block with a bare label, `backend` and `cloud`, and an `aws` or `awscc` provider that sets credentials, a proxy or an endpoint.
- Runner mode relaxes the code-execution gates and records two exceptions: `Xrole`, an aws provider whose `assume_role` is one literal role ARN, and `Xdocker`, the Docker provider or a module call with `build_in_docker = true`.

`--scope` is `clone`, the clone's own files, or `downloaded`, the files of a downloaded module. `--provider` takes `ns/name` or `ns/*` and may be repeated; any other value is a usage error. The command reads the listed files and the names in their directories, and nothing else: no network, no program.

| Code | Meaning |
|------|---------|
| 0 | No hit. |
| 1 | At least one hit. |
| 2 | An error: bad flags, a file list over 8 MiB or 10000 files, files over 64 MiB in all, the output cap or the timeout. A caller must stop. |

## rules, version and completion

```sh
mtf-facts rules [--json]
mtf-facts version
mtf-facts completion bash
```

`rules` lists the implemented rules, one per line with its tier (`tool` or `candidate`), sorted by id. `rules --json` prints one object in the shape of [rules.schema.json](../schema/rules.schema.json): `schema_version` and `rules`, each rule with its `id`, `tier`, `class`, `scope`, `area_kinds` (sorted), `cross_area`, `needs_base` and `reads_unread_config`, every field always written. `tier` is the registry's `engine` for the two values the binary implements; `class`, `scope`, `cross_area` and `area_kinds` carry the registry's values. `version` prints the binary version, as does `mtf-facts --version`. `completion` prints a shell completion script for `bash`, `zsh`, `fish` or `powershell` to stdout.

`version --json` prints one line of JSON, [version.schema.json](../schema/version.schema.json), for a script that needs to know what the binary supports. Read it once instead of parsing `--help` or reading exit 2 as "flag not supported":

```json
{"schema_version": 1, "name": "mtf-facts", "version": "v0.9.0", "commit": "<40 hex>", "modified": false, "go": "go1.27.3",
 "reports": {"affected": 1, "behaviour": 1, "claims-eval": 1, "depth": 1, "fixture-check": 1, "lint": 2, "rules": 1, "schema-diff": 1},
 "features": ["behaviour.l2-count-for-each", "behaviour.null-sides", "cmd:affected", "...", "flag:fixture-check:expect", "..."]}
```

- `version` is the text `mtf-facts version` prints: a tag, or `(devel)` for a local build. `commit` and `modified` come from the build; both are `null` for `go install` of a tag, which records no commit.
- `reports` gives the `schema_version` each command writes at the top of its JSON report, keyed by the command. For `lint` it is the one at `.envelope.schema_version`. `bundle` is not listed: its `schema_version` versions only the optional ownership unit. A key with a hyphen needs the bracket form in jq: `.reports["claims-eval"]`, not `.reports.claims-eval`.
- `features` is sorted. `cmd:<path>` names each command and `flag:<path>:<name>` each long flag it accepts, where `<path>` is the command names joined by dots (`cmd:merge.check`); both are read from the command tree. `<path>.<name>` names a behaviour or an accepted value: `fixture-check.level.L1` to `L3`, `behaviour.null-sides` (with `--schema`, an attribute a change adds or removes gets a null side, `base_absent` or `head_absent`) and `behaviour.l2-count-for-each` (a `count` or `for_each` change selects L2), `behaviour.declarations` (inputs and variable triggers carry `declaration`), `behaviour.schema-required` (with `--schema`, resource and data source triggers carry `schema_required`), `behaviour.off-ladder-cap` (the cap of 40 counts only triggers that may need evidence; up to 40 more that no ladder level can verify are listed apart) and `lint.in-repo-downgrade` (the lint report carries `in_repo_downgrade`) and `lint.no-change-not-applicable` (with no change, a rule that needs the base is `not_applicable` with `no_change_given`) and `depth.module-root` (depth reads the `module_root` and `unresolved` fields of an affected report) and `plan-gates.structural-source` (plan-gates also reads each module source as Terraform does and adds a G2 hit where the two reads differ). A feature name, once released, keeps its meaning and is not removed; the test holds every released name in `cmd/mtf-facts/testdata/features.txt`.

A binary older than this report exits 2 on `--json`. A script that needs a feature checks it with jq, for example `jq -e '.features | index("flag:fixture-check:expect")'`.
