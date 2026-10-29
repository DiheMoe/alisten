package bootstrap

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	clockInterval = 30 * time.Second
	clockTimeout  = 5 * time.Second
)

// 估算本实例与 Redis 服务器的时钟偏差，两个实例之间的时钟差就是两者偏差之差。
// 估算失败时保留上一次的值：连不上 Redis 已经反映在就绪检查中。
func watchClock(ctx context.Context, measure func(context.Context) (time.Duration, error), offset prometheus.Gauge) {
	ticker := time.NewTicker(clockInterval)
	defer ticker.Stop()
	for {
		measureCtx, cancel := context.WithTimeout(ctx, clockTimeout)
		if d, err := measure(measureCtx); err == nil {
			offset.Set(d.Seconds())
		}
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
