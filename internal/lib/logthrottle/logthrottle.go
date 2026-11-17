package logthrottle

import (
	"log/slog"
	"sync/atomic"
	"time"
)

// 一分钟一条 WARN 足以看出故障从什么时候开始、是否还在持续，日志量又不随重试的频率增长。
const Interval = time.Minute

// Warning 用于故障期间会反复出现的一类错误。Interval 内只有第一条记 WARN，其余记 DEBUG，这样日志量不随重试次数和并发的调用方增长。
// 零值可以直接使用。方法可以并发调用，不加锁，也不阻塞。
type Warning struct {
	// 上一条 WARN 的时刻，Recovered 之后为空。
	last atomic.Pointer[time.Time]
}

// 并发调用时只有一个调用方拿到 WARN。
func (w *Warning) Level() slog.Level {
	if last := w.last.Load(); last == nil || time.Since(*last) >= Interval {
		if now := time.Now(); w.last.CompareAndSwap(last, &now) {
			return slog.LevelWarn
		}
	}
	return slog.LevelDebug
}

// 操作成功之后调用。这样故障再次出现时，第一条错误立即记 WARN。
func (w *Warning) Recovered() {
	w.last.Store(nil)
}
