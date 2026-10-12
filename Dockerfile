FROM --platform=$BUILDPLATFORM busybox:1.38.0@sha256:fd7dc98638c8e305f4dc34e979f1c0fdfdcaeb0fbf8fcff77ae834b6da3d7e6e AS build
ARG TARGETOS
ARG TARGETARCH
COPY dist/portfolio-tracker_${TARGETOS}_${TARGETARCH}/portfolio-tracker /portfolio-tracker

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
