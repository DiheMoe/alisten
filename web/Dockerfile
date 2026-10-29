FROM --platform=$BUILDPLATFORM node:24-alpine AS build
ARG VERSION=dev
WORKDIR /src
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ .
RUN npm run build \
    && find dist -type f \( -name '*.html' -o -name '*.js' -o -name '*.css' -o -name '*.svg' -o -name '*.json' -o -name '*.webmanifest' \) -exec gzip -9 -k {} + \
    && sed "s|@VERSION@|${VERSION}|" nginx/default.conf > /tmp/default.conf

FROM nginxinc/nginx-unprivileged:1.30-alpine
ENV NGINX_ENTRYPOINT_QUIET_LOGS=1
COPY web/nginx/nginx.conf /etc/nginx/nginx.conf
COPY --from=build /tmp/default.conf /etc/nginx/conf.d/default.conf
COPY --from=build /src/dist /usr/share/nginx/html
COPY web/nginx/templates /etc/nginx/alisten-templates
COPY LICENSE /usr/share/doc/alisten/
EXPOSE 8080 9090
HEALTHCHECK --start-period=30s --start-interval=1s --interval=10s --timeout=3s --retries=3 \
    CMD wget -q -O /dev/null http://127.0.0.1:9090/healthz || exit 1
