FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
ARG TARGETOS TARGETARCH
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
COPY cmd ./cmd
COPY db ./db
COPY internal ./internal
COPY protocol ./protocol
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -tags grpcnotrace \
    -ldflags "-s -w -X github.com/dihemoe/alisten/internal/bootstrap.version=$VERSION" \
    -o /out/alisten ./cmd/alisten

FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS tools
ARG TARGETARCH
ARG YTDLP_VERSION=2026.10.21
ARG DENO_VERSION=v2.9.8
RUN case "$TARGETARCH" in amd64) ytdlp=yt-dlp_linux deno=x86_64 ;; arm64) ytdlp=yt-dlp_linux_aarch64 deno=aarch64 ;; esac \
    && wget -q --tries=3 --timeout=30 -O /tmp/yt-dlp.zip "https://github.com/yt-dlp/yt-dlp/releases/download/$YTDLP_VERSION/$ytdlp.zip" \
    && wget -q --tries=3 --timeout=30 -O /tmp/deno.zip "https://github.com/denoland/deno/releases/download/$DENO_VERSION/deno-$deno-unknown-linux-gnu.zip" \
    && mkdir -p /out/opt/yt-dlp /out/usr/local/bin \
    && unzip -q /tmp/yt-dlp.zip -d /out/opt/yt-dlp \
    && unzip -q /tmp/deno.zip -d /out/usr/local/bin \
    && ln -s "/opt/yt-dlp/$ytdlp" /out/usr/local/bin/yt-dlp

FROM debian:trixie-slim AS alisten-worker
RUN apt-get update \
    && DEBIAN_FRONTEND=noninteractive apt-get install --yes --no-install-recommends ca-certificates ffmpeg \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --system --gid 65532 alisten \
    && useradd --system --uid 65532 --gid alisten --home-dir /var/cache/alisten --no-create-home --shell /usr/sbin/nologin alisten \
    && install -d -o alisten -g alisten /var/cache/alisten
COPY LICENSE /usr/share/doc/alisten/
COPY --from=tools /out /
COPY --from=build /out/alisten /usr/local/bin/alisten
ENV XDG_CACHE_HOME=/var/cache/alisten
USER 65532:65532
EXPOSE 9090
HEALTHCHECK --start-period=30s --start-interval=1s --interval=10s --timeout=3s --retries=3 \
    CMD ["/usr/local/bin/alisten", "healthcheck"]
ENTRYPOINT ["/usr/local/bin/alisten"]
CMD ["worker"]
