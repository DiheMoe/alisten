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
	"time"

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
	o, err := in.gateway.Observe(t.Context(), roomID, m, nil, bot, credential)
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

		f.gateway.Revoke(t.Context(), locked)
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

		f.a.gateway.RevokeAPIKey(t.Context(), f.room)
		checkObserved(t, bot, fmt.Sprintf("closed %d", CloseRevoked))
		checkReceived(t, session)
		checkObserved(t, member)
	})
}

func TestRevokedSessionsCloseOnlyTheirConnections(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		f := newClusterFixture(t, store, store)
		lounge := createRoom(t, store, room.Persistent)
		join := func(in *instance, roomID string, m member.Member, memberSession string) *Session {
			t.Helper()
			s, err := in.gateway.Join(t.Context(), roomID, m, []byte(memberSession), nil)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { in.gateway.Leave(s) })
			return s
		}
		observe := func(in *instance, m member.Member, memberSession []byte, bot bool, credential string) *Observer {
			t.Helper()
			o, err := in.gateway.Observe(t.Context(), f.room, m, memberSession, bot, credential)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { in.gateway.Unobserve(o) })
			return o
		}
		revokedOnA, revokedOnB, kept := join(f.a, f.room, alice, "alice-1"), join(f.b, lounge, alice, "alice-1"), join(f.b, f.room, alice, "alice-2")
		other := join(f.b, f.room, bob, "bob-1")
		revokedStream, keptStream := observe(f.b, alice, []byte("alice-1"), false, "t1"), observe(f.b, alice, []byte("alice-2"), false, "t2")
		bot := observe(f.b, robot, nil, true, "key")
		for _, q := range []interface {
			Next(context.Context) (Message, CloseReason, error)
		}{revokedOnA, revokedOnB, kept, other, revokedStream, keptStream, bot} {
			received(t, q)
		}

		f.a.gateway.RevokeSessions(t.Context(), [][]byte{[]byte("alice-1"), []byte("carol-1")})
		revoked := fmt.Sprintf("closed %d", CloseRevoked)
		checkReceived(t, revokedOnA, revoked)
		checkReceived(t, revokedOnB, revoked)
		checkObserved(t, revokedStream, revoked)
		for _, s := range []*Session{kept, other} {
			if got := received(t, s); slices.Contains(got, revoked) {
				t.Errorf("%s 的会话也被关闭了：%v", s.Member().Name, got)
			}
		}
		for _, o := range []*Observer{keptStream, bot} {
			if got := received(t, o); slices.Contains(got, revoked) {
				t.Errorf("%s 的事件流也被关闭了：%v", o.member.Name, got)
			}
		}
	})
}

// 会话和事件流都用 store 中真实的票据建立。作废凭证时只修改 store、不发通知，就像通知丢失了。
type credentialFixture struct {
	*instance
	store *porttest.Store
	room  string
}

// 只能在 synctest 的 bubble 中调用。
func newCredentialFixture(t *testing.T, store *porttest.Store, tickets port.ExistingTickets, logger *slog.Logger) *credentialFixture {
	t.Helper()
	in := startLoggingInstance(t, porttest.NewRedis(), "a", store, store, tickets, newSongs(), logger)
	return &credentialFixture{instance: in, store: store, room: createRoom(t, store, room.Persistent)}
}

// 返回新成员和它的会话的 token 哈希。
func (f *credentialFixture) member(t *testing.T, name string) (member.Member, []byte) {
	t.Helper()
	session := []byte(name + " 的会话")
	m, err := f.store.CreateMember(t.Context(), session, name)
	if err != nil {
		t.Fatal(err)
	}
	return m, session
}

// 用 session 签发的、哈希为 ticket 的票据加入房间。
func (f *credentialFixture) join(t *testing.T, m member.Member, session []byte, ticket string) *Session {
	t.Helper()
	if err := f.store.CreateTicket(t.Context(), port.NewTicket{TokenHash: []byte(ticket), RoomID: f.room, MemberID: m.ID, SessionTokenHash: session}); err != nil {
		t.Fatal(err)
	}
	s, err := f.gateway.Join(t.Context(), f.room, m, session, []byte(ticket))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.gateway.Leave(s) })
	return s
}

