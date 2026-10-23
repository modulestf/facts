# facts

`mtf-facts` is an offline Go CLI that parses Terraform module trees and reports deterministic facts: rule results, candidate findings with evidence, interface and schema diffs.

It serves two uses:

- Module review: a facts bundle that lets a model reviewer skip the rules a parser can decide.
- Module authoring: `lint`, a provider coverage `ledger` check, and `schema-diff` for provider upgrades.

It reads files only. It makes no network call and runs no Terraform or other program.

## Install

```sh
go install github.com/modulestf/facts/cmd/mtf-facts@latest
```

Or build from a checkout:

```sh
go build -trimpath -o bin/mtf-facts ./cmd/mtf-facts
```

## Usage

Check a whole module tree:

```sh
mtf-facts lint path/to/module
```

Check only the areas a change touched, against the tree the change started from:

```sh
mtf-facts lint path/to/module --base path/to/base-tree
```

Add `--json` for the full report: the envelope, one manifest entry per rule and area (one under `.` for a cross-area rule), findings in the reviewer's findings schema, and candidates. `--registry <rule-facts.md>` records the registry hash, and `--head-sha` and `--merge-base-sha` record the commits the trees came from. Each one is `null` when its flag is absent, and a host that checks a report against its pins must treat `null` as invalid. The JSON schemas are in [schema/](schema/).

`--schema-handoff <file>` names the schema handoff the schema job writes, gzipped or not, and `--schema-files <file>`, repeatable, a `terraform providers schema -json` output. Both are read offline under size caps; a missing, oversized or malformed one exits 2. The handoff is read only when `--head-sha`, `--merge-base-sha` and the `.tf` tree hashes of the head and `--base` trees equal the ones it records; otherwise every lookup in it is `schema_unavailable`. The envelope's `schema_inputs` records the handoff's hash, commits and tree hashes and the hash of each schema file, named by its base name. Two schema files with one base name exit 2. With schema files and no handoff, it records the hash of each schema file and the handoff keys are null. `handoff_sha256` hashes the file's bytes as read, so a gzipped and a plain copy of the same handoff hash differently: a host that checks it must hash the form it passed. The `coverage.*` rules read the bound handoff when one is given, and never fall back to the schema files. With schema files and no handoff, as in local `lint`, they resolve each block's provider source from `required_providers` and read the files as the latest schema; a provider no file has stays `schema_unavailable`. A schema file records no provider version, so its schema facts carry the version `schema-file`, and `coverage.argument-above-min-version`, which needs the declared minimum, stays `not_evaluated(schema_unavailable)`.

`--profile-dir <dir>` names the module profile directory: its `PROFILE.md` profile-constants blocks and the templates they name, read without following a link and with size caps. The output envelope records its `profile_sha256`. A path that is missing, not a directory or itself a link exits 2; a link in a parent component of the path is resolved. A directory whose profile cannot be read or parsed is no error: the rules that need the profile are `not_evaluated` with `profile_unavailable`, as they are without `--profile-dir`.

`--timeout` (default `60s`, must be greater than zero) stops a run that takes longer: it writes `mtf-facts: timeout` to stderr and exits with code 2.

Exit codes:

| Code | Meaning |
|------|---------|
| 0 | No findings, and every rule was decided in every area. |
| 1 | At least one finding. |
| 2 | An error: bad input, a limit, the timeout, or a failed integrity check. |
| 3 | No findings, but at least one rule was `not_evaluated` in some area. |

Host contract:

- Exit code 2 means stdout is not a report. Ignore stdout on exit 2: a failed write to stdout can leave partial output.
- A timeout, or any error before output, writes nothing to stdout.
- Drain both stdout and stderr while the process runs, and keep your own wall-clock kill. `--timeout` does not replace it.

Candidates are unconfirmed: the author confirms or rejects each one, and they never change the exit code.

List the implemented rules and their tier:

```sh
mtf-facts rules
```

### Merge

`mtf-facts merge` reads the bundle file `mtf-facts bundle` writes. It checks a model reviewer's reply to that bundle and merges the two into the final findings. The reply is untrusted: it is read with exact keys, no duplicate key, no trailing data and a 1 MiB cap.

