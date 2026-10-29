package redis

import (
	"slices"
	"testing"
	"time"
)

func TestLiveness(t *testing.T) {
	client := newClient(t)
	a, b := NewLiveness(client, "a"), NewLiveness(client, "b")
	if existed, err := a.Heartbeat(t.Context(), 15*time.Second); err != nil || existed {
		t.Fatalf("第一次刷新 = %v, %v，期望存活键之前不存在", existed, err)
	}
	if existed, err := a.Heartbeat(t.Context(), 15*time.Second); err != nil || !existed {
		t.Errorf("第二次刷新 = %v, %v，期望存活键已经存在", existed, err)
	}
	if _, err := b.Heartbeat(t.Context(), 100*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if expired, err := a.Expired(t.Context(), []string{"a", "b", "c"}); err != nil || !slices.Equal(expired, []string{"c"}) {
		t.Errorf("Expired = %q, %v，期望只有没有刷新过存活键的 c", expired, err)
	}
	time.Sleep(200 * time.Millisecond)
	if expired, err := a.Expired(t.Context(), []string{"a", "b", "c"}); err != nil || !slices.Equal(expired, []string{"b", "c"}) {
		t.Errorf("b 的存活键过期之后 Expired = %q, %v，期望 [b c]", expired, err)
	}
	if existed, err := b.Heartbeat(t.Context(), 15*time.Second); err != nil || existed {
		t.Errorf("过期之后刷新 = %v, %v，期望存活键之前不存在", existed, err)
	}
	if ttl, err := client.PTTL(t.Context(), aliveKey("a")).Result(); err != nil || ttl <= 14*time.Second || ttl > 15*time.Second {
		t.Errorf("存活键的剩余时间 = %v, %v，期望约 15 秒", ttl, err)
	}
	if expired, err := a.Expired(t.Context(), nil); err != nil || len(expired) != 0 {
		t.Errorf("Expired(nil) = %q, %v", expired, err)
	}
}
