package postgres

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/dihemoe/alisten/internal/adapter/out/postgres/internal/query"
	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/playlist"
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
			ShareID:    orEmpty(r.ShareID),
		}
	}
	return lists, nil
}

func (s *Store) CreatePlaylist(ctx context.Context, ownerID, name string, limit int) (port.PlaylistRecord, error) {
	var created query.CreatePlaylistRow
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) (err error) {
		created, err = createPlaylist(ctx, s.q.WithTx(tx), ownerID, name, limit)
		return err
	})
	if err != nil {
		return port.PlaylistRecord{}, fmt.Errorf("成员 %s 新建歌单：%w", ownerID, err)
	}
	return port.PlaylistRecord{ID: created.ID, Name: name, CreatedAt: created.CreatedAt, UpdatedAt: created.UpdatedAt}, nil
}

func (s *Store) ImportPlaylist(ctx context.Context, ownerID, name string, items []port.PlaylistItem, limit int) (port.PlaylistRecord, error) {
	var created query.CreatePlaylistRow
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) (err error) {
		q := s.q.WithTx(tx)
		if created, err = createPlaylist(ctx, q, ownerID, name, limit); err != nil {
			return err
		}
		seq := int64(len(items))
		rows := make([]query.InsertPlaylistItemsParams, len(items))
		for i, item := range items {
			t, m := item.Track, item.Meta
			rows[i] = query.InsertPlaylistItemsParams{
				PlaylistID: created.ID,
				Source:     string(t.Source),
				TrackID:    t.ID,
				Title:      m.Title,
				Artist:     nullable(m.Artist),
				Album:      nullable(m.Album),
				CoverURL:   nullable(m.CoverURL),
				CoverKey:   nullable(string(m.CoverKey)),
				DurationMs: m.Duration.Milliseconds(),
				PageURL:    m.PageURL,
				Position:   seq - int64(i),
			}
		}
		if _, err = q.InsertPlaylistItems(ctx, rows); err != nil {
			return err
		}
		return q.SetPlaylistSeq(ctx, query.SetPlaylistSeqParams{ID: created.ID, ItemSeq: seq})
	})
	if err != nil {
		return port.PlaylistRecord{}, fmt.Errorf("成员 %s 导入 %d 首歌到新歌单：%w", ownerID, len(items), err)
	}
	return port.PlaylistRecord{ID: created.ID, Name: name, TrackCount: len(items), CreatedAt: created.CreatedAt, UpdatedAt: created.UpdatedAt, Cover: firstCover(items)}, nil
}

// 要在事务里调用，这样“我喜欢”的行锁会持续到新建之后，同一个账号并发新建时才会依次计数。
func createPlaylist(ctx context.Context, q *query.Queries, ownerID, name string, limit int) (query.CreatePlaylistRow, error) {
	if err := q.LockFavorites(ctx, ownerID); err != nil {
		return query.CreatePlaylistRow{}, err
	}
	n, err := q.CountPlaylists(ctx, ownerID)
	if err != nil {
		return query.CreatePlaylistRow{}, err
	}
	if n >= int64(limit) {
		return query.CreatePlaylistRow{}, port.ErrPlaylistLimit
	}
	return q.CreatePlaylist(ctx, query.CreatePlaylistParams{OwnerID: ownerID, Name: name})
}

func firstCover(items []port.PlaylistItem) *port.Cover {
	for _, item := range items {
		if m := item.Meta; m.CoverURL != "" || m.CoverKey != "" {
			return &port.Cover{URL: m.CoverURL, Key: m.CoverKey}
		}
	}
	return nil
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

// 改名或删除没有影响任何行，说明要么是“我喜欢”，要么歌单不属于这个成员。
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
	return playlistItems(rows), nil
}

func playlistItems(rows []query.PlaylistItem) []port.PlaylistItem {
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
	return items
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

func (s *Store) RemovePlaylistItems(ctx context.Context, ownerID, id string, tracks []track.Track) error {
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
		sources, ids := splitTracks(tracks)
		return q.DeletePlaylistItems(ctx, query.DeletePlaylistItemsParams{PlaylistID: id, Sources: sources, TrackIds: ids})
	})
	if err != nil {
		return fmt.Errorf("从歌单 %s 移除 %d 首歌：%w", id, len(tracks), err)
	}
	return nil
}

func (s *Store) TrackPlaylists(ctx context.Context, ownerID string, t track.Track) ([]string, error) {
	ids, err := s.q.TrackPlaylists(ctx, query.TrackPlaylistsParams{OwnerID: ownerID, Source: string(t.Source), TrackID: t.ID})
	if err != nil {
		return nil, fmt.Errorf("查询曲目 %s 所在的歌单：%w", t.ID, err)
	}
	if ids == nil {
		ids = []string{}
	}
	return ids, nil
}

