package redis

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	goredis "github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/member"
	"github.com/dihemoe/alisten/internal/domain/room"
	"github.com/dihemoe/alisten/internal/domain/track"
)

type busFixture struct {
	bus        *Bus
	instance   string
	deliveries chan port.Delivery
	metrics    *prometheus.Registry
	logs       *logBuffer
	tracer     *sdktrace.TracerProvider
	spans      *tracetest.SpanRecorder
}

type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *logBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// Pub/Sub 不按逻辑库隔离，所以实例 ID 和房间 ID 都是随机的。
func newBus(t *testing.T, client *goredis.Client, version string) *busFixture {
	t.Helper()
	f := &busFixture{instance: rand.Text(), deliveries: make(chan port.Delivery, 64), metrics: prometheus.NewRegistry(), logs: &logBuffer{}, spans: tracetest.NewSpanRecorder()}
	f.tracer = sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(f.spans))
	// 生产环境的 logger 固定带有本实例的构建版本 version 字段。
	f.bus = NewBus(client, f.instance, version, f.metrics, f.tracer, slog.New(slog.NewJSONHandler(f.logs, nil)).With("version", version))
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			d, err := f.bus.Receive(ctx)
			if err != nil {
				return
			}
			f.deliveries <- d
		}
	})
	t.Cleanup(func() {
		cancel()
		wg.Wait()
	})
	return f
}

func (f *busFixture) next(t *testing.T) port.Delivery {
	t.Helper()
	select {
	case d := <-f.deliveries:
		return d
	case <-time.After(20 * time.Second):
		t.Fatal("没有收到消息")
		return nil
	}
}

func randomRoom() string { return rand.Text() }

var (
	when    = time.Date(2026, 10, 22, 8, 30, 0, 123456789, time.UTC)
	alice   = member.Member{ID: "00000000-0000-7000-8000-00000000000a", Name: "alice", AvatarHash: "c160f8cc69a4f0bf2b0362752353d060"}
	sunny   = track.Track{Source: track.NetEase, ID: "186016"}
	waiting = room.Entry{
		ID: 12, Track: sunny, By: alice, Likes: 2, Ready: true, AddedAt: when,
		Meta: track.Meta{Title: "晴天", Artist: "周杰伦", Album: "叶惠美", CoverURL: "https://p1.music.126.net/example.jpg", PageURL: "https://music.163.com/#/song?id=186016", Duration: 269 * time.Second},
		Prep: track.Prepared{Duration: 269500 * time.Millisecond, Gain: 0.75, Lyric: "[00:01.00]故事的小黄花"},
	}
	link    = room.Entry{ID: 13, Track: track.Track{Source: track.Link, ID: "https://www.youtube.com/watch?v=x"}, Meta: track.Meta{Title: "https://www.youtube.com/watch?v=x"}, Auto: true, AddedAt: when}
	current = room.NowPlaying{Entry: waiting, StartedAt: when.Add(time.Minute)}
	view    = room.View{
		Term: 3, Seq: 128,
		Snapshot: room.Snapshot{
			Info:     room.Info{ID: "01a15778-1911-7a28-8b2e-e08c2a25f388", Name: "大厅", Description: "常驻", Kind: room.Persistent},
			Mode:     room.Random,
			EntrySeq: 13,
			Waiting:  []room.Entry{link},
			Pending:  &waiting,
			Current:  &current,
		},
		Sessions: []room.Session{{ID: "a/s1", Member: alice}},
	}
)

