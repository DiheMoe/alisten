// Package loudness 实现 ITU-R BS.1770（EBU R128）响度分析，
// 并按 Spotify 播放器的响度归一化策略给出平衡增益（ReplayGain 风格）。
package loudness

import (
	"errors"
	"io"
	"math"
	"time"

	"github.com/dihemoe/alisten/internal/lib/wav"
)

// SpotifyTarget 是 Spotify 的 Normal 档目标响度（LUFS）。
const SpotifyTarget = -14.0

// peakCeiling 是提升音量后允许的采样峰值上限（dBFS），用来避免削波。
const peakCeiling = -1.0

type Result struct {
	Integrated float64 // 综合响度（LUFS），全静音时为 -Inf
	Peak       float64 // 采样峰值（dBFS），全静音时为 -Inf
	Duration   time.Duration
}

// target 的单位是 LUFS，返回的增益单位是 dB；静音时返回 0。
func (r *Result) GainTo(target float64) float64 {
	if math.IsInf(r.Integrated, -1) {
		return 0
	}
	return target - r.Integrated
}

func (r *Result) SpotifyGain() float64 {
	g := r.GainTo(SpotifyTarget)
	if g > 0 && !math.IsInf(r.Peak, -1) && r.Peak+g > peakCeiling {
		g = peakCeiling - r.Peak
	}
	return g
}

func (r *Result) Coefficient() float64 {
	return GainToCoefficient(r.SpotifyGain())
}

func GainToCoefficient(gainDb float64) float64 {
	return math.Pow(10, gainDb/20)
}

