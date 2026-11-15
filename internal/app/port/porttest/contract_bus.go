package porttest

import (
	"context"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/member"
	"github.com/dihemoe/alisten/internal/domain/room"
	"github.com/dihemoe/alisten/internal/domain/track"
)

// Redis 的订阅要等 Receive 收到服务端的确认才返回，所以先开始接收，再订阅。
func receive(t *testing.T, bus port.Bus) <-chan port.Delivery {
	ctx := t.Context()
	deliveries := make(chan port.Delivery, 64)
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			d, err := bus.Receive(ctx)
			if err != nil {
				return
			}
			select {
			case deliveries <- d:
			case <-ctx.Done():
				return
			}
		}
	})
	t.Cleanup(wg.Wait)
	return deliveries
}

func nextDelivery(t *testing.T, deliveries <-chan port.Delivery) port.Delivery {
	t.Helper()
	select {
	case d := <-deliveries:
		return d
	case <-time.After(20 * time.Second):
		t.Fatal("20 秒内没有收到消息")
		return nil
	}
}

func expectInput(t *testing.T, deliveries <-chan port.Delivery, want port.RoomInput) {
	t.Helper()
	d := nextDelivery(t, deliveries)
	got, ok := d.(port.InputDelivery)
	if !ok || !reflect.DeepEqual(got.Input, want) {
		t.Errorf("收到 %#v\n期望输入 %#v", d, want)
		return
	}
	if got.Context != context.Background() {
		t.Errorf("发送时没有追踪的输入收到时 Context = %v，期望 context.Background()", got.Context)
	}
}

var (
	addedAt    = time.Date(2026, 10, 22, 8, 30, 0, 123456789, time.UTC)
	alice      = member.Member{ID: "00000000-0000-7000-8000-00000000000a", Name: "alice", AvatarHash: "c160f8cc69a4f0bf2b0362752353d060"}
	sunnyEntry = room.Entry{
		ID: 12, Track: track.Track{Source: track.NetEase, ID: "186016"}, By: alice, Likes: 2, Ready: true, AddedAt: addedAt,
		Meta: track.Meta{Title: "晴天", Artist: "周杰伦", Album: "叶惠美", CoverURL: "https://p1.music.126.net/example.jpg", PageURL: "https://music.163.com/#/song?id=186016", Duration: 269 * time.Second},
	}
	preparedEntry = withPrep(sunnyEntry)
	linkEntry     = room.Entry{ID: 13, Track: track.Track{Source: track.Link, ID: "https://www.youtube.com/watch?v=x"}, Meta: track.Meta{Title: "https://www.youtube.com/watch?v=x"}, Auto: true, AddedAt: addedAt}
	nowPlaying    = room.NowPlaying{Entry: preparedEntry, StartedAt: addedAt.Add(time.Minute)}
	roomView      = room.View{
		Term: 3, Seq: 128, Next: 13,
		Snapshot: room.Snapshot{
			Info:     room.Info{ID: "01a15778-1911-7a28-8b2e-e08c2a25f388", Name: "大厅", Description: "常驻", Kind: room.Persistent},
			Mode:     room.Random,
			EntrySeq: 13,
			Waiting:  []room.Entry{linkEntry},
			Pending:  &sunnyEntry,
			Current:  &nowPlaying,
		},
		Sessions: []room.Session{{ID: "a/s1", Member: alice}},
	}
)

func withPrep(e room.Entry) room.Entry {
	e.Prep = track.Prepared{Duration: 269500 * time.Millisecond, Gain: 0.75, Lyric: "[00:01.00]故事的小黄花"}
	return e
}

