package port

import (
	"context"
	"time"
)

// 按调用方拼出的键计数的额度，所有实例共用。
type Quota interface {
	// 从 key 的额度中取一次：最多突发 burst 次，之后每隔 refill 恢复一次。取到时返回 0，额度用完时返回距离下一次有额度还要等待的时长。
	Take(ctx context.Context, key string, burst int, refill time.Duration) (time.Duration, error)
}
