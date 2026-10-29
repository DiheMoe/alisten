package loudness

import (
	"fmt"
	"math"
	"math/rand"
	"testing"
)

func TestGateBlocks(t *testing.T) {
	absGate := math.Pow(10, (-70+0.691)/10)
	for _, tc := range []struct {
		name     string
		energies []float64
		want     float64
		ok       bool
	}{
		{"absolute boundary", []float64{absGate / 2, absGate}, 0, false},
		{"above absolute", []float64{absGate / 2, absGate * 2}, absGate * 2, true},
		{"relative gate", []float64{0, 0.001, 1, 1}, 1, true},
		{"relative boundary", []float64{1, 19}, 19, true},
		// 相对门限比绝对门限低时，不能把绝对门限以下的块重新算入。
		{"retain absolute gate", []float64{absGate / 2, absGate * 2, absGate * 3}, absGate * 2.5, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := gateBlocks(tc.energies)
			if ok != tc.ok || math.Abs(got-tc.want) > 1e-14*math.Abs(tc.want) {
				t.Fatalf("gateBlocks = (%g, %v), want (%g, %v)", got, ok, tc.want, tc.ok)
			}
		})
	}
}

func sine(sampleRate int, freq, amp, seconds float64) []float64 {
	frames := int(float64(sampleRate) * seconds)
	samples := make([]float64, frames*2)
	for f := 0; f < frames; f++ {
		v := amp * math.Sin(2*math.Pi*freq*float64(f)/float64(sampleRate))
		samples[f*2] = v
		samples[f*2+1] = v
	}
	return samples
}

func TestAnalyzeSamplesSine(t *testing.T) {
	const fs = 48000
	// 997 Hz、振幅 0.25：原始 RMS 对应约 -12.73 LUFS，
	// K 加权在 1 kHz 附近约有 +0.6 dB 增益，预期约 -12.1 LUFS。
	res, err := AnalyzeSamples(sine(fs, 997, 0.25, 3), fs, 2)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(res.Integrated-(-12.1)) > 1.0 {
		t.Errorf("Integrated = %.2f LUFS, 预期约 -12.1", res.Integrated)
	}
	if math.Abs(res.Peak-(-12.04)) > 0.5 {
		t.Errorf("Peak = %.2f dBFS, 预期约 -12.04", res.Peak)
	}
	if res.Duration < 2900*1e6 || res.Duration > 3100*1e6 {
		t.Errorf("Duration = %v, 预期约 3s", res.Duration)
	}
}

func monoOf(stereo []float64) []float64 {
	mono := make([]float64, len(stereo)/2)
	for i := range mono {
		mono[i] = stereo[2*i]
	}
	return mono
}

func TestAnalyzeSamplesMonoIsDualMono(t *testing.T) {
	const fs = 48000
	for _, tt := range []struct {
		name    string
		seconds float64
	}{
		{"多个 400ms 块", 3},
		{"不足 400ms", 0.3},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dual := sine(fs, 997, 0.25, tt.seconds)
			want, err := AnalyzeSamples(dual, fs, 2)
			if err != nil {
				t.Fatal(err)
			}
			got, err := AnalyzeSamples(monoOf(dual), fs, 1)
			if err != nil {
				t.Fatal(err)
			}
			if math.Abs(got.Integrated-want.Integrated) > 1e-9 || got.Peak != want.Peak || got.Duration != want.Duration {
				t.Errorf("单声道 = %+v，期望与双声道相同：%+v", *got, *want)
			}
			// 振幅 0.25 的正弦波每个声道的均方是 0.03125，左右声道之和 0.0625 即 -12.04 dB；997 Hz 处 K 加权的增益与 -0.691 的修正基本抵消。
			if math.Abs(got.Integrated-(-12.04)) > 0.1 {
				t.Errorf("Integrated = %.2f LUFS，期望约 -12.04", got.Integrated)
			}
		})
	}
}