func AnalyzeReader(r io.Reader) (*Result, error) {
	dec, err := wav.NewDecoder(r)
	if err != nil {
		return nil, err
	}
	an := NewAnalyzer(dec.SampleRate(), dec.Channels())
	buf := make([]float64, 32768)
	for {
		n, err := dec.Read(buf)
		an.Write(buf[:n])
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	return an.Result()
}

// samples 是按声道交错的 PCM 采样，满刻度为 1，可以超出 [-1, 1]。
func AnalyzeSamples(samples []float64, sampleRate, channels int) (*Result, error) {
	if sampleRate <= 0 || channels <= 0 {
		return nil, errors.New("loudness: 采样率与声道数必须为正")
	}
	an := NewAnalyzer(sampleRate, channels)
	an.Write(samples[:len(samples)/channels*channels])
	return an.Result()
}

// blockSegs 是一个 400ms 响度块包含的 100ms 段数（BS.1770：400ms 块，75% 重叠）。
const blockSegs = 4

// Analyzer 是流式的 BS.1770 响度分析器：只保留滤波器状态与滚动的块能量，整段音频无需驻留内存。
type Analyzer struct {
	sampleRate int
	channels   int
	// 浏览器把单声道同时放到左右两个声道，所以单声道按双单声道（libebur128 的 DUAL_MONO）计算：
	// 能量是单声道的 2 倍（+3.01 LU），峰值不变。其余声道布局的权重为 1。
	weight     float64
	segSize    int // 100ms 段的帧数
	filters    [][2]biquadState
	segSums    []float64
	totalSums  []float64 // 全部采样的各声道能量（不足 400ms 时兜底）
	window     [blockSegs][]float64
	wpos, wlen int // 环形窗口写入位置与已填充段数
	segFrames  int
	nextCh     int       // 下一个采样所属声道（跨 Write 调用保持帧对齐）
	energies   []float64 // 每个完整 400ms 块的能量
	peak       float64   // 采样峰值（未经过 K 加权滤波）
	frames     int64

	simdOK bool
}

func NewAnalyzer(sampleRate, channels int) *Analyzer {
	segSize := int(0.100 * float64(sampleRate))
	if segSize < 1 {
		segSize = 1
	}
	a := &Analyzer{
		sampleRate: sampleRate,
		channels:   channels,
		weight:     1,
		segSize:    segSize,
		filters:    make([][2]biquadState, channels),
		segSums:    make([]float64, channels),
		totalSums:  make([]float64, channels),
		simdOK:     channels == 2 && hasAVX2,
	}
	if channels == 1 {
		a.weight = 2
	}
	for c := 0; c < channels; c++ {
		shelf, highpass := kWeightingFilters(sampleRate)
		a.filters[c] = [2]biquadState{{b: shelf}, {b: highpass}}
	}
	for i := range a.window {
		a.window[i] = make([]float64, channels)
	}
	return a
}

// Write 写入一块按声道交错的 PCM 采样，块长度无需按声道或段边界对齐。
func (a *Analyzer) Write(samples []float64) {
	if a.simdOK {
		a.writeStereoSIMD(samples)
		return
	}
	for _, v := range samples {
		a.stepScalar(v)
	}
}

// writeStereoSIMD 逐段调用汇编内核：IIR 在时间维上必须串行，并行的是 L、R 两个声道。
func (a *Analyzer) writeStereoSIMD(samples []float64) {
	for len(samples) > 0 {
		if a.nextCh == 1 || len(samples) < 2 {
			// 处在奇数边界时，用标量方式处理一个采样，以保持帧对齐。
			a.stepScalar(samples[0])
			samples = samples[1:]
			continue
		}
		n := a.segSize - a.segFrames
		if rem := len(samples) / 2; rem < n {
			n = rem
		}
		before0, before1 := a.segSums[0], a.segSums[1]
		stereoProcessAVX2(samples[:2*n], (*[2][2]biquadState)(a.filters), (*[2]float64)(a.segSums), &a.peak)
		a.totalSums[0] += a.segSums[0] - before0
		a.totalSums[1] += a.segSums[1] - before1
		a.frames += int64(n)
		a.segFrames += n
		samples = samples[2*n:]
		if a.segFrames == a.segSize {
			a.flushSegment()
		}
	}
}

func (a *Analyzer) stepScalar(v float64) {
	if av := math.Abs(v); av > a.peak {
		a.peak = av
	}
	c := a.nextCh
	f := &a.filters[c]
	y := f[1].step(f[0].step(v))
	e := y * y
	a.segSums[c] += e
	a.totalSums[c] += e
	if c++; c == a.channels {
		c = 0
		a.frames++
		if a.segFrames++; a.segFrames == a.segSize {
			a.flushSegment()
		}
	}
	a.nextCh = c
}

func (a *Analyzer) flushSegment() {
	w := a.window[a.wpos]
	copy(w, a.segSums)
	a.wpos = (a.wpos + 1) % blockSegs
	if a.wlen < blockSegs {
		a.wlen++
	}
	clear(a.segSums)
	a.segFrames = 0

	if a.wlen == blockSegs {
		e := 0.0
		for i := 0; i < blockSegs; i++ {
			for _, s := range a.window[i] {
				e += s
			}
		}
		a.energies = append(a.energies, a.weight*e/float64(blockSegs*a.segSize))
	}
}

func (a *Analyzer) Result() (*Result, error) {
	if a.frames == 0 {
		return nil, errors.New("loudness: 没有音频采样")
	}

	energies := a.energies
	if len(energies) == 0 {
		e := 0.0
		for _, s := range a.totalSums {
			e += s
		}
		energies = []float64{a.weight * e / float64(a.frames)}
	}

	integrated := math.Inf(-1)
	if e, ok := gateBlocks(energies); ok {
		integrated = energyToLoudness(e)
	}

	peakDb := math.Inf(-1)
	if a.peak > 0 {
		peakDb = 20 * math.Log10(a.peak)
	}

	return &Result{
		Integrated: integrated,
		Peak:       peakDb,
		Duration:   time.Duration(a.frames) * time.Second / time.Duration(a.sampleRate),
	}, nil
}

// energyToLoudness 返回 LUFS，包含 BS.1770 的 -0.691 修正项。
func energyToLoudness(e float64) float64 {
	return -0.691 + 10*math.Log10(e)
}

func gateBlocks(energies []float64) (float64, bool) {
	// BS.1770 的 -70 LUFS 绝对门限直接换算到能量域比较，省去逐块的 LUFS 对数换算。
	absGate := math.Pow(10, (-70+0.691)/10)
	sum, n := sumAbove(energies, absGate)
	if n == 0 {
		return 0, false
	}
	// 相对门限是均值响度以下 10 LU，即均值能量的 1/10；与绝对门限取大，保证两级门限同时生效。
	sum, n = sumAbove(energies, max(absGate, sum/float64(n)/10))
	if n == 0 {
		return 0, false
	}
	return sum / float64(n), true
}

func sumAbove(energies []float64, threshold float64) (float64, int) {
	sum, n := 0.0, 0
	for _, e := range energies {
		if e > threshold {
			sum += e
			n++
		}
	}
	return sum, n
}

type biquad struct {
	b0, b1, b2, a1, a2 float64
}

type biquadState struct {
	b              biquad
	x1, x2, y1, y2 float64
}

func (s *biquadState) step(v float64) float64 {
	y := s.b.b0*v + s.b.b1*s.x1 + s.b.b2*s.x2 - s.b.a1*s.y1 - s.b.a2*s.y2
	s.x2, s.x1 = s.x1, v
	s.y2, s.y1 = s.y1, y
	return y
}

// kWeightingFilters 的系数取自 De Man 对 BS.1770 系数的推导。
func kWeightingFilters(sampleRate int) (shelf, highpass biquad) {
	fs := float64(sampleRate)

	{
		const (
			f0 = 1681.974450955533
			G  = 3.999843853973347
			Q  = 0.7071752369554196
		)
		K := math.Tan(math.Pi * f0 / fs)
		Vh := math.Pow(10, G/20)
		Vb := math.Pow(Vh, 0.499666774155)
		a0 := 1 + K/Q + K*K
		shelf = biquad{
			b0: (Vh + Vb*K/Q + K*K) / a0,
			b1: 2 * (K*K - Vh) / a0,
			b2: (Vh - Vb*K/Q + K*K) / a0,
			a1: 2 * (K*K - 1) / a0,
			a2: (1 - K/Q + K*K) / a0,
		}
	}

	{
		const (
			f0 = 38.13547087602444
			Q  = 0.5003270373238773
		)
		K := math.Tan(math.Pi * f0 / fs)
		a0 := 1 + K/Q + K*K
		highpass = biquad{
			b0: 1 / a0,
			b1: -2 / a0,
			b2: 1 / a0,
			a1: 2 * (K*K - 1) / a0,
			a2: (1 - K/Q + K*K) / a0,
		}
	}
	return shelf, highpass
}
