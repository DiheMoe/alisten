package room

import (
	"testing"
	"time"
)

func TestLimiterAllowsBurstThenRefills(t *testing.T) {
	var l limiter
	for i := range 5 {
		if !l.allow(t0, 5, time.Minute) {
			t.Fatalf("第 %d 次应在突发额度内", i+1)
		}
	}
	if l.allow(t0, 5, time.Minute) {
		t.Fatal("突发额度用完后应拒绝")
	}
	if l.allow(t0.Add(59*time.Second), 5, time.Minute) {
		t.Fatal("不到一分钟不恢复")
	}
	if !l.allow(t0.Add(time.Minute), 5, time.Minute) {
		t.Fatal("一分钟后恢复一次")
	}
	if l.allow(t0.Add(time.Minute), 5, time.Minute) {
		t.Fatal("一分钟只恢复一次")
	}
	for i := range 5 {
		if !l.allow(t0.Add(time.Hour), 5, time.Minute) {
			t.Fatalf("长时间空闲后额度恢复满，第 %d 次失败", i+1)
		}
	}
	if l.allow(t0.Add(time.Hour), 5, time.Minute) {
		t.Fatal("额度不超过突发上限")
	}
}
