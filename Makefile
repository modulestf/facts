.PHONY: build test vet

build:
	go build -trimpath -o bin/mtf-facts ./cmd/mtf-facts

test:
	go test -race -count=1 ./...

vet:
	go vet ./...
