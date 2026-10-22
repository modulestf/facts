# mtf-facts oracle fixtures

Synthetic module trees with a human-curated oracle each, for the deterministic facts engine. They cover every `tool` and `candidate` rule of the reviewer skill's `rule-facts.md` registry with at least one positive and one negative case. `TestOracle` in `internal/facts/oracle_test.go` runs them.

Everything here is written for these fixtures. The resource types (`aws_demo_widget`, `aws_demo_gadget` and the like) are invented, the schemas in `schema-handoff.json` describe only those invented types, and the commit SHAs in it are placeholders. No file is taken from a module repository. Two profile templates are reused byte for byte where a rule compares a file with its template: `.editorconfig` in `profile-*`, and the README banner line in `new-module` and `scope-*`.

## Layout

Each fixture is a directory:

| Path | What it is |
|------|------------|
| `head/` | The tree under review. |
| `base/` | The merge-base tree. Absent in `no-base`, which runs without `--base`. |
| `empty-base` | Marker: the base is an empty tree. The oracle test creates `base/` as an empty directory. |
| `symlinks.txt` | Links the oracle test creates, one `<link> <target>` per line. The link sits under `head/` or `base/` with no `..` or `.` component, and its target, resolved from the link's directory, stays inside the fixture; the oracle test refuses anything else. |
| `head/**/gitattributes` | A `.gitattributes` file, stored without the dot. The oracle test renames it. |
| `schema-handoff.json` | The schema handoff the engine reads, in the shape of `schema-handoff.schema.json`. Only the `coverage-*` fixtures and `compat-versions-floor` have one. |
| `outside.tf` | In `symlinks` only: the file a link points to from outside the tree. |
| `expected.json` | The oracle: `manifest`, `findings` and `candidates`. |

Git keeps no empty directory, and a link or a `.gitattributes` in this repository would act on this repository: a `.gitattributes` with `export-subst` would rewrite files in every archive of it. So those three are created by the oracle test in a copy, and the committed tree has none of them.

## Running

```sh
MTF_FACTS_REGISTRY=/path/to/rule-facts.md go test ./internal/facts -run 'TestOracle|TestLookaheadPatterns'
```

`MTF_FACTS_REGISTRY` names a copy of the reviewer skill's `rule-facts.md`, the one CI fetches at a pinned commit. Without it the registry tests skip, and under CI they fail.

`TestOracle` copies each fixture into a temporary directory and does what the setup script in the repository the fixtures came from did: it creates the links, renames `gitattributes`, creates an empty `base/`, and refuses an unsafe link. It runs the engine on `head/` with `base/` and `schema-handoff.json` when the fixture has them, and with the fixture's own `profile/` directory or, without one, the profile in `testdata/profile`, and compares the results of the implemented rules only, on the keys below. A rule that lands is compared from then on; the oracle files do not change. `TestEveryRuleHasGoldenCases` counts these fixtures as positive and negative cases.

These tests check the oracle files themselves, for every rule, implemented or not:

- `TestOracleSchemas` validates each `expected.json` against [expected.schema.json](expected.schema.json) and each `schema-handoff.json` against the facts `schema-handoff.schema.json`. The validator is the Go subset of JSON Schema in `schema_test.go`. The path patterns RE2 cannot compile, which use lookaheads, are checked by hand-written functions keyed by their exact pattern text. `TestLookaheadPatterns` checks those functions against the results of a JavaScript RegExp on the same text. A pattern RE2 cannot compile that has no function fails as unsupported.
- `TestOracleInvariants` holds the committed fixtures to the registry and to their trees. It checks the areas in scope (see convention 1), and one manifest entry per tool or candidate rule and area in scope. It checks `not_applicable` against `area_kinds`, and that `reason_code` is present exactly on `not_evaluated`, from the closed list. It checks registry severities and engines, sequential ids, and area-scoped subjects. It checks evidence lines against the trees, cited lines against the block their subject names, and handoff file hashes against the trees. Every `tool` and `candidate` rule must have a positive case, a finding or candidate status, in some fixture.
- `TestOracleChecksCatchMutations` and `TestOracleChecksCatchMissingHead` mutate a copy of the fixtures, one change at a time. Each mutation must fail the check it targets, with that check's message.

