#!/usr/bin/env bash
# Rebuild a published mtf-facts release and compare its SHA-256 values.
#
# Usage: scripts/verify-release.sh <tag>
#
# It clones the tag into a temporary directory, builds it with
# scripts/build-release.sh from that clone, and compares the result with the
# release's SHA256SUMS. Exit 0 means every published binary matches the
# rebuild byte for byte.
#
# Overrides, for testing: REPO_URL is the repository to clone, SUMS_FILE a
# local SHA256SUMS to compare against instead of the published one.
set -euo pipefail

if [ "$#" -ne 1 ]; then
	echo "usage: $0 <tag>" >&2
	exit 2
fi
tag=$1
if ! [[ $tag =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$ ]]; then
	echo "verify-release: tag ${tag} is not vMAJOR.MINOR.PATCH[-PRERELEASE]" >&2
	exit 2
fi
repo_url=${REPO_URL:-https://github.com/modulestf/facts.git}

tmp=$(mktemp -d "${TMPDIR:-/tmp}/verify-release.XXXXXX")
trap 'rm -rf "$tmp"' EXIT

git -c advice.detachedHead=false clone --quiet --depth 1 --branch "$tag" "$repo_url" "${tmp}/src"

if [ -n "${SUMS_FILE:-}" ]; then
	cp "$SUMS_FILE" "${tmp}/published"
else
	curl -fsSL --proto '=https' --tlsv1.2 --retry 2 --max-time 60 \
		-o "${tmp}/published" \
		"https://github.com/modulestf/facts/releases/download/${tag}/SHA256SUMS"
fi

(cd "${tmp}/src" && ./scripts/build-release.sh "$tag" "${tmp}/dist" > /dev/null)

if diff -u "${tmp}/published" "${tmp}/dist/SHA256SUMS"; then
	echo "verify-release: ${tag} matches the rebuild"
	cat "${tmp}/dist/SHA256SUMS"
else
	echo "verify-release: ${tag} does not match the rebuild" >&2
	exit 1
fi
