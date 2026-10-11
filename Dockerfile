# Build stage
FROM golang:1.23-alpine AS builder

WORKDIR /app

# Install build dependencies
RUN apk add --no-cache git make

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Build
RUN CGO_ENABLED=0 go build -o portfolio-tracker ./cmd/server

# Final stage
FROM alpine:3.24

# Install runtime dependencies
RUN apk add --no-cache ca-certificates tzdata sqlite-libs

# Create non-root user
RUN adduser -D -H -s /sbin/nologin appuser

WORKDIR /app

# Copy binary
COPY --from=builder /app/portfolio-tracker /usr/local/bin/portfolio-tracker

# Create data directory
RUN mkdir -p /data && chown appuser:appuser /data

USER appuser

EXPOSE 8080

VOLUME ["/data"]

ENV DB_PATH=/data/portfolio.db
ENV PORT=8080
ENV GIN_MODE=release

CMD ["portfolio-tracker"]