// session 为 nil 时 ticket 是房间的 API Key。
func (f *credentialFixture) observe(t *testing.T, m member.Member, session []byte, ticket string) *Observer {
	t.Helper()
	bot := session == nil
	var err error
	if bot {
		err = f.store.ResetRoomAPIKey(t.Context(), f.room, port.NewAPIKey{TokenHash: []byte(ticket)})
	} else {
		err = f.store.CreateTicket(t.Context(), port.NewTicket{TokenHash: []byte(ticket), RoomID: f.room, MemberID: m.ID, SessionTokenHash: session})
	}
	if err != nil {
		t.Fatal(err)
	}
	o, err := f.gateway.Observe(t.Context(), f.room, m, session, bot, ticket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.gateway.Unobserve(o) })
	return o
}

func TestRevokedCredentialsCloseWithoutNotice(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		f := newCredentialFixture(t, store, store, slog.New(slog.DiscardHandler))
		alice, aliceSession := f.member(t, "alice")
		bob, bobSession := f.member(t, "bob")
		revokedSession, revokedStream := f.join(t, alice, aliceSession, "alice-ws"), f.observe(t, alice, aliceSession, "alice-sse")
		keptSession, keptStream := f.join(t, bob, bobSession, "bob-ws"), f.observe(t, bob, bobSession, "bob-sse")
		bot := f.observe(t, robot, nil, "key-1")
		for _, q := range []interface {
			Next(context.Context) (Message, CloseReason, error)
		}{revokedSession, revokedStream, keptSession, keptStream, bot} {
			received(t, q)
		}

		if err := store.DeleteSession(t.Context(), aliceSession); err != nil {
			t.Fatal(err)
		}
		if err := store.ResetRoomAPIKey(t.Context(), f.room, port.NewAPIKey{TokenHash: []byte("key-2")}); err != nil {
			t.Fatal(err)
		}
		checkReceived(t, revokedSession)
		checkObserved(t, revokedStream)
		checkObserved(t, bot)

		time.Sleep(credentialCheckInterval)
		revoked := fmt.Sprintf("closed %d", CloseRevoked)
		checkReceived(t, revokedSession, revoked)
		checkObserved(t, revokedStream, revoked)
		checkObserved(t, bot, revoked)
		checkReceived(t, keptSession)
		checkObserved(t, keptStream)
	})
}

type unreadableTickets struct {
	*porttest.Store
	down atomic.Bool
}

func (s *unreadableTickets) ExistingTickets(ctx context.Context, tokenHashes [][]byte) ([][]byte, error) {
	if s.down.Load() {
		return nil, errors.New("连接被拒绝")
	}
	return s.Store.ExistingTickets(ctx, tokenHashes)
}

// 核对失败时不知道哪些凭证已经作废，这一轮不断开任何连接。
func TestUnreadableCredentialsCloseNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		tickets := &unreadableTickets{Store: store}
		var logs logBuffer
		f := newCredentialFixture(t, store, tickets, slog.New(slog.NewJSONHandler(&logs, nil)))
		alice, session := f.member(t, "alice")
		s, o := f.join(t, alice, session, "alice-ws"), f.observe(t, alice, session, "alice-sse")
		received(t, s)
		received(t, o)
		if err := store.DeleteSession(t.Context(), session); err != nil {
			t.Fatal(err)
		}

		tickets.down.Store(true)
		time.Sleep(credentialCheckInterval)
		checkReceived(t, s)
		checkObserved(t, o)
		if n := len(logs.entries(t, "核对连接所用的凭证失败")); n != 1 {
			t.Errorf("核对失败的日志有 %d 条，期望 1 条", n)
		}

		tickets.down.Store(false)
		time.Sleep(credentialCheckInterval)
		revoked := fmt.Sprintf("closed %d", CloseRevoked)
		checkReceived(t, s, revoked)
		checkObserved(t, o, revoked)
	})
}

// 像 Redis 客户端一样，ctx 结束之后不再发出命令。
type contextBus struct {
	port.Bus
}

