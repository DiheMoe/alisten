package realtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/app/port/porttest"
	"github.com/dihemoe/alisten/internal/domain/member"
	"github.com/dihemoe/alisten/internal/domain/room"
)

var (
	carol = member.Member{ID: "00000000-0000-7000-8000-00000000000c", Name: "carol"}
	robot = member.Member{ID: "00000000-0000-7000-8000-0000000000b0", Name: "bot"}
)

// 只能在 synctest 的 bubble 中调用。
func (in *instance) observe(t *testing.T, roomID string, m member.Member, bot bool, credential string) *Observer {
	t.Helper()
	o, err := in.gateway.Observe(t.Context(), roomID, m, bot, credential)
	if err != nil {
		t.Fatalf("%s 打开事件流：%v", m.Name, err)
	}
	t.Cleanup(func() { in.gateway.Unobserve(o) })
	synctest.Wait()
	return o
}

func checkObserved(t *testing.T, o *Observer, want ...string) {
	t.Helper()
	if got := received(t, o); !slices.Equal(got, want) {
		t.Errorf("%s 的事件流收到：\n%s\n期望：\n%s", o.member.Name, strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestObserverGetsSnapshotThenEvents(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		f := newGatewayFixture(t, store, store)
		id := createRoom(t, store, room.Persistent)
		f.join(t, id, alice)
		o := f.observe(t, id, robot, true, "key")
		if _, err := f.gateway.Do(t.Context(), id, robot, room.Chat{Text: "你好"}); err != nil {
			t.Fatal(err)
		}
		checkObserved(t, o, "snapshot 1/1 [alice]", "1/2 room.ChatPosted")
	})
}

func TestObserverIsNotASession(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		f := newGatewayFixture(t, store, store)
		id := createRoom(t, store, room.Persistent)
		o := f.observe(t, id, robot, true, "key")
		for _, m := range []member.Member{alice, bob, carol} {
			f.join(t, id, m)
		}
		if got := received(t, o); len(got) == 0 || got[0] != "snapshot 1/0 []" {
			t.Errorf("事件流收到 %q，期望以空的在线列表开始", got)
		}
		if n := f.online(t, id); n != 3 {
			t.Errorf("在线人数 = %d，期望只有 3 个会话", n)
		}
		if _, err := f.gateway.Do(t.Context(), id, alice, room.Enqueue{Track: sunny, Meta: sunnyM}); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		// 3 个会话时 1 票就切歌，4 个时要 2 票。
		res, err := f.gateway.Do(t.Context(), id, alice, room.VoteSkip{})
		if err != nil {
			t.Fatal(err)
		}
		if vote := res.(room.VoteResult); vote.Required != 1 || !vote.Skipped {
			t.Errorf("3 个会话和 1 个事件流时投票的结果 = %+v，期望只要 1 票就切歌", vote)
		}
	})
}

func TestRoomNoticesCloseObservers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		f := newGatewayFixture(t, store, store)
		locked, deleted, other := createRoom(t, store, room.Persistent), createRoom(t, store, room.Persistent), createRoom(t, store, room.Persistent)
		member, bot := f.observe(t, locked, alice, false, "alice"), f.observe(t, locked, robot, true, "key")
		gone, restarted := f.observe(t, deleted, alice, false, "alice"), f.observe(t, other, robot, true, "key")
		received(t, bot)

		if err := f.gateway.Revoke(t.Context(), locked); err != nil {
			t.Fatal(err)
		}
		checkObserved(t, member, fmt.Sprintf("closed %d", CloseRevoked))
		checkObserved(t, bot)
		if err := f.gateway.Delete(t.Context(), deleted); err != nil {
			t.Fatal(err)
		}
		checkObserved(t, gone, fmt.Sprintf("closed %d", CloseRoomGone))
		f.node.Disconnect()
		checkObserved(t, restarted, fmt.Sprintf("closed %d", CloseRestart))
	})
}

func TestAPIKeyResetClosesOnlyBotObservers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		f := newClusterFixture(t, store, store)
		session := f.b.join(t, f.room, alice)
		member, bot := f.b.observe(t, f.room, bob, false, "bob"), f.b.observe(t, f.room, robot, true, "key")
		received(t, session)
		received(t, member)

		if err := f.a.gateway.RevokeAPIKey(t.Context(), f.room); err != nil {
			t.Fatal(err)
		}
		checkObserved(t, bot, fmt.Sprintf("closed %d", CloseRevoked))
		checkReceived(t, session)
		checkObserved(t, member)
	})
}

func TestObserverGetsNewSnapshotWhenRunChanges(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := &failingStore{Store: porttest.New()}
		f := newGatewayFixture(t, store, store.Store)
		id := createRoom(t, store.Store, room.Persistent)
		o := f.observe(t, id, robot, true, "key")
		if _, err := f.gateway.Do(t.Context(), id, alice, room.Chat{Text: "你好"}); err != nil {
			t.Fatal(err)
		}
		checkObserved(t, o, "snapshot 1/0 []", "1/1 room.ChatPosted")

		store.fail.Store(true)
		if _, err := f.gateway.Do(t.Context(), id, alice, room.SetPlayMode{Mode: room.Random}); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("落盘失败时的错误 = %v，期望 ErrUnavailable", err)
		}
		store.fail.Store(false)
		// 放弃房间的那次运行在自己的 goroutine 里收尾，收尾完之前到达的请求会得到 ErrUnavailable；等它收尾完，Sync 才会重新接管房间。
		synctest.Wait()
		if _, err := f.gateway.Do(t.Context(), id, alice, room.Sync{}); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if _, err := f.gateway.Do(t.Context(), id, alice, room.Chat{Text: "又来了"}); err != nil {
			t.Fatal(err)
		}
		checkObserved(t, o, "snapshot 2/0 []", "2/1 room.ChatPosted")
	})
}

