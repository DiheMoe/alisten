package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/dihemoe/alisten/internal/adapter/out/postgres/internal/query"
	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/track"
)

var _ port.MediaLibrary = (*Store)(nil)

var mediaSorts = map[port.MediaSort]string{port.MediaByStored: "stored", port.MediaByIdle: "idle", port.MediaBySize: "size"}

// 搜索的文字按字面匹配：LIKE 的通配符和转义符都要转义。
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

func (s *Store) ListStoredMedia(ctx context.Context, q port.MediaQuery) (port.MediaPage, error) {
	pattern := likeEscaper.Replace(q.Title)
	var page port.MediaPage
	// 总数和这一页在同一个快照里读，两者才对得上。
	err := pgx.BeginTxFunc(ctx, s.pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		qtx := s.q.WithTx(tx)
		count, err := qtx.CountStoredMedia(ctx, pattern)
		if err != nil {
			return err
		}
		page.Total, page.TotalBytes = int(count.Total), count.TotalBytes
		// 页码来自客户端，可能大到算偏移量时溢出；超出最后一页时也不必再查。
		if int64(q.Page-1) > count.Total/port.MediaPageSize {
			return nil
		}
		rows, err := qtx.ListStoredMedia(ctx, query.ListStoredMediaParams{
			Pattern:  pattern,
			Sort:     mediaSorts[q.Sort],
			Skip:     int64(q.Page-1) * port.MediaPageSize,
			PageSize: port.MediaPageSize,
		})
		if err != nil {
			return err
		}
		page.Items = make([]port.StoredMedia, len(rows))
		for i, r := range rows {
			page.Items[i] = storedMedia(query.StoredMediaRow(r))
		}
		return nil
	})
	if err != nil {
		return port.MediaPage{}, fmt.Errorf("读取已存储的媒体：%w", err)
	}
	return page, nil
}

func (s *Store) StoredMedia(ctx context.Context, key string) (port.StoredMedia, bool, error) {
	row, err := s.q.StoredMedia(ctx, key)
	if errors.Is(err, pgx.ErrNoRows) {
		return port.StoredMedia{}, false, nil
	}
	if err != nil {
		return port.StoredMedia{}, false, fmt.Errorf("读取已存储的媒体 %s：%w", key, err)
	}
	return storedMedia(row), true, nil
}

func (s *Store) DeleteStoredMedia(ctx context.Context, key string) error {
	if err := s.q.DeleteStoredMedia(ctx, key); err != nil {
		return fmt.Errorf("清除已存储的媒体 %s 的准备结果：%w", key, err)
	}
	return nil
}

func storedMedia(r query.StoredMediaRow) port.StoredMedia {
	return port.StoredMedia{
		Key:          r.MediaKey,
		Source:       track.Source(r.Source),
		Link:         r.TrackID,
		Title:        r.Title,
		Artist:       orEmpty(r.Artist),
		Size:         r.MediaSize,
		CoverURL:     orEmpty(r.CoverURL),
		CoverKey:     track.ObjectKey(orEmpty(r.CoverKey)),
		StoredAt:     r.PreparedAt.UTC(),
		LastPlayedAt: utc(r.LastPlayedAt),
	}
}

func utc(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}
