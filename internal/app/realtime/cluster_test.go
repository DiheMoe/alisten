package realtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/app/port/porttest"
	"github.com/dihemoe/alisten/internal/domain/member"
	"github.com/dihemoe/alisten/internal/domain/room"
)

type clusterFixture struct {
	store *porttest.Store
	redis *porttest.Redis
	a, b  *instance
	room  string
}

func newClusterFixture(t *testing.T, snapshotsOfA port.RoomSnapshots, store *porttest.Store) *clusterFixture {
	t.Helper()
	songs := newSongs()
	songs.add(sunny, sunnyM, sunnyP)
	redis := porttest.NewRedis()
	f := &clusterFixture{store: store, redis: redis}
	f.a = startInstance(t, redis, "a", snapshotsOfA, store, songs)
	f.b = startInstance(t, redis, "b", store, store, songs)
	synctest.Wait()
	f.room = createRoom(t, store, room.Persistent)
	if _, err := f.a.gateway.Do(t.Context(), f.room, alice, room.Sync{}); err != nil {
		t.Fatal(err)
	}
	if owner := f.a.owner(t, f.room); owner != "a" {
		t.Fatalf("房间的 owner 是 %q，期望 a", owner)
	}
	return f
}

func TestCommandsReachOwnerOnAnotherInstance(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		f := newClusterFixture(t, store, store)

		onB := f.b.join(t, f.room, bob)
		checkReceived(t, onB, "hello bob 1/1 [bob]")
		onA := f.a.join(t, f.room, alice)
		checkReceived(t, onA, "hello alice 1/2 [bob alice]")
		checkReceived(t, onB, "1/2 room.MembersChanged")
		if n := f.b.online(t, f.room); n != 2 {
			t.Errorf("在线人数 = %d，期望 2", n)
		}

		onB.Begin()
		if err := f.b.gateway.Command(t.Context(), onB, "c1", room.Enqueue{Track: sunny, Meta: sunnyM}); err != nil {
			t.Fatal(err)
		}
		checkReceived(t, onB,
			"1/3 room.Activity",
			"1/4 room.QueueChanged",
			"reply c1 room.EnqueueResult",
			"1/5 room.QueueChanged",
			"1/6 room.PlaybackChanged",
		)
		checkReceived(t, onA, "1/3 room.Activity", "1/4 room.QueueChanged", "1/5 room.QueueChanged", "1/6 room.PlaybackChanged")
		if owner := f.b.owner(t, f.room); owner != "a" {
			t.Errorf("房间的 owner 是 %q，期望一直是 a", owner)
		}
		if n := f.b.host.RoomsOwned(); n != 0 {
			t.Errorf("b 的 RoomsOwned = %d，期望 0", n)
		}
		time.Sleep(2 * aliveTTL)
		res, err := f.a.gateway.Do(t.Context(), f.room, onA.Member(), room.Sync{})
		if err != nil {
			t.Fatal(err)
		}
		if names := sessionNames(res.(room.SyncResult).View); !slices.Equal(names, []string{"bob", "alice"}) {
			t.Errorf("30 秒之后的在线列表 = %q，期望两个会话都在", names)
		}
	})
}

func TestOwnerOutOfReachIsTakenOver(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		f := newClusterFixture(t, store, store)
		onB := f.b.join(t, f.room, bob)
		received(t, onB)

		f.a.node.SetDown(true)
		start := time.Now()
		synctest.Wait()
		if _, err := f.b.gateway.Do(t.Context(), f.room, bob, room.Sync{}); !errors.Is(err, ErrUnavailable) {
			t.Errorf("owner 不可达时的错误 = %v，期望 ErrUnavailable", err)
		}
		if time.Since(start) != 0 {
			t.Error("owner 没有订阅它的频道时应立即失败")
		}
		for f.b.owner(t, f.room) != "b" {
			if time.Since(start) > leaseTTL+reconcileInterval+reconcileJitter {
				t.Fatalf("%v 之后 b 仍没有接管房间", time.Since(start))
			}
			time.Sleep(100 * time.Millisecond)
			synctest.Wait()
		}
		if elapsed := time.Since(start); elapsed < leaseTTL-renewInterval {
			t.Errorf("%v 之后就接管了房间，租约还没有过期", elapsed)
		}
		if got := received(t, onB); !slices.Contains(got, "2/1 room.MembersChanged") {
			t.Errorf("b 的会话收到 %q，期望新任期的在线列表", got)
		}
		res, err := f.b.gateway.Do(t.Context(), f.room, bob, room.Sync{})
		if err != nil {
			t.Fatal(err)
		}
		if view := res.(room.SyncResult).View; view.Term != 2 || len(view.Sessions) != 1 {
			t.Errorf("接管之后的快照：任期 %d，在线 %d，期望任期 2、只有 b 的会话", view.Term, len(view.Sessions))
		}
		f.a.node.SetDown(false)
	})
}

