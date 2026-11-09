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

var _ port.Playlists = (*Store)(nil)

const favoritesKind = "favorites"

func (s *Store) MemberPlaylists(ctx context.Context, ownerID string) ([]port.PlaylistRecord, error) {
	rows, err := s.q.MemberPlaylists(ctx, ownerID)
	if err != nil {
		return nil, fmt.Errorf("读取成员 %s 的歌单：%w", ownerID, err)
	}
	coverRows, err := s.q.MemberPlaylistCovers(ctx, ownerID)
	if err != nil {
		return nil, fmt.Errorf("读取成员 %s 的歌单封面：%w", ownerID, err)
	}
	covers := make(map[string]*port.Cover, len(coverRows))
	for _, c := range coverRows {
		covers[c.PlaylistID] = &port.Cover{URL: orEmpty(c.CoverURL), Key: track.ObjectKey(orEmpty(c.CoverKey))}
	}
	lists := make([]port.PlaylistRecord, len(rows))
	for i, r := range rows {
		lists[i] = port.PlaylistRecord{
			ID:         r.ID,
			Name:       r.Name,
			Favorites:  r.Kind == favoritesKind,
			TrackCount: int(r.TrackCount),
			CreatedAt:  r.CreatedAt,
			UpdatedAt:  r.UpdatedAt,
			Cover:      covers[r.ID],
		}
	}
	return lists, nil
}

func (s *Store) CreatePlaylist(ctx context.Context, ownerID, name string, limit int) (port.PlaylistRecord, error) {
	var created query.CreatePlaylistRow
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if err := q.LockFavorites(ctx, ownerID); err != nil {
			return err
		}
		n, err := q.CountPlaylists(ctx, ownerID)
		if err != nil {
			return err
		}
		if n >= int64(limit) {
			return port.ErrPlaylistLimit
		}
		created, err = q.CreatePlaylist(ctx, query.CreatePlaylistParams{OwnerID: ownerID, Name: name})
		return err
	})
	if err != nil {
		return port.PlaylistRecord{}, fmt.Errorf("成员 %s 新建歌单：%w", ownerID, err)
	}
	return port.PlaylistRecord{ID: created.ID, Name: name, CreatedAt: created.CreatedAt, UpdatedAt: created.UpdatedAt}, nil
}

func (s *Store) RenamePlaylist(ctx context.Context, ownerID, id, name string) error {
	if !isUUID(id) {
		return port.ErrPlaylistNotFound
	}
	n, err := s.q.RenamePlaylist(ctx, query.RenamePlaylistParams{ID: id, OwnerID: ownerID, Name: name})
	if err == nil && n == 0 {
		err = s.unchangeable(ctx, ownerID, id)
	}
	if err != nil {
		return fmt.Errorf("歌单 %s 改名：%w", id, err)
	}
	return nil
}

func (s *Store) DeletePlaylist(ctx context.Context, ownerID, id string) error {
	if !isUUID(id) {
		return port.ErrPlaylistNotFound
	}
	n, err := s.q.DeletePlaylist(ctx, query.DeletePlaylistParams{ID: id, OwnerID: ownerID})
	if err == nil && n == 0 {
		err = s.unchangeable(ctx, ownerID, id)
	}
	if err != nil {
		return fmt.Errorf("删除歌单 %s：%w", id, err)
	}
	return nil
}

// 改名或删除没有影响任何行：要么是“我喜欢”，要么歌单不属于这个成员。
func (s *Store) unchangeable(ctx context.Context, ownerID, id string) error {
	kind, err := s.q.PlaylistKind(ctx, query.PlaylistKindParams{ID: id, OwnerID: ownerID})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return port.ErrPlaylistNotFound
	case err != nil:
		return err
	case kind == favoritesKind:
		return port.ErrFavoritesFixed
	}
	return port.ErrPlaylistNotFound
}

