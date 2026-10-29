package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/dihemoe/alisten/internal/adapter/out/postgres/internal/query"
	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/track"
)

var _ port.Tracks = (*Store)(nil)

func (s *Store) PreparedTrack(ctx context.Context, t track.Track) (port.PreparedTrack, bool, error) {
	row, err := s.q.PreparedTrack(ctx, query.PreparedTrackParams{Source: string(t.Source), TrackID: t.ID})
	if errors.Is(err, pgx.ErrNoRows) {
		return port.PreparedTrack{}, false, nil
	}
	if err != nil {
		return port.PreparedTrack{}, false, fmt.Errorf("读取曲目 %s %s 的准备结果：%w", t.Source, t.ID, err)
	}
	meta, result := prepared(row.Title, orEmpty(row.Artist), orEmpty(row.Album), orEmpty(row.CoverURL), orEmpty(row.CoverKey), row.PageURL, row.DurationMs, row.Gain, orEmpty(row.Lyric))
	return port.PreparedTrack{Meta: meta, Result: result, MediaKey: orEmpty(row.MediaKey), MediaSize: orZero(row.MediaSize)}, true, nil
}

func (s *Store) SavePreparedTrack(ctx context.Context, t track.Track, p port.PreparedTrack) error {
	params := query.SavePreparedTrackParams{
		Source:     string(t.Source),
		TrackID:    t.ID,
		Title:      p.Meta.Title,
		Artist:     nullable(p.Meta.Artist),
		Album:      nullable(p.Meta.Album),
		CoverURL:   nullable(p.Meta.CoverURL),
		CoverKey:   nullable(string(p.Meta.CoverKey)),
		PageURL:    p.Meta.PageURL,
		DurationMs: p.Result.Duration.Milliseconds(),
		Gain:       p.Result.Gain,
		Lyric:      nullable(p.Result.Lyric),
	}
	if p.MediaKey != "" {
		params.MediaKey, params.MediaSize = &p.MediaKey, &p.MediaSize
	}
	if err := s.q.SavePreparedTrack(ctx, params); err != nil {
		return fmt.Errorf("保存曲目 %s %s 的准备结果：%w", t.Source, t.ID, err)
	}
	return nil
}

func prepared(title, artist, album, coverURL, coverKey, pageURL string, durationMs int64, gain float64, lyric string) (track.Meta, track.Prepared) {
	d := time.Duration(durationMs) * time.Millisecond
	return track.Meta{Title: title, Artist: artist, Album: album, CoverURL: coverURL, CoverKey: track.ObjectKey(coverKey), PageURL: pageURL, Duration: d},
		track.Prepared{Duration: d, Gain: gain, Lyric: lyric}
}

func orZero(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}
