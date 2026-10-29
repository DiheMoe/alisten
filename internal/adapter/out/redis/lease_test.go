package redis

import (
	"slices"
	"testing"
	"time"
)

const (
	hall  = "01a15778-1911-7a28-8b2e-e08c2a25f388"
	party = "01a15778-1911-7a28-8b2e-e08c2a25f389"
)

func TestLeaseBelongsToOneInstance(t *testing.T) {
	client := newClient(t)
	a, b := NewLeases(client, "a"), NewLeases(client, "b")
	ctx := t.Context()

	if ok, err := a.Acquire(ctx, hall, 15*time.Second); err != nil || !ok {
		t.Fatalf("a 取得空闲的租约 = %v, %v", ok, err)
	}
	if ok, err := b.Acquire(ctx, hall, 15*time.Second); err != nil || ok {
		t.Errorf("b 取得 a 的租约 = %v, %v，期望 false", ok, err)
	}
	if ok, err := a.Acquire(ctx, hall, 15*time.Second); err != nil || !ok {
		t.Errorf("a 再次取得自己的租约 = %v, %v，期望 true", ok, err)
	}
	if owners, err := b.Owners(ctx, []string{hall, party}); err != nil || !slices.Equal(owners, []string{"a", ""}) {
		t.Errorf("Owners = %q, %v，期望 [a \"\"]", owners, err)
	}

	if ok, err := b.Renew(ctx, hall, 15*time.Second); err != nil || ok {
		t.Errorf("b 续期 a 的租约 = %v, %v，期望 false", ok, err)
	}
	if ok, err := b.SetOnline(ctx, hall, 5); err != nil || ok {
		t.Errorf("b 写入 a 的房间的在线人数 = %v, %v，期望 false", ok, err)
	}
	if counts, err := a.OnlineCounts(ctx, []string{hall, party}); err != nil || !slices.Equal(counts, []int{0, 0}) {
		t.Errorf("取得租约之后的在线人数 = %v, %v，期望 [0 0]", counts, err)
	}
	if err := client.PExpire(ctx, ownerKey(hall), 10*time.Second).Err(); err != nil {
		t.Fatal(err)
	}
	if ok, err := a.SetOnline(ctx, hall, 3); err != nil || !ok {
		t.Fatalf("a 写入在线人数 = %v, %v", ok, err)
	}
	if counts, err := b.OnlineCounts(ctx, []string{hall, party}); err != nil || !slices.Equal(counts, []int{3, 0}) {
		t.Errorf("写入之后的在线人数 = %v, %v，期望 [3 0]", counts, err)
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
	if counts, _ := b.OnlineCounts(ctx, []string{hall}); !slices.Equal(counts, []int{3}) {
		t.Errorf("续期之后的在线人数 = %v，期望不变", counts)
	}

	if err := b.Release(ctx, hall); err != nil {
		t.Fatal(err)
	}
	if owners, _ := a.Owners(ctx, []string{hall}); !slices.Equal(owners, []string{"a"}) {
		t.Errorf("b 释放 a 的租约之后 owner = %q，期望不变", owners)
	}
	if err := a.Release(ctx, hall); err != nil {
		t.Fatal(err)
	}
	if owners, _ := a.Owners(ctx, []string{hall}); !slices.Equal(owners, []string{""}) {
		t.Errorf("释放之后 owner = %q，期望没有", owners)
	}
	if counts, _ := a.OnlineCounts(ctx, []string{hall}); !slices.Equal(counts, []int{0}) {
		t.Errorf("释放之后在线人数 = %v，期望 0", counts)
	}
	if ok, err := a.Renew(ctx, hall, 15*time.Second); err != nil || ok {
		t.Errorf("续期已释放的租约 = %v, %v，期望 false", ok, err)
	}
	if ok, err := b.Acquire(ctx, hall, 15*time.Second); err != nil || !ok {
		t.Errorf("释放之后 b 取得租约 = %v, %v，期望 true", ok, err)
	}
}

func TestLeaseExpires(t *testing.T) {
	client := newClient(t)
	a, b := NewLeases(client, "a"), NewLeases(client, "b")
	if ok, err := a.Acquire(t.Context(), hall, 100*time.Millisecond); err != nil || !ok {
		t.Fatalf("Acquire = %v, %v", ok, err)
	}
	if ok, err := a.SetOnline(t.Context(), hall, 2); err != nil || !ok {
		t.Fatalf("SetOnline = %v, %v", ok, err)
	}
	time.Sleep(200 * time.Millisecond)
	if owners, err := b.Owners(t.Context(), []string{hall}); err != nil || !slices.Equal(owners, []string{""}) {
		t.Errorf("过期之后 owner = %q, %v，期望没有", owners, err)
	}
	if counts, err := b.OnlineCounts(t.Context(), []string{hall}); err != nil || !slices.Equal(counts, []int{0}) {
		t.Errorf("过期之后在线人数 = %v, %v，期望 0", counts, err)
	}
	if ok, err := a.Renew(t.Context(), hall, 100*time.Millisecond); err != nil || ok {
		t.Errorf("续期已过期的租约 = %v, %v，期望 false", ok, err)
	}
	if ok, err := b.Acquire(t.Context(), hall, 15*time.Second); err != nil || !ok {
		t.Errorf("过期之后 b 取得租约 = %v, %v，期望 true", ok, err)
	}
}

// 本实例重新取得自己的租约就是房间的一次新运行：它从空的在线列表开始，上一次运行写下的在线人数作废。
func TestReacquiringOwnLeaseResetsOnlineCount(t *testing.T) {
	a := NewLeases(newClient(t), "a")
	if ok, err := a.Acquire(t.Context(), hall, time.Minute); err != nil || !ok {
		t.Fatalf("Acquire = %v, %v", ok, err)
	}
	if ok, err := a.SetOnline(t.Context(), hall, 3); err != nil || !ok {
		t.Fatalf("SetOnline = %v, %v", ok, err)
	}
	if ok, err := a.Acquire(t.Context(), hall, time.Minute); err != nil || !ok {
		t.Fatalf("再次 Acquire = %v, %v", ok, err)
	}
	if counts, err := a.OnlineCounts(t.Context(), []string{hall}); err != nil || counts[0] != 0 {
		t.Errorf("重新取得租约之后在线人数 = %v, %v，期望 0", counts, err)
	}
}
