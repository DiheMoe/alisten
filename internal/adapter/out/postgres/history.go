package postgres

import (
	"context"
	"fmt"

	"github.com/dihemoe/alisten/internal/adapter/out/postgres/internal/query"
	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/track"
)

var (
	_ port.PlayHistory = (*Store)(nil)
	_ port.TrackUsage  = (*Store)(nil)
)

func (s *Store) AutoDJSeeds(ctx context.Context, roomID string, includeQueue bool) ([]track.Track, error) {
	ids, err := s.q.AutoDJSeeds(ctx, query.AutoDJSeedsParams{RoomID: roomID, IncludeQueue: includeQueue})
	if err != nil {
		return nil, fmt.Errorf("读取房间 %s 的 AutoDJ 种子：%w", roomID, err)
	}
	seeds := make([]track.Track, len(ids))
	for i, id := range ids {
		seeds[i] = track.Track{Source: track.NetEase, ID: id}
	}
	return seeds, nil
}

func (s *Store) AutoDJExclusions(ctx context.Context, roomID string) ([]track.Track, error) {
	rows, err := s.q.AutoDJExclusions(ctx, roomID)
	if err != nil {
		return nil, fmt.Errorf("读取房间 %s 的 AutoDJ 排除项：%w", roomID, err)
	}
	excluded := make([]track.Track, len(rows))
	for i, r := range rows {
		excluded[i] = track.Track{Source: track.Source(r.Source), ID: r.TrackID}
	}
	return excluded, nil
}

func (s *Store) TrackInUse(ctx context.Context, t track.Track) (bool, error) {
	used, err := s.q.TrackInUse(ctx, query.TrackInUseParams{Source: string(t.Source), TrackID: t.ID})
	if err != nil {
		return false, fmt.Errorf("检查曲目 %s %s 是否仍被房间引用：%w", t.Source, t.ID, err)
	}
	return used, nil
}
