#!/bin/sh
# Writes the unified diffs of this directory from a throwaway repository,
# with the git on PATH. TestUnifiedGitCorpus holds the head tree and the
# base each diff must rebuild.
set -eu
out=$(cd "$(dirname "$0")" && pwd)
dir=$(mktemp -d)
trap 'rm -rf "$dir"' EXIT
cd "$dir"
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1
export GIT_AUTHOR_DATE='2026-01-01T00:00:00Z' GIT_COMMITTER_DATE='2026-01-01T00:00:00Z'
git init -q
git config user.name corpus
git config user.email corpus@example.invalid
printf 'a = 1\nb = 2\nc = 3\n' > main.tf
printf 'm\n' > m.tf
: > e.tf
printf 'x\n' > r.tf
printf 'c\n' > c.tf
printf 'q\n' > q.tf
printf 'w1\nw2\nw3\nw4\nw5\n' > w.tf
ln -s a.tf link.tf
printf '\000\001old\n' > bin.dat
git add .
git update-index --add --cacheinfo 160000,1111111111111111111111111111111111111111,sub
git commit -qm base
printf 'a = 1\nb = 20\nc = 3\n' > main.tf
chmod +x m.tf
git rm -q e.tf
: > n.tf
git mv r.tf r2.tf
cp c.tf c2.tf
git mv q.tf q2.tf
chmod +x q2.tf
git mv w.tf w2.tf
printf 'w1\nw2\nw3\nw4\nw5x\n' > w2.tf
rm link.tf
ln -s b.tf link.tf
printf '\000\001new\n' > bin.dat
git add -A
git update-index --add --cacheinfo 160000,2222222222222222222222222222222222222222,sub
d() { git -c core.quotePath=true diff --cached --no-color --no-ext-diff --no-textconv --src-prefix=a/ --dst-prefix=b/ "$@"; }
d -M -C --find-copies-harder > "$out/renames.diff"
d -M -C --find-copies-harder --binary > "$out/binary.diff"
d --no-renames > "$out/no-renames.diff"
d -M -C --find-copies-harder -U0 > "$out/u0.diff"
d -M -C --find-copies-harder --full-index > "$out/full-index.diff"
git commit -qm head
git -c core.quotePath=true format-patch -1 --stdout -M -C --find-copies-harder --no-color --no-ext-diff --no-textconv --src-prefix=a/ --dst-prefix=b/ > "$out/format-patch.diff"