func TestOwnerSubscriptionLossHandsOver(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		f := newClusterFixture(t, store, store)
		onA := f.a.join(t, f.room, alice)
		onB := f.b.join(t, f.room, bob)
		received(t, onA)
		received(t, onB)

		f.a.node.Disconnect()
		synctest.Wait()
		checkReceived(t, onA, fmt.Sprintf("closed %d", CloseRestart))
		if owner := f.b.owner(t, f.room); owner != "b" {
			t.Errorf("a 的订阅断开之后 owner 是 %q，期望 b", owner)
		}
		got := received(t, onB)
		if !slices.Contains(got, "2/1 room.MembersChanged") {
			t.Errorf("b 的会话收到 %q，期望新任期的在线列表", got)
		}
		other := createRoom(t, store, room.Persistent)
		if _, err := f.a.gateway.Do(t.Context(), other, onA.Member(), room.Sync{}); err != nil {
			t.Errorf("a 重新订阅之后接管新房间失败：%v", err)
		}
	})
}

type flakyBus struct {
	port.Bus
	failing atomic.Bool
}

func (b *flakyBus) SubscribeInstance(ctx context.Context) error {
	if b.failing.Load() {
		return porttest.ErrRedisDown
	}
	return b.Bus.SubscribeInstance(ctx)
}

func newFlakyBusInstance(t *testing.T, store *porttest.Store, logger *slog.Logger) (*Host, *Gateway, *porttest.Node, *flakyBus) {
	t.Helper()
	songs := newSongs()
	node := porttest.NewRedis().Node("a")
	bus := &flakyBus{Bus: node}
	h, g := New(store, store, songs.music, songs.preparer, Cluster{Instance: "a", Leases: node, Liveness: node, Bus: bus}, logger)
	return h, g, node, bus
}

// 本实例的频道订阅生效之前不接管房间：此时收不到其他实例转发来的输入，接管之后房间的回复也可能漏掉。
func TestNoTakeoverBeforeInstanceChannelIsSubscribed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		h, g, node, bus := newFlakyBusInstance(t, store, slog.New(slog.DiscardHandler))
		bus.failing.Store(true)
		ctx, stop := context.WithCancel(context.Background())
		var wait func()
		var startErr error
		started := make(chan struct{})
		go func() {
			defer close(started)
			wait, startErr = Start(ctx, h, g)
		}()
		t.Cleanup(func() {
			_ = h.Shutdown(context.Background())
			_ = g.Shutdown(context.Background())
			stop()
			<-started
			if wait != nil {
				wait()
			}
		})
		id := createRoom(t, store, room.Persistent)
		synctest.Wait()

		if _, err := g.Do(t.Context(), id, alice, room.Sync{}); !errors.Is(err, ErrUnavailable) {
			t.Errorf("订阅之前接管房间的请求 = %v，期望 ErrUnavailable", err)
		}
		owners, err := node.Owners(t.Context(), []string{id})
		if err != nil {
			t.Fatal(err)
		}
		if term := store.Term(id); owners[0] != "" || term != 0 {
			t.Errorf("订阅之前房间的 owner = %q、任期 = %d，期望没有接管", owners[0], term)
		}

		bus.failing.Store(false)
		time.Sleep(retryInterval)
		<-started
		if startErr != nil {
			t.Fatal(startErr)
		}
		if _, err := g.Do(t.Context(), id, alice, room.Sync{}); err != nil {
			t.Errorf("订阅生效之后的请求失败：%v", err)
		}
	})
}

