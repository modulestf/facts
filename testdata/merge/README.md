# mtf-facts merge fixtures

Inputs and expected outputs of `mtf-facts merge`. `TestFinalFixtures` in `internal/merge/merge_test.go` runs every fixture but `confirm-rebuild-revert`, which tests the confirm step, not the merge.

Every file here is a byte-identical copy of the merge fixtures the facts engine spec was written against, except this README and `counts-from-repair/`, which tests the optional `counts` object added after them. `confirm-outcomes/confirm/C-004.invalid.enum.json` was renamed from `C-004.invalid.json` to name the keyword it fails on.

`../merge-golden/` holds the merged findings `TestHostileGolden` in `internal/merge/golden_test.go` writes from hostile replies; `go test ./internal/merge -run TestHostileGolden -update` rewrites them. `TestMergedFindingsSchema` in `internal/facts` validates them, and every `expected-findings.json`, against `merged-findings.schema.json`.

## Layout

Each fixture is a directory:

| Path | What it is |
|------|------------|
| `note.txt` | One line: the rule the fixture tests. |
| `bundle.json` | The facts bundle the lead reads, in the shape of `facts-bundle.schema.json`. |
| `model-output.json` | The lead's reply, in the shape of `model-output.schema.json`. |
| `model-output.repair.json` | Optional: the reply to the repair turn. Where the first reply is valid it is a decoy that a correct merge never reads. |
| `expected-findings.json` | The merged findings, in the shape of `merged-findings.schema.json`. |
| `expected-counts.json` | Optional: what `merge counts` prints. A fixture without it expects `{"counts":null}`. |
| `host.json`, `bundle.before-confirm.json`, `bundle.rebuilt.json`, `confirm/` | Inputs of the confirm step, which the merge does not read. A `confirm/<id>.invalid.<keyword>.json` is a confirmer output that must fail `confirm-output.schema.json` on `<keyword>`. |

`registry.json` holds the rule rows the fixtures use and the per-rule host templates; the test passes it to the merge as `--templates`. The merge reads its rules from the pinned `rule-facts.md` that `MTF_FACTS_REGISTRY` names.

The test compares the output with `expected-findings.json` as a multiset, ignoring `id`, and checks that the ids run from `F-001` in the Final Step sort: severity, then `file`, then `line` with null last.

## Running

```sh
MTF_FACTS_REGISTRY=/path/to/rule-facts.md go test ./internal/merge
go test ./internal/facts -run 'TestMergeFixtureSchemas|TestMergedFindingsSchema'
```

`TestMergeFixtureSchemas` in `internal/facts` checks every `bundle*.json`, `model-output*.json` and `confirm/` output against its schema under `schema/`, that every invalid confirm example fails on the keyword its name gives, that `note.txt` is one line and that `host.json` and `registry.json` are JSON. CI runs both tests.
