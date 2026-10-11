# Portfolio Tracker

Self-hosted personal portfolio tracker — HTMX + Go (server-rendered) • SQLite • Chart.js

## Features

- **Multi-asset support**: Stocks, ETFs, Crypto, Gold, TR Funds, Cash
- **12 price providers**: Yahoo Finance, Alpha Vantage, Twelve Data, Finnhub, Pusula Portföy, Garanti BBVA, İZKO Gold, TradingView BIST/Crypto, CoinGecko, Frankfurter, Manual
- **Real-time prices** with caching, TTL, and circuit breaker (3 failures = open; retry after a 5-minute cooldown)
- **Hourly snapshots** with DB advisory lock leader election
- **Responsive UI**: Desktop table + mobile cards, dark/light mode
- **Charts**: Portfolio value over time, allocation pies (by type/basket/location/currency)
- **HTMX-powered**: Server-rendered, minimal JavaScript, works on low-power devices
- **Privacy-first**: Self-hosted, no telemetry, single-user

## Quick Start

```bash
# Build
go build -o portfolio-tracker ./cmd/server

# Run
DB_PATH=/data/portfolio.db PORT=8080 ./portfolio-tracker
```

Then visit http://localhost:8080

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `DB_PATH` | `/data/portfolio.db` | SQLite database path |
| `PORT` | `8080` | HTTP server port |
| `GIN_MODE` | `release` | Gin mode (debug/release) |
| `ALPHA_VANTAGE_KEY` | - | Alpha Vantage API key |
| `TWELVE_DATA_KEY` | - | Twelve Data API key |
| `FINNHUB_KEY` | - | Finnhub API key |
| `COINGECKO_KEY` | - | CoinGecko API key |

## Architecture

- **Stack**: Go 1.27+ • Gin • HTMX • Chart.js • SQLite
- **Single binary**; templates and static assets are read from `./web` at runtime
- **In-app cron** (robfig/cron) for hourly snapshots
- **Leader election** via SQLite advisory lock
- **Price cache** with TTL per provider
- **Circuit breaker** after 3 consecutive failures

## Authentication

The application has no built-in authentication or authorization, by design.
Run it behind a reverse proxy that handles authentication (for example
OAuth2 Proxy, Authelia, or HTTP basic auth over TLS) and only expose the
proxy. The API includes destructive endpoints (clearing all data and
restoring from a backup), so the server must not be reachable without the
proxy in front of it.

## Deployment

### Docker

```bash
docker build -t portfolio-tracker .
docker run -p 8080:8080 -v portfolio-data:/data portfolio-tracker
```

The image is built with cgo (required by the SQLite driver) and includes the `web/` assets.

## Development

```bash
# Install tools
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
go install github.com/goreleaser/goreleaser@latest

# Lint
make lint

# Test
make test

# Build
make build

# Release
make release
```

## License

GNU Affero General Public License v3.0 - see [LICENSE](LICENSE)