func roomMessageSamples() []port.RoomMessage {
	return []port.RoomMessage{
		port.OwnerChanged{Term: 3},
		port.Emitted{Emit: room.Emit{Term: 3, Seq: 1, Event: room.PlaybackChanged{Current: &current}}},
		port.Emitted{Emit: room.Emit{Term: 3, Seq: 3, Event: room.QueueChanged{Pending: &waiting, Waiting: []room.Entry{link, waiting}}}},
		port.Emitted{Emit: room.Emit{Term: 3, Seq: 4, Event: room.MembersChanged{Sessions: view.Sessions}}},
		port.Emitted{Emit: room.Emit{Term: 3, Seq: 5, Event: room.ModeChanged{Mode: room.Random}}},
		port.Emitted{Emit: room.Emit{Term: 3, Seq: 6, Event: room.InfoChanged{Name: "大厅", Description: "常驻"}}},
		port.Emitted{Emit: room.Emit{Term: 3, Seq: 7, Event: room.ChatPosted{Member: alice, Text: "你好", SentAt: when}}},
		port.Emitted{Emit: room.Emit{Term: 3, Seq: 1 << 40, Event: room.Activity{Kind: room.ActivityVoted, Member: alice, Entry: waiting, Votes: 1, Required: 2, Reason: "原因"}}},
		port.Replied{Reply: room.Reply{RequestID: "a/r1", Result: room.Done{}}},
		port.Replied{Reply: room.Reply{RequestID: "a/r2", Result: room.EnqueueResult{Entry: waiting}}},
		port.Replied{Reply: room.Reply{RequestID: "a/r3", Result: room.LikeResult{Likes: 3}}},
		port.Replied{Reply: room.Reply{RequestID: "a/r4", Result: room.VoteResult{Votes: 1, Required: 2, Skipped: true}}},
		port.Replied{Reply: room.Reply{RequestID: "a/r5", Result: room.SyncResult{View: view}}},
		port.Replied{Reply: room.Reply{RequestID: "a/r6", Err: &room.Error{Kind: room.QueueFull, Message: "临时房间的播放列表最多 10 首歌，等播放几首之后再点。"}}},
		port.Rejected{RequestID: "a/r8", NotFound: true},
		port.RoomClosed{},
		port.OwnerReleased{},
		port.TicketsRevoked{},
		port.APIKeyReset{},
	}
}

func TestRoomMessagesArriveInOrder(t *testing.T) {
	client := newClient(t)
	a, b := newBus(t, client, "v1"), newBus(t, client, "v1")
	roomID := randomRoom()
	if err := b.bus.SubscribeRoom(t.Context(), roomID); err != nil {
		t.Fatal(err)
	}
	samples := roomMessageSamples()
	for _, m := range samples {
		if err := a.bus.PublishRoom(t.Context(), roomID, m); err != nil {
			t.Fatalf("发布 %T：%v", m, err)
		}
	}
	for _, want := range samples {
		got := b.next(t)
		if !reflect.DeepEqual(got, port.RoomDelivery{Room: roomID, Message: want}) {
			t.Errorf("收到 %#v\n期望 %#v", got, want)
		}
	}
}

func TestRoomInputs(t *testing.T) {
	client := newClient(t)
	a, b := newBus(t, client, "v1"), newBus(t, client, "v1")
	join := port.RoomInput{Room: randomRoom(), Gateway: a.instance, Input: room.Join{Session: "a/s1", Member: alice}, Takeover: true}
	if ok, err := a.bus.SendInput(t.Context(), b.instance, join); err != nil || ok {
		t.Errorf("对方还没有订阅时 SendInput = %v, %v，期望 false", ok, err)
	}
	if err := b.bus.SubscribeInstance(t.Context()); err != nil {
		t.Fatal(err)
	}
	name := "新大厅"
	samples := []port.RoomInput{join}
	for _, a := range []room.Action{room.Enqueue{Track: sunny, Meta: waiting.Meta}, room.Remove{Entry: 12}, room.Like{Entry: 12}, room.VoteSkip{}, room.SetPlayMode{Mode: room.Random}, room.Chat{Text: "你好"}, room.Sync{}} {
		samples = append(samples, port.RoomInput{Room: join.Room, Gateway: "g", Input: room.Command{RequestID: "g/r1", Member: alice, Action: a}, Takeover: true})
	}
	samples = append(samples,
		port.RoomInput{Room: join.Room, Gateway: "g", Input: room.Leave{Session: "a/s1"}},
		port.RoomInput{Room: join.Room, Gateway: "g", Input: room.MemberUpdated{Member: alice}},
		port.RoomInput{Room: join.Room, Gateway: "g", Input: room.Delete{RequestID: "g/r2"}, Takeover: true},
		port.RoomInput{Room: join.Room, Gateway: "g", Patch: &port.InfoPatch{RequestID: "g/r3", Name: &name}, Takeover: true},
	)
	for _, in := range samples {
		if ok, err := a.bus.SendInput(t.Context(), b.instance, in); err != nil || !ok {
			t.Fatalf("SendInput(%#v) = %v, %v", in.Input, ok, err)
		}
	}
	for _, want := range samples {
		got, ok := b.next(t).(port.InputDelivery)
		if !ok || !reflect.DeepEqual(got.Input, want) {
			t.Errorf("收到 %#v\n期望 %#v", got, want)
		}
		if ok && trace.SpanContextFromContext(got.Context).IsValid() {
			t.Errorf("发送时没有 span 的输入带着追踪上下文：%#v", got)
		}
	}
	if n := len(b.spans.Ended()); n != 0 {
		t.Errorf("没有追踪上下文的输入记了 %d 个 span", n)
	}
	if err := a.bus.PublishMember(t.Context(), alice); err != nil {
		t.Fatal(err)
	}
	if got := b.next(t); got != (port.MemberDelivery{Member: alice}) {
		t.Errorf("收到 %#v，期望成员资料", got)
	}
	if _, err := a.bus.SendInput(t.Context(), b.instance, port.RoomInput{Room: join.Room, Input: room.Timer{Token: 1}}); err == nil {
		t.Error("计时器只在本实例内投递，不应能编码")
	}
}

