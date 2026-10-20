.PHONY: build test vet vendor

build:
	go build -trimpath -mod=vendor -o bin/mtf-facts ./cmd/mtf-facts

test:
	go test -mod=vendor ./...

vet:
	go vet -mod=vendor ./...

vendor:
	go mod tidy
	go mod vendor
