package realtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/app/port/porttest"
	"github.com/dihemoe/alisten/internal/domain/member"
	"github.com/dihemoe/alisten/internal/domain/room"
	"github.com/dihemoe/alisten/internal/domain/track"
)

type instance struct {
	id      string
	node    *porttest.Node
	host    *Host
	gateway *Gateway
}

// 只能在 synctest 的 bubble 中调用。
func startInstance(t *testing.T, redis *porttest.Redis, id string, snapshots port.RoomSnapshots, store *porttest.Store, songs *songs) *instance {
	t.Helper()
	return startLoggingInstance(t, redis, id, snapshots, store, songs, slog.New(slog.DiscardHandler))
}

func startLoggingInstance(t *testing.T, redis *porttest.Redis, id string, snapshots port.RoomSnapshots, store *porttest.Store, songs *songs, logger *slog.Logger) *instance {
	t.Helper()
	node := redis.Node(id)
	h, g := New(snapshots, store, songs.music, songs.preparer, cluster(node, id), logger)
	ctx, stop := context.WithCancel(context.Background())
	wait, err := Start(ctx, h, g)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := h.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
		if err := g.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
		stop()
		wait()
	})
	return &instance{id: id, node: node, host: h, gateway: g}
}

func (in *instance) join(t *testing.T, roomID string, m member.Member) *Session {
	t.Helper()
	s, err := in.gateway.Join(t.Context(), roomID, m)
	if err != nil {
		t.Fatalf("%s 加入房间：%v", m.Name, err)
	}
	t.Cleanup(func() { in.gateway.Leave(s) })
	synctest.Wait()
	return s
}

func (in *instance) online(t *testing.T, roomID string) int {
	t.Helper()
	counts, err := in.node.OnlineCounts(t.Context(), []string{roomID})
	if err != nil {
		t.Fatal(err)
	}
	return counts[0]
}

func (in *instance) owner(t *testing.T, roomID string) string {
	t.Helper()
	owners, err := in.node.Owners(t.Context(), []string{roomID})
	if err != nil {
		t.Fatal(err)
	}
	return owners[0]
}

type gatewayFixture struct {
	*instance
	store *porttest.Store
	redis *porttest.Redis
	songs *songs
}

func newGatewayFixture(t *testing.T, snapshots port.RoomSnapshots, store *porttest.Store) *gatewayFixture {
	t.Helper()
	songs := newSongs()
	songs.add(sunny, sunnyM, sunnyP)
	redis := porttest.NewRedis()
	return &gatewayFixture{instance: startInstance(t, redis, "a", snapshots, store, songs), store: store, redis: redis, songs: songs}
}

