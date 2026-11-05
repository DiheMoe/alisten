FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
ARG TARGETOS TARGETARCH
ARG VERSION=dev
WORKDIR /src
COPY --parents go.mod go.sum cmd db internal protocol ./
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -tags grpcnotrace \
    -ldflags "-s -w -X github.com/dihemoe/alisten/internal/bootstrap.version=$VERSION" \
    -o /out/usr/local/bin/alisten ./cmd/alisten
COPY LICENSE /out/usr/share/doc/alisten/

FROM --platform=$BUILDPLATFORM debian:trixie-slim AS ffmpeg
ARG TARGETARCH
ARG FFMPEG_VERSION=7.1.5
COPY docker/build-ffmpeg.sh /tmp/
RUN sh /tmp/build-ffmpeg.sh

FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS tools
ARG TARGETARCH
ARG YTDLP_VERSION=2026.10.21
ARG DENO_VERSION=v2.9.8
COPY --from=ffmpeg /out /out
COPY docker/fetch-tools.sh /tmp/
RUN sh /tmp/fetch-tools.sh

FROM debian:trixie-slim AS alisten-worker
COPY --from=tools /out /
RUN apt-get update \
    && DEBIAN_FRONTEND=noninteractive apt-get install --yes --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --system --gid 65532 alisten \
    && useradd --system --uid 65532 --gid alisten --home-dir /var/cache/alisten --no-create-home \
        --shell /usr/sbin/nologin alisten \
    && install -d -o alisten -g alisten /var/cache/alisten \
    && ffmpeg -hide_banner -encoders | grep -q libmp3lame \
    && ffprobe -hide_banner -version >/dev/null
COPY --from=build /out /
ENV XDG_CACHE_HOME=/var/cache/alisten
USER 65532:65532
EXPOSE 9090
HEALTHCHECK --start-period=30s --start-interval=1s --interval=10s --timeout=3s --retries=3 \
    CMD ["/usr/local/bin/alisten", "healthcheck"]
ENTRYPOINT ["/usr/local/bin/alisten"]
CMD ["worker"]
