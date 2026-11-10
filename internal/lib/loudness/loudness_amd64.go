//go:build amd64

package loudness

import "golang.org/x/sys/cpu"

var hasAVX2 = cpu.X86.HasAVX2

// stereoProcessAVX2 处理双声道交错采样。每帧更新两个声道共用的峰值 peak，以及 L、R 声道各自的两级 K 加权 biquad 状态，
// 滤波后的能量按声道累加进 sums。samples 的长度必须为偶数，st 的内存布局见 loudness_amd64.s。
//
//go:noescape
func stereoProcessAVX2(samples []float64, st *[2][2]biquadState, sums *[2]float64, peak *float64)
