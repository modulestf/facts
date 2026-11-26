#!/usr/bin/env bash
# Validates the JSON schemas under schema/ against their examples.
#
# Every schema/<name>.schema.json except finding.schema.json (shared, referenced
# by the others) must have schema/examples/<name>.valid.json, which must pass,
# as must each schema/examples/<name>.valid.<slug>.json,
# and at least one schema/examples/<name>.invalid.<keyword>[.<slug>].json, each
# of which must fail with an error whose keyword is <keyword> (for example
# additionalProperties, oneOf, required) in ajv's --all-errors JSON output.
#
# With a built binary it also checks the live output of every command that
# writes JSON, and the stats record lint --stats-file writes.
#
# Validator: ajv 8.20.0 as ajv-cli 5.0.0 installs it, draft 2020-12, strict
# mode. tests/ajv/validate.js checks every schema and example in one Node.js
# process, with the options ajv-cli uses for `ajv test --spec=draft2020
# --strict=true -s <schema> -r <others>`; the script used to start ajv-cli
# once per example, about 200 processes. Needs Node.js and npm.
# tests/ajv/package-lock.json pins ajv-cli and every package it pulls in, with
# integrity hashes. Each run installs that lock with npm ci into its own
# temporary directory, with its own npm cache there, so concurrent runs share
# no writable path; the install needs network access to the npm registry, and
# the check then runs that local copy offline.
# Set AJV_NODE_MODULES to a node_modules directory that holds ajv 8, for
# example a local install, to skip the install.
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
schemas="$root/schema"
tmp=$(mktemp -d "${TMPDIR:-/tmp}/schemas-test.XXXXXX")
trap 'rm -rf "$tmp"' EXIT
if [ -n "${AJV_NODE_MODULES:-}" ]; then
  modules=$AJV_NODE_MODULES
else
  cp "$root/tests/ajv/package.json" "$root/tests/ajv/package-lock.json" "$tmp/"
  if ! (cd "$tmp" && npm ci --cache "$tmp/npm-cache" --ignore-scripts \
    --no-audit --no-fund >"$tmp/install.log" 2>&1); then
    echo "FAIL could not install ajv-cli from tests/ajv/package-lock.json:"
    sed 's/^/     /' "$tmp/install.log"
    exit 1
  fi
  modules=$tmp/node_modules
fi

