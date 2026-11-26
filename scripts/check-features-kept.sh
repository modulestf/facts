#!/usr/bin/env bash
# Fails when cmd/mtf-facts/testdata/features.txt lost a line against a base
# revision. A released feature name is never removed: scripts test for it.
#
#   bash scripts/check-features-kept.sh <base-revision>
#
# CI compares the pull request's merge commit with the tip of the base
# branch. For a local run, pass the merge base:
#
#   bash scripts/check-features-kept.sh "$(git merge-base HEAD origin/master)"
#
# Exit 1 when a name was removed, 2 when the base revision does not resolve.
set -euo pipefail

base=${1:?usage: check-features-kept.sh <base-revision>}
file=cmd/mtf-facts/testdata/features.txt
root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"

git rev-parse --verify --quiet "$base^{commit}" >/dev/null || { echo "FAIL base revision $base not found"; exit 2; }
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
