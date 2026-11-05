#!/bin/sh
set -eu

case "$TARGETARCH" in
    amd64) arch=x86_64 triple=x86_64-linux-gnu cross_gcc=gcc-x86-64-linux-gnu ;;
    arm64) arch=aarch64 triple=aarch64-linux-gnu cross_gcc=gcc-aarch64-linux-gnu ;;
esac

dpkg --add-architecture "$TARGETARCH"
apt-get update
DEBIAN_FRONTEND=noninteractive apt-get install --yes --no-install-recommends \
    ca-certificates gcc libc6-dev make nasm wget "$cross_gcc" \
    "libc6-dev:$TARGETARCH" "libmp3lame-dev:$TARGETARCH" "zlib1g-dev:$TARGETARCH"

wget -q --tries=3 --timeout=30 -O /tmp/ffmpeg.tar.gz \
    "https://github.com/FFmpeg/FFmpeg/archive/refs/tags/n$FFMPEG_VERSION.tar.gz"
mkdir /tmp/ffmpeg
tar -xzf /tmp/ffmpeg.tar.gz -C /tmp/ffmpeg --strip-components=1
cd /tmp/ffmpeg

audio_decoders=$(sed -n '/\/\* audio codecs \*\//,/\/\* subtitles \*\//s/.*ff_\([a-z0-9_]*\)_decoder;.*/\1/p' \
    libavcodec/allcodecs.c | paste -sd,)
audio_decoders_listed_as_video=comfortnoise,dvaudio,eightsvx_exp,eightsvx_fib,rka,s302m,speex

./configure \
    --enable-cross-compile --arch="$arch" --target-os=linux --cross-prefix="$triple-" \
    --disable-everything --disable-autodetect --disable-doc --disable-debug --disable-network \
    --disable-ffplay --disable-avdevice --disable-swscale \
    --extra-ldexeflags=-static \
    --enable-zlib --enable-libmp3lame --enable-encoder=libmp3lame \
    --enable-protocol=file --enable-demuxers --enable-parsers --enable-bsfs \
    --enable-muxers --enable-filter=aresample \
    --enable-decoder="$audio_decoders,$audio_decoders_listed_as_video"
make -j"$(nproc)"
install -D -t /out/usr/local/bin ffmpeg ffprobe