// 单声道的权重在门控之前计入，与 libebur128 的 DUAL_MONO 一致。997 Hz 的正弦波振幅为 a 时，按单声道算约 20·log10(a) - 3.01 LUFS，
// 振幅 3.55e-4 的安静段是 -72 LUFS，低于 -70 LUFS 的绝对门限；按双单声道算是 -69 LUFS，要参与综合响度。
// 权重放到门控之后，这一段会被绝对门限丢掉，结果与复制成左右声道的立体声不同。
func TestAnalyzeSamplesMonoWeightsBeforeGating(t *testing.T) {
	const fs, quiet = 48000, 3.55e-4
	for _, tt := range []struct {
		name string
		dual []float64
		want float64 // LUFS
	}{
		// 安静段（-69 LUFS）之后是响 6 dB 的一段（-63 LUFS），两段等长：能量平均约 -65 LUFS，安静段在相对门限之上。
		{"安静段和较响的一段", append(sine(fs, 997, quiet, 2), sine(fs, 997, 2*quiet, 2)...), -65},
		{"不足 400ms 的安静段", sine(fs, 997, quiet, 0.3), -69},
	} {
		t.Run(tt.name, func(t *testing.T) {
			want, err := AnalyzeSamples(tt.dual, fs, 2)
			if err != nil {
				t.Fatal(err)
			}
			got, err := AnalyzeSamples(monoOf(tt.dual), fs, 1)
			if err != nil {
				t.Fatal(err)
			}
			if math.Abs(got.Integrated-want.Integrated) > 1e-9 {
				t.Errorf("单声道 Integrated = %.3f LUFS，期望与双声道相同：%.3f", got.Integrated, want.Integrated)
			}
			// 两边都是 -Inf 时上一条比较不会失败（结果是 NaN），所以还要核对绝对值。
			if math.Abs(got.Integrated-tt.want) > 0.1 {
				t.Errorf("Integrated = %.3f LUFS，期望约 %v", got.Integrated, tt.want)
			}
		})
	}
}

func interleave(mono []float64, gains ...float64) []float64 {
	out := make([]float64, 0, len(mono)*len(gains))
	for _, v := range mono {
		for _, g := range gains {
			out = append(out, g*v)
		}
	}
	return out
}

func TestAnalyzeSamplesMultichannelAddsChannelEnergies(t *testing.T) {
	const fs, ref = 48000, 0.25
	tone := monoOf(sine(fs, 997, 1, 3))
	stereo, err := AnalyzeSamples(interleave(tone, ref, ref), fs, 2)
	if err != nil {
		t.Fatal(err)
	}
	for _, gains := range [][]float64{
		{0.05, 0.1, 0.15, 0.2, 0.25, 0.3},
	} {
		t.Run(fmt.Sprintf("%d 声道", len(gains)), func(t *testing.T) {
			got, err := AnalyzeSamples(interleave(tone, gains...), fs, len(gains))
			if err != nil {
				t.Fatal(err)
			}
			squares := 0.0
			for _, g := range gains {
				squares += g * g
			}
			want := stereo.Integrated + 10*math.Log10(squares/(2*ref*ref))
			if math.Abs(got.Integrated-want) > 1e-6 {
				t.Errorf("Integrated = %.4f LUFS，期望 %.4f", got.Integrated, want)
			}
		})
	}
}

func TestAnalyzeSamplesSilence(t *testing.T) {
	res, err := AnalyzeSamples(make([]float64, 48000*2), 48000, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !math.IsInf(res.Integrated, -1) {
		t.Errorf("静音 Integrated = %v, 预期 -Inf", res.Integrated)
	}
	if g := res.SpotifyGain(); g != 0 {
		t.Errorf("静音 SpotifyGain = %v, 预期 0", g)
	}
}

func TestAnalyzeSamplesInvalid(t *testing.T) {
	if _, err := AnalyzeSamples(nil, 48000, 2); err == nil {
		t.Error("空采样应返回错误")
	}
	if _, err := AnalyzeSamples([]float64{0}, 0, 2); err == nil {
		t.Error("非法采样率应返回错误")
	}
}

func TestSpotifyGain(t *testing.T) {
	cases := []struct {
		name string
		r    Result
		want float64
	}{
		{"正常提升", Result{Integrated: -20, Peak: -10}, 6},
		// 响度 -20 LUFS、峰值 -2 dBFS：增益 +6 dB 会使峰值达到 +4 dBFS，被限制到 -1 - (-2) = +1 dB。
		{"峰值限制", Result{Integrated: -20, Peak: -2}, 1},
		{"需要降低", Result{Integrated: -10, Peak: -6}, -4},
	}
	for _, c := range cases {
		if got := c.r.SpotifyGain(); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("%s: SpotifyGain = %v, 预期 %v", c.name, got, c.want)
		}
	}
}

