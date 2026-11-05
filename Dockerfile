FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
ARG TARGETOS TARGETARCH
ARG VERSION=dev
WORKDIR /src
COPY --parents go.mod go.sum cmd db internal protocol ./
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -tags grpcnotrace \
    -ldflags "-s -w -X github.com/dihemoe/alisten/internal/bootstrap.version=$VERSION" \
    -o /out/usr/local/bin/alisten ./cmd/alisten \
    && install -Dm644 /etc/ssl/certs/ca-certificates.crt /out/etc/ssl/certs/ca-certificates.crt
COPY LICENSE /out/usr/share/doc/alisten/

FROM gcr.io/distroless/static-debian13:nonroot AS alisten
COPY --from=build /out /
USER 65532:65532
EXPOSE 8080 9090
HEALTHCHECK --start-period=30s --start-interval=1s --interval=10s --timeout=3s --retries=3 \
    CMD ["/usr/local/bin/alisten", "healthcheck"]
ENTRYPOINT ["/usr/local/bin/alisten"]
CMD ["serve"]
