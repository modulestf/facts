# Terraform Modules Facts

`mtf-facts` is an offline Go CLI that parses Terraform module trees and reports deterministic facts: rule results, candidate findings with evidence, interface and schema diffs.

It serves two uses:

- Module authoring: `lint`, a provider coverage `ledger` check, and `schema-diff` for provider upgrades.
- Module review: a facts bundle that lets a model reviewer skip the rules a parser can decide, and `fixture-check`, the allowlist a verification ladder scratch tree passes before Terraform runs on it.

It reads files only. It makes no network call and runs no Terraform or other program.

## Quick start

Install `mtf-facts`, then lint what a branch of your Terraform module changed. On macOS or Linux, install with Homebrew:

```sh
brew install modulestf/tap/mtf-facts
```

Or download a binary from the [releases](https://github.com/modulestf/facts/releases) and verify it against that release's `SHA256SUMS`. The example uses `v0.8.0`; set `V` to the latest tag on the [releases page](https://github.com/modulestf/facts/releases) and `P` to `darwin_arm64`, `darwin_amd64`, `linux_amd64` or `linux_arm64`. Put the binary in a directory on your `PATH`:

```sh
V=v0.8.0 P=darwin_arm64
curl -fsSLO "https://github.com/modulestf/facts/releases/download/$V/mtf-facts_${V}_${P}"
curl -fsSLO "https://github.com/modulestf/facts/releases/download/$V/SHA256SUMS"
shasum -a 256 --ignore-missing -c SHA256SUMS    # sha256sum on Linux
chmod +x "mtf-facts_${V}_${P}" && mv "mtf-facts_${V}_${P}" ~/bin/mtf-facts
```

A module profile is the rule configuration for a module family. The default profile, `terraform-aws-modules`, is in the [modulestf skills repository](https://github.com/modulestf/modulestf/tree/master/.agents/skills/terraform-module-maintainer/profiles):

```sh
git clone --depth 1 https://github.com/modulestf/modulestf.git skills
PROFILE=$PWD/skills/.agents/skills/terraform-module-maintainer/profiles/terraform-aws-modules
```

To lint only what your branch changed, check out its merge base in a worktree next to the module, and pass the worktree path as `--base`:

```sh
cd my-module
git worktree add ../my-module-base "$(git merge-base origin/master HEAD)"
mtf-facts lint . --base ../my-module-base --profile-dir "$PROFILE"
git worktree remove ../my-module-base
```

Without `--base`, `lint` checks the whole tree. Exit 0 means no findings. Exit 1 means at least one finding. Exit 3 means no findings, but a rule it could not decide (`not_evaluated`). Exit 2 means an error, and stdout is then not a report.

The [usage guide](docs/usage.md) has more examples and a worked review of a real pull request. The [command reference](docs/commands.md) has every flag.


## Contents

- [Usage](docs/usage.md)
  - [Install](docs/usage.md#install)
  - [Lint examples](docs/usage.md#lint-examples)
  - [Example: review a pull request](docs/usage.md#example-review-a-pull-request)
  - [Common tasks](docs/usage.md#common-tasks)
  - [Run in a review pipeline](docs/usage.md#run-in-a-review-pipeline)
- [Command reference](docs/commands.md)
  - [lint](docs/commands.md#lint), [affected](docs/commands.md#affected), [depth](docs/commands.md#depth), [behaviour](docs/commands.md#behaviour), [claims-eval](docs/commands.md#claims-eval), [bundle](docs/commands.md#bundle), [merge](docs/commands.md#merge)
  - [profile-check](docs/commands.md#profile-check), [profile-snapshot](docs/commands.md#profile-snapshot), [ledger](docs/commands.md#ledger), [schema-diff](docs/commands.md#schema-diff), [fixture-check](docs/commands.md#fixture-check), [rules, version and completion](docs/commands.md#rules-version-and-completion)
- [How rules are evaluated](docs/rules.md): areas, module ownership, profile options, in-repo downgrade, scope rules at area boundaries, platform
- [Benchmarks](docs/benchmarks.md): run the benchmarks and compare two runs
- [Releases](docs/releases.md): cut a release, verify a release, update a consumer pin
- JSON report schemas: [schema/](schema/)

## License

Apache-2.0. See [LICENSE](LICENSE).