// 样例要包含每一种房间消息、事件和回复，因为经过 Redis 时每一种都要编码再解码。
func roomMessageSamples() []port.RoomMessage {
	return []port.RoomMessage{
		port.OwnerChanged{Term: 3},
		port.Emitted{Emit: room.Emit{Term: 3, Seq: 1, Event: room.PlaybackChanged{Current: &nowPlaying}}},
		port.Emitted{Emit: room.Emit{Term: 3, Seq: 3, Event: room.QueueChanged{Pending: &sunnyEntry, Waiting: []room.Entry{linkEntry, sunnyEntry}, Next: 13}}},
		port.Emitted{Emit: room.Emit{Term: 3, Seq: 4, Event: room.MembersChanged{Sessions: roomView.Sessions}}},
		port.Emitted{Emit: room.Emit{Term: 3, Seq: 5, Event: room.ModeChanged{Mode: room.Random}}},
		port.Emitted{Emit: room.Emit{Term: 3, Seq: 6, Event: room.InfoChanged{Name: "大厅", Description: "常驻"}}},
		port.Emitted{Emit: room.Emit{Term: 3, Seq: 7, Event: room.ChatPosted{Member: alice, Text: "你好", SentAt: addedAt}}},
		port.Emitted{Emit: room.Emit{Term: 3, Seq: 1 << 40, Event: room.Activity{Kind: room.ActivityVoted, Member: alice, Entry: sunnyEntry, Votes: 1, Required: 2, Reason: "原因"}}},
		port.Replied{Reply: room.Reply{RequestID: "a/r1", Result: room.Done{}}},
		port.Replied{Reply: room.Reply{RequestID: "a/r2", Result: room.EnqueueResult{Entry: sunnyEntry}}},
		port.Replied{Reply: room.Reply{RequestID: "a/r3", Result: room.LikeResult{Likes: 3}}},
		port.Replied{Reply: room.Reply{RequestID: "a/r4", Result: room.VoteResult{Votes: 1, Required: 2, Skipped: true}}},
		port.Replied{Reply: room.Reply{RequestID: "a/r5", Result: room.SyncResult{View: roomView}}},
		port.Replied{Reply: room.Reply{RequestID: "a/r6", Err: &room.Error{Kind: room.QueueFull, Message: "临时房间的播放列表最多 10 首歌，等播放几首之后再点。"}}},
		port.Rejected{RequestID: "a/r8", NotFound: true},
		port.RoomClosed{},
		port.OwnerReleased{},
		port.TicketsRevoked{},
		port.APIKeyReset{},
	}
}

// 样例要包含每一种跨实例的输入和动作，因为经过 Redis 时每一种都要编码再解码。
func inputSamples(roomID, gateway string) []port.RoomInput {
	name := "新大厅"
	samples := []port.RoomInput{{Room: roomID, Gateway: gateway, Input: room.Join{Session: "a/s1", Member: alice}, Takeover: true}}
	for _, a := range []room.Action{room.Enqueue{Track: sunnyEntry.Track, Meta: sunnyEntry.Meta}, room.Remove{Entry: 12}, room.Like{Entry: 12}, room.VoteSkip{}, room.SetPlayMode{Mode: room.Random}, room.Chat{Text: "你好"}, room.Sync{}} {
		samples = append(samples, port.RoomInput{Room: roomID, Gateway: "g", Input: room.Command{RequestID: "g/r1", Member: alice, Action: a}, Takeover: true})
	}
	return append(samples,
		port.RoomInput{Room: roomID, Gateway: "g", Input: room.Leave{Session: "a/s1"}},
		port.RoomInput{Room: roomID, Gateway: "g", Input: room.MemberUpdated{Member: alice}},
		port.RoomInput{Room: roomID, Gateway: "g", Input: room.Delete{RequestID: "g/r2"}, Takeover: true},
		port.RoomInput{Room: roomID, Gateway: "g", Input: room.UpdateInfo{RequestID: "g/r3", Name: &name}, Takeover: true},
	)
}

