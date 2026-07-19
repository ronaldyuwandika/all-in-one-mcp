.PHONY: build test lint bench install

build:
	GOWORK=off go build -o bin/vault ./cmd/vault
	GOWORK=off go build -o bin/vaultctl ./cmd/vaultctl

test:
	GOWORK=off go test -race -count=1 ./...

lint:
	GOWORK=off golangci-lint run ./...

bench:
	GOWORK=off go test -bench=. -benchmem ./bench/...

install:
	GOWORK=off go install ./cmd/vault ./cmd/vaultctl