# The live output of version --json must pass its schema too, so the schema
# cannot drift from the binary. It needs a built binary, MTF_FACTS or
# bin/mtf-facts (CI builds it first); without one it is skipped, except in CI.
# validate.js checks it after the examples; a failure to run the binary, or
# no binary in CI, reaches it as a note.
#
# The same holds for every other command that writes JSON, run on the fixtures
# under testdata/ (see live_case below). bundle and merge final read inputs
# built from one another; their reports are checked where they are built, by
# the Go tests.
bin=${MTF_FACTS:-$root/bin/mtf-facts}
live=()
n=0
# live_case <schema> <label> <exit codes allowed, space separated> <args...>
# runs the binary in the repository root and adds its stdout to the live list.
live_case() {
  local schema=$1 label=$2 allowed=$3 code out
  shift 3
  n=$((n + 1))
  out=$tmp/live-$n.json
  code=0
  (cd "$root" && TMPDIR=$tmp "$bin" "$@") >"$out" 2>"$tmp/live-$n.err" || code=$?
  case " $allowed " in
    *" $code "*) live+=(--live "$schema" "$out" "$label") ;;
    *)
      { echo "FAIL $label exited $code, want one of: $allowed"; sed 's/^/     /' "$tmp/live-$n.err"; } >"$tmp/note-$n"
      live+=(--note fail "$tmp/note-$n")
      ;;
  esac
}
if [ -x "$bin" ]; then
  d=testdata/diff-base
  l=testdata/ledger
  m=testdata/merge/answered-once-duplicate
  b=internal/behaviour/testdata/pr556
  e=schema/examples
  live_case version "version --json" "0" version --json
  live_case rules "rules --json" "0" rules --json
  live_case depth "depth" "0" depth --head $d/head --base $d/base --files $d/files.json
  live_case lint "lint --json" "0 3" lint testdata/variable-untyped-base/head --base testdata/variable-untyped-base/base --json
  live_case lint "lint --json --ai" "0 3" lint testdata/variable-untyped-base/head --base testdata/variable-untyped-base/base --json --ai
  live_case affected "affected --json" "0" affected $d/head --base $d/base --json
  live_case changes "changes --json" "0" changes testdata/changes/heredoc/head --base testdata/changes/heredoc/base --json
  live_case behaviour "behaviour --json" "0" behaviour $b/head --base $b/base --json
  live_case claims-eval "claims-eval --json" "0" claims-eval --behaviour $e/behaviour.valid.json --ladder $e/ladder-record.valid.json \
    --json --head-sha 0000000000000000000000000000000000000000 --base-sha 1111111111111111111111111111111111111111
  live_case ledger-report "ledger" "0" ledger $l/module --schema-files $l/aws.json
  live_case ledger-report "ledger --check" "0" ledger $l/module --schema-files $l/aws.json --check $l/valid.json
  live_case ledger-report "ledger --check with problems" "0 1" ledger $l/module --schema-files $l/aws.json --check $l/unmapped.json
  live_case schema-diff "schema-diff" "0" schema-diff --from testdata/schema-diff/from.json --to testdata/schema-diff/to.json \
    --types-from testdata/schema-diff/module
  live_case fixture-check "fixture-check --json" "0" fixture-check testdata/fixture-check/l1 --json
  live_case profile-check "profile-check" "0" profile-check --profile-dir testdata/profile
  live_case profile-snapshot "profile-snapshot" "0" profile-snapshot --profile-dir testdata/profile
  # The stats record lint --stats-file writes is a file, not stdout.
  live_case lint "lint --json for the stats record" "0 1 3" lint testdata/variable-untyped-base/head --json --stats-file "$tmp/stats.json"
  if [ -f "$tmp/stats.json" ]; then
    live+=(--live stats "$tmp/stats.json" "lint --stats-file record")
  else
    echo "FAIL lint --stats-file wrote no record" >"$tmp/stats-note"
    live+=(--note fail "$tmp/stats-note")
  fi
  # A minimal tree: no examples, modules, variables or change. Every report
  # must still pass its schema, empty lists and null fields included.
  mkdir -p "$tmp/min/head" "$tmp/min/base" "$tmp/min/vars"
  echo '# empty' >"$tmp/min/head/main.tf"
  echo '# empty' >"$tmp/min/base/main.tf"
  printf 'variable "x" {\n  type = string\n}\n' >"$tmp/min/vars/variables.tf"
  : >"$tmp/min/empty.diff"
  echo '[]' >"$tmp/min/files.json"
  min=$tmp/min
  live_case lint "lint --json on a tree with no change" "0 1 3" lint $min/head --json
  live_case lint "lint --json on a tree with no difference" "0 1 3" lint $min/head --base $min/base --json
  live_case lint "lint --json --ai on a tree with no difference" "0 1 3" lint $min/head --base $min/base --json --ai
  live_case lint "lint --json on a tree that adds a variable" "0 1 3" lint $min/vars --base $min/base --json
  live_case lint "lint --json on an empty diff" "0 1 3" lint $min/head --diff $min/empty.diff --json
  live_case affected "affected --json on a tree with no change" "0" affected $min/head --json
  live_case affected "affected --json on a tree with no examples" "0" affected $min/head --base $min/base --json
  live_case affected "affected --json on a tree that adds a variable" "0" affected $min/vars --base $min/base --json
  live_case affected "affected --json on an empty diff" "0" affected $min/head --diff $min/empty.diff --json
  live_case changes "changes --json on a tree with no difference" "0" changes $min/head --base $min/base --json
  live_case changes "changes --json on a tree that adds a variable" "0" changes $min/vars --base $min/base --json
  live_case behaviour "behaviour --json on a tree with no difference" "0" behaviour $min/head --base $min/base --json
  live_case behaviour "behaviour --json on a tree that adds a variable" "0" behaviour $min/vars --base $min/base --json
  live_case behaviour "behaviour --json on an empty diff" "0" behaviour $min/head --diff $min/empty.diff --json
  live_case depth "depth on a tree with no change" "0" depth --head $min/head --base $min/base --files $min/files.json
  live_case fixture-check "fixture-check --json on a minimal tree" "0" fixture-check $min/head --json
  # A resource type whose schema block has no field, and an empty change set
  # carried through claims-eval.
  mkdir -p "$tmp/min/empty-res"
  echo 'resource "aws_empty" "x" {}' >"$tmp/min/empty-res/main.tf"
  echo '{"format_version":"1.0","provider_schemas":{"registry.terraform.io/hashicorp/aws":{"resource_schemas":{"aws_empty":{"version":0,"block":{}}}}}}' >"$tmp/min/empty-schema.json"
  live_case ledger-report "ledger on a type with no field" "0" ledger $min/empty-res --schema-files $min/empty-schema.json
  (cd "$root" && "$bin" behaviour "$min/head" --base "$min/base" --json) >"$tmp/min/behaviour.json" 2>/dev/null || true
  live_case claims-eval "claims-eval --json on a change with no trigger" "0" claims-eval --behaviour $min/behaviour.json --ladder $e/ladder-record.valid.json \
    --json --head-sha 0000000000000000000000000000000000000000 --base-sha 1111111111111111111111111111111111111111
  # merge check and counts read a bundle in the region form bundle writes.
  node -e '
    const fs = require("fs");
    const p = JSON.stringify(JSON.stringify(JSON.parse(fs.readFileSync(process.argv[1], "utf8"))));
    fs.writeFileSync(process.argv[2], `FACTS-BUNDLE-BYTES ${Buffer.byteLength(p)}\nFACTS-BUNDLE BEGIN\n${p}\nFACTS-BUNDLE END\n`);
  ' "$root/$m/bundle.json" "$tmp/region.txt"
  live_case merge-check "merge check" "0 1" merge check "$tmp/region.txt" $m/model-output.repair.json
  live_case merge-check "merge check repair" "0 1" merge check "$tmp/region.txt" $m/model-output.json
  live_case merge-counts "merge counts" "0" merge counts "$tmp/region.txt" $m/model-output.repair.json
elif [ -n "${CI:-}" ]; then
  echo "FAIL no built binary at $bin to check the live output of every command against its schema" >"$tmp/note"
  live=(--note fail "$tmp/note")
else
  echo "skip live output of every command: no built binary at $bin" >"$tmp/note"
  live=(--note skip "$tmp/note")
fi
node "$root/tests/ajv/validate.js" "$schemas" "$modules" "${live[@]}"
