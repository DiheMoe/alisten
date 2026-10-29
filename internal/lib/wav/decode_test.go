package wav

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/DiheMoe/gl3d"
)

func chunk(id string, body []byte) []byte {
	b := append([]byte(id), binary.LittleEndian.AppendUint32(nil, uint32(len(body)))...)
	b = append(b, body...)
	if len(body)%2 == 1 {
		b = append(b, 0)
	}
	return b
}

func fmtChunk(format uint16, channels, rate int, bits uint16) []byte {
	b := binary.LittleEndian.AppendUint16(nil, format)
	b = binary.LittleEndian.AppendUint16(b, uint16(channels))
	b = binary.LittleEndian.AppendUint32(b, uint32(rate))
	align := uint16(channels) * bits / 8
	b = binary.LittleEndian.AppendUint32(b, uint32(rate)*uint32(align))
	b = binary.LittleEndian.AppendUint16(b, align)
	return binary.LittleEndian.AppendUint16(b, bits)
}

func riff(chunks ...[]byte) []byte {
	body := []byte("WAVE")
	for _, c := range chunks {
		body = append(body, c...)
	}
	return append(append([]byte("RIFF"), binary.LittleEndian.AppendUint32(nil, uint32(len(body)))...), body...)
}

func le[T int16 | int32 | uint32 | uint64](values ...T) []byte {
	var b bytes.Buffer
	for _, v := range values {
		if err := binary.Write(&b, binary.LittleEndian, v); err != nil {
			panic(err)
		}
	}
	return b.Bytes()
}

func int24(values ...int32) []byte {
	var b []byte
	for _, v := range values {
		b = append(b, byte(v), byte(v>>8), byte(v>>16))
	}
	return b
}

func TestDecodeWAV(t *testing.T) {
	// WAVE_FORMAT_EXTENSIBLE：fmt 块 40 字节，子格式 GUID 的前两个字节是真正的格式。
	extensible := append(fmtChunk(formatExtensible, 1, 8000, 16), le[int16](22, 16)...)
	extensible = append(extensible, le[uint32](0)...)
	extensible = append(extensible, binary.LittleEndian.AppendUint16(nil, formatPCM)...)
	extensible = append(extensible, make([]byte, 14)...)
	tests := []struct {
		name     string
		file     []byte
		channels int
		want     []float64
	}{
		{"8 位无符号", riff(chunk("fmt ", fmtChunk(formatPCM, 1, 8000, 8)), chunk("data", []byte{0, 64, 128, 192, 255})),
			1, []float64{-1, -0.5, 0, 0.5, 127.0 / 128}},
		{"16 位双声道", riff(chunk("fmt ", fmtChunk(formatPCM, 2, 8000, 16)), chunk("data", le[int16](-32768, 16384, 0, 32767))),
			2, []float64{-1, 0.5, 0, 32767.0 / 32768}},
		{"24 位", riff(chunk("fmt ", fmtChunk(formatPCM, 1, 8000, 24)), chunk("data", int24(-8388608, 4194304, -1))),
			1, []float64{-1, 0.5, -1.0 / 8388608}},
		{"32 位", riff(chunk("fmt ", fmtChunk(formatPCM, 1, 8000, 32)), chunk("data", le[int32](math.MinInt32, 1<<30))),
			1, []float64{-1, 0.5}},
		{"32 位浮点", riff(chunk("fmt ", fmtChunk(formatFloat, 1, 8000, 32)), chunk("data", le(math.Float32bits(-1), math.Float32bits(0.25)))),
			1, []float64{-1, 0.25}},
		{"64 位浮点", riff(chunk("fmt ", fmtChunk(formatFloat, 1, 8000, 64)), chunk("data", le(math.Float64bits(-0.75), math.Float64bits(1)))),
			1, []float64{-0.75, 1}},
		{"WAVE_FORMAT_EXTENSIBLE", riff(chunk("fmt ", extensible), chunk("data", le[int16](16384))),
			1, []float64{0.5}},
		{"跳过 data 之前的其他块，包括奇数长度块的填充字节", riff(chunk("fmt ", fmtChunk(formatPCM, 1, 8000, 16)), chunk("LIST", []byte("abc")), chunk("data", le[int16](-16384))),
			1, []float64{-0.5}},
		{"data 末尾不足一个采样的字节忽略", riff(chunk("fmt ", fmtChunk(formatPCM, 1, 8000, 16)), chunk("data", append(le[int16](16384, -16384), 7))),
			1, []float64{0.5, -0.5}},
		{"fmt 块比用到的字节更长，包括奇数长度的填充字节", riff(chunk("fmt ", append(fmtChunk(formatPCM, 1, 8000, 16), make([]byte, 35)...)), chunk("data", le[int16](-16384))),
			1, []float64{-0.5}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, err := Decode(bytes.NewReader(tt.file))
			if err != nil {
				t.Fatal(err)
			}
			if a.SampleRate != 8000 || a.Channels != tt.channels || !slices.Equal(a.Samples, tt.want) {
				t.Errorf("解码为 %d Hz、%d 声道、%v，期望 8000 Hz、%d 声道、%v", a.SampleRate, a.Channels, a.Samples, tt.channels, tt.want)
			}
			if want := time.Duration(len(tt.want)/tt.channels) * time.Second / 8000; a.Duration() != want {
				t.Errorf("Duration = %v，期望 %v", a.Duration(), want)
			}
		})
	}
}

