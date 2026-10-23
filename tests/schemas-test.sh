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
# Validator: ajv-cli 5.0.0, draft 2020-12 (--spec=draft2020), strict mode.
# Needs Node.js and npm. tests/ajv/package-lock.json pins ajv-cli and every
# package it pulls in, with integrity hashes. Each run installs that lock with
# npm ci into its own temporary directory, with its own npm cache there, so
# concurrent runs share no writable path; the install needs network access to
# the npm registry, and every check then runs that local copy offline.
# Override the command with AJV, for example AJV="ajv" for a local install, to
# skip the install.
#
# Each ajv call writes to a file, never to a pipe. ajv-cli prints its errors and
# then calls process.exit, and Node.js writes to a pipe asynchronously on macOS,
# so output that did not fit in the pipe at exit was lost. Under load the
# capture was cut short at varying lengths, and a fixture whose expected
# keyword came late in the output was reported as failing on another keyword.
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
schemas="$root/schema"
examples="$schemas/examples"
tmp=$(mktemp -d "${TMPDIR:-/tmp}/schemas-test.XXXXXX")
trap 'rm -rf "$tmp"' EXIT
out="$tmp/out"
if [ -n "${AJV:-}" ]; then
  read -r -a ajv <<<"$AJV"
else
  cp "$root/tests/ajv/package.json" "$root/tests/ajv/package-lock.json" "$tmp/"
  if ! (cd "$tmp" && npm ci --cache "$tmp/npm-cache" --ignore-scripts \
    --no-audit --no-fund >"$tmp/install.log" 2>&1); then
    echo "FAIL could not install ajv-cli from tests/ajv/package-lock.json:"
    sed 's/^/     /' "$tmp/install.log"
    exit 1
  fi
  ajv=("$tmp/node_modules/.bin/ajv")
fi

fail=0
checked=0
for schema in "$schemas"/*.schema.json; do
  name=$(basename "$schema" .schema.json)
  [ "$name" = finding ] && continue

  refs=()
  for other in "$schemas"/*.schema.json; do
    [ "$other" = "$schema" ] || refs+=(-r "$other")
  done
  opts=(--spec=draft2020 --strict=true -s "$schema" "${refs[@]}")
  run=("${ajv[@]}" test "${opts[@]}")

  valid="$examples/$name.valid.json"
  valids=("$valid")
  for f in "$examples/$name".valid.*.json; do
    [ -f "$f" ] && valids+=("$f")
  done
  invalid=("$examples/$name".invalid.*.json)
  if [ ! -f "$valid" ] || [ ! -f "${invalid[0]}" ]; then
    echo "FAIL $name: needs $name.valid.json and at least one $name.invalid.<keyword>.json"
    fail=1
    continue
  fi

  for f in "${valids[@]}"; do
    if "${run[@]}" -d "$f" --valid >"$out" 2>&1; then
      echo "ok   $(basename "$f") passes"
    else
      echo "FAIL $(basename "$f") does not pass:"
      sed 's/^/     /' "$out"
      fail=1
    fi
  done
  # One call per file, so a failure names the file and its expected keyword.
  for f in "${invalid[@]}"; do
    base=$(basename "$f" .json)
    keyword=$(printf '%s' "$base" | cut -d. -f3)
    if "${ajv[@]}" validate "${opts[@]}" -d "$f" --all-errors --errors=json >"$out" 2>&1; then
      echo "FAIL $base.json passes but must fail"
      fail=1
    elif grep -Eq "\"keyword\": ?\"$keyword\"" "$out"; then
      echo "ok   $base.json fails on $keyword"
    else
      echo "FAIL $base.json fails, but not on $keyword:"
      sed 's/^/     /' "$out"
      fail=1
    fi
  done
  checked=$((checked + 1))
done

[ "$checked" -gt 0 ] || { echo "FAIL no schemas found"; exit 1; }
[ "$fail" -eq 0 ] && echo "PASS $checked schemas" || { echo "FAILED"; exit 1; }
