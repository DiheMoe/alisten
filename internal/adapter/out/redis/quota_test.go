package redis

import (
	"testing"
	"time"
)

func TestQuota(t *testing.T) {
	quota := NewQuota(newClient(t))
	for i := range 2 {
		if wait, err := quota.Take(t.Context(), "login:ip:203.0.113.7", 2, time.Minute); err != nil || wait != 0 {
			t.Fatalf("第 %d 次 = %v, %v，突发额度之内应直接取到", i+1, wait, err)
		}
	}
	wait, err := quota.Take(t.Context(), "login:ip:203.0.113.7", 2, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if wait < 59*time.Second || wait > time.Minute {
		t.Errorf("额度用完后要等 %v，期望约 1 分钟", wait)
	}
	if wait, err := quota.Take(t.Context(), "login:user:alice", 2, time.Minute); err != nil || wait != 0 {
		t.Errorf("另一个键 = %v, %v，额度按键计算", wait, err)
	}
}
