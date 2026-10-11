# Portfolio Tracker - Makefile
# Usage: make <target>

.PHONY: help tidy lint test test/cover audit build release

# Default target
help:
	@echo "Portfolio Tracker - Available targets:"
	@echo "  make tidy          # go fmt + go mod tidy"
	@echo "  make lint          # tidy + golangci-lint run --fix"
	@echo "  make test          # run all tests with -race"
	@echo "  make test/cover    # run tests and open coverage report in browser"
	@echo "  make audit         # vet + staticcheck + govulncheck + tests"
	@echo "  make build         # build binary"
	@echo "  make release       # goreleaser release"

# Format and tidy
tidy:
	go fmt ./...
	go mod tidy

# Lint
lint: tidy
	golangci-lint run --fix ./...

# Test
test:
	go test -race -v ./...

test/cover:
	go test -race -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out

# Audit
audit:
	go vet ./...
	staticcheck ./...
	govulncheck ./...
	go test -race ./...

# Build
build:
	go build -o portfolio-tracker ./cmd/server

# Release
release:
	goreleaser release --clean