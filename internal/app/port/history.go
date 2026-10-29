package port

import (
	"context"

	"github.com/dihemoe/alisten/internal/domain/track"
)

type PlayHistory interface {
	// 本房间最近 32 首不同的、由成员点播的网易云歌曲：先是正在播放的条目，includeQueue 时接着是待播和
	// 等待队列中的条目，然后是按开播时间从近到远的播放记录。
	AutoDJSeeds(ctx context.Context, roomID string, includeQueue bool) ([]track.Track, error)
	// 本房间最近 128 首由 AutoDJ 加入的歌（包括正在播放的条目），加上待播和等待队列中的全部条目。
	AutoDJExclusions(ctx context.Context, roomID string) ([]track.Track, error)
}

type TrackUsage interface {
	TrackInUse(ctx context.Context, t track.Track) (bool, error)
}
