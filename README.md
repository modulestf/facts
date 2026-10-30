# Terraform Modules Facts

`mtf-facts` is an offline Go CLI that parses Terraform module trees and reports deterministic facts: rule results, candidate findings with evidence, interface and schema diffs.

It serves two uses:

- Module authoring: `lint`, a provider coverage `ledger` check, and `schema-diff` for provider upgrades.
- Module review: a facts bundle that lets a model reviewer skip the rules a parser can decide.

It reads files only. It makes no network call and runs no Terraform or other program.

## Install

```sh
go install github.com/modulestf/facts/cmd/mtf-facts@latest
```

Pin a release with `@v0.3.0-rc.2` in place of `@latest`, or download a release binary and check it as in [Verify a release](#verify-a-release).

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
git -C skills checkout de257cfac1df0bcf91db77bba665fdfc626f45fd
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

With `v0.3.0-rc.2` it prints this and exits 1 (the `not evaluated` lines are cut):

```text
examples/fixtures: MEDIUM examples.versions-missing: Example "examples/fixtures" has no versions.tf, so its Terraform and provider constraints are not in the file the profile and its sibling examples use.
  fix: Add `versions.tf` to `examples/fixtures`, copied from a sibling example, or rename the file that holds its `terraform` block to `versions.tf`.
examples/function-url/main.tf:45: unconfirmed structure.version-floor-understated: examples/function-url:required_providers.external, confirm or reject
examples/function-url/main.tf:45: unconfirmed structure.version-floor-understated: examples/function-url:required_providers.local, confirm or reject
examples/function-url/main.tf:45: unconfirmed structure.version-floor-understated: examples/function-url:required_providers.null, confirm or reject
1 findings, 3 candidates; manifest: 1 finding, 1 candidate, 75 clean, 2 not_evaluated, 68 not_applicable
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

1. `lint --json` on the head tree with `--base`, `--registry`, `--profile-dir`, `--schema-handoff`, `--head-sha` and `--merge-base-sha`.
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
mtf-facts lint <dir> [--base <dir> | --diff <file>] [--full-tree] [--json] [--timeout <duration>]
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
| `--timeout <duration>` | Stop a run that takes longer. Default `60s`. |
| `--registry <rule-facts.md>` | Record the registry hash. |
| `--profile-dir <dir>` | The module profile directory. |
| `--schema-files <file>` | A `terraform providers schema -json` output. Repeatable. |
| `--schema-handoff <file>` | The schema handoff the schema job writes, gzipped or not. |
| `--head-sha <sha>` | Record the commit the head tree came from. |
| `--merge-base-sha <sha>` | Record the commit the base tree came from. |

The `--json` report holds the envelope, one manifest entry per rule and area (see [Areas](#areas) for a cross-area rule), findings in the reviewer's findings schema, and candidates. It also holds:

- `scope`: how the change was given (`none`, `base` or `diff`), `full_tree`, and three disjoint lists of areas. `areas_checked` have their own manifest entries. `areas_calling` are the other areas that call a module listed in `callers`. `areas_left_out` are the rest, so no area is dropped without a trace.
- `callers`, with a change only: each root or submodule area the change touches whose `versions.tf` or `terraform` block, or `variables.tf` or a `variable` block, changed, with its version floors at base (`null` for a new module) and at head, and every area that calls it by a local path: the call's file and line, whether that area is checked, the floors it declares, and the ids of the `structure.version-floor-understated` candidates filed in it. A floor is an `address` (`required_version` or `required_providers.<name>`), the provider `source` and the `constraint` as written. Callers under `wrappers/` are not listed: no area reads that directory.

The text report prints a `scope:` line, and with a change one line per changed module with the floors the change moves, and one line per caller with its own floor at those addresses. The JSON schemas are in [schema/](schema/).

#### Change as a diff

`--diff` reads a GitHub pull request files list, the JSON array `gh api repos/<owner>/<repo>/pulls/<number>/files --paginate --slurp | jq add` writes, or a unified diff as `git diff` writes it, with or without `diff --git` lines. The format is detected from the first byte. Paths must be relative to `<dir>`. The base tree is the head tree with each changed file rebuilt by reversing its hunks: a removed file comes back from its removed lines, an added file is left out, and a renamed file moves back to its old path. Every context and added line must match the head file, line ending included, or the run exits 2, and so does a path that is not clean and relative, a path listed twice, or a status the tool does not know.

A file the diff gives no patch for, as GitHub does for a binary or very large file, has no known base content. It is in `inputs.base` with kind `no_patch`, and the configuration rules of its area are `not_evaluated` with `no_base` and a reason that says the diff has no patch for it. A GitHub rename with no patch and no added or removed line is a pure rename when the head file is text, as git judges it (no NUL byte in the first 8000 bytes), and a binary one is `no_patch`. The envelope records the diff in `inputs.diff`: its `format`, `github-files` or `unified`, and its SHA-256. A report from `--diff` has a base tree, so `bundle` accepts it.

Two schema files with one base name exit 2. The schema handoff and the schema files are read offline under size caps; a missing, oversized or malformed one exits 2. The handoff is read only when `--head-sha`, `--merge-base-sha` and the `.tf` tree hashes of the head and `--base` trees equal the ones it records; otherwise every lookup in it is `schema_unavailable`.

The envelope's `schema_inputs` records the handoff's hash, commits and tree hashes, and the hash of each schema file, named by its base name. With schema files and no handoff, it records the hash of each schema file and the handoff keys are null. `handoff_sha256` hashes the file's bytes as read, so a gzipped and a plain copy of the same handoff hash differently: a host that checks it must hash the form it passed.

The `coverage.*` rules read the bound handoff when one is given, and never fall back to the schema files. With schema files and no handoff, as in local `lint`, they resolve each block's provider source from `required_providers` and read the files as the latest schema; a provider no file has stays `schema_unavailable`. A schema file records no provider version, so its schema facts carry the version `schema-file`, and `coverage.argument-above-min-version`, which needs the declared minimum, stays `not_evaluated(schema_unavailable)`.

`--profile-dir` reads the directory's `PROFILE.md` profile-constants blocks and the templates they name, without following a link and with size caps. The output envelope records its `profile_sha256`. A path that is missing, not a directory or itself a link exits 2; a link in a parent component of the path is resolved. A profile that cannot be read or parsed is no error: the rules that need it are `not_evaluated` with `profile_unavailable`, as they are without `--profile-dir`.

`--timeout` must be greater than zero. A run that takes longer writes `mtf-facts: timeout` to stderr and exits with code 2.

The exit codes are in [Quick start](#quick-start).

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

`merge final` prints the merged findings, in the shape of [merged-findings.schema.json](schema/merged-findings.schema.json), and exits 0. It merges the first reply when that one is valid, and otherwise the repair reply, which must then be given: an empty file when the repair turn returned nothing. A repair reply that is still invalid keeps only its valid entries; a candidate it leaves undecided becomes `review.check-not-run`. `--report` takes a `lint --json` report, whose index derives the subject a lead finding dedups by. `--templates` takes the per-rule host text for confirmed entries the model gave no text for.

`merge counts` takes the replies as `merge final` does, picks the same reply, and prints its counts as `{"counts":{...}}`, or `{"counts":null}` when that reply has none, and exits 0. A reply without counts has none: no count is ever read as 0 for its absence. A repair reply that is still invalid keeps its counts when they are well formed. `merge counts` does not load the registry, the report or the templates, so it can succeed where `merge final` fails: a host reads the counts only after `merge final` succeeds.

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

With a base tree, the root is in scope only when the change touches it. A cross-area rule has one entry under `.` for its pass, whether or not the root is in scope, and one more under each other area that holds one of its findings or candidates: the deepest area directory that holds the item's `file`. The `.` entry is `not_evaluated` when the pass is, and otherwise has the status of the items in the root area, `clean` when there are none. A `structure.version-floor-understated` candidate points at the caller's own floor, or at the call when the caller declares none, so it is filed under the caller's area; it is in the change when the caller's floor, the callee's floor or the call is a changed line. A cross-area pass reads every area of the module, in scope or not, and keeps only the findings and candidates of the areas in the change. When any area it reads cannot be evaluated, untouched areas included, that entry is `not_evaluated` with one reason code (`parse_error`, `unsupported_file`, `symlink` or `limit_exceeded`) and no findings or candidates.

Files Terraform ignores (a leading `.`, a trailing `~`, `#...#`) are skipped. A directory gets `not_evaluated` for its configuration rules, never `clean`, when it has a parse error, a `.tf.json` file, an override file, a `.tf` file that is a link or cannot be read, or a file over the token, nesting or unary operator limits. Path rules still run: a link where a path rule looks for a file counts as present and is never followed. An area directory that is a link or cannot be read gets `not_evaluated` for every rule.

### Scope rules at area boundaries

No area reads `tests/` or a directory more than one level under `modules/<name>/`, though both hold boundary directories for the scope rules. So these rules are `not_evaluated(unsupported_file)` in these cases:

- `scope.module-call` and `scope.provider` at the root, when the change adds or edits a `.tf` or `.tf.json` file, or any link, special or unreadable entry, under `tests/`, or when `tests` itself is such an entry.
- `scope.module-call` and `scope.provider` at `modules/<name>`, when the change adds or edits a `.tf` or `.tf.json` file, or any link, special or unreadable entry, in a nested directory there, or adds a link, special or unreadable entry directly in `modules/<name>/` that is not a `.tf` or `.tf.json` file (one that is keeps its own reason code).
- `scope.new-submodule` at `modules/<name>`, on the same entries when they lie in a nested directory the base lacks, or are a link, special or unreadable entry.

The `modules/<name>` cases exempt a regular `.tf` file in a nested directory that parses with no issue and holds only `variable`, `output`, `locals`, `moved` and `removed` blocks. For a new `modules/<name>`, a candidate from its own top level comes first, and the nested entries give `not_evaluated` only when that top level gives none.

### Platform

The build supports Unix only: the tree reader opens files with `O_NOFOLLOW`.

## Releases

The current release is the pre-release `v0.3.0-rc.2`. Each release publishes one binary per platform, `mtf-facts_<tag>_linux_amd64` and `mtf-facts_<tag>_linux_arm64`, a `SHA256SUMS` file, and a build provenance attestation for each binary. The build is reproducible: the same tag and the Go version in `.go-version` give the same bytes on any host. [scripts/build-release.sh](scripts/build-release.sh) fixes the build settings known to change the output: `-trimpath`, an empty build ID, the toolchain, `CGO_ENABLED=0`, `GOFLAGS=-mod=readonly`, `GOWORK=off`, an empty `GOEXPERIMENT`, `GOFIPS140=off` and the architecture levels. Go embeds no build time; the module version comes from the tag.

### Cut a release

1. Pick a commit on `master` whose `ci` run passed. The workflow refuses a tag whose commit is not on `master`.
2. Tag it and push the tag. Use a pre-release tag such as `v0.1.0-rc.1` to test the workflow; it is published as a pre-release.

   ```sh
   git tag -a v0.1.0 -m v0.1.0 <commit>
   git push origin v0.1.0
   ```

3. The `release` workflow builds both binaries, attests them and creates the GitHub release. The build job holds no token that can sign or write. The attestation runs in the `release` environment, so it waits for approval once that environment has reviewers. Only the job that creates the release can write to the repository.

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
