# Data

`core-features.json` is a pinned table of Terraform core language features and the Terraform version that introduced each one. The rule `structure.feature-above-version-floor` reads it.

Only features that can be found in HCL syntax are listed: block types, meta-arguments, built-in functions, type constraints, block arguments and expression forms. A feature that needs the provider schema to detect is not here. Write-only arguments (Terraform 1.11) are left out for that reason; the provider-schema rule covers them.

A feature the table does not list is `not_evaluated`. Its absence is never read as "available in every version".

## How the rule uses the table

- The floor is the lowest Terraform version the module's `required_version` constraint admits. For `>= 1.3` or `>= 1.3.0` it is 1.3.0. For `~> 1.3` it is 1.3.0, and for `~> 1.3.2` it is 1.3.2. For a range such as `>= 1.3, < 2.0` it is the lowest version that satisfies every part. A `!=` excludes only that one version, so `>= 1.3, != 1.3.0` gives 1.3.1. For `= 1.5.7`, or a bare `1.5.7`, it is 1.5.7.
- Versions are compared as `MAJOR.MINOR.PATCH`, down to the patch. The rule fires when the floor is lower than the row's `introduced`.
- A construct can match more than one row. For example, an `import` block with `for_each` matches `import_block` and `import_for_each`, and a `moved` block from `null_resource` to `terraform_data` matches both `moved_cross_resource_type` (1.8.0) and `moved_null_resource_to_terraform_data` (1.9.0). The same happens with the `moved`, `removed`, `optional`, `terraform_data`, `action` and provider function rows. The rule uses the matching row with the highest `introduced` and cites that row.
- With no `required_version` constraint, the rule's own check text decides. If the check text does not say, the result is `not_evaluated`.

## What is scanned

- Only HCL expressions and blocks in `.tf` files. Template text in heredocs and the files that `templatefile` reads are not scanned.
- A `function` row matches only a call to that exact name. A `provider::` call matches a built-in function row only when the row's `match` names the `provider::` form; any other `provider::` call matches `provider_defined_function`.

## Row schema

Each row is a JSON object with these keys:

- `id`: unique snake_case name of the feature.
- `kind`: one of `block`, `meta_argument`, `function`, `type_constraint`, `argument`, `expression`.
- `match`: what in HCL shows the feature is used, precise enough to write a detector.
- `introduced`: the first Terraform release with the feature as generally available, as `MAJOR.MINOR.PATCH`. An opt-in experiment does not count.
- `source`: a public URL that states the version: the Terraform CHANGELOG for that minor on github.com/hashicorp/terraform, or a page on developer.hashicorp.com.

## Adding a row

1. Find the feature in the CHANGELOG of the minor that introduced it (`https://github.com/hashicorp/terraform/blob/v<MAJOR.MINOR>/CHANGELOG.md`), under the `<MAJOR.MINOR>.0` heading or the patch release that added it. For releases older than v0.12, cite a later changelog that carries the older history. For example, the v0.11 changelog holds the 0.6.16 entry that `output_sensitive` cites.
2. Add the row with that version and URL. Do not take the version from memory.
3. Keep `id` unique and the file plain ASCII.
4. A feature you cannot verify against a source stays out.
