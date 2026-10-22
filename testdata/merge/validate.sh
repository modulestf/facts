#!/usr/bin/env bash
# Validates every merge fixture file against the modulestf/facts JSON schemas.
#
# Usage: FACTS_SCHEMA_DIR=<checkout of modulestf/facts>/schema bash validate.sh
#
# Validator: ajv-cli 5.0.0 through npx, draft 2020-12, strict mode, as the
# facts repository's own tests/schemas-test.sh runs it. Needs Node.js and
# network access to the npm registry on first run. Override with AJV.
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
schemas=${FACTS_SCHEMA_DIR:?set FACTS_SCHEMA_DIR to the schema directory of modulestf/facts}
[ -f "$schemas/merged-findings.schema.json" ] || { echo "FAIL $schemas has no merged-findings.schema.json; use a facts checkout that has it"; exit 1; }
read -r -a ajv <<<"${AJV:-npx --yes ajv-cli@5.0.0}"

fail=0
# Usage: check <schema file> <valid|invalid> <data file>...
check() {
  local schema=$1 want=$2 refs=() other f
  shift 2
  for other in "$schemas"/*.schema.json; do
    [ "$other" = "$schema" ] || refs+=(-r "$other")
  done
  for f in "$@"; do
    # A schema that fails to compile is an error, never an invalid data file.
    if out=$("${ajv[@]}" validate --spec=draft2020 --strict=true -s "$schema" "${refs[@]}" -d "$f" 2>&1); then
      got=valid
    elif printf '%s' "$out" | grep -F "$f invalid" > /dev/null; then
      got=invalid
    else
      got="an error: $out"
    fi
    if [ "$got" = "$want" ]; then
      echo "ok   ${f#"$here"/} is $want"
    else
      echo "FAIL ${f#"$here"/} is $got, want $want"
      fail=1
    fi
  done
}

shopt -s nullglob
for dir in "$here"/*/; do
  dir=${dir%/}
  [ "$(wc -l <"$dir/note.txt")" -eq 1 ] || { echo "FAIL ${dir#"$here"/}/note.txt is not one line"; fail=1; }
  [ -f "$dir/expected-findings.json" ] || { echo "FAIL ${dir#"$here"/} has no expected-findings.json"; fail=1; }
  check "$schemas/facts-bundle.schema.json" valid "$dir"/bundle*.json
  check "$schemas/model-output.schema.json" valid "$dir"/model-output*.json
  valid=()
  for f in "$dir"/confirm/*.json; do
    case $f in *.invalid.json) ;; *) valid+=("$f") ;; esac
  done
  check "$schemas/confirm-output.schema.json" valid "${valid[@]}"
  check "$schemas/confirm-output.schema.json" invalid "$dir"/confirm/*.invalid.json
  check "$schemas/merged-findings.schema.json" valid "$dir/expected-findings.json"
done

for f in "$here"/registry.json "$here"/*/host.json; do
  python3 -c 'import json,sys; json.load(open(sys.argv[1]))' "$f" || { echo "FAIL $f is not JSON"; fail=1; }
done

[ "$fail" -eq 0 ] && echo PASS || { echo FAILED; exit 1; }