func TestNoTakeoverAfterLossUntilInstanceChannelIsSubscribedAgain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		h, g, node, bus := newFlakyBusInstance(t, store, slog.New(slog.DiscardHandler))
		ctx, stop := context.WithCancel(context.Background())
		wait, err := Start(ctx, h, g)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = h.Shutdown(context.Background())
			_ = g.Shutdown(context.Background())
			stop()
			wait()
		})
		synctest.Wait()
		held, fresh := createRoom(t, store, room.Persistent), createRoom(t, store, room.Persistent)
		if _, err := g.Do(t.Context(), held, alice, room.Sync{}); err != nil {
			t.Fatal(err)
		}

		bus.failing.Store(true)
		node.Disconnect()
		synctest.Wait()
		for range 3 {
			for _, id := range []string{held, fresh} {
				if _, err := g.Do(t.Context(), id, alice, room.Sync{}); !errors.Is(err, ErrUnavailable) {
					t.Errorf("重新订阅之前接管房间的请求 = %v，期望 ErrUnavailable", err)
				}
			}
			time.Sleep(retryInterval)
		}
		if n, termHeld, termFresh := h.RoomsOwned(), store.Term(held), store.Term(fresh); n != 0 || termHeld != 1 || termFresh != 0 {
			t.Errorf("重新订阅之前运行 %d 个房间，任期 %d 和 %d，期望释放了全部房间、没有再接管", n, termHeld, termFresh)
		}

		bus.failing.Store(false)
		time.Sleep(retryInterval)
		synctest.Wait()
		res, err := g.Do(t.Context(), held, alice, room.Sync{})
		if err != nil {
			t.Fatalf("重新订阅之后的请求失败：%v", err)
		}
		if view := res.(room.SyncResult).View; view.Term != 2 {
			t.Errorf("重新订阅之后接管房间的任期 = %d，期望 2", view.Term)
		}
	})
}

// 释放全部房间、断开全部会话会影响整个实例，所以必须记录日志。
func TestSubscriptionLossIsLogged(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		var logs logBuffer
		h, g, node, bus := newFlakyBusInstance(t, store, slog.New(slog.NewJSONHandler(&logs, nil)))
		ctx, stop := context.WithCancel(context.Background())
		wait, err := Start(ctx, h, g)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = h.Shutdown(context.Background())
			_ = g.Shutdown(context.Background())
			stop()
			wait()
		})
		synctest.Wait()

		bus.failing.Store(true)
		node.Disconnect()
		synctest.Wait()
		lost := logs.entries(t, "订阅连接断开过，释放全部房间并断开会话")
		if len(lost) != 1 || lost[0]["level"] != "WARN" {
			t.Errorf("订阅断开的日志 = %v，期望一条 WARN", lost)
		}
		if back := logs.entries(t, "订阅已恢复，重新开始接管房间"); len(back) != 0 {
			t.Errorf("还没有重新订阅就记了恢复的日志：%v", back)
		}

		bus.failing.Store(false)
		time.Sleep(retryInterval)
		synctest.Wait()
		back := logs.entries(t, "订阅已恢复，重新开始接管房间")
		if len(back) != 1 || back[0]["level"] != "INFO" {
			t.Errorf("重新订阅之后的日志 = %v，期望一条 INFO", back)
		}
	})
}

func TestSessionsOfStoppedGatewayLeave(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		f := newClusterFixture(t, store, store)
		onA := f.a.join(t, f.room, alice)
		f.b.join(t, f.room, bob)
		received(t, onA)

		f.b.node.SetDown(true)
		time.Sleep(aliveTTL + reconcileInterval + reconcileJitter)
		synctest.Wait()
		res, err := f.a.gateway.Do(t.Context(), f.room, onA.Member(), room.Sync{})
		if err != nil {
			t.Fatal(err)
		}
		if sessions := res.(room.SyncResult).View.Sessions; len(sessions) != 1 || sessions[0].Member.ID != onA.Member().ID {
			t.Errorf("在线列表 = %+v，期望只剩 a 的会话", sessions)
		}
		f.b.node.SetDown(false)
	})
}

