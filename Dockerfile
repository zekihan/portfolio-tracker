# Build stage
FROM golang:1.27-alpine AS builder

WORKDIR /app

# Install build dependencies
RUN apk add --no-cache git make gcc musl-dev

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Build
# The sqlite driver (mattn/go-sqlite3) requires cgo.
RUN CGO_ENABLED=1 go build -o portfolio-tracker ./cmd/server

# Final stage
FROM alpine:3.24

# Install runtime dependencies
RUN apk add --no-cache ca-certificates tzdata sqlite-libs

# Create non-root user
RUN adduser -D -H -s /sbin/nologin appuser

WORKDIR /app

# Copy binary
COPY --from=builder /app/portfolio-tracker /usr/local/bin/portfolio-tracker
# Templates and static assets are read from ./web at runtime.
COPY --from=builder /app/web /app/web

# Create data directory
RUN mkdir -p /data && chown appuser:appuser /data

USER appuser

EXPOSE 8080

VOLUME ["/data"]

ENV DB_PATH=/data/portfolio.db
ENV PORT=8080
ENV GIN_MODE=release

CMD ["portfolio-tracker"]