func TestDecodeRejectsMalformedWAV(t *testing.T) {
	pcm16 := chunk("fmt ", fmtChunk(formatPCM, 1, 8000, 16))
	tests := []struct {
		name string
		file []byte
		want string
	}{
		{"不是 WAVE", append([]byte("RIFF\x04\x00\x00\x00WAVX"), make([]byte, 8)...), "无法识别的音频格式"},
		{"data 在 fmt 之前", riff(chunk("data", le[int16](1)), pcm16), "缺少 fmt 块"},
		{"没有 data 块", riff(pcm16), "缺少 data 块"},
		{"MP3 编码的 WAV", riff(chunk("fmt ", fmtChunk(0x55, 1, 8000, 16)), chunk("data", le[int16](1))), "不支持的音频格式 0x55"},
		{"4 位", riff(chunk("fmt ", fmtChunk(formatPCM, 1, 8000, 4)), chunk("data", []byte{1})), "不支持的位深 4"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Decode(bytes.NewReader(tt.file)); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("错误 = %v，应包含 %q", err, tt.want)
			}
		})
	}
}

func TestDecodeDoesNotAllocateByDeclaredChunkSize(t *testing.T) {
	header := append([]byte("RIFF"), le[uint32](0xFFFFFFFF)...)
	header = append(header, "WAVEfmt "...)
	header = append(header, le[uint32](0xFFFFFFF0)...)
	for _, tt := range []struct {
		name string
		body int
	}{
		{"文件在 fmt 块用到的字节之前结束", 24},
		{"文件在 fmt 块用到的字节之后结束", 100},
	} {
		t.Run(tt.name, func(t *testing.T) {
			file := append(slices.Clone(header), make([]byte, tt.body)...)
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			_, err := Decode(bytes.NewReader(file))
			runtime.ReadMemStats(&after)
			if err == nil || !strings.Contains(err.Error(), "读取 fmt 块失败") {
				t.Errorf("错误 = %v，应包含 %q", err, "读取 fmt 块失败")
			}
			if alloc := after.TotalAlloc - before.TotalAlloc; alloc > 1<<20 {
				t.Errorf("fmt 块声明了 %d 字节，文件只有 %d 字节，解码却分配了 %d 字节", uint32(0xFFFFFFF0), len(file), alloc)
			}
		})
	}
}