func TestInputCarriesTraceContext(t *testing.T) {
	client := newClient(t)
	a, b := newBus(t, client, "v1"), newBus(t, client, "v1")
	if err := b.bus.SubscribeInstance(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx, sending := a.tracer.Tracer("test").Start(t.Context(), "WS queue.add")
	in := port.RoomInput{Room: randomRoom(), Gateway: a.instance, Input: room.Command{RequestID: "a/r1", Member: alice, Action: room.Sync{}}, Takeover: true}
	if ok, err := a.bus.SendInput(ctx, b.instance, in); err != nil || !ok {
		t.Fatalf("SendInput = %v, %v", ok, err)
	}
	sending.End()

	got, ok := b.next(t).(port.InputDelivery)
	if !ok || !reflect.DeepEqual(got.Input, in) {
		t.Fatalf("收到 %#v，期望 %#v", got, in)
	}
	received := b.spans.Ended()
	if len(received) != 1 {
		t.Fatalf("收到输入时记了 %d 个 span，期望 1 个", len(received))
	}
	span := received[0]
	if span.Name() != "receive room input" || span.SpanKind() != trace.SpanKindConsumer {
		t.Errorf("span = %s（%v），期望 consumer span receive room input", span.Name(), span.SpanKind())
	}
	if span.SpanContext().TraceID() != sending.SpanContext().TraceID() || span.Parent().SpanID() != sending.SpanContext().SpanID() || !span.Parent().IsRemote() {
		t.Errorf("span 的父 span = %v，期望是网关一侧发送时的 span %v", span.Parent(), sending.SpanContext())
	}
	if attrs := span.Attributes(); !slices.ContainsFunc(attrs, func(kv attribute.KeyValue) bool { return kv.Key == "alisten.room_id" && kv.Value.AsString() == in.Room }) {
		t.Errorf("span 的属性 = %v，期望带着房间 ID", attrs)
	}
	if sc := trace.SpanContextFromContext(got.Context); sc.SpanID() != span.SpanContext().SpanID() {
		t.Errorf("交给房间的 ctx 中的 span = %v，期望是收到输入时记的 span", sc)
	}
}

func TestDroppedInputRecordsNoSpan(t *testing.T) {
	client := newClient(t)
	a, b := newBus(t, client, "v1"), newBus(t, client, "v1")
	if err := b.bus.SubscribeInstance(t.Context()); err != nil {
		t.Fatal(err)
	}
	const traceparent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	payload := fmt.Sprintf(`{"Version":"v1","Kind":"RoomInput","Trace":{"traceparent":%q},"Data":{"Input":"Teleported","Data":{}}}`, traceparent)
	if err := client.Publish(t.Context(), instanceChannel(b.instance), payload).Err(); err != nil {
		t.Fatal(err)
	}
	// 同一个频道上的消息按顺序处理：收到后面这条有效的输入时，前面那条已经丢弃了。
	valid := port.RoomInput{Room: randomRoom(), Gateway: a.instance, Input: room.Join{Session: "a/s1", Member: alice}, Takeover: true}
	if ok, err := a.bus.SendInput(t.Context(), b.instance, valid); err != nil || !ok {
		t.Fatalf("SendInput = %v, %v", ok, err)
	}
	if got, ok := b.next(t).(port.InputDelivery); !ok || !reflect.DeepEqual(got.Input, valid) {
		t.Fatalf("收到 %#v，期望前面无法解码的输入被丢弃", got)
	}
	if got := droppedCounts(t, b.metrics)["malformed"]; got != 1 {
		t.Errorf("alisten_bus_dropped_total{malformed} = %v，期望 1", got)
	}
	if n := len(b.spans.Started()); n != 0 {
		t.Errorf("丢弃的输入开始了 %d 个 span", n)
	}
}

func TestPreparedOutcomes(t *testing.T) {
	client := newClient(t)
	a, b := newBus(t, client, "v1"), newBus(t, client, "v1")
	for _, f := range []*busFixture{a, b} {
		if err := f.bus.SubscribeInstance(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	outcomes := []port.PrepareOutcome{
		{Track: sunny, JobID: 1},
		{Track: track.Track{Source: track.QQ, ID: "0039MnYb0qxYhV"}, JobID: 1 << 40, Reason: "酷我没有提供这首歌的播放地址"},
	}
	for _, o := range outcomes {
		if err := a.bus.PublishPrepared(t.Context(), o); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []*busFixture{a, b} {
		for _, o := range outcomes {
			if got := f.next(t); got != (port.PreparedDelivery{Outcome: o}) {
				t.Errorf("收到 %#v，期望 %#v", got, o)
			}
		}
	}
}

func TestUnsubscribeRoom(t *testing.T) {
	client := newClient(t)
	a, b := newBus(t, client, "v1"), newBus(t, client, "v1")
	gone, kept := randomRoom(), randomRoom()
	if err := b.bus.SubscribeRoom(t.Context(), gone); err != nil {
		t.Fatal(err)
	}
	if err := b.bus.UnsubscribeRoom(t.Context(), gone); err != nil {
		t.Fatal(err)
	}
	// 退订不等服务端确认。同一个连接上的命令按顺序处理，之后的订阅生效时，退订已经生效。
	if err := b.bus.SubscribeRoom(t.Context(), kept); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{gone, kept} {
		if err := a.bus.PublishRoom(t.Context(), id, port.RoomClosed{}); err != nil {
			t.Fatal(err)
		}
	}
	if got := b.next(t); got != (port.RoomDelivery{Room: kept, Message: port.RoomClosed{}}) {
		t.Errorf("退订之后收到 %#v，期望只收到另一个房间的消息", got)
	}
}

func TestDroppedMessages(t *testing.T) {
	client := newClient(t)
	old, b := newBus(t, client, "v0"), newBus(t, client, "v1")
	roomID := randomRoom()
	if err := b.bus.SubscribeRoom(t.Context(), roomID); err != nil {
		t.Fatal(err)
	}
	if err := old.bus.PublishRoom(t.Context(), roomID, port.RoomClosed{}); err != nil {
		t.Fatal(err)
	}
	for _, payload := range []string{"不是 JSON", `{"Version":"v1","Kind":"Teleported","Data":{}}`} {
		if err := client.Publish(t.Context(), roomChannel(roomID), payload).Err(); err != nil {
			t.Fatal(err)
		}
	}
	if err := b.bus.PublishRoom(t.Context(), roomID, port.OwnerReleased{}); err != nil {
		t.Fatal(err)
	}
	if got := b.next(t); got != (port.RoomDelivery{Room: roomID, Message: port.OwnerReleased{}}) {
		t.Errorf("收到 %#v，期望前三条消息都被丢弃", got)
	}
	if got, want := droppedCounts(t, b.metrics), map[string]float64{"version": 1, "malformed": 2}; !maps.Equal(got, want) {
		t.Errorf("alisten_bus_dropped_total = %v，期望 %v", got, want)
	}
	// 对端的构建版本不能占用 logger 固定的 version 字段，否则 JSON 里有两个 version，Loki 只会留下其中一个。
	type logged struct {
		Level, From, Version string
		RemoteVersion        string `json:"remote_version"`
	}
	var got []logged
	for line := range strings.Lines(strings.TrimSpace(b.logs.String())) {
		if n := strings.Count(line, `"version"`); n != 1 {
			t.Errorf("日志中有 %d 个 version 字段，期望 1 个：%s", n, line)
		}
		var entry logged
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatal(err)
		}
		got = append(got, entry)
	}
	want := []logged{
		{Level: "ERROR", From: old.instance, Version: "v1", RemoteVersion: "v0"},
		{Level: "ERROR", Version: "v1"},
		{Level: "ERROR", Version: "v1"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("日志 = %+v，期望 %+v", got, want)
	}
}

func droppedCounts(t *testing.T, metrics *prometheus.Registry) map[string]float64 {
	t.Helper()
	families, err := metrics.Gather()
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]float64{}
	for _, f := range families {
		if f.GetName() != "alisten_bus_dropped_total" {
			continue
		}
		for _, m := range f.GetMetric() {
			counts[m.GetLabel()[0].GetValue()] = m.GetCounter().GetValue()
		}
	}
	return counts
}

func TestSubscribeRoomWaitsForServer(t *testing.T) {
	b := newBus(t, newClient(t), "v1")
	if err := b.bus.SubscribeInstance(t.Context()); err != nil {
		t.Fatal(err)
	}
	pause := 500 * time.Millisecond
	if err := b.bus.client.Do(t.Context(), "CLIENT", "PAUSE", pause.Milliseconds(), "ALL").Err(); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := b.bus.SubscribeRoom(t.Context(), randomRoom()); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed < pause*4/5 {
		t.Errorf("服务端暂停 %v 时订阅 %v 就返回了，期望等到服务端处理之后", pause, elapsed)
	}
}

func killSubscriptions(t *testing.T, client *goredis.Client) {
	t.Helper()
	clients, err := client.Do(t.Context(), "CLIENT", "LIST", "TYPE", "pubsub").Text()
	if err != nil {
		t.Fatal(err)
	}
	db := fmt.Sprintf(" db=%d ", client.Options().DB)
	killed := 0
	for line := range strings.Lines(clients) {
		id, _, _ := strings.Cut(strings.TrimPrefix(line, "id="), " ")
		if strings.Contains(line, db) {
			if err := client.Do(t.Context(), "CLIENT", "KILL", "ID", id).Err(); err != nil {
				t.Fatal(err)
			}
			killed++
		}
	}
	if killed != 1 {
		t.Fatalf("断开了 %d 个订阅连接，期望 1 个", killed)
	}
}

func TestSubscriptionLostAfterDisconnect(t *testing.T) {
	a, b := newBus(t, newClient(t), "v1"), newBus(t, newClient(t), "v1")
	roomID := randomRoom()
	if err := b.bus.SubscribeRoom(t.Context(), roomID); err != nil {
		t.Fatal(err)
	}
	killSubscriptions(t, b.bus.client)
	if got := b.next(t); got != (port.SubscriptionLost{}) {
		t.Fatalf("订阅连接断开之后收到 %#v，期望 SubscriptionLost", got)
	}
	if err := b.bus.SubscribeRoom(t.Context(), randomRoom()); err != nil {
		t.Fatal(err)
	}
	if err := a.bus.PublishRoom(t.Context(), roomID, port.RoomClosed{}); err != nil {
		t.Fatal(err)
	}
	if got := b.next(t); got != (port.RoomDelivery{Room: roomID, Message: port.RoomClosed{}}) {
		t.Errorf("重新连接之后收到 %#v", got)
	}
}

func TestSubscriptionLostWhenConnectionStalls(t *testing.T) {
	a, b := newBus(t, newClient(t), "v1"), newBus(t, newClient(t), "v1")
	roomID := randomRoom()
	// 接收循环的超时从它收到订阅确认时算起，早于 CLIENT PAUSE 返回，所以从订阅之前开始计时。
	start := time.Now()
	if err := b.bus.SubscribeRoom(t.Context(), roomID); err != nil {
		t.Fatal(err)
	}
	pause := 2*pingInterval + 2*time.Second
	if err := a.bus.client.Do(t.Context(), "CLIENT", "PAUSE", pause.Milliseconds(), "ALL").Err(); err != nil {
		t.Fatal(err)
	}
	if got := b.next(t); got != (port.SubscriptionLost{}) {
		t.Fatalf("连接停止响应之后收到 %#v，期望 SubscriptionLost", got)
	}
	if elapsed := time.Since(start); elapsed < 2*pingInterval || elapsed > pause {
		t.Errorf("%v 之后才发现连接停止响应，期望约 %v", elapsed, 2*pingInterval)
	}
	if err := b.bus.SubscribeRoom(t.Context(), randomRoom()); err != nil {
		t.Fatal(err)
	}
	if err := a.bus.PublishRoom(t.Context(), roomID, port.RoomClosed{}); err != nil {
		t.Fatal(err)
	}
	if got := b.next(t); got != (port.RoomDelivery{Room: roomID, Message: port.RoomClosed{}}) {
		t.Errorf("换了新连接之后收到 %#v", got)
	}
}

func TestLateCallerDoesNotBreakTheSharedConnection(t *testing.T) {
	a, b := newBus(t, newClient(t), "v1"), newBus(t, newClient(t), "v1")
	kept, other := randomRoom(), randomRoom()
	for _, id := range []string{kept, other} {
		if err := b.bus.SubscribeRoom(t.Context(), id); err != nil {
			t.Fatal(err)
		}
	}

	expired, cancel := context.WithTimeout(t.Context(), -time.Millisecond)
	defer cancel()
	if err := b.bus.SubscribeRoom(expired, randomRoom()); err == nil {
		t.Error("期限已经过去的调用方订阅成功了，期望失败")
	}
	if err := b.bus.UnsubscribeRoom(expired, other); err == nil {
		t.Error("期限已经过去的调用方退订成功了，期望失败")
	}
	short, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	// 能否等到服务端的确认取决于调度，这里只要求订阅连接没有被打断。
	_ = b.bus.SubscribeRoom(short, randomRoom())
	_ = b.bus.UnsubscribeRoom(short, other)

	if err := a.bus.PublishRoom(t.Context(), kept, port.RoomClosed{}); err != nil {
		t.Fatal(err)
	}
	if got := b.next(t); got != (port.RoomDelivery{Room: kept, Message: port.RoomClosed{}}) {
		t.Errorf("迟到的调用方之后收到 %#v，期望订阅连接没有被打断", got)
	}
	if err := b.bus.SubscribeRoom(t.Context(), randomRoom()); err != nil {
		t.Errorf("迟到的调用方之后订阅失败：%v", err)
	}
}

// Freeze 之后，已有的连接保持打开，但收到的字节全部丢弃，不再转发：
// 对客户端来说这是半开的 TCP 连接，写得出去，读不到错误，也等不到回复。Freeze 之后新建的连接照常转发。
type freezeProxy struct {
	listener net.Listener

	mu         sync.Mutex
	conns      []*proxiedConn
	accepted   int
	hold, held chan struct{}
}

type proxiedConn struct {
	client, server net.Conn
	frozen         atomic.Bool
}

func startFreezeProxy(t *testing.T, target string) *freezeProxy {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &freezeProxy{listener: listener}
	t.Cleanup(p.close)
	go func() {
		for {
			client, err := listener.Accept()
			if err != nil {
				return
			}
			p.mu.Lock()
			p.accepted++
			hold, held := p.hold, p.held
			p.mu.Unlock()
			if hold != nil {
				select {
				case held <- struct{}{}:
				default:
				}
				<-hold
			}
			server, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(t.Context(), "tcp", target)
			if err != nil {
				_ = client.Close()
				continue
			}
			c := &proxiedConn{client: client, server: server}
			p.mu.Lock()
			p.conns = append(p.conns, c)
			p.mu.Unlock()
			go c.pump(server, client)
			go c.pump(client, server)
		}
	}()
	return p
}

func (p *freezeProxy) addr() string { return p.listener.Addr().String() }

func (p *freezeProxy) Freeze() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.conns {
		c.frozen.Store(true)
	}
}

// 此后新建的连接在建立后停住，客户端收不到任何回复，直到调用返回的 release；每停住一个连接，held 通知一次。
func (p *freezeProxy) HoldNew() (held <-chan struct{}, release func()) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.hold, p.held = make(chan struct{}), make(chan struct{}, 1)
	hold := p.hold
	return p.held, func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.hold, p.held = nil, nil
		close(hold)
	}
}

