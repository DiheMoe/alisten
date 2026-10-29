package loudness

import (
	"bytes"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// testdata 中的 MP3 是 3 秒的 LAME 恒定码率文件，内容是粉红噪声，或左右声道不同的两组音调。在 testdata 目录中重新生成：
//
//	ffmpeg -nostdin -loglevel error -f lavfi -i 'anoisesrc=color=pink:amplitude=0.25:sample_rate=44100:duration=3:seed=7' -c:a libmp3lame -b:a 32k -fflags +bitexact -map_metadata -1 pink-44k1-32k-mono.mp3
//	ffmpeg -nostdin -loglevel error -f lavfi -i 'anoisesrc=color=pink:amplitude=0.25:sample_rate=22050:duration=3:seed=7' -c:a libmp3lame -b:a 32k -fflags +bitexact -map_metadata -1 pink-22k05-32k-mono.mp3
//	ffmpeg -nostdin -loglevel error -f lavfi -i "aevalsrc='0.2*sin(2*PI*220*t)+0.1*sin(2*PI*3300*t)|0.15*sin(2*PI*330*t)+0.12*sin(2*PI*2500*t)':s=22050:d=3" -c:a libmp3lame -b:a 32k -fflags +bitexact -map_metadata -1 tones-22k05-32k-stereo.mp3
//
// 参考值是 ffmpeg 解码同一个文件得到的采样（单声道复制成左右声道）交给 AnalyzeSamples 的结果，采样用
// ffmpeg -nostdin -loglevel error -i 文件 -f f64le - 取得。ffmpeg n9.0.2 生成的文件与仓库中的逐字节相同；
// 其他版本生成的字节可能不同，参考值要按新文件重新计算。
//
// 22.05 kHz 的低码率 MP3 解码出错时，采样会削波，响度偏高几 dB，所以要核对响度和峰值，不能只看能否解码。
// 容差 0.05 dB：gl3d 与 ffmpeg 解码出的采样只差浮点误差，容差留给其他平台上乘加融合造成的差异。
// 时长正好是 3 秒：Info 帧里的 LAME 标签有效，gl3d 把编码器在两端加的延迟和填充裁掉了，这条断言也固定了裁剪。
func TestAnalyzeReaderMP3MatchesReference(t *testing.T) {
	tests := []struct {
		file       string
		integrated float64 // LUFS
		peak       float64 // dBFS
	}{
		{"pink-44k1-32k-mono.mp3", -24.964, -14.83},
		{"pink-22k05-32k-mono.mp3", -24.913, -14.64},
		{"tones-22k05-32k-stereo.mp3", -13.471, -10.54},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", tt.file))
			if err != nil {
				t.Fatal(err)
			}
			got, err := AnalyzeReader(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			if math.Abs(got.Integrated-tt.integrated) > 0.05 {
				t.Errorf("Integrated = %.3f LUFS，期望 %.3f", got.Integrated, tt.integrated)
			}
			if math.Abs(got.Peak-tt.peak) > 0.05 {
				t.Errorf("Peak = %.2f dBFS，期望 %.2f", got.Peak, tt.peak)
			}
			if got.Duration != 3*time.Second {
				t.Errorf("Duration = %v，期望 3 秒", got.Duration)
			}
		})
	}
}