func (s *Store) CopyPlaylistItems(ctx context.Context, ownerID, fromID, toID string, tracks []track.Track, limit int) (int, error) {
	var added int64
	err := s.owned(ctx, ownerID, fromID)
	if err == nil && !isUUID(toID) {
		err = port.ErrPlaylistNotFound
	}
	if err == nil {
		err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
			q := s.q.WithTx(tx)
			seq, err := q.LockPlaylist(ctx, query.LockPlaylistParams{ID: toID, OwnerID: ownerID})
			if errors.Is(err, pgx.ErrNoRows) {
				return port.ErrPlaylistNotFound
			}
			if err != nil {
				return err
			}
			sources, ids := splitTracks(tracks)
			added, err = q.CopyPlaylistItems(ctx, query.CopyPlaylistItemsParams{FromID: fromID, ToID: toID, ItemSeq: seq, Sources: sources, TrackIds: ids})
			if err != nil || added == 0 {
				return err
			}
			n, err := q.CountPlaylistItems(ctx, toID)
			if err != nil {
				return err
			}
			if n > int64(limit) {
				return port.ErrPlaylistLimit
			}
			return q.SetPlaylistSeq(ctx, query.SetPlaylistSeqParams{ID: toID, ItemSeq: seq + added})
		})
	}
	if err != nil {
		return 0, fmt.Errorf("从歌单 %s 向歌单 %s 复制 %d 首歌：%w", fromID, toID, len(tracks), err)
	}
	return int(added), nil
}

func (s *Store) MovePlaylistItems(ctx context.Context, ownerID, id string, tracks []track.Track, before *track.Track) error {
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
		rows, err := q.PlaylistTracks(ctx, id)
		if err != nil {
			return err
		}
		order := make([]track.Track, len(rows))
		for i, r := range rows {
			order[i] = track.Track{Source: track.Source(r.Source), ID: r.TrackID}
		}
		moved, err := playlist.Move(order, tracks, before)
		if err != nil || slices.Equal(moved, order) {
			return err
		}
		// 显示顺序里最靠前的歌取最大的排序值。歌单的 item_seq 不小于曲目数，所以新加入的歌仍然排在最前。
		positions := make([]int64, len(moved))
		for i := range moved {
			positions[i] = int64(len(moved) - i)
		}
		sources, ids := splitTracks(moved)
		if err := q.SetPlaylistPositions(ctx, query.SetPlaylistPositionsParams{PlaylistID: id, Sources: sources, TrackIds: ids, Positions: positions}); err != nil {
			return err
		}
		return q.TouchPlaylist(ctx, id)
	})
	if err != nil {
		return fmt.Errorf("调整歌单 %s 的顺序：%w", id, err)
	}
	return nil
}

func (s *Store) SetPlaylistShare(ctx context.Context, ownerID, id, shareID string) error {
	if !isUUID(id) {
		return port.ErrPlaylistNotFound
	}
	n, err := s.q.SetPlaylistShare(ctx, query.SetPlaylistShareParams{ID: id, OwnerID: ownerID, ShareID: nullable(shareID)})
	if err == nil && n == 0 {
		err = port.ErrPlaylistNotFound
	}
	if err != nil {
		return fmt.Errorf("设置歌单 %s 的分享：%w", id, err)
	}
	return nil
}

func (s *Store) SharedPlaylist(ctx context.Context, shareID string) (port.SharedPlaylist, error) {
	var shared port.SharedPlaylist
	// 歌单和曲目在同一个快照里读。如果分开读，删除歌单可能恰好发生在两次读取之间，就会读到歌单还在、曲目却已经没了。
	err := pgx.BeginTxFunc(ctx, s.pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		p, err := q.SharedPlaylist(ctx, shareID)
		if errors.Is(err, pgx.ErrNoRows) {
			return port.ErrShareNotFound
		}
		if err != nil {
			return err
		}
		rows, err := q.PlaylistItems(ctx, p.ID)
		if err != nil {
			return err
		}
		shared = port.SharedPlaylist{
			Name:            p.Name,
			Favorites:       p.Kind == favoritesKind,
			OwnerName:       p.OwnerName,
			OwnerAvatarHash: orEmpty(p.OwnerAvatarHash),
			Items:           playlistItems(rows),
		}
		return nil
	})
	if err != nil {
		return port.SharedPlaylist{}, fmt.Errorf("读取分享的歌单：%w", err)
	}
	return shared, nil
}

func (s *Store) CopySharedPlaylist(ctx context.Context, ownerID, shareID, name string, limit int) (port.PlaylistRecord, error) {
	var record port.PlaylistRecord
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		created, err := createPlaylist(ctx, q, ownerID, name, limit)
		if err != nil {
			return err
		}
		copied, err := q.CopySharedPlaylistItems(ctx, query.CopySharedPlaylistItemsParams{ShareID: shareID, ToID: created.ID})
		if err != nil {
			return err
		}
		if !copied.Found {
			return port.ErrShareNotFound
		}
		if err := q.SetPlaylistSeq(ctx, query.SetPlaylistSeqParams{ID: created.ID, ItemSeq: copied.Copied}); err != nil {
			return err
		}
		record = port.PlaylistRecord{ID: created.ID, Name: name, TrackCount: int(copied.Copied), CreatedAt: created.CreatedAt, UpdatedAt: created.UpdatedAt}
		cover, err := q.PlaylistCover(ctx, created.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		record.Cover = &port.Cover{URL: orEmpty(cover.CoverURL), Key: track.ObjectKey(orEmpty(cover.CoverKey))}
		return nil
	})
	if err != nil {
		return port.PlaylistRecord{}, fmt.Errorf("成员 %s 复制分享的歌单：%w", ownerID, err)
	}
	return record, nil
}

func splitTracks(tracks []track.Track) (sources, ids []string) {
	sources, ids = make([]string, len(tracks)), make([]string, len(tracks))
	for i, t := range tracks {
		sources[i], ids[i] = string(t.Source), t.ID
	}
	return sources, ids
}