func (p *freezeProxy) DropAll() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.conns {
		c.shut()
	}
}

func (p *freezeProxy) Accepted() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.accepted
}

func (p *freezeProxy) close() {
	_ = p.listener.Close()
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.conns {
		c.shut()
	}
}

func (c *proxiedConn) shut() {
	_ = c.client.Close()
	_ = c.server.Close()
}

func (c *proxiedConn) pump(dst, src net.Conn) {
	defer c.shut()
	buf := make([]byte, 32<<10)
	for {
		n, err := src.Read(buf)
		if n > 0 && !c.frozen.Load() {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

func TestHalfOpenSubscriptionIsReplaced(t *testing.T) {
	proxy := startFreezeProxy(t, server.Addr)
	opts := *server
	opts.Addr = proxy.addr()
	opts.DB = int(databases.Add(1))
	through := goredis.NewClient(&opts)
	t.Cleanup(func() { _ = through.Close() })

	a, b := newBus(t, newClient(t), "v1"), newBus(t, through, "v1")
	roomID := randomRoom()
	if err := b.bus.SubscribeRoom(t.Context(), roomID); err != nil {
		t.Fatal(err)
	}
	proxy.Freeze()
	if got := b.next(t); got != (port.SubscriptionLost{}) {
		t.Fatalf("连接半开之后收到 %#v，期望 SubscriptionLost", got)
	}

	// 订阅生效之前发布的消息会丢失，所以反复发布，直到收到为止。
	closed := port.RoomDelivery{Room: roomID, Message: port.RoomClosed{}}
	timeout := time.After(20 * time.Second)
	for {
		if err := a.bus.PublishRoom(t.Context(), roomID, port.RoomClosed{}); err != nil {
			t.Fatal(err)
		}
		select {
		case got := <-b.deliveries:
			if got != closed {
				t.Fatalf("换了新连接之后收到 %#v，期望 %#v", got, closed)
			}
			return
		case <-timeout:
			t.Fatal("连接半开之后 20 秒内仍收不到房间的消息，订阅连接没有换成新的")
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func proxiedClient(t *testing.T, proxy *freezeProxy) *goredis.Client {
	t.Helper()
	opts := *server
	opts.Addr = proxy.addr()
	opts.DB = int(databases.Add(1))
	client := goredis.NewClient(&opts)
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestCallerWaitingOnReconnectDoesNotBreakTheNewConnection(t *testing.T) {
	proxy := startFreezeProxy(t, server.Addr)
	b := newBus(t, proxiedClient(t, proxy), "v1")
	if err := b.bus.SubscribeRoom(t.Context(), randomRoom()); err != nil {
		t.Fatal(err)
	}
	held, release := proxy.HoldNew()
	proxy.DropAll()
	select {
	case <-held:
	case <-time.After(10 * time.Second):
		t.Fatal("连接断开之后 go-redis 没有重新连接")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- b.bus.SubscribeRoom(ctx, randomRoom()) }()
	time.Sleep(500 * time.Millisecond)
	release()
	if err := <-done; err == nil {
		t.Error("期限已经用完的调用方订阅成功了，期望失败")
	}
	time.Sleep(2 * time.Second)
	if n := proxy.Accepted(); n != 2 {
		t.Errorf("代理接受了 %d 个连接，期望 2 个：迟到的写入让刚建立的连接被当作坏连接重建了", n)
	}
}

func TestSubscribeGivesUpWithCallerWhileConnecting(t *testing.T) {
	opts := *server
	opts.Dialer = func(ctx context.Context, _, _ string) (net.Conn, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	client := goredis.NewClient(&opts)
	t.Cleanup(func() { _ = client.Close() })
	bus := NewBus(client, "a", "v1", prometheus.NewRegistry(), noop.NewTracerProvider(), slog.New(slog.DiscardHandler))
	start := time.Now()
	var wg sync.WaitGroup
	for i := range 3 {
		wg.Go(func() {
			ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
			defer cancel()
			if err := bus.SubscribeRoom(ctx, fmt.Sprint("r", i)); err == nil {
				t.Error("连不上 Redis 时订阅成功了")
			}
		})
	}
	wg.Wait()
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("3 个期限 200ms 的订阅 %v 之后才全部返回，期望随调用方结束", elapsed)
	}
}

func TestSubscribeIsConfirmedOnReplacedConnection(t *testing.T) {
	proxy := startFreezeProxy(t, server.Addr)
	b := newBus(t, proxiedClient(t, proxy), "v1")
	if err := b.bus.SubscribeInstance(t.Context()); err != nil {
		t.Fatal(err)
	}
	proxy.Freeze()
	// 半开的连接要 2 个 pingInterval 才被发现并换掉。
	ctx, cancel := context.WithTimeout(t.Context(), 4*pingInterval)
	defer cancel()
	if err := b.bus.SubscribeRoom(ctx, randomRoom()); err != nil {
		t.Fatalf("换了新连接之后订阅仍没有得到确认：%v", err)
	}
}

func TestReplaceDoesNotWaitForWriterOnOldSubscription(t *testing.T) {
	proxy := startFreezeProxy(t, server.Addr)
	bus := NewBus(proxiedClient(t, proxy), "a", "v1", prometheus.NewRegistry(), noop.NewTracerProvider(), slog.New(slog.DiscardHandler))
	old := bus.ps
	var wg sync.WaitGroup
	defer wg.Wait()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	held, release := proxy.HoldNew()
	// old 还没有连接，这次订阅持有 old 的锁建立连接，而代理不回复。
	wg.Go(func() { _ = bus.SubscribeRoom(ctx, randomRoom()) })
	select {
	case <-held:
	case <-time.After(10 * time.Second):
		release()
		t.Fatal("订阅没有建立连接")
	}

	// ReceiveTimeout 也要 old 的锁，无法从外部安排写入恰好在接收循环最后一次接收之后取得锁，所以直接调用 replace。
	replaced := make(chan struct{})
	go func() {
		bus.replace(old)
		close(replaced)
	}()
	select {
	case <-replaced:
	case <-time.After(time.Second):
		t.Error("换下订阅对象 1 秒内没有返回，期望不等旧对象上卡在网络上的写入")
	}
	expired, cancelExpired := context.WithTimeout(t.Context(), -time.Millisecond)
	defer cancelExpired()
	unsubscribed := make(chan struct{})
	go func() {
		_ = bus.UnsubscribeRoom(expired, randomRoom())
		close(unsubscribed)
	}()
	select {
	case <-unsubscribed:
	case <-time.After(time.Second):
		t.Error("换下订阅对象之后退订 1 秒内没有返回，期望不等旧对象上卡在网络上的写入")
	}

	release()
	<-replaced
	deadline := time.Now().Add(10 * time.Second)
	for !errors.Is(old.Ping(t.Context()), goredis.ErrClosed) {
		if time.Now().After(deadline) {
			t.Fatal("旧的订阅对象没有关闭")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestSubscribeAfterCloseDoesNotWaitForReconnect(t *testing.T) {
	proxy := startFreezeProxy(t, server.Addr)
	bus := NewBus(proxiedClient(t, proxy), "a", "v1", prometheus.NewRegistry(), noop.NewTracerProvider(), slog.New(slog.DiscardHandler))
	var wg sync.WaitGroup
	defer wg.Wait()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	wg.Go(func() {
		for {
			if _, err := bus.Receive(ctx); err != nil {
				return
			}
		}
	})
	if err := bus.SubscribeRoom(t.Context(), randomRoom()); err != nil {
		t.Fatal(err)
	}
	held, release := proxy.HoldNew()
	proxy.DropAll()
	select {
	case <-held:
	case <-time.After(10 * time.Second):
		release()
		t.Fatal("连接断开之后 go-redis 没有重新连接")
	}

	// 接收循环正持有订阅对象的锁重新连接，而代理不回复。
	cancel()
	<-bus.done
	subscribed := make(chan error, 1)
	go func() { subscribed <- bus.SubscribeRoom(t.Context(), randomRoom()) }()
	select {
	case err := <-subscribed:
		if !errors.Is(err, errBusClosed) {
			t.Errorf("关闭之后订阅返回 %v，期望 %v", err, errBusClosed)
		}
	case <-time.After(time.Second):
		t.Error("关闭之后订阅 1 秒内没有返回，期望不等 go-redis 重新连接")
	}
	release()
}

func TestInputOnRoomChannelIsDropped(t *testing.T) {
	b := newBus(t, newClient(t), "v1")
	roomID := randomRoom()
	if err := b.bus.SubscribeRoom(t.Context(), roomID); err != nil {
		t.Fatal(err)
	}
	data, err := encodeInput(port.RoomInput{Room: roomID, Gateway: "x", Input: room.Leave{Session: "x/s1"}})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(envelope{Version: "v1", From: "x", Kind: kindInput, Data: data})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.bus.client.Publish(t.Context(), roomChannel(roomID), payload).Err(); err != nil {
		t.Fatal(err)
	}
	if err := b.bus.PublishRoom(t.Context(), roomID, port.RoomClosed{}); err != nil {
		t.Fatal(err)
	}
	if got := b.next(t); got != (port.RoomDelivery{Room: roomID, Message: port.RoomClosed{}}) {
		t.Errorf("收到 %#v，期望房间频道上的 input 被丢弃", got)
	}
	if got := droppedCounts(t, b.metrics)["malformed"]; got != 1 {
		t.Errorf("丢弃的 malformed 消息 = %v，期望 1", got)
	}
}
