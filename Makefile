GO ?= go
PROJECT_NAME := portfolio-tracker
RELEASE_VERSION := $(shell tr -d '[:space:]' < VERSION)
OS_INFO := $(shell $(GO) env GOOS)
ARCH_INFO := $(shell $(GO) env GOARCH)
BINARY := dist/$(PROJECT_NAME)_$(OS_INFO)_$(ARCH_INFO)/$(PROJECT_NAME)$(shell $(GO) env GOEXE)
DB_PATH ?= ./portfolio.db

.PHONY: help build run fmt tidy build/local build/all build/release new_version new_version/patch new_version/minor new_version/major lint test test/web test/scripts test/cover audit release-check build/release/all build/docker build/release/docker release container-test
help:
	@echo "Targets: build run fmt tidy build/local build/all build/release new_version new_version/patch new_version/minor new_version/major lint test test/web test/scripts test/cover audit release-check build/release/all build/docker build/release/docker release container-test"

build: tidy build/local

build/local:
	goreleaser build --clean --id all --snapshot --single-target

build/all:
	goreleaser build --clean --id all --snapshot

build/release: build/release/all

run: build
	DB_PATH="$(DB_PATH)" ./$(BINARY)

fmt:
	golangci-lint fmt

tidy:
	$(GO) mod tidy

lint: tidy
	golangci-lint fmt
	golangci-lint run --fix || true
	golangci-lint run

test: tidy test/web test/scripts
	$(GO) test -count=1 -buildvcs -vet=all ./...
	$(GO) test -race -count=1 -buildvcs -vet=all ./...

test/web:
	node --check web/static/app.js
	node --test web/tests/*.test.cjs

test/scripts:
	node --test scripts/*.test.cjs

test/cover:
	$(GO) test -race -coverprofile=coverage.out ./...

audit: tidy
	$(GO) mod verify
	$(GO) vet ./...
	golangci-lint run --enable-only=staticcheck
	$(GO) run golang.org/x/vuln/cmd/govulncheck@latest ./...
	$(GO) test -buildvcs -vet=all ./...

release-check:
	goreleaser check

build/release/all:
	GORELEASER_CURRENT_TAG=v$(RELEASE_VERSION) goreleaser build --clean --id all --skip=validate

build/docker:
	goreleaser build --clean --snapshot --id docker

build/release/docker:
	GORELEASER_CURRENT_TAG=v$(RELEASE_VERSION) goreleaser build --clean --id docker --skip=validate

release:
	goreleaser release --clean

container-test: build/docker
	./scripts/container_smoke.sh

new_version:
	./scripts/new_version.sh $(VERSION)

new_version/patch: new_version

new_version/minor:
	./scripts/new_version.sh "$$(awk -F. '{printf "%d.%d.0", $$1, $$2 + 1}' VERSION)"

new_version/major:
	./scripts/new_version.sh "$$(awk -F. '{printf "%d.0.0", $$1 + 1}' VERSION)"
