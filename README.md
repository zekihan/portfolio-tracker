# portfolio-tracker

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

## Run

```bash
make build
DB_PATH=./portfolio.db PORT=8080 ./dist/portfolio-tracker_$(go env GOOS)_$(go env GOARCH)/portfolio-tracker
```

Then visit http://localhost:8080. Requires the Go version in `go.mod`. SQLite uses the pure-Go
[modernc driver](https://pkg.go.dev/modernc.org/sqlite), so builds need no C compiler. Run from the repository root so `./web` is available.

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `DB_PATH` | `/data/portfolio.db` | SQLite database path |
| `PORT` | `8080` | HTTP server port |
| `ALPHA_VANTAGE_KEY` | - | Alpha Vantage API key |
| `TWELVE_DATA_KEY` | - | Twelve Data API key |
| `FINNHUB_KEY` | - | Finnhub API key |
| `COINGECKO_KEY` | - | CoinGecko API key |

## Architecture

- **Stack**: Go (see `go.mod`) • net/http • HTMX • Chart.js • SQLite
- **Single binary**; templates and static assets are read from `./web` at runtime
- **In-app cron** (robfig/cron) for hourly snapshots
- **Leader election** via SQLite advisory lock
- **Price cache** with TTL per provider
- **Circuit breaker** after 3 consecutive failures

## Valuation and refresh behavior

Unsupported or missing quotes leave the affected holding visible but unvalued.
Dashboard totals and allocations are explicitly marked partial until every holding
can be valued; incomplete valuations are not saved as complete snapshots. Known
unreliable historical points are shown as gaps rather than apparent losses.

Network-provider prices are cached for 15 minutes, including FX rates.
Expired cached prices return immediately with stale status while a background
refresh runs. Cold lookups and explicit refreshes share in-flight work, with at
most four upstream requests globally and one per provider. Valuation uses four
workers and a shared 20-second budget. Pusula fund lists and Garanti shared
metadata use coalesced 15-minute caches. Explicit refresh waits for results.
FX freshness is shown alongside quote freshness, and each valuation uses one
consistent FX sample. Failure to persist a newly fetched price makes that lookup
unavailable rather than presenting it as successfully refreshed.

Shutdown cancels and joins background work before closing SQLite. Clear/restore
blocks new refreshes and drains the previous generation so old responses cannot
repopulate replaced data.

## Authentication

The application has no built-in authentication or authorization, by design.
Run it behind a reverse proxy that handles authentication (for example
OAuth2 Proxy, Authelia, or HTTP basic auth over TLS) and only expose the
proxy. The API includes destructive endpoints (clearing all data and
restoring from a backup), so the server must not be reachable without the
proxy in front of it.

## Deployment

### Upgrading snapshot-lock ownership

The database migration adds ownership tokens to snapshot locks while preserving
existing locks until their normal expiry. Stop all older application instances
before starting this version against a shared database. Mixed old/new instances
are not safe: older code can unconditionally delete a newer instance's lock.

### Docker

```bash
make build/docker
docker build -t portfolio-tracker .
docker run --rm -p 127.0.0.1:8080:8080 -v portfolio-data:/data portfolio-tracker
```

GoReleaser builds the Linux binaries before Docker runs. The Dockerfile selects
the prebuilt binary for the target architecture and includes the `web/` assets.

## Development

Install [golangci-lint](https://golangci-lint.run/) v2.14.0 and
[GoReleaser](https://goreleaser.com/) v2.18.3 to match CI. Node.js 24.19.0 runs the dependency-free browser-logic tests.

```sh
make fmt tidy         # format source and tidy modules
make lint             # format, apply lint fixes, and check source
make test             # run browser-logic tests plus Go tests with the race detector
make test/web         # run only dependency-free JavaScript checks
make build            # build dist/portfolio-tracker
make audit            # verify modules, vet, staticcheck, vulnerability scan, and tests
make release-check    # validate GoReleaser configuration
make container-test   # build and smoke-test the container with networking disabled
```

Lint follows YouAud's configuration: all linters enabled by default, its shared
rule settings and formatter suite. Only JSON naming exceptions needed for the
existing API and upstream providers remain. `make lint` formats and applies fixes
before checking; CI rejects uncommitted changes produced by those steps.
`make test/cover` writes `coverage.out`; use `go tool cover -html=coverage.out` to view it.

## Releases

PR checks cross-compile every supported platform and are reused by the release
workflows. Tags run checks before publishing GitHub releases. Every push to
`main` (or a manual run) publishes Docker images after those checks.
GoReleaser cross-compiles Linux, macOS, and Windows binaries for amd64 and arm64
with `CGO_ENABLED=0`, plus a universal macOS binary. Releases contain tar.gz
archives (zip on Windows) and `checksums.txt`. Archives include `web/`; extract
the entire archive and run from that directory.

The container workflow publishes Linux amd64 and arm64 images to
`ghcr.io/zekihan/portfolio-tracker` and `docker.io/zekihan/portfolio-tracker`.
Tags come from `VERSION`: `latest`, full version, major/minor, major, equivalents
prefixed with `v`, and full-version tags suffixed with the workflow run number.
CI runs `make build/release/docker` before building images; Docker copies the matching
Linux binary from `dist/`, following YouAud's workflow. The final image uses
`zekihan/distroless`, runs as UID 25110, and stores its database in `/data`.
Bind mounts must be writable by that UID. The image has no shell; container
smoke tests use a separate BusyBox container sharing its isolated network.

Builds embed version, commit timestamp, commit hash, and working-tree state.
The server reports them in its startup log. Releases use grouped changelogs,
automatic prerelease detection, and Docker pull commands in the release notes.

`VERSION` starts at `0.1.0`. Use `make new_version/patch`,
`make new_version/minor`, `make new_version/major`, or
`make new_version VERSION=0.2.0` to create a local version commit and signed tag.
These helpers require a clean checkout, passing lint/tests, and configured Git
signing. They never push; publication requires a separate explicit push.

Repository secrets `DOCKER_USERNAME` and `DOCKER_PASSWORD` are required for
Docker Hub publication and README updates. GHCR and GitHub releases use the
workflow's `GITHUB_TOKEN`. README changes on `main` update the Docker Hub description.
`make release` publishes a release and requires an appropriate token.

## License

[GNU Affero General Public License v3.0](LICENSE).
