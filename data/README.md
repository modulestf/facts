# Data

`core-features.json` is a pinned table of Terraform core language features and the Terraform version that introduced each one. The rule `structure.feature-above-version-floor` reads it: when a module uses a listed feature and its `required_version` does not reach the `introduced` version, the rule fires.

Only features that can be found in HCL syntax are listed: block types, meta-arguments, built-in functions, type constraints, block arguments and expression forms. Provider schema facts are not here.

A feature the table does not list is `not_evaluated`. Its absence is never read as "available in every version".

## Row schema

Each row is a JSON object with these keys:

- `id`: unique snake_case name of the feature.
- `kind`: one of `block`, `meta_argument`, `function`, `type_constraint`, `argument`, `expression`.
- `match`: what in HCL shows the feature is used, precise enough to write a detector.
- `introduced`: the first Terraform release with the feature as generally available, as `MAJOR.MINOR.PATCH`. An opt-in experiment does not count.
- `source`: a public URL that states the version: the Terraform CHANGELOG for that minor on github.com/hashicorp/terraform, or a page on developer.hashicorp.com.

## Adding a row

1. Find the feature in the CHANGELOG of the minor that introduced it (`https://github.com/hashicorp/terraform/blob/v<MAJOR.MINOR>/CHANGELOG.md`), under the `<MAJOR.MINOR>.0` heading or the patch release that added it.
2. Add the row with that version and URL. Do not take the version from memory.
3. Keep `id` unique and the file plain ASCII.
4. A feature you cannot verify against a source stays out.