// 房间的事件发布卡住时，房间停在这一批 effect 上，之后的输入都在排队。
type heldEmits struct {
	port.Bus
	holding atomic.Bool
	release chan struct{}
}

func (b *heldEmits) PublishRoom(ctx context.Context, roomID string, m port.RoomMessage) error {
	if _, ok := m.(port.Emitted); ok && b.holding.Load() {
		<-b.release
	}
	return b.Bus.PublishRoom(ctx, roomID, m)
}

// a 的租约已经丢失，但它还没有发现：排在它那里的快照请求在 b 接管、发出变更通知之后才得到回复，这份快照已经过时。
func TestObserverSkipsSnapshotOfEarlierRun(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		songs := newSongs()
		redis := porttest.NewRedis()
		node := redis.Node("a")
		bus := &heldEmits{Bus: node, release: make(chan struct{})}
		h, g := New(store, store, songs.music, songs.preparer, Cluster{Instance: "a", Leases: node, Liveness: node, Bus: bus}, slog.New(slog.DiscardHandler))
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
		b := startInstance(t, redis, "b", store, store, songs)
		synctest.Wait()
		id := createRoom(t, store, room.Persistent)
		if _, err := g.Do(t.Context(), id, alice, room.Sync{}); err != nil {
			t.Fatal(err)
		}

		bus.holding.Store(true)
		go func() { _, _ = g.Do(context.Background(), id, alice, room.Chat{Text: "你好"}) }()
		synctest.Wait()
		var o *Observer
		opened := make(chan error, 1)
		go func() {
			var err error
			o, err = b.gateway.Observe(context.Background(), id, robot, true, "key")
			opened <- err
		}()
		synctest.Wait()
		redis.Flush()
		if _, err := b.gateway.Do(t.Context(), id, bob, room.Sync{}); err != nil {
			t.Fatal(err)
		}
		close(bus.release)
		if err := <-opened; err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { b.gateway.Unobserve(o) })
		checkObserved(t, o, "snapshot 2/0 []")
	})
}

func TestObserverLimitPerRoom(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		f := newGatewayFixture(t, store, store)
		full, other := createRoom(t, store, room.Persistent), createRoom(t, store, room.Persistent)
		first, err := f.gateway.Observe(t.Context(), full, robot, true, "key")
		if err != nil {
			t.Fatal(err)
		}
		for range observerLimit - 1 {
			f.observe(t, full, robot, true, "key")
		}
		if _, err := f.gateway.Observe(t.Context(), full, alice, false, "alice"); !errors.Is(err, ErrTooManyObservers) {
			t.Errorf("第 %d 个事件流的错误 = %v，期望 ErrTooManyObservers", observerLimit+1, err)
		}
		f.observe(t, other, alice, false, "alice")
		f.gateway.Unobserve(first)
		f.observe(t, full, alice, false, "alice")
	})
}

func TestFullRoomReplacesOldestObserverOfSameCredential(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		f := newGatewayFixture(t, store, store)
		id := createRoom(t, store, room.Persistent)
		alices := f.observe(t, id, alice, false, "alice")
		oldest := f.observe(t, id, robot, true, "key")
		bobs := f.observe(t, id, bob, false, "bob")
		newer := f.observe(t, id, robot, true, "key")
		carols := f.observe(t, id, carol, false, "carol")
		replacing := f.observe(t, id, robot, true, "key")
		checkObserved(t, oldest, fmt.Sprintf("closed %d", CloseReplaced))
		open := []*Observer{alices, bobs, newer, carols, replacing}
		for _, o := range open {
			checkObserved(t, o, "snapshot 1/0 []")
		}

		if _, err := f.gateway.Observe(t.Context(), id, bob, false, "bob 的另一张票据"); !errors.Is(err, ErrTooManyObservers) {
			t.Errorf("名额用完、没有同一凭证的事件流时的错误 = %v，期望 ErrTooManyObservers", err)
		}
		for _, o := range open {
			checkObserved(t, o)
		}
	})
}

func TestFailedObserveFreesItsSlot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		f := newGatewayFixture(t, store, store)
		missing := "01a15778-1911-7a28-8b2e-e08c2a25f388"
		for i := range observerLimit + 1 {
			if _, err := f.gateway.Observe(t.Context(), missing, robot, true, fmt.Sprint(i)); !errors.Is(err, port.ErrRoomNotFound) {
				t.Fatalf("第 %d 次打开不存在的房间的事件流 = %v，期望 ErrRoomNotFound", i+1, err)
			}
		}
	})
}
