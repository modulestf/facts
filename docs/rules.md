# How rules are evaluated

All 42 tool and candidate rules of the reviewer's rule registry are implemented. CI checks that the `mtf-facts rules` listing equals the registry's tool and candidate rules, tier for tier, against a registry pinned by commit and SHA-256.

## Areas

Areas are the root module, each `modules/<name>` and each `examples/<name>`, parsed one directory at a time. A change under `.github/` or to a root profile file such as `.editorconfig` counts as the root. Candidate subjects are scoped to their area, `<area>:<address>`, such as `.:var.name`.

A tree outside that layout, such as a monorepo with `infra/modules/network` called from `infra/live/prod`, gets one more kind of area, `module`: each other directory that directly holds a `.tf` or `.tf.json` file, at head or at base. A file belongs to the nearest such directory above it, whatever its extension, so a change to `infra/modules/network/main.tf` puts that module in scope, not the root. Directories with a `tests`, `wrappers` or `examples` segment, a dot segment or a top-level `modules/` are not discovered, and their files stay the root's as before, so a module under such a directory is read as part of the root. A module area takes only the `compat.*` and `coverage.*` rules; the convention, profile and layout rules (`structure.*`, `profile.*`, `scope.*`, `examples.*`) are `not_applicable` there, `scope.tooling` included, so a scanner config file added under a module is not judged. A local `source` that resolves to a module area counts as a call in `callers` and `scope.areas_calling`. A directory that held configuration at base and is a link at head stays an area and is `not_evaluated` for every rule that applies; a link that never held configuration is not an area, and links are never followed. A tree with more than 512 discovered areas is a limit: exit 2, `area_limit_exceeded`, no report. Reports of a tree with no discovered area are unchanged.

With a base tree, the root is in scope only when the change touches it. A cross-area rule has one entry under `.` for its pass, whether or not the root is in scope, and one more under each other area that holds one of its findings or candidates: the deepest area directory that holds the item's `file`. The `.` entry is `not_evaluated` when the pass is, and otherwise has the status of the items in the root area, `clean` when there are none. A `structure.version-floor-understated` candidate points at the caller's own floor, or at the call when the caller declares none, so it is filed under the caller's area; it is in the change when the caller's floor, the callee's floor or the call is a changed line. A cross-area pass reads every area of the module, in scope or not, and keeps only the findings and candidates of the areas in the change. When any area it reads cannot be evaluated, untouched areas included, that entry is `not_evaluated` with one reason code (`parse_error`, `unsupported_file`, `symlink` or `limit_exceeded`) and no findings or candidates.

Files Terraform ignores (a leading `.`, a trailing `~`, `#...#`) are skipped. A directory gets `not_evaluated` for its configuration rules, never `clean`, when it has a parse error, a `.tf.json` file, an override file, a `.tf` file that is a link or cannot be read, or a file over the token, nesting or unary operator limits. Path rules still run: a link where a path rule looks for a file counts as present and is never followed. An area directory that is a link or cannot be read gets `not_evaluated` for every rule.

## Module ownership

`facts.AreaOf(file, module_areas)` is the one ownership function, shared by the engine and the merge: the top-level `modules/<m>` or `examples/<n>`; a module area whose own path the file is; the root for a file under a `tests`, `wrappers`, `examples` or dot segment; the nearest module area above the file; the root. It refuses a path that is empty, absolute, holds a backslash or a drive letter, is not clean or has a `..` segment.

`module_callers` has one entry per head area of kind root, submodule or module (never example) with a file the change adds, changes or removes, sorted by `module`: `{module, complete, callers: [{area, file, line, file_changed}]}`, callers sorted by area, file and line. A module the change removes gets no entry. Callers come from every head `.tf` file, those under `tests/`, `wrappers/` and `examples/` included; `.git` and `.terraform` are never read. A literal source starting `./` or `../` is joined to the calling file's directory and must name exactly a discovered area path: `.`, a submodule or a module. Registry addresses, `git::`, `hg::`, `s3::`, `gcs::`, `http(s)://`, `github.com/` and `bitbucket.org/` sources are ignored. `file_changed` is true when the change adds the caller's file, changes its bytes or, with `--diff`, lists it. More than 4096 caller records exit 2.

