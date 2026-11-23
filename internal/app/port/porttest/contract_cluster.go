package porttest

import (
	"crypto/rand"
	"slices"
	"testing"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
)

type ClusterNode interface {
	port.Leases
	port.OnlineCounts
	port.Liveness
	port.Bus
}

// 每个子测试都会调用一次 open。open 返回的 node 按实例 ID 取出一个实例，同一个 node 取出的实例共用一份租约、存活键和频道。
func RunClusterContract(t *testing.T, open func(t *testing.T) (node func(instanceID string) ClusterNode)) {
	runLeasesContract(t, open)
	runLivenessContract(t, open)
	runBusContract(t, open)
}

// Redis 的 Pub/Sub 不按逻辑库隔离，所以实例 ID 和房间 ID 都带随机后缀，免得和别的用例用到同一个频道。
func unique(name string) string { return name + "-" + rand.Text() }

func runLeasesContract(t *testing.T, open func(t *testing.T) func(string) ClusterNode) {
	t.Run("租约属于先取得它的实例", func(t *testing.T) {
		node := open(t)
		aID := unique("a")
		a, b := node(aID), node(unique("b"))
		hall, party := unique("hall"), unique("party")
		ctx := t.Context()
		if ok, err := a.Acquire(ctx, hall, time.Minute); err != nil || !ok {
			t.Fatalf("a 取得空闲的租约 = %v, %v", ok, err)
		}
		if ok, err := b.Acquire(ctx, hall, time.Minute); err != nil || ok {
			t.Errorf("b 取得 a 的租约 = %v, %v，期望 false", ok, err)
		}
		if ok, err := a.Acquire(ctx, hall, time.Minute); err != nil || !ok {
			t.Errorf("a 再次取得自己的租约 = %v, %v，期望 true", ok, err)
		}
		if owners, err := b.Owners(ctx, []string{hall, party}); err != nil || !slices.Equal(owners, []string{aID, ""}) {
			t.Errorf("Owners = %q, %v，期望 [%s \"\"]", owners, err, aID)
		}
	})

	t.Run("只有 owner 能续期和写入在线人数", func(t *testing.T) {
		node := open(t)
		a, b := node(unique("a")), node(unique("b"))
		hall, party := unique("hall"), unique("party")
		ctx := t.Context()
		if ok, err := a.Acquire(ctx, hall, time.Minute); err != nil || !ok {
			t.Fatalf("Acquire = %v, %v", ok, err)
		}
		if ok, err := b.Renew(ctx, hall, time.Minute); err != nil || ok {
			t.Errorf("b 续期 a 的租约 = %v, %v，期望 false", ok, err)
		}
		if ok, err := b.SetOnlineCount(ctx, hall, 5); err != nil || ok {
			t.Errorf("b 写入 a 的房间的在线人数 = %v, %v，期望 false", ok, err)
		}
		if counts, err := a.OnlineCounts(ctx, []string{hall, party}); err != nil || !slices.Equal(counts, []int{0, 0}) {
			t.Errorf("取得租约之后的在线人数 = %v, %v，期望 [0 0]", counts, err)
		}
		if ok, err := a.SetOnlineCount(ctx, hall, 3); err != nil || !ok {
			t.Fatalf("a 写入在线人数 = %v, %v", ok, err)
		}
		if counts, err := b.OnlineCounts(ctx, []string{hall, party}); err != nil || !slices.Equal(counts, []int{3, 0}) {
			t.Errorf("写入之后的在线人数 = %v, %v，期望 [3 0]", counts, err)
		}
		if ok, err := a.Renew(ctx, hall, time.Minute); err != nil || !ok {
			t.Fatalf("a 续期自己的租约 = %v, %v", ok, err)
		}
		if counts, err := b.OnlineCounts(ctx, []string{hall}); err != nil || !slices.Equal(counts, []int{3}) {
			t.Errorf("续期之后的在线人数 = %v, %v，期望不变", counts, err)
		}
	})

	// 本实例重新取得自己的租约，就是房间的一次新运行。新运行从空的在线列表开始，上一次运行写下的在线人数作废。
	t.Run("重新取得自己的租约时在线人数归零", func(t *testing.T) {
		a := open(t)(unique("a"))
		hall := unique("hall")
		if ok, err := a.Acquire(t.Context(), hall, time.Minute); err != nil || !ok {
			t.Fatalf("Acquire = %v, %v", ok, err)
		}
		if ok, err := a.SetOnlineCount(t.Context(), hall, 3); err != nil || !ok {
			t.Fatalf("SetOnlineCount = %v, %v", ok, err)
		}
		if ok, err := a.Acquire(t.Context(), hall, time.Minute); err != nil || !ok {
			t.Fatalf("再次 Acquire = %v, %v", ok, err)
		}
		if counts, err := a.OnlineCounts(t.Context(), []string{hall}); err != nil || !slices.Equal(counts, []int{0}) {
			t.Errorf("重新取得租约之后在线人数 = %v, %v，期望 0", counts, err)
		}
	})

	t.Run("只有 owner 能释放租约", func(t *testing.T) {
		node := open(t)
		aID := unique("a")
		a, b := node(aID), node(unique("b"))
		hall := unique("hall")
		ctx := t.Context()
		if ok, err := a.Acquire(ctx, hall, time.Minute); err != nil || !ok {
			t.Fatalf("Acquire = %v, %v", ok, err)
		}
		if ok, err := a.SetOnlineCount(ctx, hall, 3); err != nil || !ok {
			t.Fatalf("SetOnlineCount = %v, %v", ok, err)
		}
		if err := b.Release(ctx, hall); err != nil {
			t.Fatal(err)
		}
		if owners, err := a.Owners(ctx, []string{hall}); err != nil || !slices.Equal(owners, []string{aID}) {
			t.Errorf("b 释放 a 的租约之后 owner = %q, %v，期望不变", owners, err)
		}
		if err := a.Release(ctx, hall); err != nil {
			t.Fatal(err)
		}
		if owners, err := a.Owners(ctx, []string{hall}); err != nil || !slices.Equal(owners, []string{""}) {
			t.Errorf("释放之后 owner = %q, %v，期望没有", owners, err)
		}
		if counts, err := a.OnlineCounts(ctx, []string{hall}); err != nil || !slices.Equal(counts, []int{0}) {
			t.Errorf("释放之后在线人数 = %v, %v，期望 0", counts, err)
		}
		if ok, err := a.Renew(ctx, hall, time.Minute); err != nil || ok {
			t.Errorf("续期已释放的租约 = %v, %v，期望 false", ok, err)
		}
		if ok, err := b.Acquire(ctx, hall, time.Minute); err != nil || !ok {
			t.Errorf("释放之后 b 取得租约 = %v, %v，期望 true", ok, err)
		}
	})

	t.Run("租约过期之后没有 owner", func(t *testing.T) {
		node := open(t)
		a, b := node(unique("a")), node(unique("b"))
		acquired, renewed := unique("acquired"), unique("renewed")
		ctx := t.Context()
		// 100ms 的租约上只断言过期之后的结果。还没过期的断言要看下一次调用能不能在 100ms 内完成，会偶发失败。
		// 所以 renewed 先用长租约写入在线人数，再续期成 100ms。
		if ok, err := a.Acquire(ctx, acquired, 100*time.Millisecond); err != nil || !ok {
			t.Fatalf("Acquire = %v, %v", ok, err)
		}
		if ok, err := a.Acquire(ctx, renewed, time.Minute); err != nil || !ok {
			t.Fatalf("Acquire = %v, %v", ok, err)
		}
		if ok, err := a.SetOnlineCount(ctx, renewed, 2); err != nil || !ok {
			t.Fatalf("SetOnlineCount = %v, %v", ok, err)
		}
		if ok, err := a.Renew(ctx, renewed, 100*time.Millisecond); err != nil || !ok {
			t.Fatalf("Renew = %v, %v", ok, err)
		}
		time.Sleep(200 * time.Millisecond)
		rooms := []string{acquired, renewed}
		if owners, err := b.Owners(ctx, rooms); err != nil || !slices.Equal(owners, []string{"", ""}) {
			t.Errorf("过期之后 owner = %q, %v，期望都没有", owners, err)
		}
		if counts, err := b.OnlineCounts(ctx, rooms); err != nil || !slices.Equal(counts, []int{0, 0}) {
			t.Errorf("过期之后在线人数 = %v, %v，期望都是 0", counts, err)
		}
		if ok, err := a.Renew(ctx, acquired, time.Minute); err != nil || ok {
			t.Errorf("续期已过期的租约 = %v, %v，期望 false", ok, err)
		}
		if ok, err := a.SetOnlineCount(ctx, renewed, 3); err != nil || ok {
			t.Errorf("在已过期的租约上写入在线人数 = %v, %v，期望 false", ok, err)
		}
		if ok, err := b.Acquire(ctx, renewed, time.Minute); err != nil || !ok {
			t.Errorf("过期之后 b 取得租约 = %v, %v，期望 true", ok, err)
		}
	})
}

