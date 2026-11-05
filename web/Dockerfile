FROM --platform=$BUILDPLATFORM node:24-alpine AS build
ARG VERSION=dev
WORKDIR /src
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ .
RUN npm run build \
    && find dist -type f ! -name '*.png' -exec gzip -9 -k {} + \
    && install -d -m 775 /out/etc/nginx /out/etc/nginx/conf.d \
    && mkdir -p /out/usr/share/nginx \
    && sed "s|@VERSION@|${VERSION}|" nginx/default.conf > /out/etc/nginx/conf.d/default.conf \
    && cp nginx/nginx.conf /out/etc/nginx/nginx.conf \
    && cp -R nginx/templates /out/etc/nginx/alisten-templates \
    && mv dist /out/usr/share/nginx/html
COPY LICENSE /out/usr/share/doc/alisten/

FROM nginxinc/nginx-unprivileged:1.30-alpine
ENV NGINX_ENTRYPOINT_QUIET_LOGS=1
COPY --from=build --parents \
    /out/./etc/nginx/nginx.conf \
    /out/./etc/nginx/conf.d/default.conf \
    /out/./etc/nginx/alisten-templates \
    /out/./usr/share/nginx/html \
    /out/./usr/share/doc/alisten/LICENSE \
    /
EXPOSE 8080 9090
HEALTHCHECK --start-period=30s --start-interval=1s --interval=10s --timeout=3s --retries=3 \
    CMD wget -q -O /dev/null http://127.0.0.1:9090/healthz || exit 1
