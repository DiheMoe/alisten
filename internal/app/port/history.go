package port

import (
	"context"

	"github.com/dihemoe/alisten/internal/domain/track"
)

// AutoDJ 从多大的范围里取种子和排除的歌，由用例决定。
type AutoDJWindow struct {
	// 只看最近这么多次播放。
	Plays int
	// 最多取这么多颗种子。
	Seeds int
	// 最多排除这么多首由 AutoDJ 加入过的歌。
	Exclusions int
}

type PlayHistory interface {
	// 本房间最近 w.Seeds 首成员点播的网易云歌曲，不重复。先是正在播放的条目，includeQueue 为真时接着是待播和
	// 等待队列中的条目，然后是最近 w.Plays 次播放里的歌，按开播时间从近到远。队列中的条目按等待队列的排序规则排列。
	AutoDJSeeds(ctx context.Context, roomID string, includeQueue bool, w AutoDJWindow) ([]track.Track, error)
	// 最近 w.Plays 次播放里最近 w.Exclusions 首由 AutoDJ 加入的歌（包括正在播放的条目），加上待播和等待队列中的全部条目。
	AutoDJExclusions(ctx context.Context, roomID string, w AutoDJWindow) ([]track.Track, error)
}

type TrackUsage interface {
	TrackInUse(ctx context.Context, t track.Track) (bool, error)
}
