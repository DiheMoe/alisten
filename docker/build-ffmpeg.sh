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

sed -n '/\/\* audio codecs \*\//,/\/\* subtitles \*\//s/.*ff_\([a-z0-9_]*\)_decoder;.*/\1/p' \
    libavcodec/allcodecs.c >/tmp/audio-decoders
# allcodecs.c 的注释一变，提取结果就会变短甚至为空，configure 照样成功。FFmpeg 7.1.5 能提取到 201 个。
audio_decoder_count=$(wc -l </tmp/audio-decoders)
if [ "$audio_decoder_count" -lt 150 ]; then
    echo "只从 libavcodec/allcodecs.c 提取到 $audio_decoder_count 个音频解码器，检查 /* audio codecs */ 和 /* subtitles */ 这两条注释是否还在" >&2
    exit 1
fi
audio_decoders=$(paste -sd, /tmp/audio-decoders)
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

# 交叉编译时，这里靠 binfmt 模拟运行目标架构的 ffmpeg，和最终阶段的 RUN 一样。
./ffmpeg -hide_banner -decoders >/tmp/decoders
./ffmpeg -hide_banner -encoders >/tmp/encoders
for decoder in aac opus flac mp3; do
    grep -Eq "^ A[^ ]* $decoder " /tmp/decoders || { echo "编出来的 ffmpeg 没有 $decoder 解码器" >&2; exit 1; }
done
grep -Eq "^ A[^ ]* libmp3lame " /tmp/encoders || { echo "编出来的 ffmpeg 没有 libmp3lame 编码器" >&2; exit 1; }
install -D -t /out/usr/local/bin ffmpeg ffprobe