func TestMemberUpdateReachesOtherInstances(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		f := newClusterFixture(t, store, store)
		onA := f.a.join(t, f.room, alice)
		onB := f.b.join(t, f.room, bob)
		received(t, onA)
		received(t, onB)

		renamed := member.Member{ID: onB.Member().ID, Name: "乙"}
		if err := f.a.gateway.MemberUpdated(t.Context(), renamed); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if onB.Member() != renamed {
			t.Errorf("b 上会话记录的资料 = %+v，期望 %+v", onB.Member(), renamed)
		}
		checkReceived(t, onA, "1/3 room.MembersChanged")
		res, err := f.a.gateway.Do(t.Context(), f.room, onA.Member(), room.Sync{})
		if err != nil {
			t.Fatal(err)
		}
		if names := sessionNames(res.(room.SyncResult).View); !slices.Equal(names, []string{"alice", "乙"}) {
			t.Errorf("在线列表 = %q，期望新的昵称", names)
		}
	})
}

type flakyLiveness struct {
	port.Liveness
	fail atomic.Bool
}

func (l *flakyLiveness) Heartbeat(ctx context.Context, ttl time.Duration) (bool, error) {
	if l.fail.Load() {
		return false, porttest.ErrRedisDown
	}
	return l.Liveness.Heartbeat(ctx, ttl)
}

func TestGatewayRejoinsAfterItsAliveKeyExpired(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		songs := newSongs()
		redis := porttest.NewRedis()
		a := startInstance(t, redis, "a", store, store, songs)
		node := redis.Node("b")
		liveness := &flakyLiveness{Liveness: node}
		h, g := New(store, store, songs.music, songs.preparer, Cluster{Instance: "b", Leases: node, Liveness: liveness, Bus: node}, slog.New(slog.DiscardHandler))
		ctx, stop := context.WithCancel(context.Background())
		wait, err := Start(ctx, h, g)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = h.Shutdown(context.Background())
			_ = g.Shutdown(context.Background())
			stop()
			wait()
		})
		synctest.Wait()
		id := createRoom(t, store, room.Persistent)
		if _, err := a.gateway.Do(t.Context(), id, alice, room.Sync{}); err != nil {
			t.Fatal(err)
		}
		s, err := g.Join(t.Context(), id, bob)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { g.Leave(s) })
		synctest.Wait()

		online := func() []string {
			res, err := a.gateway.Do(t.Context(), id, alice, room.Sync{})
			if err != nil {
				t.Fatal(err)
			}
			return sessionNames(res.(room.SyncResult).View)
		}
		liveness.fail.Store(true)
		time.Sleep(aliveTTL + reconcileInterval + reconcileJitter)
		synctest.Wait()
		if names := online(); len(names) != 0 {
			t.Errorf("b 的存活键过期之后在线列表 = %q，期望为空", names)
		}
		liveness.fail.Store(false)
		time.Sleep(heartbeatInterval)
		synctest.Wait()
		if names := online(); !slices.Equal(names, []string{"bob"}) {
			t.Errorf("b 恢复刷新之后在线列表 = %q，期望 b 的会话重新加入", names)
		}
	})
}

type flakyOwners struct {
	port.Leases
	failing atomic.Bool
}

func (l *flakyOwners) Owners(ctx context.Context, roomIDs []string) ([]string, error) {
	if l.failing.Load() {
		return nil, porttest.ErrRedisDown
	}
	return l.Leases.Owners(ctx, roomIDs)
}

type flakyForwards struct {
	port.Bus
	failing atomic.Bool
	calls   atomic.Int32
}

func (b *flakyForwards) SendInput(ctx context.Context, instanceID string, in port.RoomInput) (bool, error) {
	b.calls.Add(1)
	if b.failing.Load() {
		return false, porttest.ErrRedisDown
	}
	return b.Bus.SendInput(ctx, instanceID, in)
}

