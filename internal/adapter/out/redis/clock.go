package redis

import (
	"context"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// 单程时延按往返时间的一半估算。
func ClockOffset(ctx context.Context, client *goredis.Client) (time.Duration, error) {
	start := time.Now()
	server, err := client.Time(ctx).Result()
	if err != nil {
		return 0, fmt.Errorf("读取 Redis 服务器的时间：%w", err)
	}
	return server.Sub(start.Add(time.Since(start) / 2)), nil
}
