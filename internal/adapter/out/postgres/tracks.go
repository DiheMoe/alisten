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
	meta, result := preparedColumns{
		metaColumns: metaColumns{Title: row.Title, Artist: row.Artist, Album: row.Album, CoverURL: row.CoverURL, CoverKey: row.CoverKey, PageURL: row.PageURL, DurationMs: row.DurationMs},
		Gain:        row.Gain,
		Lyric:       row.Lyric,
		LyricWords:  row.LyricWords,
		LyricTrans:  row.LyricTrans,
	}.prepared()
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
		found[track.Track{Source: track.Source(r.Source), ID: r.TrackID}] = metaColumns{Title: r.Title, Artist: r.Artist, Album: r.Album, CoverURL: r.CoverURL, CoverKey: r.CoverKey, PageURL: r.PageURL, DurationMs: r.DurationMs}.meta()
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

// 曲目元数据在 tracks、room_entries、listens、playlist_items 四张表里都是这七列。读出的行都经过 meta 换算，
// 这样空值、对象键和时长的换算只写一处。
type metaColumns struct {
	Title      string
	Artist     *string
	Album      *string
	CoverURL   *string
	CoverKey   *string
	PageURL    string
	DurationMs int64
}

func (c metaColumns) meta() track.Meta {
	return track.Meta{
		Title:    c.Title,
		Artist:   orEmpty(c.Artist),
		Album:    orEmpty(c.Album),
		CoverURL: orEmpty(c.CoverURL),
		CoverKey: track.ObjectKey(orEmpty(c.CoverKey)),
		PageURL:  c.PageURL,
		Duration: time.Duration(c.DurationMs) * time.Millisecond,
	}
}

type preparedColumns struct {
	metaColumns
	Gain       float64
	Lyric      *string
	LyricWords *string
	LyricTrans *string
}

func (c preparedColumns) prepared() (track.Meta, track.Prepared) {
	meta := c.meta()
	return meta, track.Prepared{Duration: meta.Duration, Gain: c.Gain, Lyric: orEmpty(c.Lyric), LyricWords: orEmpty(c.LyricWords), LyricTrans: orEmpty(c.LyricTrans)}
}

func orZero(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}