## The oracle

`expected.json` uses the facts schemas, so a fixture can be compared with a real run field by field:

- `manifest`: `manifest.schema.json`. One entry per tool or candidate rule and per area in scope, in registry order.
- `findings`: the bundle's `tool_finding` from `facts-bundle.schema.json`, the seven finding fields plus `source` and `subject`.
- `candidates`: the bundle's `candidate`, with `source_lines`, `base`, `head` and `schema_facts` evidence.

Compare a run on `{rule_id, area, status, reason_code}` for the manifest, `{rule_id, file, line, severity}` for findings and `{rule_id, file, line, subject}` for candidates. The `summary`, `suggested_fix` and evidence text are curated, not the engine's wording, and the `reason` of a `not_evaluated` entry repeats its `reason_code`, where the binary writes a sentence.

Subjects are area-scoped, `<area>:<address>`: `.:var.retention_days`, `.:aws_demo_widget.extra.bogus_flag`, `examples/complete:local.region`. A file-level subject, such as that of a path rule, is the file or directory path itself. This is the owner's decision.

## Status conventions

The engine's text leaves some outcomes to the reader. These fixtures read them this way. Conventions 1, 3, 5, 8, 10 and 11, the `compat.provider-floor-raised` sentence of convention 4, and the reading of `compat.variable-now-required` below are owner decisions.

1. Areas: with a base, the root `.`, `modules/<name>` and `examples/<name>` that hold a changed, added or removed path; without one, every area. Paths under `.github/` and the root profile files count as `.`, as rule-facts.md says. The root `.` is in scope only when the change touches it, like any other area. A rule with `cross_area` `yes` has exactly one entry, under `.`, for the cross-area pass, whether or not the root is in scope. That pass reads every area of the module, in scope or not; its findings and candidates land only on lines in the change and keep their own `file`. A cross-area candidate is anchored at the change that causes it (owner decision): an example that reads an output the change removes gets its `examples.stale-reference` candidate on the called module's `outputs.tf` with a null line, and one the change renames on the head line of the new output; a candidate with no line is in the change when its path is. The entry is never `not_applicable`. If an area the pass reads cannot be read, or for a `config-semantic` rule cannot be parsed, the entry is `not_evaluated` with no items; a `path` rule still runs, as convention 3 says. `TestOracleInvariants` derives the areas the change touches from `base/` and `head/`, comparing each file's content and mode and each link's target in `symlinks.txt`, and holds the manifest to them.
2. `not_applicable` exactly when the area kind is outside the rule's `area_kinds`.
3. In an area with a parse error, a `.tf.json` or override file, a `.tf` file that is a link, or a file over a limit, every `config-semantic` rule is `not_evaluated` with `parse_error`, `unsupported_file`, `symlink` or `limit_exceeded`. Path rules still run. A path rule whose subject is a link runs on it: the link counts as present and is never followed, so an example whose `versions.tf` is a link has no `examples.versions-missing` finding. An area directory that is itself a link is `not_evaluated` for every applicable rule.
4. A `coverage.*` rule is `not_evaluated` with `schema_unavailable` in an area where the change adds or edits a `resource` or `data` block and no handoff is given. With no such block it is `clean`. In the same way, `compat.provider-floor-raised` is `not_evaluated` with `schema_unavailable` in an area where the change raises a provider floor within a major version and no handoff is given. With no such raise it is `clean`.
5. Without `--base`, every block counts as added, as the binary already reads it. Rules whose check text tells a change from a new module or reads the base are `not_evaluated` with `no_base`: every `compat.*` and `scope.*` rule, `examples.stale-reference`, `examples.submodule-missing`, `profile.template-drift`, `profile.required-file-missing` and `profile.putin-khuylo-missing`.
6. An empty base is a known new module, not a missing base: nothing can break, so `compat.*` is `clean`.
7. `profile.required-file-missing` fires for a module the change adds. A directory the change adds inside an existing module is held to its own file set, and an existing module missing a file it never had is no finding of this change.
8. A new variable with no default, in a module that exists at the base, is `compat.variable-now-required`: every existing caller must now set it.
9. For `compat.provider-floor-raised`, `schema_facts` are the arguments set in the `resource` and `data` blocks of the area, looked up at the old floor. Attributes the area reads, such as an `id` in an output, are not included.
10. A new variable with a non-null default in an existing module is a `compat.default-changed` candidate.
11. An added object attribute is a `compat.type-narrowed` candidate, `optional()` with a default included.

