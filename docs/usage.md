# Usage

Install options, lint examples, a worked review of a real pull request, common tasks and the order a review pipeline runs the commands in. The short path is the [quick start](../README.md#quick-start).

## Install

With Homebrew, on macOS or Linux:

```sh
brew install modulestf/tap/mtf-facts
```

Or with Go:

```sh
go install github.com/modulestf/facts/cmd/mtf-facts@latest
```

Pin a release with `@v0.4.0` in place of `@latest`, or download a release binary and check it as in [Verify a release](releases.md#verify-a-release).

Or build from a checkout:

```sh
go build -trimpath -o bin/mtf-facts ./cmd/mtf-facts
```

## Lint examples

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

Or the AI report a model reviewer reads, which leaves out what a reviewer never decides on (see [AI report](commands.md#ai-report)):

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

Without `--profile-dir`, the rules that need the profile are `not_evaluated` with `profile_unavailable`. Without a schema, a `coverage.*` rule is `not_evaluated(schema_unavailable)` in an area where the change adds or edits a resource or data block. See [lint](commands.md#lint) for each flag.

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

When the ledger is incomplete, the check exits 1 and lists each problem in `problems`. See [ledger](commands.md#ledger).

### Upgrade a provider

Compare the provider schema of the current and the target version, for the types the module tree uses:

```sh
mtf-facts schema-diff --from current.json --to target.json --types-from path/to/module
```

Each schema file is a `terraform providers schema -json` output. See [schema-diff](commands.md#schema-diff).

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
- Pin one version and its SHA-256 per platform, and check the attestation. See [Update a consumer pin](releases.md#update-a-consumer-pin).
