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
go build -trimpath -mod=vendor -o bin/mtf-facts ./cmd/mtf-facts
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

Add `--json` for the full report: the envelope, one manifest entry per rule and area, findings in the reviewer's findings schema, and candidates. `--registry <rule-facts.md>` records the registry hash, and `--head-sha` and `--merge-base-sha` record the commits the trees came from. Each one is `null` when its flag is absent, and a host that checks a report against its pins must treat `null` as invalid. The JSON schemas are in [schema/](schema/).

`--timeout` (default `60s`) stops a run that takes longer, with exit code 2.

Exit codes:

| Code | Meaning |
|------|---------|
| 0 | No findings, and every rule was decided in every area. |
| 1 | At least one finding. |
| 2 | An error: bad input, a limit, the timeout, or a failed integrity check. |
| 3 | No findings, but at least one rule was `not_evaluated` in some area. |

Candidates are unconfirmed: the author confirms or rejects each one, and they never change the exit code.

List the implemented rules and their tier:

```sh
mtf-facts rules
```

## Status

Early. Implemented rules:

| Rule | Tier |
|------|------|
| `examples.versions-missing` | tool |
| `structure.missing-description` | tool |
| `structure.variable-untyped` | candidate |

Areas are the root module, each `modules/<name>` and each `examples/<name>`, parsed one directory at a time. Files Terraform ignores (a leading `.`, a trailing `~`, `#...#`) are skipped. A directory with a parse error, a `.tf.json` file, an override file, a `.tf` file that is a link or cannot be read, or a file over the token, nesting or unary operator limits gets `not_evaluated` for its configuration rules, never `clean`. An area directory that is a link or cannot be read gets `not_evaluated` for every rule.

The build supports Unix only: the tree reader opens files with `O_NOFOLLOW`.

Not yet: provider schema input, `ledger`, `schema-diff`, and the remaining rules.

## License

Apache-2.0. See [LICENSE](LICENSE).
