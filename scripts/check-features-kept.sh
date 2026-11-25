#!/usr/bin/env bash
# Fails when cmd/mtf-facts/testdata/features.txt lost a line against a base
# revision. A released feature name is never removed: scripts test for it.
#
#   bash scripts/check-features-kept.sh <base-revision>
#
# CI runs it on a pull request with the merge base of the base branch.
set -euo pipefail

base=${1:?usage: check-features-kept.sh <base-revision>}
file=cmd/mtf-facts/testdata/features.txt
root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"

if ! git cat-file -e "$base:$file" 2>/dev/null; then
  echo "ok   $file is new against $base"
  exit 0
fi
names() { grep -v -e '^#' -e '^$' | LC_ALL=C sort -u; }
removed=$(LC_ALL=C comm -23 <(git show "$base:$file" | names) <(names < "$file"))
if [ -n "$removed" ]; then
  echo "FAIL $file lost released feature names against $base:"
  printf '     %s\n' $removed
  exit 1
fi
echo "ok   $file keeps every feature name of $base"