func (b contextBus) PublishRoom(ctx context.Context, roomID string, m port.RoomMessage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return b.Bus.PublishRoom(ctx, roomID, m)
}

func (b contextBus) PublishSessionsRevoked(ctx context.Context, sessionTokenHashes [][]byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return b.Bus.PublishSessionsRevoked(ctx, sessionTokenHashes)
}

// 通知在凭证作废之后发出。发起请求的客户端已经断开时照样发出，发不出去时只记日志。
func TestRevocationNoticesAreBestEffort(t *testing.T) {
	redis := porttest.NewRedis()
	node, listener := redis.Node("a"), redis.Node("b")
	if err := listener.SubscribeInstance(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := listener.SubscribeRoom(t.Context(), "r"); err != nil {
		t.Fatal(err)
	}
	store := porttest.New()
	var logs logBuffer
	_, g := New(store, store, store, unrevoked{}, nil, porttest.NewPreparer(), Cluster{Instance: "a", Leases: node, Liveness: node, Bus: contextBus{node}}, slog.New(slog.NewJSONHandler(&logs, nil)))
	revokeAll := func(ctx context.Context) {
		g.RevokeSessions(ctx, [][]byte{[]byte("s")})
		g.Revoke(ctx, "r")
		g.RevokeAPIKey(ctx, "r")
	}

	request, cancel := context.WithCancel(t.Context())
	cancel()
	revokeAll(request)
	var got []string
	for range 3 {
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		d, err := listener.Receive(ctx)
		cancel()
		if err != nil {
			t.Fatalf("请求取消之后只收到 %v：%v", got, err)
		}
		switch d := d.(type) {
		case port.SessionsRevokedDelivery:
			got = append(got, "sessions")
		case port.RoomDelivery:
			got = append(got, fmt.Sprintf("%T", d.Message))
		}
	}
	if want := []string{"sessions", "port.TicketsRevoked", "port.APIKeyReset"}; !slices.Equal(got, want) {
		t.Errorf("收到的通知 = %v，期望 %v", got, want)
	}

	node.SetDown(true)
	revokeAll(t.Context())
	if n := len(logs.entries(t, "广播凭证作废的通知失败")); n != 3 {
		t.Errorf("通知发不出去的日志有 %d 条，期望 3 条", n)
	}
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
		// 放弃房间的那次运行在自己的 goroutine 里收尾，收尾完之前到达的请求会得到 ErrUnavailable。等这次运行收尾完，Sync 才会重新接管房间。
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

// a 的租约已经丢失，但 a 还没有发现。排在 a 那里的快照请求，在 b 接管、发出变更通知之后才得到回复，所以这份快照已经过时。
func TestObserverSkipsSnapshotOfEarlierRun(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		songs := newSongs()
		redis := porttest.NewRedis()
		node := redis.Node("a")
		bus := &heldEmits{Bus: node, release: make(chan struct{})}
		h, g := New(store, store, store, unrevoked{}, songs.music, songs.preparer, Cluster{Instance: "a", Leases: node, Liveness: node, Bus: bus}, slog.New(slog.DiscardHandler))
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
			o, err = b.gateway.Observe(context.Background(), id, robot, nil, true, "key")
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
		first, err := f.gateway.Observe(t.Context(), full, robot, nil, true, "key")
		if err != nil {
			t.Fatal(err)
		}
		for range observerLimit - 1 {
			f.observe(t, full, robot, true, "key")
		}
		if _, err := f.gateway.Observe(t.Context(), full, alice, nil, false, "alice"); !errors.Is(err, ErrTooManyObservers) {
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

		if _, err := f.gateway.Observe(t.Context(), id, bob, nil, false, "bob 的另一张票据"); !errors.Is(err, ErrTooManyObservers) {
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
			if _, err := f.gateway.Observe(t.Context(), missing, robot, nil, true, fmt.Sprint(i)); !errors.Is(err, port.ErrRoomNotFound) {
				t.Fatalf("第 %d 次打开不存在的房间的事件流 = %v，期望 ErrRoomNotFound", i+1, err)
			}
		}
	})
}
