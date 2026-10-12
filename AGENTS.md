# AGENTS.md

This file provides guidance to AI Agents when working with code in this repository.

## Commands

```bash
make fmt tidy      # format source + go mod tidy
make lint          # check formatting + golangci-lint
make test          # run all tests with -race
make test/cover    # write coverage.out
make audit         # lint + tests + go mod verify
make build         # build dist/portfolio-tracker
make release-check # validate release configuration
make container-test # build and smoke-test Docker image
make release       # goreleaser release
```

To run a single test:
```bash
go test -v -run TestFunctionName ./internal/...
```

## Architecture

This is a self-hosted personal portfolio tracker with HTMX + Go server-rendered templates.

**Packages:**
- `cmd/server` — Entry point, Gin server, cron scheduler, graceful shutdown
- `internal/db` — SQLite schema, migrations (v1-v2), connection management
- `internal/models` — Domain types (Holding, Price, Snapshot, Provider, DashboardData)
- `internal/handlers` — HTTP handlers (REST API + server-rendered pages)
- `internal/price` — 12 price providers + service with caching, circuit breaker, leader election
- `internal/services` — Business logic (HoldingService, SnapshotService, SettingsService)
- `internal/charts` — Chart.js configuration helpers
- `web/templates` — Go HTML templates (base, dashboard, holdings, history, sources, settings)
- `web/static` — CSS, JS, Chart.js (served from disk at ./web/static; the Docker image copies ./web)

**Key Design Decisions:**
- **Single binary** that reads templates and static assets from `./web` at runtime
- **HTMX** for interactivity — minimal JS, server-rendered HTML
- **SQLite** with migrations (v2 fixes NULL manual_price with DEFAULT 0)
- **In-app cron** (robfig/cron/v3) for hourly snapshots
- **Leader election** via SQLite advisory lock for cron
- **Price cache** with TTL per provider + circuit breaker (3 failures = open)
- **Currency normalization** — all values stored in base currency, displayed in user's selected currency via Frankfurter FX rates
- **12 price providers**: Yahoo Finance, Alpha Vantage, Twelve Data, Finnhub, Pusula Portföy, Garanti BBVA, İZKO Gold, TradingView BIST, TradingView Crypto, CoinGecko, Frankfurter, Manual

## Linting Rules

The linter (`golangci-lint`, config in `.golangci.yaml`) uses the focused checks
shared by sibling Go services: errcheck, govet, ineffassign, staticcheck, and unused.
Formatting uses gofmt. `make lint` does not modify files; use `make fmt tidy` first.

## Development Workflow

1. Make changes
2. Run `make fmt tidy` and `make lint` to format and check the source
3. Run `make test` to ensure tests pass
4. Run `make build` to verify build succeeds
5. Test manually: `DB_PATH=/tmp/portfolio.db PORT=8080 ./dist/portfolio-tracker`