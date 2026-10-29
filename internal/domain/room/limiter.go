package room

import "time"

// 令牌桶用 GCRA 的理论到达时间表示，只做整数时间运算：浮点运算在不同平台上可能被编译器融合成不同的结果，状态机就不再确定。
type limiter struct {
	tat time.Time
}

func (l *limiter) allow(now time.Time, burst int, interval time.Duration) bool {
	tat := l.tat
	if tat.Before(now) {
		tat = now
	}
	if tat.Sub(now) > time.Duration(burst-1)*interval {
		return false
	}
	l.tat = tat.Add(interval)
	return true
}
