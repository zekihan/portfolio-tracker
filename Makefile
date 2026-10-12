GO ?= go
BINARY := dist/portfolio-tracker

.PHONY: help build run fmt tidy lint test test/cover audit release-check build/release/all build/docker build/release/docker release container-test
help:
	@echo "Targets: build run fmt tidy lint test test/cover audit release-check build/release/all build/docker build/release/docker release container-test"

build:
	mkdir -p dist
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags '-s -w' -o $(BINARY) ./cmd/server

run: build
	./$(BINARY)

fmt:
	golangci-lint fmt

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

build/docker:
	goreleaser build --clean --snapshot --id docker

build/release/docker:
	goreleaser build --clean --id docker --skip=validate

release:
	goreleaser release --clean

container-test: build/docker
	./scripts/container_smoke.sh