func startFlakyInstance(t *testing.T, redis *porttest.Redis, id string, snapshots port.RoomSnapshots, store *porttest.Store, songs *songs) (*instance, *flakyOwners, *flakyForwards) {
	t.Helper()
	node := redis.Node(id)
	leases, bus := &flakyOwners{Leases: node}, &flakyForwards{Bus: node}
	h, g := New(snapshots, store, songs.music, songs.preparer, Cluster{Instance: id, Leases: leases, Liveness: node, Bus: bus}, slog.New(slog.DiscardHandler))
	ctx, stop := context.WithCancel(context.Background())
	wait, err := Start(ctx, h, g)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = h.Shutdown(context.Background())
		_ = g.Shutdown(context.Background())
		stop()
		wait()
	})
	return &instance{id: id, node: node, host: h, gateway: g}, leases, bus
}

func TestLostLeaveIsRetried(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		songs := newSongs()
		redis := porttest.NewRedis()
		a := startInstance(t, redis, "a", store, store, songs)
		b, owners, _ := startFlakyInstance(t, redis, "b", store, store, songs)
		synctest.Wait()
		id := createRoom(t, store, room.Ephemeral)
		if _, err := a.gateway.Do(t.Context(), id, alice, room.Sync{}); err != nil {
			t.Fatal(err)
		}
		s := b.join(t, id, bob)
		if n := a.online(t, id); n != 1 {
			t.Fatalf("加入之后在线人数 = %d，期望 1", n)
		}

		owners.failing.Store(true)
		b.gateway.Leave(s)
		time.Sleep(3 * time.Second)
		owners.failing.Store(false)
		time.Sleep(time.Minute)
		synctest.Wait()
		if n := a.online(t, id); n != 0 {
			t.Errorf("Leave 失败、Redis 恢复一分钟之后在线人数 = %d，期望会话已经离开", n)
		}
		time.Sleep(5 * time.Minute)
		synctest.Wait()
		if _, err := a.gateway.Do(t.Context(), id, alice, room.Sync{}); !errors.Is(err, port.ErrRoomNotFound) {
			t.Errorf("无人的临时房间过了 5 分钟之后的请求 = %v，期望房间已经关闭（ErrRoomNotFound）", err)
		}
	})
}

func TestFailedRejoinIsRetried(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := &failingStore{Store: porttest.New()}
		songs := newSongs()
		redis := porttest.NewRedis()
		a := startInstance(t, redis, "a", store, store.Store, songs)
		b, owners, _ := startFlakyInstance(t, redis, "b", store.Store, store.Store, songs)
		synctest.Wait()
		id := createRoom(t, store.Store, room.Persistent)
		a.join(t, id, alice)
		b.join(t, id, bob)

		owners.failing.Store(true)
		store.fail.Store(true)
		_, _ = a.gateway.Do(t.Context(), id, alice, room.SetPlayMode{Mode: room.Random})
		store.fail.Store(false)
		synctest.Wait()
		if _, err := a.gateway.Do(t.Context(), id, alice, room.Sync{}); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		time.Sleep(3 * time.Second)
		owners.failing.Store(false)
		time.Sleep(time.Minute)
		synctest.Wait()

		res, err := a.gateway.Do(t.Context(), id, alice, room.Sync{})
		if err != nil {
			t.Fatal(err)
		}
		view := res.(room.SyncResult).View
		if names := sessionNames(view); view.Term != 2 || !slices.Equal(names, []string{"alice", "bob"}) {
			t.Errorf("Redis 恢复一分钟之后：任期 %d，在线 %q，期望任期 2、两个会话都在线", view.Term, names)
		}
	})
}

func TestRetriedJoinIsDroppedWhenSessionEnds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := &failingStore{Store: porttest.New()}
		songs := newSongs()
		redis := porttest.NewRedis()
		a := startInstance(t, redis, "a", store, store.Store, songs)
		b, owners, _ := startFlakyInstance(t, redis, "b", store.Store, store.Store, songs)
		synctest.Wait()
		id := createRoom(t, store.Store, room.Persistent)
		onA := a.join(t, id, alice)
		onB := b.join(t, id, bob)
		received(t, onA)
		received(t, onB)

		owners.failing.Store(true)
		store.fail.Store(true)
		_, _ = a.gateway.Do(t.Context(), id, alice, room.SetPlayMode{Mode: room.Random})
		store.fail.Store(false)
		synctest.Wait()
		if _, err := a.gateway.Do(t.Context(), id, alice, room.Sync{}); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		b.gateway.Leave(onB)
		time.Sleep(3 * time.Second)
		owners.failing.Store(false)
		time.Sleep(time.Minute)
		synctest.Wait()

		checkReceived(t, onA, "2/1 room.MembersChanged")
	})
}