// testdata 中的文件由 ffmpeg 的 sine 源生成：1000 Hz、振幅 1/8、0.5 秒。在 testdata 目录中重新生成：
//
//	ffmpeg -nostdin -loglevel error -f lavfi -i 'sine=frequency=1000:sample_rate=44100:duration=0.5' -c:a flac -sample_fmt s16 -fflags +bitexact -map_metadata -1 sine.flac
//	ffmpeg -nostdin -loglevel error -f lavfi -i 'sine=frequency=1000:sample_rate=44100:duration=0.5' -c:a libmp3lame -q:a 5 -fflags +bitexact -map_metadata -1 sine-mpeg1.mp3
//	ffmpeg -nostdin -loglevel error -f lavfi -i 'sine=frequency=1000:sample_rate=22050:duration=0.5' -c:a libmp3lame -q:a 5 -fflags +bitexact -map_metadata -1 sine-mpeg2.mp3
//	ffmpeg -nostdin -loglevel error -f lavfi -i 'sine=frequency=1000:sample_rate=8000:duration=0.5' -c:a libmp3lame -q:a 5 -fflags +bitexact -map_metadata -1 sine-mpeg25.mp3
//
// ffmpeg n9.0.2 生成的文件与仓库中的逐字节相同；其他版本生成的字节可能不同，测试只检查解码出的采样率、声道数、时长、
// 峰值和半周期数。MP3 编码器在两端加了静音：Info 帧里的 LAME 标签有效，gl3d 把它们裁掉，
// 所以时长正好是 0.5 秒，这条断言也固定了裁剪。
func TestDecodeCompressed(t *testing.T) {
	tests := []struct {
		file           string
		rate, channels int
	}{
		{"sine.flac", 44100, 1},
		{"sine-mpeg1.mp3", 44100, 1},
		{"sine-mpeg2.mp3", 22050, 1},
		{"sine-mpeg25.mp3", 8000, 1},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", tt.file))
			if err != nil {
				t.Fatal(err)
			}
			a, err := Decode(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			if a.SampleRate != tt.rate || a.Channels != tt.channels {
				t.Fatalf("解码为 %d Hz、%d 声道，期望 %d Hz、%d 声道", a.SampleRate, a.Channels, tt.rate, tt.channels)
			}
			if d := a.Duration(); d != 500*time.Millisecond {
				t.Errorf("Duration = %v，期望 500ms", d)
			}
			// 只统计越过 ±0.05 的正负交替，编码器在两端加的静音中的细小噪声不算在内。
			peak, halves, sign := 0.0, 0, 0.0
			for i := 0; i < len(a.Samples); i += tt.channels {
				v := a.Samples[i]
				peak = max(peak, math.Abs(v))
				if math.Abs(v) > 0.05 && math.Signbit(v) != math.Signbit(sign) {
					if sign != 0 {
						halves++
					}
					sign = v
				}
			}
			if peak < 0.11 || peak > 0.15 {
				t.Errorf("峰值 = %.4f，期望约 0.125", peak)
			}
			// 1000 Hz 的正弦波 0.5 秒有 1000 个半周期。
			if halves < 995 || halves > 1005 {
				t.Errorf("正负交替 %d 次，期望约 1000 次", halves)
			}
		})
	}
}

// 417 字节是 128 kbps、44.1 kHz 的帧长。帧头之后全是零：Layer III 的这样一帧解码出 1152 个采样帧的静音。
func mp3Frame(header ...byte) []byte {
	return append(header, make([]byte, 417-len(header))...)
}

var (
	silentFrame = mp3Frame(0xFF, 0xFB, 0x90, 0x00)
	layer2Frame = mp3Frame(0xFF, 0xFD, 0x90, 0x00)
)

// 准备任务在 serve 进程中解码整首歌，稳态下每次 Read 都不能分配内存。
func TestMP3ReadDoesNotAllocate(t *testing.T) {
	dec, err := NewDecoder(bytes.NewReader(bytes.Repeat(silentFrame, 300)))
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]float64, 32768)
	if allocs := testing.AllocsPerRun(100, func() {
		if _, err := dec.Read(buf); err != nil {
			t.Fatal(err)
		}
	}); allocs != 0 {
		t.Errorf("每次 Read 分配 %v 次，期望 0", allocs)
	}
}

func asError[T error](err error) bool {
	var target T
	return errors.As(err, &target)
}

func TestDecodeRejectsUndecodableMP3(t *testing.T) {
	tests := []struct {
		name string
		file []byte
		want string
		is   func(error) bool
	}{
		{"Layer II", bytes.Repeat(layer2Frame, 3), "不支持的 MP3 格式", asError[*gl3d.UnsupportedError]},
		{"最后一帧被截断", bytes.Repeat(silentFrame, 4)[:3*len(silentFrame)+200], "MP3 数据被截断", asError[*gl3d.TruncatedError]},
		{"ID3 标签声明的长度超过上限", []byte("ID3\x04\x00\x00\x7f\x7f\x7f\x7f\x00\x00"), "MP3 超出解码的资源上限", asError[*gl3d.LimitError]},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Decode(bytes.NewReader(tt.file)); err == nil || !strings.Contains(err.Error(), tt.want) || !tt.is(err) {
				t.Errorf("错误 = %v，应包含 %q 并保留 gl3d 的错误", err, tt.want)
			}
		})
	}
}

type failingReader struct{ err error }

func (r failingReader) Read([]byte) (int, error) { return 0, r.err }

func TestDecodeMP3ReturnsReaderError(t *testing.T) {
	file := bytes.Repeat(silentFrame, 20)
	r := io.MultiReader(bytes.NewReader(file[:5*len(silentFrame)+100]), failingReader{context.Canceled})
	if _, err := Decode(r); !errors.Is(err, context.Canceled) {
		t.Errorf("错误 = %v，期望 reader 返回的 %v", err, context.Canceled)
	}
}