```sh
mtf-facts merge check bundle.json reply.json
mtf-facts merge final --registry rule-facts.md [--report lint.json] [--templates templates.json] \
  bundle.json reply.json [repair.json]
```

`merge check` prints `{"result":"valid"}` and exits 0, or `{"result":"repair","reasons":[...]}` and exits 1 when the reply needs the repair turn. Its reasons are fixed text and quote nothing from the reply.

`merge final` prints the merged findings, in the shape of [merged-findings.schema.json](schema/merged-findings.schema.json), and exits 0. It merges the first reply when that one is valid, and otherwise the repair reply, which must then be given: an empty file when the repair turn returned nothing. A repair reply that is still invalid keeps only its valid entries; a candidate it leaves undecided becomes `review.check-not-run`. `--report` takes a `lint --json` report, whose index derives the subject a lead finding dedups by. `--templates` takes the per-rule host text for confirmed entries the model gave no text for. Exit code 2 is an error, and stdout is then not output.

## Status

Early. `mtf-facts rules` lists the implemented rules and their tier.

Areas are the root module, each `modules/<name>` and each `examples/<name>`, parsed one directory at a time. A change under `.github/` or to a root profile file such as `.editorconfig` counts as the root. With a base tree, the root is in scope only when the change touches it; a cross-area rule still has its one entry under `.`. A cross-area pass reads every area of the module, in scope or not, and keeps only the findings and candidates of the areas in the change. When any area it reads cannot be evaluated, untouched areas included, that entry is `not_evaluated` with one reason code (`parse_error`, `unsupported_file`, `symlink` or `limit_exceeded`) and no findings or candidates. Files Terraform ignores (a leading `.`, a trailing `~`, `#...#`) are skipped. A directory with a parse error, a `.tf.json` file, an override file, a `.tf` file that is a link or cannot be read, or a file over the token, nesting or unary operator limits gets `not_evaluated` for its configuration rules, never `clean`. Path rules still run: a link where a path rule looks for a file counts as present and is never followed. An area directory that is a link or cannot be read gets `not_evaluated` for every rule. No area reads `tests/` or a directory more than one level under `modules/<name>/`, though both hold boundary directories for the scope rules. So `scope.module-call` and `scope.provider` are `not_evaluated(unsupported_file)` at the root when the change adds or edits a `.tf` or `.tf.json` file under `tests/`, and at `modules/<name>` when it adds or edits one in a nested directory there, unless that nested directory parses with no issue and holds only `variable`, `output`, `locals`, `moved` and `removed` blocks. Candidate subjects are scoped to their area, `<area>:<address>`, such as `.:var.name`.

The build supports Unix only: the tree reader opens files with `O_NOFOLLOW`.

Not yet: provider schema input and the remaining rules.

## Releases

Each release publishes one binary per platform, `mtf-facts_<tag>_linux_amd64` and `mtf-facts_<tag>_linux_arm64`, a `SHA256SUMS` file, and a build provenance attestation for each binary. The build is reproducible: the same tag and the Go version in `.go-version` give the same bytes on any host. [scripts/build-release.sh](scripts/build-release.sh) fixes the build settings known to change the output: `-trimpath`, an empty build ID, the toolchain, `CGO_ENABLED=0`, `GOFLAGS=-mod=readonly`, `GOWORK=off`, an empty `GOEXPERIMENT`, `GOFIPS140=off` and the architecture levels. Go embeds no build time; the module version comes from the tag.

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
3. Change the version and every SHA-256 in one commit. A version without its matching SHA-256 values fails the download check, which is the intended result.
4. Keep the attestation check in the download step, with the same flags as above and the pinned tag, so the pin rests on both the hash and the provenance:

   ```sh
   gh attestation verify "mtf-facts_${VERSION}_linux_amd64" -R modulestf/facts \
     --signer-workflow modulestf/facts/.github/workflows/release.yml \
     --source-ref "refs/tags/${VERSION}" \
     --deny-self-hosted-runners
   ```

## License

Apache-2.0. See [LICENSE](LICENSE).
