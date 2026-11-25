.PHONY: build test vet bench release-build release-verify

build:
	go build -trimpath -o bin/mtf-facts ./cmd/mtf-facts

test:
	go test -race -count=1 ./...

vet:
	go vet ./...

# Run every benchmark six times and write the results to BENCH_OUT, the
# input benchstat compares. A half second per run keeps the whole target
# under two minutes. Usage: make bench BENCH_OUT=/tmp/new.txt
BENCH_OUT ?= bench.txt
bench:
	go test -run '^$$' -bench . -benchmem -benchtime 500ms -count 6 ./... > $(BENCH_OUT); \
		status=$$?; cat $(BENCH_OUT); exit $$status

# Build the release binaries of the checked-out tag into OUT, as the release
# workflow does. Usage: make release-build TAG=v0.1.0 OUT=/tmp/dist
release-build:
	./scripts/build-release.sh "$(TAG)" "$(OUT)"

# Rebuild a published release and compare its SHA-256 values.
# Usage: make release-verify TAG=v0.1.0
release-verify:
	./scripts/verify-release.sh "$(TAG)"