`complete` is one value for the whole tree, repeated on every entry. It is false when a local source escapes the tree or names no area (no ancestor is tried), a source is empty, not a literal, absolute, a drive letter or of no known form, a `.tf` or `.tf.json` file cannot be parsed or read, any entry is a link, special or unreadable, the tree holds a file that may call a module but that the engine does not parse (a `.tftest.hcl` or `.tftest.json` test file, a `terragrunt.hcl` or any other `.hcl` file except `.terraform.lock.hcl`, or an OpenTofu `.tofu` or `.tofu.json` file), or a change path is not a clean relative path. A changed or unchanged such file anywhere in the head tree is enough, so a Terragrunt caller beside the `.tf` ones blocks the downgrade.

## Profile options

A profile may hold a `profile-options` constants block: `format: 1`, `consumers` (`public` or `in-repo`), `partitions` (`portable` or `fixed`), `examples` (`required` or `none`) and `release` (`registry` or `none`), every key required. Without it the options are the defaults `public`, `portable`, `required`, `registry`; any other value relaxes the review. An unknown format, key or value, or another unknown block, makes the profile invalid (`profile_unavailable`). A key written as `<key>:` with nothing after the colon has the empty value: an empty list, or a single value that is not set; `<key>: ` with a trailing space stays invalid. Only these keys may be empty: `required-files` `example` and `example-dirs` (and only with `examples: none`, which needs both empty), `profile-owned-files` `verbatim` and `workflow-dir`, `tooling` `files` and `dirs`, and `banner` `line`. `template-destinations` may hold no key at all. An empty value judges nothing: no verbatim file and no workflow directory for `scope.profile-file`, no banner for `scope.readme-banner`, no scanner file or directory for `scope.tooling`, and no template for `profile.template-drift`.

The gate table is held in the binary; `profile-check --gates` prints it. A listed value makes only the listed rules `not_applicable` with reason code `profile_option`:

| Option | Rules |
|--------|-------|
| `partitions: fixed` | `structure.arn-partition-hardcoded` |
| `examples: none` | `examples.argument-undemonstrated`, `examples.disabled-call-missing`, `examples.feature-undemonstrated`, `examples.submodule-missing`, `examples.versions-missing` |
| `release: none` | `profile.changelog-handwritten`, `profile.commit-type-mismatch`, `profile.migration-notes-misplaced`, `profile.owner-identifier`, `profile.provider-meta-missing`, `profile.release-config-changed`, `profile.template-drift`, `profile.title-misspelling`, `profile.title-truncated`, `profile.title-uninformative`, `profile.unrelated-change-bundled` |

`profile.required-file-missing` is never gated; a profile with `examples: none` has no example entries for it to require. `consumers: in-repo` gates nothing: it only feeds the downgrade below.

## In-repo downgrade

`in_repo_downgrade` lists the modules of `module_callers` that have at least one caller and every caller's `file_changed` set, when the profile sets `consumers: in-repo`, the change is `base` or `diff`, the run is not `--full-tree`, and `complete` is true. The candidates of `compat.variable-removed`, `compat.variable-now-required`, `compat.type-narrowed`, `compat.default-changed` and `compat.generated-policy-narrowed` in those areas carry `downgrade: "in-repo"` and `severities: ["MEDIUM"]`.

Depth limitation: `depth` does not read module areas. Examples nested under a module area, such as `infra/modules/network/examples/basic`, are out of its scope.

## Scope rules at area boundaries

No area reads `tests/` or a directory more than one level under `modules/<name>/`, though both hold boundary directories for the scope rules. So these rules are `not_evaluated(unsupported_file)` in these cases:

- `scope.module-call` and `scope.provider` at the root, when the change adds or edits a `.tf` or `.tf.json` file, or any link, special or unreadable entry, under `tests/`, or when `tests` itself is such an entry.
- `scope.module-call` and `scope.provider` at `modules/<name>`, when the change adds or edits a `.tf` or `.tf.json` file, or any link, special or unreadable entry, in a nested directory there, or adds a link, special or unreadable entry directly in `modules/<name>/` that is not a `.tf` or `.tf.json` file (one that is keeps its own reason code).
- `scope.new-submodule` at `modules/<name>`, on the same entries when they lie in a nested directory the base lacks, or are a link, special or unreadable entry.

The `modules/<name>` cases exempt a regular `.tf` file in a nested directory that parses with no issue and holds only `variable`, `output`, `locals`, `moved` and `removed` blocks. For a new `modules/<name>`, a candidate from its own top level comes first, and the nested entries give `not_evaluated` only when that top level gives none.

## Platform

The build supports Unix only: the tree reader opens files with `O_NOFOLLOW`.