## Coverage

| `rule_id` | Positive | Negative |
|-----------|----------|----------|
| `compat.default-changed` | `compat-variables-positive` | `compat-variables-negative` |
| `compat.generated-policy-narrowed` | `compat-resources-positive` | `compat-resources-negative` |
| `compat.provider-floor-raised` | `compat-versions-floor` | `compat-versions-major` |
| `compat.resource-address-changed` | `compat-resources-positive` | `compat-resources-negative` |
| `compat.type-narrowed` | `compat-variables-positive`, `compat-variables-negative` | `compat-resources-negative` |
| `compat.variable-now-required` | `compat-variables-positive` | `compat-variables-negative` |
| `compat.variable-removed` | `compat-variables-positive`, `examples-positive`, `examples-negative` | `compat-variables-negative` |
| `compat.version-constraint-raised` | `compat-versions-major` | `compat-versions-minor` |
| `coverage.argument-above-min-version` | `coverage-positive` | `coverage-negative` |
| `coverage.attribute-not-output` | `coverage-positive` | `coverage-negative` |
| `coverage.block-attribute-mismatch` | `coverage-positive` | `coverage-negative` |
| `coverage.deprecated-argument` | `coverage-positive` | `coverage-negative` |
| `coverage.optional-argument-unexposed` | `coverage-positive` | `coverage-negative` |
| `coverage.required-argument-unreachable` | `coverage-positive` | `coverage-negative` |
| `coverage.unknown-argument` | `coverage-positive` | `coverage-negative` |
| `examples.disabled-call-missing` | `examples-positive` | `examples-negative` |
| `examples.stale-reference` | `examples-positive` | `examples-negative` |
| `examples.submodule-missing` | `examples-positive`, `scope-positive` | `examples-negative`, `scope-negative` |
| `examples.versions-missing` | `examples-positive`, `parse-error`, `no-base` | `examples-negative` |
| `profile.nonstandard-config-file` | `nonstandard-positive` | `nonstandard-negative` |
| `profile.putin-khuylo-missing` | `profile-positive` | `profile-negative` |
| `profile.region-not-allowed` | `profile-positive` | `profile-negative` |
| `profile.required-file-missing` | `new-module` | `examples-negative` |
| `profile.template-drift` | `profile-positive`, `profile-file-only` | `profile-negative`, `scope-negative` |
| `scope.module-call` | `scope-positive` | `scope-negative` |
| `scope.new-submodule` | `scope-positive` | `scope-negative`, `examples-positive` |
| `scope.pre-commit-hook` | `scope-positive` | `scope-negative` |
| `scope.profile-file` | `scope-positive` | `scope-negative` |
| `scope.provider` | `scope-positive` | `scope-negative` |
| `scope.readme-banner` | `scope-positive` | `scope-negative` |
| `scope.tooling` | `scope-positive` | `scope-negative` |
| `structure.argument-order` | `structure-positive` | `structure-negative` |
| `structure.arn-partition-hardcoded` | `structure-positive` | `structure-negative` |
| `structure.feature-above-version-floor` | `version-floor-positive` | `version-floor-negative` |
| `structure.missing-description` | `structure-positive`, `gitattributes`, `submodule-only` | `structure-negative`, `deep-nesting` |
| `structure.multi-provider` | `structure-positive` | `structure-negative` |
| `structure.output-no-try` | `structure-positive` | `structure-negative` |
| `structure.passthrough-default-drift` | `passthrough-positive` | `passthrough-negative` |
| `structure.passthrough-field-drift` | `passthrough-positive` | `passthrough-negative` |
| `structure.variable-untyped` | `structure-positive`, `no-base` | `structure-negative`, `deep-nesting` |
| `structure.version-floor-understated` | `version-floor-positive` | `version-floor-negative` |
| `structure.wrong-file` | `structure-positive` | `structure-negative` |

