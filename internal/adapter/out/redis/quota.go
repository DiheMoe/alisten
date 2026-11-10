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

var _ port.RoomQuota = (*RoomQuota)(nil)

type RoomQuota struct {
	limiter *redis_rate.Limiter
}

func NewRoomQuota(client *goredis.Client) *RoomQuota {
	return &RoomQuota{limiter: redis_rate.NewLimiter(client)}
}

func (q *RoomQuota) TakeSearch(ctx context.Context, roomID string, burst int, refill time.Duration) (time.Duration, error) {
	return q.take(ctx, roomID, "search", "搜索", burst, refill)
}

func (q *RoomQuota) TakePick(ctx context.Context, roomID string, burst int, refill time.Duration) (time.Duration, error) {
	return q.take(ctx, roomID, "pick", "点歌", burst, refill)
}

func (q *RoomQuota) take(ctx context.Context, roomID, bucket, what string, burst int, refill time.Duration) (time.Duration, error) {
	wait, err := take(ctx, q.limiter, "room:"+roomID+":"+bucket, burst, refill)
	if err != nil {
		return 0, fmt.Errorf("取房间 %s 的%s额度：%w", roomID, what, err)
	}
	return wait, nil
}

var _ port.AccountQuota = (*AccountQuota)(nil)

type AccountQuota struct {
	limiter *redis_rate.Limiter
}

func NewAccountQuota(client *goredis.Client) *AccountQuota {
	return &AccountQuota{limiter: redis_rate.NewLimiter(client)}
}

func (q *AccountQuota) Take(ctx context.Context, key string, burst int, refill time.Duration) (time.Duration, error) {
	wait, err := take(ctx, q.limiter, "account:"+key, burst, refill)
	if err != nil {
		return 0, fmt.Errorf("取 %s 的额度：%w", key, err)
	}
	return wait, nil
}

func take(ctx context.Context, limiter *redis_rate.Limiter, key string, burst int, refill time.Duration) (time.Duration, error) {
	res, err := limiter.Allow(ctx, key, redis_rate.Limit{Rate: 1, Burst: burst, Period: refill})
	if err != nil {
		return 0, err
	}
	if res.Allowed > 0 {
		return 0, nil
	}
	return res.RetryAfter, nil
}
