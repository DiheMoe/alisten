package redis

import (
	"testing"
	"time"
)

func TestHeartbeatSetsTTL(t *testing.T) {
	client := newClient(t)
	a := NewLiveness(client, "a")
	if _, err := a.Heartbeat(t.Context(), 15*time.Second); err != nil {
		t.Fatal(err)
	}
	if ttl, err := client.PTTL(t.Context(), aliveKey("a")).Result(); err != nil || ttl <= 14*time.Second || ttl > 15*time.Second {
		t.Errorf("存活键的剩余时间 = %v, %v，期望约 15 秒", ttl, err)
	}
}