func createRoom(t *testing.T, store *porttest.Store, kind room.Kind) string {
	t.Helper()
	id, err := store.CreateRoom(t.Context(), port.NewRoom{Kind: kind, Name: "房间"})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// received 返回会话或事件流的发送队列中已有的消息，不等待新消息。
func received(t *testing.T, q interface {
	Next(context.Context) (Message, CloseReason, error)
}) []string {
	t.Helper()
	synctest.Wait()
	done, cancel := context.WithCancel(t.Context())
	cancel()
	var got []string
	for {
		m, reason, err := q.Next(done)
		switch {
		case err != nil:
			return got
		case reason != 0:
			return append(got, fmt.Sprintf("closed %d", reason))
		}
		got = append(got, describeMessage(m))
	}
}

func describeMessage(m Message) string {
	switch m := m.(type) {
	case Hello:
		names := make([]string, len(m.View.Sessions))
		for i, s := range m.View.Sessions {
			names[i] = s.Member.Name
		}
		return fmt.Sprintf("hello %s %d/%d [%s]", m.Member.Name, m.View.Term, m.View.Seq, strings.Join(names, " "))
	case Snapshot:
		return fmt.Sprintf("snapshot %d/%d [%s]", m.View.Term, m.View.Seq, strings.Join(sessionNames(m.View), " "))
	case Event:
		return fmt.Sprintf("%d/%d %T", m.Emit.Term, m.Emit.Seq, m.Emit.Event)
	case Reply:
		if m.Err != nil {
			return fmt.Sprintf("reply %v %s", m.Tag, m.Err)
		}
		return fmt.Sprintf("reply %v %T", m.Tag, m.Result)
	case Answer:
		return fmt.Sprintf("answer %v %v", m.Tag, m.Data)
	}
	return fmt.Sprintf("%T", m)
}

func checkReceived(t *testing.T, s *Session, want ...string) {
	t.Helper()
	if got := received(t, s); !slices.Equal(got, want) {
		t.Errorf("%s 收到：\n%s\n期望：\n%s", s.Member().Name, strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestRequestLimit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		f := newGatewayFixture(t, store, store)
		a := f.join(t, createRoom(t, store, room.Persistent), alice)
		received(t, a)
		for i := range requestLimit {
			if !a.Begin() {
				t.Fatalf("第 %d 个请求被拒绝", i+1)
			}
		}
		if a.Begin() {
			t.Fatal("第 17 个未完成的请求应被拒绝")
		}
		a.Fail("c1", ErrUnavailable)
		if !a.Begin() {
			t.Error("回复一个请求之后应能再占用名额")
		}
		a.Answer("q1", "搜索结果")
		if !a.Begin() {
			t.Error("回复不经过房间的请求之后应能再占用名额")
		}
		checkReceived(t, a, "reply c1 "+ErrUnavailable.Error(), "answer q1 搜索结果")
	})
}

func TestSendQueueOverflowClosesSession(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		f := newGatewayFixture(t, store, store)
		id := createRoom(t, store, room.Persistent)
		a := f.join(t, id, alice)
		chat := func() {
			if _, err := f.gateway.Do(t.Context(), id, bob, room.Chat{Text: "刷屏"}); err != nil {
				t.Fatal(err)
			}
		}
		// 发送队列中已有握手的结果，再放 sendQueueLimit - 1 条正好占满。
		for range sendQueueLimit - 1 {
			chat()
		}
		synctest.Wait()
		if a.Context().Err() != nil {
			t.Fatal("队列正好满时不应关闭会话")
		}
		chat()
		synctest.Wait()
		if a.Context().Err() == nil {
			t.Fatal("队列溢出时应关闭会话")
		}
		checkReceived(t, a, fmt.Sprintf("closed %d", CloseOverflow))
	})
}

func TestOwnerReplyTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := &blockingStore{Store: porttest.New(), saving: make(chan struct{}, 1), release: make(chan struct{})}
		f := newGatewayFixture(t, store, store.Store)
		id := createRoom(t, store.Store, room.Persistent)
		a := f.join(t, id, alice)
		received(t, a)

		start := time.Now()
		a.Begin()
		if err := f.gateway.Command(t.Context(), a, "c1", room.SetPlayMode{Mode: room.Random}); !errors.Is(err, ErrUnavailable) {
			t.Errorf("Command 返回 %v，期望 ErrUnavailable", err)
		}
		if elapsed := time.Since(start); elapsed != ownerReplyTimeout {
			t.Errorf("等了 %v，期望 %v", elapsed, ownerReplyTimeout)
		}
		close(store.release)
		// 超时之后到达的回复不再送达，事件照常广播。
		checkReceived(t, a, "reply c1 "+ErrUnavailable.Error(), "1/2 room.ModeChanged")

		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		if _, err := f.gateway.Do(ctx, id, alice, room.Sync{}); err != nil {
			t.Errorf("房间恢复之后请求应成功：%v", err)
		}
	})
}

func TestAbandonedRoomFailsPendingRequestsAndRejoins(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := &failingStore{Store: porttest.New()}
		f := newGatewayFixture(t, store, store.Store)
		id := createRoom(t, store.Store, room.Persistent)
		a := f.join(t, id, alice)
		received(t, a)

		store.fail.Store(true)
		start := time.Now()
		a.Begin()
		if err := f.gateway.Command(t.Context(), a, "c1", room.SetPlayMode{Mode: room.Random}); !errors.Is(err, ErrUnavailable) {
			t.Errorf("Command 返回 %v，期望 ErrUnavailable", err)
		}
		if time.Since(start) != 0 {
			t.Error("房间放弃时应立即回复，而不是等到超时")
		}
		checkReceived(t, a, "reply c1 "+ErrUnavailable.Error())

		store.fail.Store(false)
		res, err := f.gateway.Do(t.Context(), id, bob, room.Sync{})
		if err != nil {
			t.Fatal(err)
		}
		if view := res.(room.SyncResult).View; view.Term != 2 {
			t.Errorf("重新接管后任期 = %d，期望 2", view.Term)
		}
		synctest.Wait()
		if got := received(t, a); !slices.Contains(got, "2/1 room.MembersChanged") {
			t.Errorf("重新接管后应收到新任期的在线列表，实际收到 %q", got)
		}
		if n := f.online(t, id); n != 1 {
			t.Errorf("在线人数 = %d，期望重新加入的 1 个会话", n)
		}
	})
}

