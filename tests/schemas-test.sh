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
# Needs Node.js and npm. It is installed once per run into a private temporary
# directory, which needs network access to the npm registry, and every check
# then runs that local copy offline. Calling npx per check instead fetched the
# package metadata from the registry on every call, so any failed fetch was
# reported as a failing fixture. Override the command with AJV, for example
# AJV="ajv" for a local install, to skip the install. If the npm cache is not
# writable, set npm_config_cache to a writable directory.
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
schemas="$root/schema"
examples="$schemas/examples"
if [ -n "${AJV:-}" ]; then
  read -r -a ajv <<<"$AJV"
else
  tmp=$(mktemp -d "${TMPDIR:-/tmp}/schemas-test.XXXXXX")
  trap 'rm -rf "$tmp"' EXIT
  if ! npm install --prefix "$tmp" --no-save --no-audit --no-fund --ignore-scripts \
    ajv-cli@5.0.0 >"$tmp/install.log" 2>&1; then
    echo "FAIL could not install ajv-cli 5.0.0:"
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
    if "${run[@]}" -d "$f" --valid >/dev/null 2>&1; then
      echo "ok   $(basename "$f") passes"
    else
      echo "FAIL $(basename "$f") does not pass:"
      "${run[@]}" -d "$f" --valid 2>&1 | sed 's/^/     /' || true
      fail=1
    fi
  done
  # One call per file, so a failure names the file and its expected keyword.
  for f in "${invalid[@]}"; do
    base=$(basename "$f" .json)
    keyword=$(printf '%s' "$base" | cut -d. -f3)
    if out=$("${ajv[@]}" validate "${opts[@]}" -d "$f" --all-errors --errors=json 2>&1); then
      echo "FAIL $base.json passes but must fail"
      fail=1
    elif printf '%s' "$out" | grep -Eq "\"keyword\": ?\"$keyword\""; then
      echo "ok   $base.json fails on $keyword"
    else
      echo "FAIL $base.json fails, but not on $keyword:"
      printf '%s\n' "$out" | sed 's/^/     /'
      fail=1
    fi
  done
  checked=$((checked + 1))
done

[ "$checked" -gt 0 ] || { echo "FAIL no schemas found"; exit 1; }
[ "$fail" -eq 0 ] && echo "PASS $checked schemas" || { echo "FAILED"; exit 1; }
