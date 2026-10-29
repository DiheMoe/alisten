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

var _ port.MediaLibrary = (*Store)(nil)

func (s *Store) ListStoredMedia(ctx context.Context) ([]port.StoredMedia, error) {
	rows, err := s.q.ListStoredMedia(ctx)
	if err != nil {
		return nil, fmt.Errorf("读取已存储的媒体：%w", err)
	}
	media := make([]port.StoredMedia, len(rows))
	for i, r := range rows {
		media[i] = storedMedia(query.StoredMediaRow(r))
	}
	return media, nil
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
		Size:         r.MediaSize,
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