func TestSessionLeavingDuringRejoinIsNotKeptOnline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := &gateStore{Store: porttest.New()}
		f := newGatewayFixture(t, store, store.Store)
		id := createRoom(t, store.Store, room.Persistent)
		a := f.join(t, id, alice)
		b := f.join(t, id, bob)
		if _, err := f.gateway.Do(t.Context(), id, alice, room.Enqueue{Track: sunny, Meta: sunnyM}); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		store.fail.Store(true)
		_, _ = f.gateway.Do(t.Context(), id, alice, room.SetPlayMode{Mode: room.Random})
		store.fail.Store(false)
		synctest.Wait()
		// porttest 恢复出的正在播放的歌没有时长：开播时间过去之后再接管，恢复时这首歌已经放完，第一个 effect 是 Persist。
		time.Sleep(time.Second)

		// 恢复时的 Persist 卡住，网关重新加入的第一个 Join 等待房间收下；两个会话在此期间先后离开。
		hold := make(chan struct{})
		store.setHold(hold)
		go func() { _, _ = f.gateway.Do(context.Background(), id, alice, room.Sync{}) }()
		synctest.Wait()
		go f.gateway.Leave(a)
		synctest.Wait()
		go f.gateway.Leave(b)
		synctest.Wait()
		store.setHold(nil)
		close(hold)
		synctest.Wait()

		res, err := f.gateway.Do(t.Context(), id, alice, room.Sync{})
		if err != nil {
			t.Fatal(err)
		}
		if sessions := res.(room.SyncResult).View.Sessions; len(sessions) != 0 {
			t.Errorf("两个会话都离开之后的在线列表 = %+v，期望为空", sessions)
		}
		if n := f.online(t, id); n != 0 {
			t.Errorf("在线人数 = %d，期望 0", n)
		}
	})
}

func TestDeleteFailureAbandonsRoom(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := failingDeleteStore{Store: porttest.New()}
		f := newGatewayFixture(t, store, store.Store)
		id := createRoom(t, store.Store, room.Persistent)
		a := f.join(t, id, alice)
		received(t, a)

		if err := f.gateway.Delete(t.Context(), id); !errors.Is(err, ErrUnavailable) {
			t.Errorf("删除失败时的错误 = %v，期望 ErrUnavailable", err)
		}
		synctest.Wait()
		if a.Context().Err() != nil {
			t.Error("删除失败时会话不应断开")
		}
		if _, err := store.FindRoom(t.Context(), id); err != nil {
			t.Errorf("删除失败之后房间应仍在：%v", err)
		}
	})
}

func TestShutdownClosesSessions(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		f := newGatewayFixture(t, store, store)
		id := createRoom(t, store, room.Persistent)
		a, err := f.gateway.Join(t.Context(), id, alice)
		if err != nil {
			t.Fatal(err)
		}
		received(t, a)

		done := make(chan error, 1)
		go func() { done <- f.gateway.Shutdown(context.Background()) }()
		checkReceived(t, a, fmt.Sprintf("closed %d", CloseRestart))
		select {
		case err := <-done:
			t.Fatalf("Shutdown 应等到会话调用 Leave，实际已返回 %v", err)
		default:
		}
		f.gateway.Leave(a)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if _, err := f.gateway.Join(t.Context(), id, bob); !errors.Is(err, ErrUnavailable) {
			t.Errorf("Shutdown 之后加入的错误 = %v，期望 ErrUnavailable", err)
		}
	})
}

func TestResolvePick(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		f := newGatewayFixture(t, store, store)
		link := track.Track{Source: track.Link, ID: "https://www.youtube.com/watch?v=x"}
		video := track.Track{Source: track.Bilibili, ID: "BV1xx411c7mD"}
		videoM := track.Meta{Title: "晴天（钢琴版）", PageURL: "https://www.bilibili.com/video/BV1xx411c7mD"}
		f.songs.bilibili.AddVideo(video.ID, track.Meta{Title: "晴天（钢琴版）"}, nil, nil, "")
		f.songs.bilibili.AddLink("https://www.bilibili.com/video/BV1xx411c7mD", video.ID)
		tests := []struct {
			pick Pick
			want room.Enqueue
			err  error
		}{
			{Pick{Track: sunny}, room.Enqueue{Track: sunny, Meta: sunnyM}, nil},
			{Pick{Track: link}, room.Enqueue{Track: link, Meta: track.Meta{Title: link.ID, PageURL: link.ID}}, nil},
			{Pick{Track: track.Track{Source: track.Link, ID: "https://www.bilibili.com/video/BV1xx411c7mD"}}, room.Enqueue{Track: video, Meta: videoM}, nil},
			{Pick{Track: video}, room.Enqueue{Track: video, Meta: videoM}, nil},
			{Pick{Source: track.NetEase, Keyword: "晴"}, room.Enqueue{Track: sunny, Meta: sunnyM}, nil},
		}
		for _, tt := range tests {
			got, _, err := f.gateway.Resolve(t.Context(), alice, false, tt.pick)
			if got != tt.want || !errors.Is(err, tt.err) {
				t.Errorf("Resolve(%+v) = %+v, %v，期望 %+v, %v", tt.pick, got, err, tt.want, tt.err)
			}
		}
	})
}

