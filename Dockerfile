FROM --platform=$BUILDPLATFORM busybox:1.38.0@sha256:fd7dc98638c8e305f4dc34e979f1c0fdfdcaeb0fbf8fcff77ae834b6da3d7e6e AS build
ARG TARGETOS
ARG TARGETARCH
COPY dist/portfolio-tracker_${TARGETOS}_${TARGETARCH}/portfolio-tracker /portfolio-tracker

RUN mkdir -p /data /tmp && chmod 755 /data && chmod 1777 /tmp

FROM ghcr.io/zekihan/distroless:v1.0.0@sha256:a095a7a0a02b4fcc31ae9c51a579e340255d9345f9a0c17490852a645164f46f AS final
COPY --from=build --chown=25110:25110 /data /data
COPY --from=build --chmod=1777 /tmp /tmp
WORKDIR /app
COPY --from=build /portfolio-tracker /usr/local/bin/portfolio-tracker
COPY web ./web
COPY LICENSE /usr/share/licenses/portfolio-tracker/LICENSE
USER 25110:25110
EXPOSE 8080
VOLUME ["/data"]
ENV DB_PATH=/data/portfolio.db PORT=8080
ENTRYPOINT ["portfolio-tracker"]
LABEL org.opencontainers.image.authors="zekihan@noqer.com" \
      org.opencontainers.image.url="https://noqer.com/" \
      org.opencontainers.image.documentation="https://github.com/zekihan/portfolio-tracker#readme" \
      org.opencontainers.image.vendor="Zekihan AZMAN" \
      org.opencontainers.image.source="https://github.com/zekihan/portfolio-tracker" \
      org.opencontainers.image.licenses="AGPL-3.0" \
      org.opencontainers.image.title="portfolio-tracker" \
      org.opencontainers.image.description="Self-hosted personal portfolio tracker"