func runBusContract(t *testing.T, open func(t *testing.T) func(string) ClusterNode) {
	t.Run("只收得到订阅着的频道上的消息", func(t *testing.T) {
		node := open(t)
		bID := unique("b")
		a, b := node(unique("a")), node(bID)
		received := receive(t, b)
		ctx := t.Context()
		never, gone, kept := unique("never"), unique("gone"), unique("kept")
		if err := b.SubscribeRoom(ctx, gone); err != nil {
			t.Fatal(err)
		}
		if err := b.UnsubscribeRoom(ctx, gone); err != nil {
			t.Fatal(err)
		}
		// Redis 的退订不等服务端确认。同一个连接上的命令按顺序处理，所以之后的订阅生效时，退订也已经生效。
		if err := b.SubscribeRoom(ctx, kept); err != nil {
			t.Fatal(err)
		}
		in := port.RoomInput{Room: kept, Gateway: "g", Input: room.Leave{Session: "g/s1"}}
		if ok, err := a.SendInput(ctx, bID, in); err != nil || ok {
			t.Errorf("b 没有订阅本实例的频道时 SendInput = %v, %v，期望 false", ok, err)
		}
		if err := a.PublishMember(ctx, alice); err != nil {
			t.Fatal(err)
		}
		if err := a.PublishSessionsRevoked(ctx, [][]byte{{0x01, 0x02}}); err != nil {
			t.Fatal(err)
		}
		if err := a.PublishPrepared(ctx, port.PrepareOutcome{Track: sunnyEntry.Track, JobID: 1}); err != nil {
			t.Fatal(err)
		}
		for _, id := range []string{never, gone, kept} {
			if err := a.PublishRoom(ctx, id, port.RoomClosed{}); err != nil {
				t.Fatal(err)
			}
		}
		// a 每次发布都等上一次返回之后才开始，所以 b 按发布的顺序收到消息。b 先收到的如果不是 kept 的消息，就说明收到了前面不该收到的消息。
		if got := nextDelivery(t, received); got != (port.RoomDelivery{Room: kept, Message: port.RoomClosed{}}) {
			t.Errorf("收到 %#v，期望只收到订阅着的房间的消息", got)
		}
	})

	t.Run("房间的消息按发布的顺序送达每个订阅的实例，包括发布方自己", func(t *testing.T) {
		node := open(t)
		a, b := node(unique("a")), node(unique("b"))
		deliveries := []<-chan port.Delivery{receive(t, a), receive(t, b)}
		roomID := unique("room")
		for _, n := range []ClusterNode{a, b} {
			if err := n.SubscribeRoom(t.Context(), roomID); err != nil {
				t.Fatal(err)
			}
		}
		samples := roomMessageSamples()
		for _, m := range samples {
			if err := a.PublishRoom(t.Context(), roomID, m); err != nil {
				t.Fatalf("发布 %T：%v", m, err)
			}
		}
		for i, received := range deliveries {
			for _, want := range samples {
				if got := nextDelivery(t, received); !reflect.DeepEqual(got, port.RoomDelivery{Room: roomID, Message: want}) {
					t.Errorf("%s 收到 %#v\n期望 %#v", []string{"a", "b"}[i], got, want)
				}
			}
		}
	})

	t.Run("跨实例的房间消息里只有正在播放的条目带准备结果", func(t *testing.T) {
		node := open(t)
		a, b := node(unique("a")), node(unique("b"))
		received := receive(t, b)
		roomID := unique("room")
		if err := b.SubscribeRoom(t.Context(), roomID); err != nil {
			t.Fatal(err)
		}
		pending, queue := preparedEntry, []room.Entry{linkEntry, preparedEntry}
		sentView, wantView := roomView, roomView
		sentView.Snapshot.Pending, sentView.Snapshot.Waiting = &pending, queue
		wantView.Snapshot.Waiting = []room.Entry{linkEntry, sunnyEntry}
		emitted := func(e room.Event) port.RoomMessage { return port.Emitted{Emit: room.Emit{Term: 3, Seq: 1, Event: e}} }
		replied := func(v room.View) port.RoomMessage {
			return port.Replied{Reply: room.Reply{RequestID: "a/r1", Result: room.SyncResult{View: v}}}
		}
		for _, c := range []struct{ sent, want port.RoomMessage }{
			{emitted(room.PlaybackChanged{Current: &nowPlaying}), emitted(room.PlaybackChanged{Current: &nowPlaying})},
			{emitted(room.QueueChanged{Pending: &pending, Waiting: queue}), emitted(room.QueueChanged{Pending: &sunnyEntry, Waiting: []room.Entry{linkEntry, sunnyEntry}})},
			{emitted(room.Activity{Kind: room.ActivityLiked, Member: alice, Entry: preparedEntry}), emitted(room.Activity{Kind: room.ActivityLiked, Member: alice, Entry: sunnyEntry})},
			{replied(sentView), replied(wantView)},
		} {
			if err := a.PublishRoom(t.Context(), roomID, c.sent); err != nil {
				t.Fatalf("发布 %T：%v", c.sent, err)
			}
			if got, want := nextDelivery(t, received), (port.RoomDelivery{Room: roomID, Message: c.want}); !reflect.DeepEqual(got, want) {
				t.Errorf("收到 %#v\n期望 %#v", got, want)
			}
		}
		// 房间状态和 effect 共用这些切片和指针。
		if pending != preparedEntry || !slices.Equal(queue, []room.Entry{linkEntry, preparedEntry}) {
			t.Error("发布时改写了房间状态")
		}
	})

	t.Run("输入只送达目标实例", func(t *testing.T) {
		node := open(t)
		aID, bID, cID := unique("a"), unique("b"), unique("c")
		a, b, c := node(aID), node(bID), node(cID)
		toA, toB, toC := receive(t, a), receive(t, b), receive(t, c)
		ctx := t.Context()
		samples := inputSamples(unique("room"), aID)
		if ok, err := a.SendInput(ctx, bID, samples[0]); err != nil || ok {
			t.Errorf("b 还没有订阅本实例的频道时 SendInput = %v, %v，期望 false", ok, err)
		}
		for _, n := range []ClusterNode{a, b, c} {
			if err := n.SubscribeInstance(ctx); err != nil {
				t.Fatal(err)
			}
		}
		for _, in := range samples {
			if ok, err := a.SendInput(ctx, bID, in); err != nil || !ok {
				t.Fatalf("SendInput(%#v) = %v, %v", in.Input, ok, err)
			}
		}
		toSelf := port.RoomInput{Room: unique("room"), Gateway: aID, Input: room.Leave{Session: "a/s2"}}
		toOther := port.RoomInput{Room: unique("room"), Gateway: aID, Input: room.Leave{Session: "a/s3"}}
		for _, s := range []struct {
			instance string
			in       port.RoomInput
		}{{aID, toSelf}, {cID, toOther}} {
			if ok, err := a.SendInput(ctx, s.instance, s.in); err != nil || !ok {
				t.Fatalf("SendInput(%s) = %v, %v", s.instance, ok, err)
			}
		}
		for _, want := range samples {
			expectInput(t, toB, want)
		}
		// 发给 a 和 c 的输入排在发给 b 的之后。a 或 c 先收到的如果不是发给自己的输入，就说明收到了发给 b 的输入。
		expectInput(t, toA, toSelf)
		expectInput(t, toC, toOther)
	})

	t.Run("成员资料、删除的会话和准备结果送达各个实例，包括发布方自己", func(t *testing.T) {
		node := open(t)
		a, b := node(unique("a")), node(unique("b"))
		deliveries := []<-chan port.Delivery{receive(t, a), receive(t, b)}
		for _, n := range []ClusterNode{a, b} {
			if err := n.SubscribeInstance(t.Context()); err != nil {
				t.Fatal(err)
			}
		}
		hashes := [][]byte{{0x01, 0x02}, {0xff, 0x00, 0x7f}}
		outcomes := []port.PrepareOutcome{
			{Track: sunnyEntry.Track, JobID: 1},
			{Track: track.Track{Source: track.QQ, ID: "0039MnYb0qxYhV"}, JobID: 1 << 40, Reason: "酷我没有提供这首歌的播放地址"},
		}
		if err := a.PublishMember(t.Context(), alice); err != nil {
			t.Fatal(err)
		}
		if err := a.PublishSessionsRevoked(t.Context(), hashes); err != nil {
			t.Fatal(err)
		}
		for _, o := range outcomes {
			if err := a.PublishPrepared(t.Context(), o); err != nil {
				t.Fatal(err)
			}
		}
		want := []port.Delivery{
			port.MemberDelivery{Member: alice},
			port.SessionsRevokedDelivery{SessionTokenHashes: hashes},
			port.PreparedDelivery{Outcome: outcomes[0]},
			port.PreparedDelivery{Outcome: outcomes[1]},
		}
		for i, received := range deliveries {
			for _, w := range want {
				if got := nextDelivery(t, received); !reflect.DeepEqual(got, w) {
					t.Errorf("%s 收到 %#v\n期望 %#v", []string{"a", "b"}[i], got, w)
				}
			}
		}
	})
}
