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

var (
	_ port.Tracks      = (*Store)(nil)
	_ port.TrackLookup = (*Store)(nil)
)

func (s *Store) PreparedTrack(ctx context.Context, t track.Track) (port.PreparedTrack, bool, error) {
	row, err := s.q.PreparedTrack(ctx, query.PreparedTrackParams{Source: string(t.Source), TrackID: t.ID})
	if errors.Is(err, pgx.ErrNoRows) {
		return port.PreparedTrack{}, false, nil
	}
	if err != nil {
		return port.PreparedTrack{}, false, fmt.Errorf("读取曲目 %s %s 的准备结果：%w", t.Source, t.ID, err)
	}
	meta, result := prepared(row.Title, orEmpty(row.Artist), orEmpty(row.Album), orEmpty(row.CoverURL), orEmpty(row.CoverKey), row.PageURL, row.DurationMs, row.Gain, orEmpty(row.Lyric), orEmpty(row.LyricWords), orEmpty(row.LyricTrans))
	return port.PreparedTrack{Meta: meta, Result: result, MediaKey: orEmpty(row.MediaKey), MediaSize: orZero(row.MediaSize)}, true, nil
}

func (s *Store) TrackMetas(ctx context.Context, ts []track.Track) (map[track.Track]track.Meta, error) {
	sources, ids := splitTracks(ts)
	rows, err := s.q.TrackMetas(ctx, query.TrackMetasParams{Sources: sources, TrackIds: ids})
	if err != nil {
		return nil, fmt.Errorf("读取 %d 首歌的元数据：%w", len(ts), err)
	}
	found := make(map[track.Track]track.Meta, len(rows))
	for _, r := range rows {
		found[track.Track{Source: track.Source(r.Source), ID: r.TrackID}] = storedMeta(r.Title, orEmpty(r.Artist), orEmpty(r.Album), orEmpty(r.CoverURL), orEmpty(r.CoverKey), r.PageURL, r.DurationMs)
	}
	return found, nil
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
		LyricWords: nullable(p.Result.LyricWords),
		LyricTrans: nullable(p.Result.LyricTrans),
	}
	if p.MediaKey != "" {
		params.MediaKey, params.MediaSize = &p.MediaKey, &p.MediaSize
	}
	if err := s.q.SavePreparedTrack(ctx, params); err != nil {
		return fmt.Errorf("保存曲目 %s %s 的准备结果：%w", t.Source, t.ID, err)
	}
	return nil
}

func prepared(title, artist, album, coverURL, coverKey, pageURL string, durationMs int64, gain float64, lyric, lyricWords, lyricTrans string) (track.Meta, track.Prepared) {
	meta := storedMeta(title, artist, album, coverURL, coverKey, pageURL, durationMs)
	return meta, track.Prepared{Duration: meta.Duration, Gain: gain, Lyric: lyric, LyricWords: lyricWords, LyricTrans: lyricTrans}
}

func storedMeta(title, artist, album, coverURL, coverKey, pageURL string, durationMs int64) track.Meta {
	return track.Meta{Title: title, Artist: artist, Album: album, CoverURL: coverURL, CoverKey: track.ObjectKey(coverKey), PageURL: pageURL, Duration: time.Duration(durationMs) * time.Millisecond}
}

func orZero(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}
