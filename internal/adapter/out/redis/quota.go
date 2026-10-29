// Package redis 只存放可以丢失的协调数据：Redis 被清空后系统能自行恢复。
package redis

import (
	"context"
	"fmt"
	"time"

	"github.com/go-redis/redis_rate/v10"
	goredis "github.com/redis/go-redis/v9"

	"github.com/dihemoe/alisten/internal/app/port"
)

var _ port.SearchQuota = (*SearchQuota)(nil)

type SearchQuota struct {
	limiter *redis_rate.Limiter
}

func NewSearchQuota(client *goredis.Client) *SearchQuota {
	return &SearchQuota{limiter: redis_rate.NewLimiter(client)}
}

func (q *SearchQuota) TakeSearch(ctx context.Context, roomID string, burst int, refill time.Duration) (time.Duration, error) {
	res, err := q.limiter.Allow(ctx, "room:"+roomID+":search", redis_rate.Limit{Rate: 1, Burst: burst, Period: refill})
	if err != nil {
		return 0, fmt.Errorf("取房间 %s 的搜索额度：%w", roomID, err)
	}
	if res.Allowed > 0 {
		return 0, nil
	}
	return res.RetryAfter, nil
}
