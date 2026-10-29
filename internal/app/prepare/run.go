package prepare

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/track"
	"github.com/dihemoe/alisten/internal/lib/loudness"
)

type Runner struct {
	sources map[track.Source]port.Source
	tracks  port.Tracks
	logger  *slog.Logger
}

func NewRunner(sources map[track.Source]port.Source, tracks port.Tracks, logger *slog.Logger) *Runner {
	return &Runner{sources: sources, tracks: tracks, logger: logger}
}

func (r *Runner) Run(ctx context.Context, t track.Track) error {
	source, ok := r.sources[t.Source]
	if !ok || t.ID == "" {
		return &port.RejectedError{Err: fmt.Errorf("不能准备来源为 %q、ID 为 %q 的歌", t.Source, t.ID)}
	}
	resolved, err := source.Resolve(ctx, t.ID)
	if err != nil {
		return err
	}
	result, err := analyze(ctx, r.logger, t, resolved.Audio, resolved.Meta.Duration, resolved.Lyric)
	_ = resolved.Audio.Close()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, storeTimeout)
	defer cancel()
	return r.tracks.SavePreparedTrack(ctx, t, port.PreparedTrack{Meta: resolved.Meta, Result: result})
}

func analyze(ctx context.Context, logger *slog.Logger, t track.Track, audio io.Reader, fallback time.Duration, lyric string) (track.Prepared, error) {
	src := &download{r: audio}
	result, err := loudness.AnalyzeReader(src)
	if src.err != nil {
		return track.Prepared{}, fmt.Errorf("下载音频：%w", src.err)
	}
	prepared := track.Prepared{Duration: fallback, Gain: 1, Lyric: lyric}
	if err == nil {
		prepared.Duration, prepared.Gain = result.Duration, result.Coefficient()
	} else {
		logger.WarnContext(ctx, "音频无法解码，增益取 1、时长取元数据", "source", t.Source, "track_id", t.ID, "error", err)
	}
	// 时长为 0 的歌会在开播时立刻结束。
	if prepared.Duration <= 0 {
		return track.Prepared{}, &port.RejectedError{Err: errors.New("这首歌的音频无法播放")}
	}
	return prepared, nil
}

// 记下读取音频时的错误，以区分下载中断和音频本身无法解码。
type download struct {
	r   io.Reader
	err error
}

func (d *download) Read(p []byte) (int, error) {
	n, err := d.r.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		d.err = err
	}
	return n, err
}
