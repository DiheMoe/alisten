package redis

import (
	"testing"
	"time"
)

const hall = "01a15778-1911-7a28-8b2e-e08c2a25f388"

func TestOnlineCountExpiresWithLease(t *testing.T) {
	client := newClient(t)
	a := NewLeases(client, "a")
	ctx := t.Context()
	if ok, err := a.Acquire(ctx, hall, 15*time.Second); err != nil || !ok {
		t.Fatalf("Acquire = %v, %v", ok, err)
	}
	if err := client.PExpire(ctx, ownerKey(hall), 10*time.Second).Err(); err != nil {
		t.Fatal(err)
	}
	if ok, err := a.SetOnline(ctx, hall, 3); err != nil || !ok {
		t.Fatalf("a 写入在线人数 = %v, %v", ok, err)
	}
	if ttl, err := client.PTTL(ctx, onlineKey(hall)).Result(); err != nil || ttl <= 9*time.Second || ttl > 10*time.Second {
		t.Errorf("在线人数的剩余时间 = %v, %v，期望与租约一样约 10 秒", ttl, err)
	}
	if ok, err := a.Renew(ctx, hall, 15*time.Second); err != nil || !ok {
		t.Fatalf("a 续期自己的租约 = %v, %v", ok, err)
	}
	for _, key := range []string{ownerKey(hall), onlineKey(hall)} {
		if ttl, err := client.PTTL(ctx, key).Result(); err != nil || ttl <= 14*time.Second || ttl > 15*time.Second {
			t.Errorf("续期之后 %s 的剩余时间 = %v, %v，期望约 15 秒", key, ttl, err)
		}
	}
}
