#!/usr/bin/env bash
# Validates the JSON schemas under schema/ against their examples.
#
# Every schema/<name>.schema.json except finding.schema.json (shared, referenced
# by the others) must have schema/examples/<name>.valid.json, which must pass,
# and at least one schema/examples/<name>.invalid*.json, each of which must fail.
#
# Validator: ajv-cli 5.0.0 through npx, draft 2020-12 (--spec=draft2020), strict
# mode. Needs Node.js and network access to the npm registry on first run.
# Override the command with AJV, for example AJV="ajv" for a local install.
# If the npm cache is not writable, set npm_config_cache to a writable directory.
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
schemas="$root/schema"
examples="$schemas/examples"
read -r -a ajv <<<"${AJV:-npx --yes ajv-cli@5.0.0}"

fail=0
checked=0
for schema in "$schemas"/*.schema.json; do
  name=$(basename "$schema" .schema.json)
  [ "$name" = finding ] && continue

  refs=()
  for other in "$schemas"/*.schema.json; do
    [ "$other" = "$schema" ] || refs+=(-r "$other")
  done
  run=("${ajv[@]}" test --spec=draft2020 --strict=true -s "$schema" "${refs[@]}")

  valid="$examples/$name.valid.json"
  invalid=("$examples/$name".invalid*.json)
  if [ ! -f "$valid" ] || [ ! -f "${invalid[0]}" ]; then
    echo "FAIL $name: needs $name.valid.json and at least one $name.invalid*.json"
    fail=1
    continue
  fi

  if "${run[@]}" -d "$valid" --valid >/dev/null 2>&1; then
    echo "ok   $name.valid.json passes"
  else
    echo "FAIL $name.valid.json does not pass:"
    "${run[@]}" -d "$valid" --valid 2>&1 | sed 's/^/     /' || true
    fail=1
  fi
  # One call per file: ajv test with several -d fails as a whole, which would hide which one.
  for f in "${invalid[@]}"; do
    if "${run[@]}" -d "$f" --invalid >/dev/null 2>&1; then
      echo "ok   $(basename "$f") fails"
    else
      echo "FAIL $(basename "$f") does not fail validation (or the schema does not compile)"
      fail=1
    fi
  done
  checked=$((checked + 1))
done

[ "$checked" -gt 0 ] || { echo "FAIL no schemas found"; exit 1; }
[ "$fail" -eq 0 ] && echo "PASS $checked schemas" || { echo "FAILED"; exit 1; }
