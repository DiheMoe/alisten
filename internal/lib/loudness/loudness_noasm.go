//go:build !amd64

package loudness

const hasAVX2 = false

func stereoProcessAVX2(samples []float64, st *[2][2]biquadState, sums *[2]float64, peak *float64) {
	panic("loudness: AVX2 内核仅在 amd64 可用")
}
