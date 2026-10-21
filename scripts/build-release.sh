#!/usr/bin/env bash
# Build the release binaries of mtf-facts reproducibly.
#
# Usage: scripts/build-release.sh <tag> <out-dir>
#
# Run it from a clean checkout of the tagged commit. It writes one binary per
# platform, named mtf-facts_<tag>_<os>_<arch>, and a SHA256SUMS file. The same
# commit and the Go version in .go-version give the same bytes on any host.
# The release workflow and scripts/verify-release.sh both call this script, so
# the published build and a local rebuild cannot drift apart.
set -euo pipefail

PLATFORMS=(linux/amd64 linux/arm64)

if [ "$#" -ne 2 ]; then
	echo "usage: $0 <tag> <out-dir>" >&2
	exit 2
fi
tag=$1
out=$2

if ! [[ $tag =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$ ]]; then
	echo "build-release: tag ${tag} is not vMAJOR.MINOR.PATCH[-PRERELEASE]" >&2
	exit 2
fi
if [ -n "$(git status --porcelain)" ]; then
	echo "build-release: the working tree is not clean" >&2
	exit 1
fi
if [ "$(git rev-parse HEAD)" != "$(git rev-parse "${tag}^{commit}")" ]; then
	echo "build-release: HEAD is not the commit of ${tag}" >&2
	exit 1
fi
if [ -e "$out" ]; then
	echo "build-release: ${out} already exists" >&2
	exit 1
fi

# Every setting that changes the output bytes is fixed here, so a user's go
# env file or shell cannot change the build. GOTOOLCHAIN selects the exact Go
# release from .go-version; a different local Go downloads that one.
GOTOOLCHAIN="go$(tr -d '[:space:]' < .go-version)"
export GOTOOLCHAIN CGO_ENABLED=0 GOFLAGS=-mod=readonly GOEXPERIMENT='' GOAMD64=v1 GOARM64=v8.0
# Go embeds no build time; this is set for any tool that reads it.
SOURCE_DATE_EPOCH=$(git log -1 --format=%ct HEAD)
export SOURCE_DATE_EPOCH

mkdir -p "$out"
# An untracked file marks the build as modified and changes its version.
if [ -n "$(git status --porcelain)" ]; then
	echo "build-release: ${out} must be outside the checkout or ignored by git" >&2
	exit 1
fi
for p in "${PLATFORMS[@]}"; do
	name="mtf-facts_${tag}_${p%/*}_${p#*/}"
	GOOS=${p%/*} GOARCH=${p#*/} go build -trimpath -buildvcs=true -ldflags=-buildid= \
		-o "${out}/${name}" ./cmd/mtf-facts
	# The module version comes from the tag through VCS stamping. A build
	# that did not see the tag would report another version, so refuse it.
	got=$(go version -m "${out}/${name}" | awk '$1 == "mod" { print $3 }')
	if [ "$got" != "$tag" ]; then
		echo "build-release: ${name} reports version ${got}, want ${tag}" >&2
		exit 1
	fi
done
sha256() { if command -v sha256sum >/dev/null; then sha256sum "$@"; else shasum -a 256 "$@"; fi; }
(cd "$out" && sha256 mtf-facts_* > SHA256SUMS)
cat "${out}/SHA256SUMS"
