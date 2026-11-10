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
	_ port.ListenRecorder = (*Store)(nil)
	_ port.Listens        = (*Store)(nil)
)

func (s *Store) RecordListen(ctx context.Context, memberIDs []string, l port.Listen) error {
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		ids, err := q.LockListeners(ctx, memberIDs)
		if err != nil || len(ids) == 0 {
			return err
		}
		m := l.Meta
		err = q.UpsertListens(ctx, query.UpsertListensParams{
			MemberIds:  ids,
			Source:     string(l.Track.Source),
			TrackID:    l.Track.ID,
			Title:      m.Title,
			Artist:     nullable(m.Artist),
			Album:      nullable(m.Album),
			CoverURL:   nullable(m.CoverURL),
			CoverKey:   nullable(string(m.CoverKey)),
			DurationMs: m.Duration.Milliseconds(),
			PageURL:    m.PageURL,
			HeardAt:    l.HeardAt,
		})
		if err != nil {
			return err
		}
		return q.TrimListens(ctx, query.TrimListensParams{MemberIds: ids, Keep: port.ListenLimit})
	})
	if err != nil {
		return fmt.Errorf("给 %d 个成员记录听到的 %s:%s：%w", len(memberIDs), l.Track.Source, l.Track.ID, err)
	}
	return nil
}

// 要在替换会话的事务里调用。这时账号行已经被互斥地锁住，所以 moveListens 和同时进行的记录、同时登录这个账号的其他游客会依次写入账号的历史。
func moveListens(ctx context.Context, q *query.Queries, fromID, toID string) error {
	if err := q.MoveListens(ctx, query.MoveListensParams{FromID: fromID, ToID: toID}); err != nil {
		return err
	}
	return q.TrimListens(ctx, query.TrimListensParams{MemberIds: []string{toID}, Keep: port.ListenLimit})
}

func (s *Store) MemberListens(ctx context.Context, memberID string) ([]port.Listen, error) {
	rows, err := s.q.MemberListens(ctx, query.MemberListensParams{MemberID: memberID, Max: port.ListenLimit})
	if err != nil {
		return nil, fmt.Errorf("读取成员 %s 的播放历史：%w", memberID, err)
	}
	listens := make([]port.Listen, len(rows))
	for i, r := range rows {
		listens[i] = port.Listen{
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
			HeardAt: r.HeardAt,
		}
	}
	return listens, nil
}

func (s *Store) DeleteListen(ctx context.Context, memberID string, t track.Track) error {
	if err := s.q.DeleteListen(ctx, query.DeleteListenParams{MemberID: memberID, Source: string(t.Source), TrackID: t.ID}); err != nil {
		return fmt.Errorf("删除成员 %s 听过的 %s:%s：%w", memberID, t.Source, t.ID, err)
	}
	return nil
}

func (s *Store) ClearListens(ctx context.Context, memberID string) error {
	if err := s.q.ClearListens(ctx, memberID); err != nil {
		return fmt.Errorf("清空成员 %s 的播放历史：%w", memberID, err)
	}
	return nil
}

func (s *Store) ListenRecording(ctx context.Context, memberID string) (bool, error) {
	on, err := s.q.ListenRecording(ctx, memberID)
	if errors.Is(err, pgx.ErrNoRows) {
		err = port.ErrMemberNotFound
	}
	if err != nil {
		return false, fmt.Errorf("读取成员 %s 是否记录播放历史：%w", memberID, err)
	}
	return on, nil
}

func (s *Store) SetListenRecording(ctx context.Context, memberID string, on bool) error {
	n, err := s.q.SetListenRecording(ctx, query.SetListenRecordingParams{ID: memberID, RecordListens: on})
	if err == nil && n == 0 {
		err = port.ErrMemberNotFound
	}
	if err != nil {
		return fmt.Errorf("设置成员 %s 是否记录播放历史：%w", memberID, err)
	}
	return nil
}