func runLivenessContract(t *testing.T, open func(t *testing.T) func(string) ClusterNode) {
	t.Run("刷新存活键时报告它之前是否存在", func(t *testing.T) {
		a := open(t)(unique("a"))
		if existed, err := a.Heartbeat(t.Context(), time.Minute); err != nil || existed {
			t.Errorf("第一次刷新 = %v, %v，期望存活键之前不存在", existed, err)
		}
		if existed, err := a.Heartbeat(t.Context(), time.Minute); err != nil || !existed {
			t.Errorf("第二次刷新 = %v, %v，期望存活键已经存在", existed, err)
		}
	})

	t.Run("没有刷新过或存活键已经过期的实例算作过期", func(t *testing.T) {
		node := open(t)
		aID, bID, cID := unique("a"), unique("b"), unique("c")
		a, b := node(aID), node(bID)
		ctx := t.Context()
		for _, n := range []ClusterNode{a, b} {
			if _, err := n.Heartbeat(ctx, time.Minute); err != nil {
				t.Fatal(err)
			}
		}
		if expired, err := a.Expired(ctx, []string{aID, bID, cID}); err != nil || !slices.Equal(expired, []string{cID}) {
			t.Errorf("Expired = %q, %v，期望只有没有刷新过存活键的 c", expired, err)
		}
		// 和租约过期的用例一样，100ms 的存活键上只断言过期之后的结果。
		if _, err := b.Heartbeat(ctx, 100*time.Millisecond); err != nil {
			t.Fatal(err)
		}
		time.Sleep(200 * time.Millisecond)
		if expired, err := a.Expired(ctx, []string{aID, bID, cID}); err != nil || !slices.Equal(expired, []string{bID, cID}) {
			t.Errorf("b 的存活键过期之后 Expired = %q, %v，期望 [b c]", expired, err)
		}
		if existed, err := b.Heartbeat(ctx, time.Minute); err != nil || existed {
			t.Errorf("过期之后刷新 = %v, %v，期望存活键之前不存在", existed, err)
		}
		if expired, err := a.Expired(ctx, nil); err != nil || len(expired) != 0 {
			t.Errorf("Expired(nil) = %q, %v", expired, err)
		}
	})
}
