# AGENTS.md

This file provides guidance to AI Agents when working with code in this repository.

## Commands

```bash
make tidy          # fmt + go mod tidy
make lint          # tidy + golangci-lint run --fix
make test          # run all tests with -race
make test/cover    # run tests and open coverage report in browser
make audit         # vet + staticcheck + govulncheck + tests
make build         # build binary
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
- `web/static` — CSS, JS, Chart.js (embedded via //go:embed)

**Key Design Decisions:**
- **Single binary** with embedded templates/static assets
- **HTMX** for interactivity — minimal JS, server-rendered HTML
- **SQLite** with migrations (v2 fixes NULL manual_price with DEFAULT 0)
- **In-app cron** (robfig/cron/v3) for hourly snapshots
- **Leader election** via SQLite advisory lock for cron
- **Price cache** with TTL per provider + circuit breaker (3 failures = open)
- **Currency normalization** — all values stored in base currency, displayed in user's selected currency via Frankfurter FX rates
- **12 price providers**: Yahoo Finance, Alpha Vantage, Twelve Data, Finnhub, Pusula Portföy, Garanti BBVA, İZKO Gold, TradingView BIST, TradingView Crypto, CoinGecko, Frankfurter, Manual

## Linting Rules

The linter (`golangci-lint`, config in `.golangci.yaml`) enforces several non-obvious rules:

- **No `testify`** — use `cmp` or standard library assertions in tests.
- **No `math/rand`** — use `math/rand/v2`.
- **No `io/ioutil`** — use `os.ReadFile`, `io.NopCloser`, etc.
- **No `log.Print*`/`log.Fatal*`/`os.Exit`/`time.Sleep`/`context.TODO`** — use `slog`, context with timeout, etc.
- **`slog` style**: `attr-only` (no kv mixed args), camelCase key names, capitalized messages.
- **Max line length**: 100 characters (golines).
- **Import order**: standard → default → project prefix → blank → dot → alias → localmodule.
- **Struct tag order**: `description`, `json`, `toml`, `yaml`/`yml`, `label`, `file`, `kv`, `export`.
- **`exhaustive`**: all switch/map enumerations must have a default case.

## Development Workflow

1. Make changes
2. Run `make lint` to fix formatting and linting issues
3. Run `make test` to ensure tests pass
4. Run `make build` to verify build succeeds
5. Test manually: `DB_PATH=/tmp/portfolio.db PORT=8080 ./portfolio-tracker`