func TestOnlyBotsFillInRequester(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		f := newGatewayFixture(t, store, store)
		bot := member.Member{ID: "00000000-0000-7000-8000-0000000000b0", Name: "bot"}
		for requester, want := range map[string]member.Member{"张三": {ID: bot.ID, Name: "张三（bot）"}, "": bot} {
			got, by, err := f.gateway.Resolve(t.Context(), bot, true, Pick{Track: sunny, Requester: requester})
			if err != nil || got.Track != sunny || by != want {
				t.Errorf("bot 点歌时代填 %q = %+v, %+v, %v，期望点歌人 %+v", requester, got, by, err, want)
			}
		}
		if _, _, err := f.gateway.Resolve(t.Context(), alice, false, Pick{Track: sunny, Requester: "张三"}); !errors.Is(err, ErrRequesterNotAllowed) {
			t.Errorf("普通成员代填点歌人的错误 = %v，期望 ErrRequesterNotAllowed", err)
		}
	})
}

func TestUnsubscribeAfterLastUse(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		f := newGatewayFixture(t, store, store)
		sessionOnly := createRoom(t, store, room.Persistent)
		withHTTP := createRoom(t, store, room.Persistent)

		s, err := f.gateway.Join(t.Context(), sessionOnly, alice)
		if err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if !f.node.Subscribed(sessionOnly) {
			t.Fatal("会话加入之后应订阅房间频道")
		}
		f.gateway.Leave(s)
		synctest.Wait()
		if f.node.Subscribed(sessionOnly) {
			t.Error("最后一个会话离开、没有 HTTP 请求时应立即退订")
		}

		if _, err := f.gateway.Do(t.Context(), withHTTP, bob, room.Sync{}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(30 * time.Second)
		s, err = f.gateway.Join(t.Context(), withHTTP, alice)
		if err != nil {
			t.Fatal(err)
		}
		f.gateway.Leave(s)
		time.Sleep(30*time.Second - time.Nanosecond)
		synctest.Wait()
		if !f.node.Subscribed(withHTTP) {
			t.Error("最后一次 HTTP 请求之后不到 60 秒，不应退订")
		}
		time.Sleep(time.Nanosecond)
		synctest.Wait()
		if f.node.Subscribed(withHTTP) {
			t.Error("最后一次 HTTP 请求 60 秒之后应退订")
		}
		if _, err := f.gateway.Do(t.Context(), withHTTP, bob, room.Sync{}); err != nil {
			t.Errorf("退订之后的请求失败：%v", err)
		}
	})
}

func TestSubscriptionLossRestartsInstance(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := &blockingStore{Store: porttest.New(), saving: make(chan struct{}, 1), release: make(chan struct{})}
		f := newGatewayFixture(t, store, store.Store)
		id := createRoom(t, store.Store, room.Persistent)
		a := f.join(t, id, alice)
		received(t, a)

		failed := make(chan error, 1)
		go func() {
			_, err := f.gateway.Do(context.Background(), id, bob, room.SetPlayMode{Mode: room.Random})
			failed <- err
		}()
		<-store.saving
		lost := time.Now()
		f.node.Disconnect()
		synctest.Wait()
		if err := <-failed; !errors.Is(err, ErrUnavailable) {
			t.Errorf("订阅断开时等待中的请求 = %v，期望 ErrUnavailable", err)
		}
		if time.Since(lost) != 0 {
			t.Error("订阅断开时等待中的请求应立即失败，而不是等到超时")
		}
		checkReceived(t, a, fmt.Sprintf("closed %d", CloseRestart))
		close(store.release)
		synctest.Wait()
		if n := f.host.RoomsOwned(); n != 0 {
			t.Errorf("RoomsOwned = %d，期望房间已经释放", n)
		}
		res, err := f.gateway.Do(t.Context(), id, bob, room.Sync{})
		if err != nil {
			t.Fatal(err)
		}
		if view := res.(room.SyncResult).View; view.Term != 2 || view.Snapshot.Mode != room.Random {
			t.Errorf("重新接管之后任期 = %d、播放模式 = %d，期望 2 和处理完的随机模式", view.Term, view.Snapshot.Mode)
		}
	})
}

