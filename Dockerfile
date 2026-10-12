FROM golang:1.27.2-alpine@sha256:f92b6ef800e499660581efdabdf25d9d817a9d124eaf900924f0504e7e27e12d AS build
WORKDIR /src
RUN apk add --no-cache gcc musl-dev
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=1 go build -trimpath -ldflags "-s -w" -o /portfolio-tracker ./cmd/server

FROM alpine:3.24.2@sha256:294b683cb724975bec92580e1e685676bd4b50bda910ddb8c51d4cabeaec77e6
RUN apk add --no-cache ca-certificates tzdata \
    && adduser -D -H -u 25110 -s /sbin/nologin appuser \
    && mkdir -p /data && chown appuser:appuser /data
WORKDIR /app
COPY --from=build /portfolio-tracker /usr/local/bin/portfolio-tracker
COPY web ./web
COPY LICENSE /usr/share/licenses/portfolio-tracker/LICENSE
USER appuser
EXPOSE 8080
VOLUME ["/data"]
ENV DB_PATH=/data/portfolio.db PORT=8080 GIN_MODE=release
ENTRYPOINT ["portfolio-tracker"]
LABEL org.opencontainers.image.source="https://github.com/zekihan/portfolio-tracker" \
      org.opencontainers.image.licenses="AGPL-3.0" \
      org.opencontainers.image.title="portfolio-tracker" \
      org.opencontainers.image.description="Self-hosted personal portfolio tracker"
