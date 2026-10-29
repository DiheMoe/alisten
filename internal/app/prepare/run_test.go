package prepare

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/app/port/porttest"
	"github.com/dihemoe/alisten/internal/domain/track"
	"github.com/dihemoe/alisten/internal/lib/loudness"
)

var meta = track.Meta{Title: "晴天", Artist: "周杰伦", PageURL: "https://music.163.com/#/song?id=186016", Duration: 269 * time.Second}

func run(t *testing.T, source port.Source, tr track.Track) (*tracksTable, error) {
	t.Helper()
	tracks, _, err := runLogged(t, source, tr)
	return tracks, err
}

func runLogged(t *testing.T, source port.Source, tr track.Track) (*tracksTable, *bytes.Buffer, error) {
	t.Helper()
	tracks, logs := newTracks(), &bytes.Buffer{}
	err := NewRunner(map[track.Source]port.Source{track.NetEase: source}, tracks, slog.New(slog.NewJSONHandler(logs, nil))).Run(t.Context(), tr)
	return tracks, logs, err
}

// 128 kbps、44.1 kHz 的静音帧：417 字节，解码出 1152 个采样帧。
var silentFrame = append([]byte{0xFF, 0xFB, 0x90, 0x00}, make([]byte, 413)...)

var truncatedMP3 = append(bytes.Repeat(silentFrame, 3), silentFrame[:200]...)

func truncatedMP3Error(t *testing.T) error {
	t.Helper()
	_, err := loudness.AnalyzeReader(bytes.NewReader(truncatedMP3))
	if err == nil {
		t.Fatal("被截断的 MP3 应该无法解码")
	}
	return err
}

func checkDecodeWarning(t *testing.T, logs *bytes.Buffer, tr track.Track, decodeErr error) {
	t.Helper()
	var warnings []map[string]any
	for dec := json.NewDecoder(bytes.NewReader(logs.Bytes())); dec.More(); {
		var entry map[string]any
		if err := dec.Decode(&entry); err != nil {
			t.Fatal(err)
		}
		if entry["level"] == "WARN" {
			warnings = append(warnings, entry)
		}
	}
	if len(warnings) != 1 {
		t.Fatalf("日志中有 %d 条 WARN，期望 1 条：\n%s", len(warnings), logs)
	}
	want := map[string]any{"source": string(tr.Source), "track_id": tr.ID, "error": decodeErr.Error()}
	for key, value := range want {
		if warnings[0][key] != value {
			t.Errorf("WARN 的 %s = %v，期望 %v", key, warnings[0][key], value)
		}
	}
}

func TestRunFallsBackWhenAudioCannotBeAnalyzed(t *testing.T) {
	source := porttest.NewSource(track.NetEase)
	source.Add("186016", meta, "", []byte("不是音频"))
	tracks, err := run(t, source, sunny)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := tracks.row(sunny); got.Result != (track.Prepared{Duration: meta.Duration, Gain: 1}) {
		t.Errorf("无法解码时的结果 = %+v，期望增益为 1、时长取元数据中的时长", got.Result)
	}

	source = porttest.NewSource(track.NetEase)
	source.Add("186016", track.Meta{Title: "晴天"}, "", []byte("不是音频"))
	var rejected *port.RejectedError
	if _, err := run(t, source, sunny); !errors.As(err, &rejected) {
		t.Errorf("元数据中也没有时长时的错误 = %v，期望不能准备", err)
	}
}

func TestRunLogsAudioThatCannotBeDecoded(t *testing.T) {
	decodeErr := truncatedMP3Error(t)
	sourceWith := func(m track.Meta, audio []byte) port.Source {
		source := porttest.NewSource(track.NetEase)
		source.Add("186016", m, "", audio)
		return source
	}

	t.Run("回退", func(t *testing.T) {
		_, logs, err := runLogged(t, sourceWith(meta, truncatedMP3), sunny)
		if err != nil {
			t.Fatal(err)
		}
		checkDecodeWarning(t, logs, sunny, decodeErr)
	})
	t.Run("元数据中也没有时长", func(t *testing.T) {
		_, _, err := runLogged(t, sourceWith(track.Meta{Title: "晴天"}, truncatedMP3), sunny)
		var rejected *port.RejectedError
		if !errors.As(err, &rejected) || err.Error() != "这首歌的音频无法播放" {
			t.Errorf("错误 = %v，期望不能准备，原因带着解码的错误 %q", err, decodeErr)
		}
	})
	t.Run("下载中断", func(t *testing.T) {
		if _, logs, _ := runLogged(t, brokenSource{}, sunny); logs.Len() != 0 {
			t.Errorf("日志 = %q，期望没有日志：这不是音频无法解码", logs)
		}
	})
}

type brokenSource struct{ port.Source }

type brokenAudio struct{ io.Reader }

func (brokenAudio) Close() error { return nil }

func (brokenSource) Resolve(context.Context, string) (port.Resolved, error) {
	audio := io.MultiReader(strings.NewReader("RIFF"), failingReader{})
	return port.Resolved{Meta: meta, Audio: brokenAudio{audio}}, nil
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("连接被重置") }

func TestRunRetriesInterruptedDownload(t *testing.T) {
	tracks, err := run(t, brokenSource{}, sunny)
	var rejected *port.RejectedError
	if err == nil || errors.As(err, &rejected) {
		t.Errorf("下载中断时的错误 = %v，期望可以重试的错误", err)
	}
	if _, ok := tracks.row(sunny); ok {
		t.Error("下载中断时不应写入曲目表")
	}
}

func TestRunRejects(t *testing.T) {
	source := porttest.NewSource(track.NetEase)
	source.Add("186016", meta, "", porttest.SilentWAV(time.Second))
	source.Reject("186016", "网易云没有提供这首歌的播放地址")
	var rejected *port.RejectedError
	if _, err := run(t, source, sunny); !errors.As(err, &rejected) || err.Error() != "网易云没有提供这首歌的播放地址" {
		t.Errorf("音源拒绝时的错误 = %v", err)
	}
	for _, tr := range []track.Track{{Source: track.Link, ID: "https://www.youtube.com/watch?v=x"}, {Source: track.NetEase}} {
		if _, err := run(t, source, tr); !errors.As(err, &rejected) {
			t.Errorf("任务参数 %v 的错误 = %v，期望不能准备", tr, err)
		}
	}
}
