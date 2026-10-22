# Rule Facts

A small registry for the bundle tests: the columns of the reviewer skill's
rule-facts.md, with a few rules of each tier.

| `rule_id` | `engine` | `class` | `scope` | `cross_area` | `area_kinds` | `bundle_sections` | `severities` | `confirm_class` | `proposed_confirm_class` |
|-----------|----------|---------|---------|--------------|--------------|-------------------|--------------|-----------------|--------------------------|
| `compat.break-unexplained` | llm | config-semantic | any-file-in-area | yes | root | tool-findings, candidates, interface-diff | MEDIUM | - | - |
| `compat.default-changed` | candidate | config-semantic | touched-expression | no | root, submodule | candidates, interface-diff | CRITICAL, HIGH, MEDIUM | lead | mid |
| `compat.variable-removed` | candidate | config-semantic | touched-block | no | root, submodule | candidates, interface-diff | CRITICAL, HIGH | lead | mid |
| `coverage.unknown-argument` | candidate | config-semantic | touched-block | no | root, submodule | candidates, schema-coverage | CRITICAL | lead | mid |
| `examples.versions-missing` | tool | path | any-file-in-area | no | example | tool-findings, file-index | MEDIUM | - | - |
| `structure.missing-description` | tool | config-semantic | newly-introduced | no | root, submodule | tool-findings | MEDIUM | - | - |
| `structure.variable-untyped` | candidate | config-semantic | newly-introduced | no | root, submodule | candidates, interface-diff | MEDIUM | lead | small |

## Dedup Overlap

| Pair | Kept | Dropped | Same defect when | From |
|------|------|---------|------------------|------|
| 1 | `compat.default-changed` | `structure.variable-untyped` | Test pair. | Test |

## End