func (s *Store) owned(ctx context.Context, ownerID, id string) error {
	if !isUUID(id) {
		return port.ErrPlaylistNotFound
	}
	_, err := s.q.PlaylistKind(ctx, query.PlaylistKindParams{ID: id, OwnerID: ownerID})
	if errors.Is(err, pgx.ErrNoRows) {
		return port.ErrPlaylistNotFound
	}
	return err
}

func (s *Store) PlaylistItems(ctx context.Context, ownerID, id string) ([]port.PlaylistItem, error) {
	if err := s.owned(ctx, ownerID, id); err != nil {
		return nil, fmt.Errorf("读取歌单 %s：%w", id, err)
	}
	rows, err := s.q.PlaylistItems(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("读取歌单 %s 的曲目：%w", id, err)
	}
	items := make([]port.PlaylistItem, len(rows))
	for i, r := range rows {
		items[i] = port.PlaylistItem{
			Track: track.Track{Source: track.Source(r.Source), ID: r.TrackID},
			Meta: track.Meta{
				Title:    r.Title,
				Artist:   orEmpty(r.Artist),
				Album:    orEmpty(r.Album),
				CoverURL: orEmpty(r.CoverURL),
				CoverKey: track.ObjectKey(orEmpty(r.CoverKey)),
				PageURL:  r.PageURL,
				Duration: time.Duration(r.DurationMs) * time.Millisecond,
			},
		}
	}
	return items, nil
}

func (s *Store) AddPlaylistItem(ctx context.Context, ownerID, id string, item port.PlaylistItem, limit int) error {
	if !isUUID(id) {
		return port.ErrPlaylistNotFound
	}
	t, m := item.Track, item.Meta
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		seq, err := q.LockPlaylist(ctx, query.LockPlaylistParams{ID: id, OwnerID: ownerID})
		if errors.Is(err, pgx.ErrNoRows) {
			return port.ErrPlaylistNotFound
		}
		if err != nil {
			return err
		}
		has, err := q.PlaylistHasItem(ctx, query.PlaylistHasItemParams{PlaylistID: id, Source: string(t.Source), TrackID: t.ID})
		if err != nil || has {
			return err
		}
		n, err := q.CountPlaylistItems(ctx, id)
		if err != nil {
			return err
		}
		if n >= int64(limit) {
			return port.ErrPlaylistLimit
		}
		seq++
		if err := q.InsertPlaylistItem(ctx, query.InsertPlaylistItemParams{
			PlaylistID: id,
			Source:     string(t.Source),
			TrackID:    t.ID,
			Title:      m.Title,
			Artist:     nullable(m.Artist),
			Album:      nullable(m.Album),
			CoverURL:   nullable(m.CoverURL),
			CoverKey:   nullable(string(m.CoverKey)),
			DurationMs: m.Duration.Milliseconds(),
			PageURL:    m.PageURL,
			Position:   seq,
		}); err != nil {
			return err
		}
		return q.SetPlaylistSeq(ctx, query.SetPlaylistSeqParams{ID: id, ItemSeq: seq})
	})
	if err != nil {
		return fmt.Errorf("向歌单 %s 加入 %s/%s：%w", id, t.Source, t.ID, err)
	}
	return nil
}

func (s *Store) RemovePlaylistItem(ctx context.Context, ownerID, id string, t track.Track) error {
	if !isUUID(id) {
		return port.ErrPlaylistNotFound
	}
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if _, err := q.LockPlaylist(ctx, query.LockPlaylistParams{ID: id, OwnerID: ownerID}); errors.Is(err, pgx.ErrNoRows) {
			return port.ErrPlaylistNotFound
		} else if err != nil {
			return err
		}
		return q.DeletePlaylistItem(ctx, query.DeletePlaylistItemParams{PlaylistID: id, Source: string(t.Source), TrackID: t.ID})
	})
	if err != nil {
		return fmt.Errorf("从歌单 %s 移除 %s/%s：%w", id, t.Source, t.ID, err)
	}
	return nil
}
