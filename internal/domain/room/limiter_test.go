package room

import (
	"testing"
	"time"
)

func TestLimiterAllowsBurstThenRefills(t *testing.T) {
	var l limiter
	for i := range 5 {
		if wait := l.take(t0, 5, time.Minute); wait != 0 {
			t.Fatalf("第 %d 次应在突发额度内，实际要等 %v", i+1, wait)
		}
	}
	if wait := l.take(t0, 5, time.Minute); wait != time.Minute {
		t.Fatalf("突发额度用完后应拒绝，还要等一分钟，实际 %v", wait)
	}
	if wait := l.take(t0.Add(59*time.Second), 5, time.Minute); wait != time.Second {
		t.Fatalf("不到一分钟不恢复，还要等一秒，实际 %v", wait)
	}
	if wait := l.take(t0.Add(time.Minute), 5, time.Minute); wait != 0 {
		t.Fatalf("一分钟后恢复一次，实际要等 %v", wait)
	}
	if wait := l.take(t0.Add(time.Minute), 5, time.Minute); wait != time.Minute {
		t.Fatalf("一分钟只恢复一次，实际 %v", wait)
	}
	for i := range 5 {
		if wait := l.take(t0.Add(time.Hour), 5, time.Minute); wait != 0 {
			t.Fatalf("长时间空闲后额度恢复满，第 %d 次要等 %v", i+1, wait)
		}
	}
	if wait := l.take(t0.Add(time.Hour), 5, time.Minute); wait == 0 {
		t.Fatal("额度不超过突发上限")
	}
}