func TestFailedMemberUpdateIsRetried(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		songs := newSongs()
		redis := porttest.NewRedis()
		a := startInstance(t, redis, "a", store, store, songs)
		b, owners, _ := startFlakyInstance(t, redis, "b", store, store, songs)
		synctest.Wait()
		id := createRoom(t, store, room.Persistent)
		b.join(t, id, bob)

		owners.failing.Store(true)
		if err := a.gateway.MemberUpdated(t.Context(), member.Member{ID: bob.ID, Name: "乙"}); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		time.Sleep(3 * time.Second)
		owners.failing.Store(false)
		time.Sleep(time.Minute)
		synctest.Wait()

		res, err := a.gateway.Do(t.Context(), id, alice, room.Sync{})
		if err != nil {
			t.Fatal(err)
		}
		if names := sessionNames(res.(room.SyncResult).View); !slices.Equal(names, []string{"乙"}) {
			t.Errorf("Redis 恢复一分钟之后在线列表 = %q，期望新的昵称", names)
		}
	})
}

func TestMemberUpdateRetryCarriesLatestProfile(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		songs := newSongs()
		redis := porttest.NewRedis()
		a := startInstance(t, redis, "a", store, store, songs)
		b, owners, _ := startFlakyInstance(t, redis, "b", store, store, songs)
		synctest.Wait()
		id := createRoom(t, store, room.Persistent)
		b.join(t, id, bob)

		owners.failing.Store(true)
		if err := a.gateway.MemberUpdated(t.Context(), member.Member{ID: bob.ID, Name: "乙"}); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		owners.failing.Store(false)
		if err := a.gateway.MemberUpdated(t.Context(), member.Member{ID: bob.ID, Name: "丙"}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Minute)
		synctest.Wait()

		res, err := a.gateway.Do(t.Context(), id, alice, room.Sync{})
		if err != nil {
			t.Fatal(err)
		}
		if names := sessionNames(res.(room.SyncResult).View); !slices.Equal(names, []string{"丙"}) {
			t.Errorf("在线列表 = %q，期望最新的昵称", names)
		}
	})
}

func TestPresenceRetriesStopAtShutdown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		songs := newSongs()
		redis := porttest.NewRedis()
		a := startInstance(t, redis, "a", store, store, songs)
		b, _, forwards := startFlakyInstance(t, redis, "b", store, store, songs)
		synctest.Wait()
		id := createRoom(t, store, room.Persistent)
		if _, err := a.gateway.Do(t.Context(), id, alice, room.Sync{}); err != nil {
			t.Fatal(err)
		}
		s := b.join(t, id, bob)

		forwards.failing.Store(true)
		forwards.calls.Store(0)
		b.gateway.Leave(s)
		time.Sleep(time.Minute)
		synctest.Wait()
		before := forwards.calls.Load()
		if before < 3 {
			t.Errorf("Leave 发不出去之后的一分钟内尝试了 %d 次，期望在后台不断重试", before)
		}
		if err := b.gateway.Shutdown(t.Context()); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Minute)
		synctest.Wait()
		if after := forwards.calls.Load(); after != before {
			t.Errorf("网关停止之后又尝试了 %d 次，期望不再重试", after-before)
		}
		// 如果还有重试的 goroutine，让它发送成功后退出，以免 bubble 无法结束。
		forwards.failing.Store(false)
	})
}

func sessionNames(v room.View) []string {
	names := make([]string, len(v.Sessions))
	for i, s := range v.Sessions {
		names[i] = s.Member.Name
	}
	return names
}

