package port

import (
	"context"
	"time"
)

// 额度按房间计数，所有实例共用。搜索与推荐列表共用一份额度，点歌另算一份。
type RoomQuota interface {
	// 从房间的额度中取一次：最多突发 burst 次，之后每隔 refill 恢复一次。取到时返回 0，
	// 额度用完时返回距离下一次有额度还要等待的时长。
	TakeSearch(ctx context.Context, roomID string, burst int, refill time.Duration) (time.Duration, error)
	// 与 TakeSearch 相同，取的是点歌额度。
	TakePick(ctx context.Context, roomID string, burst int, refill time.Duration) (time.Duration, error)
}

// 按调用方拼出的键计数的额度，所有实例共用。
type AccountQuota interface {
	// 与 RoomQuota.TakeSearch 相同：取到时返回 0，额度用完时返回还要等待的时长。
	Take(ctx context.Context, key string, burst int, refill time.Duration) (time.Duration, error)
}
