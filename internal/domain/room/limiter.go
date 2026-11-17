package room

import (
	"maps"
	"time"
)

// 令牌桶用 GCRA 的理论到达时间表示，只做整数时间运算。浮点运算在不同平台上可能被编译器融合成不同的结果，那样状态机就不再确定。
type limiter struct {
	tat time.Time
}

// 放行时扣掉一次并返回 0，否则返回还要等多久，不扣。
func (l *limiter) take(now time.Time, burst int, interval time.Duration) time.Duration {
	tat := l.tat
	if tat.Before(now) {
		tat = now
	}
	if wait := tat.Sub(now) - time.Duration(burst-1)*interval; wait > 0 {
		return wait
	}
	l.tat = tat.Add(interval)
	return 0
}

// 按成员分别计数的令牌桶。闲置到和新桶没有区别的记录随时清掉，所以记下的成员数不超过最近操作过的人数。
type memberLimits map[string]limiter

func (m memberLimits) take(memberID string, now time.Time, burst int, interval time.Duration) time.Duration {
	maps.DeleteFunc(m, func(_ string, l limiter) bool { return !l.tat.After(now) })
	l := m[memberID]
	if wait := l.take(now, burst, interval); wait > 0 {
		return wait
	}
	m[memberID] = l
	return 0
}