A negative is a near miss of its positive where one exists, and its entry for the rule is `clean` in an area where the rule applies. Most fixtures are `clean` for most other rules too; the manifest lists every entry.

Hard cases:

| Case | Fixtures |
|------|----------|
| `dynamic` blocks | `coverage-negative`: a required nested block filled by `dynamic` over an input is reachable |
| `count` to `for_each` | `compat-resources-positive` without `moved`, `compat-resources-negative` with one `moved` block per instance key |
| `moved` blocks | `compat-resources-negative`: a rename and an instance key change, both moved |
| `optional()` with defaults | `compat-variables-positive` (a changed default), `compat-variables-negative` (a new attribute with a default is a `compat.type-narrowed` candidate, convention 11), `passthrough-positive` (an unset value that differs from the submodule's) |
| `any` | `compat-variables-positive` (`any` narrowed), `compat-variables-negative`, `structure-positive` (a new `any` input) |

Adversarial cases:

| Case | Fixture |
|------|---------|
| `.gitattributes` with `export-ignore` and `export-subst` | `gitattributes` |
| Links: a `.tf` file pointing out of the tree, an area directory that is a link, a link where `versions.tf` belongs | `symlinks` |
| Deep nesting: HCL nested past the limit, and a `.tf` file in a subdirectory of a submodule | `deep-nesting` |
| `.tf.json` and override files | `nonstandard-positive`, with `nonstandard-negative` as the near miss |
| Parse errors at the root and in an example | `parse-error` |
| A change to a root profile file and nothing else | `profile-file-only` |
| A change to one submodule and nothing else: no root entries | `submodule-only` |
| A change to one example and nothing else: no root entries | `example-only` |
| An empty base | `new-module` |
| No base | `no-base` |

## Fixtures

- `structure-positive`: root only. New variables, outputs and blocks that each break one Check C convention. Coverage rules have touched blocks and no schema, so they are not evaluated.
- `structure-negative`: near misses of `structure-positive`: typed and described inputs, an output with `try()`, `count` first and `tags` last in the touched block, the role in the `iam.tf` the directory has, the ARN built from the partition, an ARN inside a comment, an untouched block with bad order and an untouched untyped variable from the base, and an aliased provider in an example, where `structure.multi-provider` does not apply.
- `version-floor-positive`: `templatestring()` is in the core feature table at 1.9.0, above the root floor 1.5.7. The example lowers its floor below the root it calls.
- `version-floor-negative`: `strcontains()` is in the table at 1.5.0, below the floor 1.5.7. The example raises its floor above the root, which the rule allows.
- `passthrough-positive`: the root adds a call to a local submodule and forwards three inputs. Two unset values differ from the submodule defaults, and the submodule type has an attribute the root type lacks. The candidate lines are where the root declares the type or attribute.
- `passthrough-negative`: near misses: an equal default, a root null into a submodule variable with `nullable = false`, and settings passed through `merge()`, which is not forwarding and is not compared.
- `compat-variables-positive`: a removed variable, a default removed, `any` narrowed to `map(string)`, a `validation` block added, a changed default, a changed `optional()` default, and a new variable with no default. A removed variable has no head line, so its line is null and its evidence is the merge base line.
- `compat-variables-negative`: near misses: a description edit, a list default written with other spacing but equal, and a new variable with a null default. A new `optional()` attribute with a default is a `compat.type-narrowed` candidate (convention 11).
- `compat-resources-positive`: `count` to `for_each` with no `moved` block, a resource renamed with no `moved` block, and an action removed from a policy document the module builds.
- `compat-resources-negative`: near misses: `count` to `for_each` with a `moved` block per instance key, a rename with a `moved` block, and an action added to the policy document.
- `compat-versions-major`: the core floor rises and the provider floor crosses a major version. Both are `compat.version-constraint-raised`; `compat.provider-floor-raised` stays `clean`.
- `compat-versions-minor`: a minor provider floor raise with no schema handoff: `compat.provider-floor-raised` is `not_evaluated` with `schema_unavailable` (convention 4), and `compat.version-constraint-raised` stays `clean`.
- `compat-versions-floor`: the tree of `compat-versions-minor` with a schema handoff for the latest version and declared minimum (6.2.0) and the old floor (6.0.0). The raise from `>= 6.0` to `>= 6.2` is a `compat.provider-floor-raised` candidate; its schema facts, read as convention 9 says, show that every argument the module sets is present at the old floor. The 6.0.0 and 6.2.0 schemas are the same, so no fixture covers yet a change that uses an argument absent at the old floor. The change touches no `resource` block, so every coverage rule is `clean`.
- `examples-positive`: the root removes an output the complete example still reads, the complete example loses its disabled call, the basic example keeps its constraints in `terraform.tf`, and a new submodule has no example. The submodule manages only an adjunct type, so `scope.new-submodule` stays `clean`.
- `examples-negative`: near misses: the root removes an output no example reads (still a compat candidate), the complete example keeps its disabled call, the basic example has none and needs none, every example has `versions.tf`, and the new submodule has an example of its own.
- `profile-file-only`: only `.editorconfig` changes. It counts as the root, so `.` is in scope and the edit is a `profile.template-drift` candidate.
- `submodule-only`: the change adds a typed variable with no description to `modules/extra` and touches nothing else. Only `modules/extra` is in scope: the manifest has no root entry for any rule but the cross-area rules, which keep their one `.` entry. The tree is the head of `examples-negative`, and the root does not call the submodule.
- `example-only`: the change edits one argument of the module call in `examples/basic` and touches nothing else. Only `examples/basic` is in scope, every entry there is `clean` or `not_applicable`, and the cross-area rules keep their one `.` entry. Same tree as `submodule-only`.
- `profile-positive`: a new root block gated on `var.create` alone, a profile-owned file edited away from its template, and an example region outside the two the profile allows.
- `profile-negative`: near misses: the new block uses `local.create`; `.editorconfig` carried drift at the base and the change restores the template line; the example adds a region as a map key and inside an ARN, which choose no region.
- `new-module`: a whole module with an empty base. The module files, both examples and the banner are present; the `LICENSE` and the nine profile dotfiles and workflows are not, one finding per path. Nothing at the base can break, so every compat rule is `clean`.
- `scope-positive`: a new provider, a registry module call, a new submodule with a managed type that is not an adjunct, a scanner config, a new pre-commit hook, a new workflow file and the banner line removed. The new submodule also has no example; the merge step pairs the two, and the engine reports each. The new hook is not read as template drift: `.pre-commit-config.yaml` is the profile file whose content is expected to move ahead of its template.
- `scope-negative`: near misses: a block of a base provider, a local module call, a new submodule of adjunct types with its own example, a linter config the closed list does not name (left to the layout rule, an `llm` rule), a pin-only pre-commit change, `docs/SCOPE.md`, and a README that lacked the banner at the base and still lacks it.
- `coverage-positive`: one new resource with a schema handoff for the latest version (6.2.0) and the declared minimum (6.0.0): an argument the schema lacks, a deprecated one, one absent at the minimum, attribute syntax for a block, the required `name` never set, an optional argument neither set nor exposed, and two computed attributes with no output.
- `coverage-negative`: near misses with the same handoff: every argument exists at the declared minimum, the required `name` comes from an input with a default (so the new input is no `compat.variable-now-required` candidate), the required `rule` block comes from a `dynamic` block over an input, every optional argument is set, and both computed attributes have outputs.
- `nonstandard-positive`: an override file at the root and a `.tf.json` file in a submodule. The path rule reports both; every config-semantic rule in both areas is not evaluated, never `clean`.
- `nonstandard-negative`: near miss: `overrides.tf` matches neither `override.tf` nor `*_override.tf`, so the area parses.
- `parse-error`: the root `main.tf` ends inside a block and the example has an argument with no value. Config-semantic rules are not evaluated in both areas; path rules still run, and the example still gets its `versions.tf` finding.
- `symlinks`: the oracle test creates three links: a root `.tf` file that points out of the tree at a file with an undescribed variable, a submodule directory that is a link, and an example `versions.tf` that is a link. No rule follows a link: the config rules that would read one are not evaluated, the link at `versions.tf` counts as present for `examples.versions-missing`, and the escaped variable gives no finding. `profile.nonstandard-config-file` is not evaluated in `.` and `examples/complete`: its OpenTofu-only block case reads the bytes of every `.tf` file the change adds or edits, and the links there are never read. A known gap: an area where the change adds both such a link and a file the name case reports, such as `x_override.tf`, is `not_evaluated`, and the HIGH finding for the file name is dropped, because a manifest entry cannot be both.
- `gitattributes`: the oracle test renames `gitattributes` to `.gitattributes`. It marks `hidden.tf` `export-ignore` and `stamp.tf` `export-subst`. The engine reads the tree as committed: `hidden.tf` is still parsed, so its undescribed variable is a finding, and `stamp.tf` keeps its `$Format:%H$` text. A host that extracted the tree with `git archive` would lose the finding.
- `deep-nesting`: `deep.tf` nests 70 brackets, over the 64 the engine allows, so the root config rules are not evaluated. A `.tf` file two directories below a submodule is not part of that module: its undescribed, untyped variable gives no finding and no candidate.
- `no-base`: `lint` with no `--base`: every area is in scope and every block counts as added. The rules convention 5 names are not evaluated with reason code `no_base`.

## Comparison with the binary

On 2026-10-21 the binary built from the facts repository's default branch implemented `examples.versions-missing`, `structure.missing-description` and `structure.variable-untyped`. Run on every fixture after the setup script in the repository the fixtures came from, it matched these oracles on every status, finding and candidate of those rules, with no difference, `submodule-only` and `example-only` included: it scopes the root only when the change touches it, counts `.github/` and the root profile files as the root, and writes area-scoped subjects.

None of its three rules is a cross-area rule, so convention 1's single `.` entry and the cross-area pass show no difference yet; the engine change is tracked in the facts repository's beads.

## Decided

- The root `.` is in scope only when the change touches it; `.github/` and the root profile files count as the root (convention 1).
- Cross-area rules have one manifest entry under `.`, read every area and land findings only on lines in the change (convention 1).
- A path rule runs on a symlinked subject, which counts as present (convention 3).
- Without `--base`, the rules that need the base are `not_evaluated` with `no_base` (convention 5).
- A provider floor raised within a major version with no schema handoff is `compat.provider-floor-raised` `not_evaluated` with `schema_unavailable`, never a candidate (convention 4).
- A new variable with no default is `compat.variable-now-required` (convention 8).
- Subjects are `<area>:<address>`, and a path rule's subject is its path.
- Adding a provider the base never had is `scope.provider` and no `compat.*` rule: Check A has no bullet for it.

## Open questions

- `lint.schema.json` allows candidate evidence for `structure.variable-untyped` only, so the candidates here use the bundle's general evidence shape. That is schema work in the facts repository.