func TestCoefficient(t *testing.T) {
	// -14 - (-20) = +6 dB，系数约 1.995。
	r := &Result{Integrated: -20, Peak: -10}
	if c := r.Coefficient(); math.Abs(c-1.9953) > 0.001 {
		t.Errorf("Coefficient = %v, 预期约 1.995", c)
	}
	// 响度 -8 LUFS 高于目标：-14 - (-8) = -6 dB，系数约 0.501。
	r = &Result{Integrated: -8, Peak: -3}
	if c := r.Coefficient(); math.Abs(c-0.5012) > 0.001 {
		t.Errorf("Coefficient = %v, 预期约 0.501", c)
	}
	r = &Result{Integrated: math.Inf(-1), Peak: math.Inf(-1)}
	if c := r.Coefficient(); c != 1 {
		t.Errorf("静音 Coefficient = %v, 预期 1", c)
	}
}

func TestAnalyzerChunkedParity(t *testing.T) {
	const fs = 44100
	samples := sine(fs, 997, 0.25, 3)

	whole, err := AnalyzeSamples(samples, fs, 2)
	if err != nil {
		t.Fatal(err)
	}

	// 以不规整的小块写入（奇数长度，跨越声道和段的边界）。
	an := NewAnalyzer(fs, 2)
	for off := 0; off < len(samples); {
		n := min(997, len(samples)-off)
		an.Write(samples[off : off+n])
		off += n
	}
	chunked, err := an.Result()
	if err != nil {
		t.Fatal(err)
	}

	if math.Abs(chunked.Integrated-whole.Integrated) > 1e-6 {
		t.Errorf("流式 Integrated = %v, 整体 = %v", chunked.Integrated, whole.Integrated)
	}
	if chunked.Peak != whole.Peak {
		t.Errorf("流式 Peak = %v, 整体 = %v", chunked.Peak, whole.Peak)
	}
	if chunked.Duration != whole.Duration {
		t.Errorf("流式 Duration = %v, 整体 = %v", chunked.Duration, whole.Duration)
	}
}

func TestAVX2ScalarParity(t *testing.T) {
	if !hasAVX2 {
		t.Skip("CPU 不支持 AVX2")
	}
	const fs = 48000
	// 伪随机采样加静音段，覆盖门控与滤波器状态。
	rng := rand.New(rand.NewSource(42))
	samples := make([]float64, fs*5*2)
	for i := range samples {
		if i/(fs/5)%2 == 0 {
			samples[i] = rng.Float64()*2 - 1
		}
	}

	run := func(simd bool) *Result {
		an := NewAnalyzer(fs, 2)
		an.simdOK = simd
		for off := 0; off < len(samples); {
			n := min(999, len(samples)-off) // 奇数长度的块，触发标量边界处理
			an.Write(samples[off : off+n])
			off += n
		}
		res, err := an.Result()
		if err != nil {
			t.Fatal(err)
		}
		return res
	}

	want := run(false)
	got := run(true)
	if diff := math.Abs(got.Integrated - want.Integrated); diff > 1e-6 {
		t.Errorf("Integrated 偏差 = %v (AVX2 %v vs 标量 %v)", diff, got.Integrated, want.Integrated)
	}
	if got.Peak != want.Peak {
		t.Errorf("Peak: AVX2 %v vs 标量 %v", got.Peak, want.Peak)
	}
	if got.Duration != want.Duration {
		t.Errorf("Duration: AVX2 %v vs 标量 %v", got.Duration, want.Duration)
	}
}
