#!/bin/sh
set -eu

case "$TARGETARCH" in
    amd64) ytdlp=yt-dlp_linux deno=x86_64 ;;
    arm64) ytdlp=yt-dlp_linux_aarch64 deno=aarch64 ;;
esac

wget -q --tries=3 --timeout=30 -O /tmp/yt-dlp.zip \
    "https://github.com/yt-dlp/yt-dlp/releases/download/$YTDLP_VERSION/$ytdlp.zip"
wget -q --tries=3 --timeout=30 -O /tmp/deno.zip \
    "https://github.com/denoland/deno/releases/download/$DENO_VERSION/deno-$deno-unknown-linux-gnu.zip"

mkdir -p /out/opt/yt-dlp /out/usr/local/bin
unzip -q /tmp/yt-dlp.zip -d /out/opt/yt-dlp
unzip -q /tmp/deno.zip -d /out/usr/local/bin
ln -s "/opt/yt-dlp/$ytdlp" /out/usr/local/bin/yt-dlp
