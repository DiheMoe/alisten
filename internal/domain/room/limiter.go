package room

import (
	"maps"
	"time"
)

// 令牌桶用 GCRA 的理论到达时间表示，只做整数时间运算。浮点运算在不同平台上可能被编译器融合成不同的结果，那样状态机就不再确定。
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

// 按成员分别计数的令牌桶。闲置到和新桶没有区别的记录随时清掉，所以记下的成员数不超过最近操作过的人数。
type memberLimits map[string]limiter

func (m memberLimits) allow(memberID string, now time.Time, burst int, interval time.Duration) bool {
	maps.DeleteFunc(m, func(_ string, l limiter) bool { return !l.tat.After(now) })
	l := m[memberID]
	if !l.allow(now, burst, interval) {
		return false
	}
	m[memberID] = l
	return true
}
