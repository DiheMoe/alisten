package bootstrap

import (
	"context"
	"log/slog"
	"regexp"
	"sync/atomic"
	"time"
)

const readinessInterval = 5 * time.Second

// 网络错误带有本机一侧的临时地址和端口（read udp 本机:端口->对端:端口），每次连接都不同，比较原因时要去掉。
var localAddress = regexp.MustCompile(`\S+->`)

func reason(err error) string {
	return localAddress.ReplaceAllString(err.Error(), "")
}

// 依赖在后台定期检查，/readyz 只读取最近一次的结果，探针请求本身不访问外部依赖。
func watchReadiness(ctx context.Context, interval time.Duration, check func(context.Context) error, ready, stopping *atomic.Bool, logger *slog.Logger) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var last error
	for first := true; ; first = false {
		checkCtx, cancel := context.WithTimeout(ctx, interval)
		err := check(checkCtx)
		cancel()
		if ctx.Err() != nil {
			return
		}
		ready.Store(err == nil)
		// 不可用的原因变化时也要记录，否则日志中只有最初的原因；原因不变时不重复记录。
		switch {
		case stopping.Load():
			// 开始退出之后实例有意不就绪，依赖检查的结果不记日志。
		case err != nil && (first || last == nil || reason(err) != reason(last)):
			logger.WarnContext(ctx, "依赖检查未通过，实例未就绪", "error", err)
		case err == nil && (first || last != nil):
			logger.InfoContext(ctx, "依赖检查通过，实例已就绪")
		}
		last = err
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
