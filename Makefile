GO ?= go
BINARY := dist/portfolio-tracker

.PHONY: help build run fmt tidy lint test test/cover audit release-check build/release/all release container-test
help:
	@echo "Targets: build run fmt tidy lint test test/cover audit release-check build/release/all release container-test"

build:
	mkdir -p dist
	CGO_ENABLED=1 $(GO) build -trimpath -ldflags '-s -w' -o $(BINARY) ./cmd/server

run: build
	./$(BINARY)

fmt:
	gofmt -w cmd internal

tidy:
	$(GO) mod tidy

lint:
	@test -z "$$(gofmt -l cmd internal)" || (gofmt -l cmd internal; exit 1)
	golangci-lint run

test:
	$(GO) test -race -count=1 ./...

test/cover:
	$(GO) test -race -coverprofile=coverage.out ./...

audit: lint test
	$(GO) mod verify

release-check:
	goreleaser check

build/release/all:
	goreleaser build --clean --snapshot --id all

release:
	goreleaser release --clean

container-test:
	./scripts/container_smoke.sh
