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
# With a built binary it also checks the live output of version --json.
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
bin=${MTF_FACTS:-$root/bin/mtf-facts}
live=()
if [ -x "$bin" ]; then
  if "$bin" version --json >"$tmp/version.json" 2>"$tmp/version.err"; then
    live=(--live version "$tmp/version.json" "version --json of $bin")
  else
    { echo "FAIL $bin version --json exited non-zero:"; sed 's/^/     /' "$tmp/version.err"; } >"$tmp/note"
    live=(--note fail "$tmp/note")
  fi
elif [ -n "${CI:-}" ]; then
  echo "FAIL no built binary at $bin to check version --json against its schema" >"$tmp/note"
  live=(--note fail "$tmp/note")
else
  echo "skip version --json: no built binary at $bin" >"$tmp/note"
  live=(--note skip "$tmp/note")
fi
node "$root/tests/ajv/validate.js" "$schemas" "$modules" "${live[@]}"
