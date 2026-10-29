package loudness

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/dihemoe/alisten/internal/lib/wav"
)

func pcm16WAV(samples []float64, sampleRate, channels int) ([]byte, []float64) {
	decoded := make([]float64, len(samples))
	data := make([]byte, 0, len(samples)*2)
	for i, v := range samples {
		s := int16(math.Round(v * 32767))
		decoded[i] = float64(s) / 32768
		data = binary.LittleEndian.AppendUint16(data, uint16(s))
	}
	b := []byte("RIFF")
	b = binary.LittleEndian.AppendUint32(b, uint32(36+len(data)))
	b = append(b, "WAVEfmt "...)
	b = binary.LittleEndian.AppendUint32(b, 16)
	b = binary.LittleEndian.AppendUint16(b, 1)
	b = binary.LittleEndian.AppendUint16(b, uint16(channels))
	b = binary.LittleEndian.AppendUint32(b, uint32(sampleRate))
	b = binary.LittleEndian.AppendUint32(b, uint32(sampleRate*channels*2))
	b = binary.LittleEndian.AppendUint16(b, uint16(channels*2))
	b = binary.LittleEndian.AppendUint16(b, 16)
	b = append(b, "data"...)
	b = binary.LittleEndian.AppendUint32(b, uint32(len(data)))
	return append(b, data...), decoded
}

func TestAnalyzeReaderMatchesSamples(t *testing.T) {
	const fs = 48000
	file, decoded := pcm16WAV(sine(fs, 997, 0.25, 3), fs, 2)
	want, err := AnalyzeSamples(decoded, fs, 2)
	if err != nil {
		t.Fatal(err)
	}
	got, err := AnalyzeReader(bytes.NewReader(file))
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(got.Integrated-want.Integrated) > 1e-9 || math.Abs(got.Peak-want.Peak) > 1e-9 || got.Duration != want.Duration {
		t.Errorf("AnalyzeReader = %+v，期望与 AnalyzeSamples 相同：%+v", *got, *want)
	}
}

func wavTestdata(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "wav", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestAnalyzeReaderMonoMatchesDualMono(t *testing.T) {
	const fs = 48000
	wavFile, _ := pcm16WAV(monoOf(sine(fs, 997, 0.25, 3)), fs, 1)
	for _, tt := range []struct {
		name string
		file []byte
	}{
		{"WAV", wavFile},
		{"FLAC", wavTestdata(t, "sine.flac")},
		{"MP3", wavTestdata(t, "sine-mpeg1.mp3")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a, err := wav.Decode(bytes.NewReader(tt.file))
			if err != nil {
				t.Fatal(err)
			}
			if a.Channels != 1 {
				t.Fatalf("解码为 %d 声道，期望单声道", a.Channels)
			}
			dual := make([]float64, 0, 2*len(a.Samples))
			for _, v := range a.Samples {
				dual = append(dual, v, v)
			}
			want, err := AnalyzeSamples(dual, a.SampleRate, 2)
			if err != nil {
				t.Fatal(err)
			}
			got, err := AnalyzeReader(bytes.NewReader(tt.file))
			if err != nil {
				t.Fatal(err)
			}
			if math.Abs(got.Integrated-want.Integrated) > 1e-9 || got.Peak != want.Peak || got.Duration != want.Duration {
				t.Errorf("AnalyzeReader = %+v，期望与双声道相同：%+v", *got, *want)
			}
		})
	}
}