type countingBus struct {
	*porttest.Node

	mu         sync.Mutex
	subscribes map[string]int
	stalled    bool
	gate       chan struct{}
}

func (b *countingBus) SubscribeRoom(ctx context.Context, roomID string) error {
	b.mu.Lock()
	b.subscribes[roomID]++
	stalled, gate := b.stalled, b.gate
	b.mu.Unlock()
	if stalled {
		<-ctx.Done()
		return ctx.Err()
	}
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return b.Node.SubscribeRoom(ctx, roomID)
}

func (b *countingBus) stall(stalled bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.stalled = stalled
}

func (b *countingBus) delay() (resume func()) {
	gate := make(chan struct{})
	b.mu.Lock()
	defer b.mu.Unlock()
	b.gate = gate
	return sync.OnceFunc(func() { close(gate) })
}

func (b *countingBus) count(roomID string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.subscribes[roomID]
}

func startCountingGateway(t *testing.T, store *porttest.Store) (*Gateway, *porttest.Node, *countingBus) {
	t.Helper()
	node := porttest.NewRedis().Node("a")
	bus := &countingBus{Node: node, subscribes: map[string]int{}}
	songs := newSongs()
	c := cluster(node, "a")
	c.Bus = bus
	h, g := New(store, store, songs.music, songs.preparer, c, slog.New(slog.DiscardHandler))
	ctx, stop := context.WithCancel(context.Background())
	wait, err := Start(ctx, h, g)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := h.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
		if err := g.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
		stop()
		wait()
	})
	return g, node, bus
}

func TestConfirmedSubscriptionIsNotRepeated(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		g, node, bus := startCountingGateway(t, store)
		id := createRoom(t, store, room.Persistent)
		s, err := g.Join(t.Context(), id, alice)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { g.Leave(s) })
		for range 3 {
			if _, err := g.Do(t.Context(), id, bob, room.Sync{}); err != nil {
				t.Fatal(err)
			}
		}
		if n := bus.count(id); n != 1 {
			t.Errorf("会话加入和之后的三个请求一共订阅了 %d 次，期望只在加入时订阅一次", n)
		}

		bus.stall(true)
		begin := time.Now()
		if _, err := g.Do(t.Context(), id, bob, room.Sync{}); err != nil || time.Since(begin) != 0 {
			t.Errorf("订阅等不到确认时，确认过订阅的房间的请求 = %v，用了 %v，期望不等订阅，立即完成", err, time.Since(begin))
		}
		bus.stall(false)

		node.Disconnect()
		synctest.Wait()
		if _, err := g.Do(t.Context(), id, bob, room.Sync{}); err != nil {
			t.Fatal(err)
		}
		if n := bus.count(id); n != 2 {
			t.Errorf("订阅连接断开过之后一共订阅了 %d 次，期望断开之后的第一个请求重新订阅", n)
		}
	})
}

func TestConfirmationAcrossDisconnectIsNotReused(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		g, node, bus := startCountingGateway(t, store)
		id := createRoom(t, store, room.Persistent)
		s, err := g.Join(t.Context(), id, alice)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { g.Leave(s) })

		node.Disconnect()
		synctest.Wait()
		resume := bus.delay()
		interrupted := make(chan struct{})
		go func() {
			defer close(interrupted)
			_, _ = g.Do(t.Context(), id, bob, room.Sync{})
		}()
		synctest.Wait()
		if n := bus.count(id); n != 2 {
			t.Fatalf("第一次断开之后一共订阅了 %d 次，期望请求正在等第二次订阅的确认", n)
		}

		node.Disconnect()
		synctest.Wait()
		resume()
		<-interrupted
		if _, err := g.Do(t.Context(), id, bob, room.Sync{}); err != nil {
			t.Fatal(err)
		}
		if n := bus.count(id); n != 3 {
			t.Errorf("第二次断开之后一共订阅了 %d 次，期望之后的请求重新订阅，等待确认时断开的那次不算", n)
		}
	})
}
