// Package redis 只存放可以丢失的协调数据，Redis 被清空后系统能自行恢复。
package redis

import (
	"context"
	"fmt"
	"time"

	"github.com/go-redis/redis_rate/v10"
	goredis "github.com/redis/go-redis/v9"

	"github.com/dihemoe/alisten/internal/app/port"
)

var _ port.Quota = (*Quota)(nil)

type Quota struct {
	limiter *redis_rate.Limiter
}

func NewQuota(client *goredis.Client) *Quota {
	return &Quota{limiter: redis_rate.NewLimiter(client)}
}

func (q *Quota) Take(ctx context.Context, key string, burst int, refill time.Duration) (time.Duration, error) {
	res, err := q.limiter.Allow(ctx, key, redis_rate.Limit{Rate: 1, Burst: burst, Period: refill})
	if err != nil {
		return 0, fmt.Errorf("取 %s 的额度：%w", key, err)
	}
	if res.Allowed > 0 {
		return 0, nil
	}
	return res.RetryAfter, nil
}
