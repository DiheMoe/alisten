package port

import (
	"context"
	"time"
)

// 搜索与推荐列表的额度按房间计数，所有实例共用。
type SearchQuota interface {
	// 从房间的额度中取一次：最多突发 burst 次，之后每隔 refill 恢复一次。取到时返回 0，
	// 额度用完时返回距离下一次有额度还要等待的时长。
	TakeSearch(ctx context.Context, roomID string, burst int, refill time.Duration) (time.Duration, error)
}