func TestRevokeAndDeleteReachOtherInstances(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		f := newClusterFixture(t, store, store)
		deleted := createRoom(t, store, room.Persistent)
		revoked := f.b.join(t, f.room, bob)
		gone := f.b.join(t, deleted, alice)
		received(t, revoked)
		received(t, gone)

		if err := f.a.gateway.Revoke(t.Context(), f.room); err != nil {
			t.Fatal(err)
		}
		checkReceived(t, revoked, fmt.Sprintf("closed %d", CloseRevoked))
		if err := f.a.gateway.Delete(t.Context(), deleted); err != nil {
			t.Fatal(err)
		}
		checkReceived(t, gone, fmt.Sprintf("closed %d", CloseRoomGone))
	})
}

type staleLeases struct {
	port.Leases
	stale atomic.Bool
}

func (l *staleLeases) Owners(ctx context.Context, roomIDs []string) ([]string, error) {
	if l.stale.CompareAndSwap(true, false) {
		return make([]string, len(roomIDs)), nil
	}
	return l.Leases.Owners(ctx, roomIDs)
}

func TestCommandFollowsOwnerAfterLosingTakeover(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		f := newClusterFixture(t, store, store)
		node := f.redis.Node("c")
		leases := &staleLeases{Leases: node}
		songs := newSongs()
		h, g := New(store, store, songs.music, songs.preparer, Cluster{Instance: "c", Leases: leases, Liveness: node, Bus: node}, slog.New(slog.DiscardHandler))
		ctx, stop := context.WithCancel(context.Background())
		wait, err := Start(ctx, h, g)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = h.Shutdown(context.Background())
			_ = g.Shutdown(context.Background())
			stop()
			wait()
		})
		synctest.Wait()

		leases.stale.Store(true)
		if _, err := g.Do(t.Context(), f.room, bob, room.Chat{Text: "你好"}); err != nil {
			t.Errorf("命令失败：%v", err)
		}
		if owner := f.a.owner(t, f.room); owner != "a" {
			t.Errorf("房间的 owner 是 %q，期望仍是 a", owner)
		}
	})
}

func TestConcurrentTakeoverHasOneOwner(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		f := newClusterFixture(t, store, store)
		fresh := createRoom(t, store, room.Persistent)
		errs := make(chan error, 2)
		for _, in := range []*instance{f.a, f.b} {
			go func() {
				_, err := in.gateway.Do(context.Background(), fresh, bob, room.Chat{Text: in.id})
				errs <- err
			}()
		}
		for range 2 {
			if err := <-errs; err != nil {
				t.Errorf("命令失败：%v", err)
			}
		}
		if term := store.Term(fresh); term != 1 {
			t.Errorf("任期 = %d，期望只被接管一次", term)
		}
		if n := f.a.host.RoomsOwned() + f.b.host.RoomsOwned(); n != 2 {
			t.Errorf("两个实例运行的房间共 %d 个，期望 2 个", n)
		}
	})
}

func TestRemoteNotFoundMeansRoomNotFound(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		songs := newSongs()
		redis := porttest.NewRedis()
		a := startInstance(t, redis, "a", store, store, songs)
		b := startInstance(t, redis, "b", store, store, songs)
		synctest.Wait()
		// 房间的数据已经删除，租约却还属于 a：a 接管时找不到房间。
		missing := "01a15778-1911-7a28-8b2e-e08c2a25f388"
		if ok, err := a.node.Acquire(t.Context(), missing, leaseTTL); err != nil || !ok {
			t.Fatalf("a 取得租约 = %v, %v", ok, err)
		}
		if _, err := b.gateway.Do(t.Context(), missing, bob, room.Sync{}); !errors.Is(err, port.ErrRoomNotFound) {
			t.Errorf("经 b 发出的请求的错误 = %v，期望 ErrRoomNotFound", err)
		}
	})
}

func TestStartRefreshesAliveKeyAtOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		redis := porttest.NewRedis()
		store := porttest.New()
		startInstance(t, redis, "a", store, store, newSongs())
		if expired, err := redis.Node("b").Expired(t.Context(), []string{"a"}); err != nil || len(expired) != 0 {
			t.Errorf("Start 之后 a 的存活键过期 = %v, %v，期望已经刷新", expired, err)
		}
	})
}
