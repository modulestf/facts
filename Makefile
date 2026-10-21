.PHONY: build test vet release-build release-verify

build:
	go build -trimpath -o bin/mtf-facts ./cmd/mtf-facts

test:
	go test -race -count=1 ./...

vet:
	go vet ./...

# Build the release binaries of the checked-out tag into OUT, as the release
# workflow does. Usage: make release-build TAG=v0.1.0 OUT=/tmp/dist
release-build:
	./scripts/build-release.sh "$(TAG)" "$(OUT)"

# Rebuild a published release and compare its SHA-256 values.
# Usage: make release-verify TAG=v0.1.0
release-verify:
	./scripts/verify-release.sh "$(TAG)"
