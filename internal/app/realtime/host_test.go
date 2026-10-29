package realtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/dihemoe/alisten/internal/app/catalog"
	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/app/port/porttest"
	"github.com/dihemoe/alisten/internal/domain/member"
	"github.com/dihemoe/alisten/internal/domain/room"
	"github.com/dihemoe/alisten/internal/domain/track"
	"github.com/dihemoe/alisten/internal/lib/logctx"
)

var (
	alice  = member.Member{ID: "00000000-0000-7000-8000-00000000000a", Name: "alice"}
	bob    = member.Member{ID: "00000000-0000-7000-8000-00000000000b", Name: "bob"}
	sunny  = track.Track{Source: track.NetEase, ID: "186016"}
	sunnyM = track.Meta{Title: "晴天", Artist: "周杰伦", Duration: 269 * time.Second}
	sunnyP = track.Prepared{Duration: 3 * time.Minute, Gain: 0.8, Lyric: "[00:01.00]故事的小黄花"}
	rain   = track.Track{Source: track.NetEase, ID: "185811"}
	rainM  = track.Meta{Title: "七里香", Artist: "周杰伦"}
	rainP  = track.Prepared{Duration: 4 * time.Minute, Gain: 1}
)

func cluster(node *porttest.Node, instance string) Cluster {
	return Cluster{Instance: instance, Leases: node, Liveness: node, Bus: node}
}

type noTracks struct{}

func (noTracks) PreparedTrack(context.Context, track.Track) (port.PreparedTrack, bool, error) {
	return port.PreparedTrack{}, false, nil
}

func (noTracks) SavePreparedTrack(context.Context, track.Track, port.PreparedTrack) error {
	return nil
}

type noHistory struct{}

func (noHistory) AutoDJSeeds(context.Context, string, bool) ([]track.Track, error) {
	return nil, nil
}

func (noHistory) AutoDJExclusions(context.Context, string) ([]track.Track, error) {
	return nil, nil
}

type songs struct {
	netease  *porttest.Source
	bilibili *porttest.Bilibili
	music    *catalog.Service
	preparer *porttest.Preparer
}

func newSongs() *songs {
	netease, bilibili := porttest.NewSource(track.NetEase), porttest.NewBilibili()
	// 网关和房间宿主只用到 TrackMeta、ResolveLink、FirstMatch 和 Recommend，用不到房间目录和搜索额度。
	music := catalog.New(netease, porttest.NewSource(track.QQ), bilibili, bilibili, noTracks{}, noHistory{}, nil, nil)
	return &songs{netease: netease, bilibili: bilibili, music: music, preparer: porttest.NewPreparer()}
}

func (s *songs) add(t track.Track, m track.Meta, p track.Prepared) {
	s.netease.Add(t.ID, m, "", nil)
	s.preparer.Add(t, m, p)
}

type spy struct {
	node *porttest.Node
}

func (s spy) watch(t *testing.T, roomID string) {
	t.Helper()
	if err := s.node.SubscribeRoom(t.Context(), roomID); err != nil {
		t.Fatal(err)
	}
}

func (s spy) take() []port.RoomMessage {
	done, cancel := context.WithCancel(context.Background())
	cancel()
	var msgs []port.RoomMessage
	for {
		d, err := s.node.Receive(done)
		if err != nil {
			return msgs
		}
		if d, ok := d.(port.RoomDelivery); ok {
			msgs = append(msgs, d.Message)
		}
	}
}

func describe(m port.RoomMessage) string {
	switch m := m.(type) {
	case port.OwnerChanged:
		return fmt.Sprintf("started %d", m.Term)
	case port.Emitted:
		return fmt.Sprintf("%d/%d %T", m.Emit.Term, m.Emit.Seq, m.Emit.Event)
	case port.Replied:
		if m.Reply.Err != nil {
			return fmt.Sprintf("reply %s %s", m.Reply.RequestID, m.Reply.Err.Message)
		}
		return fmt.Sprintf("reply %s %T", m.Reply.RequestID, m.Reply.Result)
	case port.Rejected:
		if m.NotFound {
			return fmt.Sprintf("rejected %s not found", m.RequestID)
		}
		return fmt.Sprintf("rejected %s", m.RequestID)
	case port.RoomClosed:
		return "closed"
	case port.OwnerReleased:
		return "released"
	case port.TicketsRevoked:
		return "revoked"
	}
	return fmt.Sprintf("%T", m)
}

func checkMessages(t *testing.T, msgs []port.RoomMessage, want ...string) {
	t.Helper()
	got := make([]string, len(msgs))
	for i, m := range msgs {
		got[i] = describe(m)
	}
	if !slices.Equal(got, want) {
		t.Errorf("房间消息：\n%s\n期望：\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

type hostFixture struct {
	store *porttest.Store
	redis *porttest.Redis
	node  *porttest.Node
	host  *Host
	spy   spy
}

func newHostFixture(t *testing.T, snapshots port.RoomSnapshots, store *porttest.Store) *hostFixture {
	t.Helper()
	songs := newSongs()
	songs.add(sunny, sunnyM, sunnyP)
	songs.add(rain, rainM, rainP)
	redis := porttest.NewRedis()
	node := redis.Node("a")
	return startHost(t, &hostFixture{store: store, redis: redis, node: node, spy: spy{redis.Node("spy")}},
		newHost(snapshots, store, songs.preparer, songs.music, cluster(node, "a"), slog.New(slog.DiscardHandler)))
}

func startHost(t *testing.T, f *hostFixture, h *Host) *hostFixture {
	t.Helper()
	f.host = h
	h.Start()
	t.Cleanup(func() {
		if err := h.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return f
}

func (f *hostFixture) createRoom(t *testing.T, kind room.Kind) string {
	t.Helper()
	id, err := f.store.CreateRoom(t.Context(), port.NewRoom{Kind: kind, Name: "房间"})
	if err != nil {
		t.Fatal(err)
	}
	f.spy.watch(t, id)
	return id
}

func (f *hostFixture) send(t *testing.T, roomID string, in room.Input) {
	t.Helper()
	if err := f.host.send(t.Context(), roomID, envelope{input: in}, true); err != nil {
		t.Fatalf("发送 %T：%v", in, err)
	}
	synctest.Wait()
}

func (f *hostFixture) owner(t *testing.T, roomID string) string {
	t.Helper()
	owners, err := f.node.Owners(t.Context(), []string{roomID})
	if err != nil {
		t.Fatal(err)
	}
	return owners[0]
}

func (f *hostFixture) online(t *testing.T, roomID string) int {
	t.Helper()
	counts, err := f.node.OnlineCounts(t.Context(), []string{roomID})
	if err != nil {
		t.Fatal(err)
	}
	return counts[0]
}

func command(id string, by member.Member, a room.Action) room.Command {
	return room.Command{RequestID: room.RequestID(id), Member: by, Action: a}
}

func TestEffectsRunInOrder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		f := newHostFixture(t, store, store)
		id := f.createRoom(t, room.Persistent)
		start := time.Now()

		f.send(t, id, room.Join{Session: "s1", Member: alice})
		f.send(t, id, command("r1", alice, room.Enqueue{Track: sunny, Meta: sunnyM}))
		// 准备的结果在下一批处理：点歌的回复先到，开播在后。
		checkMessages(t, f.spy.take(),
			"started 1",
			"1/1 room.MembersChanged",
			"1/2 room.Activity",
			"1/3 room.QueueChanged",
			"reply r1 room.EnqueueResult",
			"1/4 room.QueueChanged",
			"1/5 room.PlaybackChanged",
		)

		time.Sleep(sunnyP.Duration + 200*time.Millisecond)
		synctest.Wait()
		checkMessages(t, f.spy.take(), "1/6 room.PlaybackChanged")
		plays := store.Plays(id)
		startedAt := start.Add(200 * time.Millisecond)
		if len(plays) != 1 || !plays[0].StartedAt.Equal(startedAt) || !plays[0].EndedAt.Equal(startedAt.Add(sunnyP.Duration)) {
			t.Errorf("播放记录 = %+v，期望一首从 %v 播到 %v", plays, startedAt, startedAt.Add(sunnyP.Duration))
		}
	})
}

type failingStore struct {
	*porttest.Store
	fail atomic.Bool
}

func (s *failingStore) SaveRoom(ctx context.Context, term uint32, snap room.Snapshot, plays []room.RecordPlay) error {
	if s.fail.Load() {
		return errors.New("连接中断")
	}
	return s.Store.SaveRoom(ctx, term, snap, plays)
}

func TestPersistFailureAbandonsRoom(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := &failingStore{Store: porttest.New()}
		f := newHostFixture(t, store, store.Store)
		id := f.createRoom(t, room.Persistent)
		f.send(t, id, room.Join{Session: "s1", Member: alice})
		f.spy.take()

		store.fail.Store(true)
		f.send(t, id, command("r1", alice, room.SetPlayMode{Mode: room.Random}))
		checkMessages(t, f.spy.take(), "rejected r1")
		if owner := f.owner(t, id); owner != "" {
			t.Errorf("放弃房间之后租约属于 %q，期望已经释放", owner)
		}
		if n := f.host.RoomsOwned(); n != 0 {
			t.Errorf("RoomsOwned = %d，期望 0", n)
		}

		store.fail.Store(false)
		f.send(t, id, command("r2", alice, room.Sync{}))
		checkMessages(t, f.spy.take(), "started 2", "reply r2 room.SyncResult")
		if term := store.Term(id); term != 2 {
			t.Errorf("重新接管后任期 = %d，期望 2", term)
		}
		if n := f.host.LeasesLost(); n != 0 {
			t.Errorf("LeasesLost = %d，落盘失败不算失去租约", n)
		}
	})
}

// 每次 Restore 的 token 都从 1 开始：放弃房间时如果不停下计时器，它会提前关闭重新接管后的房间。
func TestTimersOfAbandonedRoomAreDiscarded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := &failingStore{Store: porttest.New()}
		f := newHostFixture(t, store, store.Store)
		id := f.createRoom(t, room.Ephemeral)
		start := time.Now()

		// 第一次运行在 start 时恢复，token 1 是 start+5m 的关闭计时器。
		f.send(t, id, command("r1", alice, room.Sync{}))
		time.Sleep(time.Minute)
		store.fail.Store(true)
		f.send(t, id, command("r2", alice, room.SetPlayMode{Mode: room.Random}))
		store.fail.Store(false)
		time.Sleep(time.Minute)
		// 第二次运行在 start+2m 时恢复，token 1 是 start+7m 的关闭计时器。
		f.send(t, id, command("r3", alice, room.Sync{}))
		f.spy.take()

		time.Sleep(time.Until(start.Add(5*time.Minute + time.Second)))
		synctest.Wait()
		checkMessages(t, f.spy.take())
		time.Sleep(time.Until(start.Add(7 * time.Minute)))
		synctest.Wait()
		checkMessages(t, f.spy.take(), "closed")
	})
}

// Prepare 不理会 ctx：检验的是宿主自己丢弃旧运行的结果，而不是靠端口的实现按 ctx 提前返回。
type gatedPreparer struct {
	calls        atomic.Int32
	stale, fresh chan struct{}
}

func (*gatedPreparer) Finished(port.PrepareOutcome) {}

func (p *gatedPreparer) Prepare(context.Context, track.Track) (track.Meta, track.Prepared, error) {
	if p.calls.Add(1) == 1 {
		<-p.stale
		return track.Meta{}, track.Prepared{}, errors.New("旧运行的准备失败")
	}
	<-p.fresh
	return sunnyM, sunnyP, nil
}

func TestPrepareOfAbandonedRunIsDiscarded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := &failingStore{Store: porttest.New()}
		preparer := &gatedPreparer{stale: make(chan struct{}), fresh: make(chan struct{})}
		redis := porttest.NewRedis()
		node := redis.Node("a")
		f := startHost(t, &hostFixture{store: store.Store, redis: redis, node: node, spy: spy{redis.Node("spy")}},
			newHost(store, store.Store, preparer, newSongs().music, cluster(node, "a"), slog.New(slog.DiscardHandler)))
		id := f.createRoom(t, room.Persistent)
		f.send(t, id, command("r1", alice, room.Enqueue{Track: sunny, Meta: sunnyM}))
		f.spy.take()

		store.fail.Store(true)
		f.send(t, id, command("r2", alice, room.SetPlayMode{Mode: room.Random}))
		store.fail.Store(false)
		checkMessages(t, f.spy.take(), "rejected r2")
		// 第二次运行恢复出准备中的待播，重新发出 Prepare。
		f.send(t, id, command("r3", alice, room.Sync{}))
		checkMessages(t, f.spy.take(), "started 2", "reply r3 room.SyncResult")

		close(preparer.stale)
		synctest.Wait()
		checkMessages(t, f.spy.take())
		close(preparer.fresh)
		synctest.Wait()
		checkMessages(t, f.spy.take(), "2/1 room.QueueChanged", "2/2 room.PlaybackChanged")
	})
}

type failingDeleteStore struct {
	*porttest.Store
}

func (failingDeleteStore) DeleteRoom(context.Context, string, uint32) error {
	return errors.New("连接中断")
}

func TestEphemeralRoomClosesFiveMinutesAfterLastLeave(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		f := newHostFixture(t, store, store)
		id := f.createRoom(t, room.Ephemeral)
		f.send(t, id, room.Join{Session: "s1", Member: alice})
		f.send(t, id, room.Join{Session: "s2", Member: bob})
		if n := f.online(t, id); n != 2 {
			t.Errorf("在线人数 = %d，期望 2", n)
		}
		f.send(t, id, room.Leave{Session: "s1"})
		f.send(t, id, room.Leave{Session: "s2"})
		f.spy.take()

		time.Sleep(5*time.Minute - time.Second)
		synctest.Wait()
		checkMessages(t, f.spy.take())
		time.Sleep(time.Second)
		synctest.Wait()
		// 关闭之后租约随之删除，不再发出释放通知。
		checkMessages(t, f.spy.take(), "closed")
		if owner := f.owner(t, id); owner != "" {
			t.Errorf("关闭之后租约属于 %q，期望已经删除", owner)
		}
		if _, err := store.FindRoom(t.Context(), id); !errors.Is(err, port.ErrRoomNotFound) {
			t.Errorf("关闭后查找房间的错误 = %v，期望 ErrRoomNotFound", err)
		}
		if err := f.host.send(t.Context(), id, envelope{input: room.Join{Session: "s3", Member: alice}}, true); !errors.Is(err, port.ErrRoomNotFound) {
			t.Errorf("关闭后加入房间的错误 = %v，期望 ErrRoomNotFound", err)
		}
	})
}

func TestReconcileTakesOverRoomsWithoutOwner(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		f := newHostFixture(t, store, store)
		persistent := f.createRoom(t, room.Persistent)
		ephemeral := f.createRoom(t, room.Ephemeral)
		elsewhere := f.createRoom(t, room.Persistent)
		other := f.redis.Node("b")
		if ok, err := other.Acquire(t.Context(), elsewhere, leaseTTL); err != nil || !ok {
			t.Fatalf("b 取得租约 = %v, %v", ok, err)
		}
		ctx, stop := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() {
			defer close(done)
			f.host.Reconcile(ctx)
		}()
		t.Cleanup(func() {
			stop()
			<-done
		})

		synctest.Wait()
		for _, id := range []string{persistent, ephemeral} {
			if term := store.Term(id); term != 1 {
				t.Errorf("对账之后房间 %s 的任期 = %d，期望 1", id, term)
			}
		}
		if term := store.Term(elsewhere); term != 0 {
			t.Errorf("由 b 运行的房间的任期 = %d，期望没有被接管", term)
		}
		time.Sleep(5 * time.Minute)
		synctest.Wait()
		if _, err := store.FindRoom(t.Context(), ephemeral); !errors.Is(err, port.ErrRoomNotFound) {
			t.Errorf("无人的临时房间应已关闭：%v", err)
		}
		if term := store.Term(persistent); term != 1 {
			t.Errorf("常驻房间的任期 = %d，期望一直是 1", term)
		}
		if term := store.Term(elsewhere); term != 1 {
			t.Errorf("b 的租约过期之后房间的任期 = %d，期望 1", term)
		}
		later := f.createRoom(t, room.Persistent)
		time.Sleep(reconcileInterval + reconcileJitter)
		synctest.Wait()
		if term := store.Term(later); term != 1 {
			t.Errorf("新建的房间在对账之后的任期 = %d，期望 1", term)
		}
	})
}

func TestHostAcceptsRoomsOnlyBetweenStartAndShutdown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		songs := newSongs()
		redis := porttest.NewRedis()
		watcher := spy{redis.Node("spy")}
		h := newHost(store, store, songs.preparer, songs.music, cluster(redis.Node("a"), "a"), slog.New(slog.DiscardHandler))
		id, err := store.CreateRoom(t.Context(), port.NewRoom{Kind: room.Persistent, Name: "房间"})
		if err != nil {
			t.Fatal(err)
		}
		watcher.watch(t, id)
		join := envelope{input: room.Join{Session: "s1", Member: alice}}
		if err := h.send(t.Context(), id, join, true); !errors.Is(err, ErrUnavailable) {
			t.Errorf("Start 之前的错误 = %v，期望 ErrUnavailable", err)
		}

		h.Start()
		if err := h.send(t.Context(), id, join, true); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if err := h.Shutdown(t.Context()); err != nil {
			t.Fatal(err)
		}
		checkMessages(t, watcher.take(), "started 1", "1/1 room.MembersChanged", "released")
		if err := h.send(t.Context(), id, join, true); !errors.Is(err, ErrUnavailable) {
			t.Errorf("Shutdown 之后的错误 = %v，期望 ErrUnavailable", err)
		}
		h.Start()
		if err := h.send(t.Context(), id, join, true); !errors.Is(err, ErrUnavailable) {
			t.Errorf("Shutdown 之后再 Start，错误 = %v，期望 ErrUnavailable", err)
		}
	})
}

type blockingStore struct {
	*porttest.Store
	saving  chan struct{}
	release chan struct{}
}

func (s *blockingStore) SaveRoom(ctx context.Context, term uint32, snap room.Snapshot, plays []room.RecordPlay) error {
	s.saving <- struct{}{}
	<-s.release
	return s.Store.SaveRoom(ctx, term, snap, plays)
}

type gateStore struct {
	*porttest.Store
	fail atomic.Bool
	mu   sync.Mutex
	hold chan struct{}
}

func (s *gateStore) setHold(hold chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hold = hold
}

func (s *gateStore) SaveRoom(ctx context.Context, term uint32, snap room.Snapshot, plays []room.RecordPlay) error {
	s.mu.Lock()
	hold := s.hold
	s.mu.Unlock()
	if hold != nil {
		<-hold
	}
	if s.fail.Load() {
		return errors.New("连接中断")
	}
	return s.Store.SaveRoom(ctx, term, snap, plays)
}

func TestShutdownFinishesCurrentInput(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := &blockingStore{Store: porttest.New(), saving: make(chan struct{}), release: make(chan struct{})}
		f := newHostFixture(t, store, store.Store)
		id := f.createRoom(t, room.Persistent)
		f.send(t, id, room.Join{Session: "s1", Member: alice})
		f.spy.take()

		go func() {
			_ = f.host.send(context.Background(), id, envelope{input: command("r1", alice, room.SetPlayMode{Mode: room.Random})}, true)
		}()
		<-store.saving
		queued := make(chan error, 1)
		go func() {
			queued <- f.host.send(context.Background(), id, envelope{input: command("r2", alice, room.Sync{})}, true)
		}()
		shutdown := make(chan error, 1)
		go func() { shutdown <- f.host.Shutdown(context.Background()) }()
		synctest.Wait()
		close(store.release)

		if err := <-shutdown; err != nil {
			t.Fatal(err)
		}
		if err := <-queued; !errors.Is(err, ErrUnavailable) {
			t.Errorf("释放房间时排队的输入，错误 = %v，期望 ErrUnavailable", err)
		}
		checkMessages(t, f.spy.take(), "1/2 room.ModeChanged", "reply r1 room.Done", "released")
		if owner := f.owner(t, id); owner != "" {
			t.Errorf("释放之后租约属于 %q，期望已经删除", owner)
		}
	})
}

func TestUnknownRoom(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		f := newHostFixture(t, store, store)
		for _, id := range []string{"01a15778-1911-7a28-8b2e-e08c2a25f388"} {
			if err := f.host.send(t.Context(), id, envelope{input: room.Join{Session: "s1", Member: alice}}, true); !errors.Is(err, port.ErrRoomNotFound) {
				t.Errorf("房间 %q 的错误 = %v，期望 ErrRoomNotFound", id, err)
			}
			if owner := f.owner(t, id); owner != "" {
				t.Errorf("房间 %q 不存在，租约应已释放，实际属于 %q", id, owner)
			}
		}
		if err := f.host.send(t.Context(), "01a15778-1911-7a28-8b2e-e08c2a25f388", envelope{input: room.Leave{Session: "s1"}}, false); err != nil {
			t.Errorf("丢弃输入时的错误 = %v", err)
		}
	})
}

func TestRenewalKeepsLeaseAndOnlineCount(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		f := newHostFixture(t, store, store)
		id := f.createRoom(t, room.Persistent)
		f.send(t, id, room.Join{Session: "s1", Member: alice})
		f.send(t, id, room.Join{Session: "s2", Member: bob})
		if n := f.online(t, id); n != 2 {
			t.Errorf("在线人数 = %d，期望 2", n)
		}
		time.Sleep(time.Minute)
		synctest.Wait()
		if owner := f.owner(t, id); owner != "a" {
			t.Errorf("一分钟之后租约属于 %q，期望一直续期", owner)
		}
		if ok, err := f.redis.Node("b").Acquire(t.Context(), id, leaseTTL); err != nil || ok {
			t.Errorf("b 取得 a 的租约 = %v, %v，期望 false", ok, err)
		}
		f.send(t, id, room.Leave{Session: "s1"})
		if n := f.online(t, id); n != 1 {
			t.Errorf("离开之后在线人数 = %d，期望 1", n)
		}
		if n := f.host.RoomsOwned(); n != 1 {
			t.Errorf("RoomsOwned = %d，期望 1", n)
		}
	})
}

func TestLostLeaseStopsRoomImmediately(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := &gateStore{Store: porttest.New()}
		f := newHostFixture(t, store, store.Store)
		id := f.createRoom(t, room.Persistent)
		f.send(t, id, room.Join{Session: "s1", Member: alice})
		f.spy.take()

		// 落盘卡住时租约丢失：落盘返回之后，本批剩下的事件和回复都不再发出。
		hold := make(chan struct{})
		store.setHold(hold)
		go func() {
			_ = f.host.send(context.Background(), id, envelope{input: command("r1", alice, room.SetPlayMode{Mode: room.Random})}, true)
		}()
		synctest.Wait()
		f.redis.Flush()
		if ok, err := f.redis.Node("b").Acquire(t.Context(), id, leaseTTL); err != nil || !ok {
			t.Fatalf("清空之后 b 取得租约 = %v, %v", ok, err)
		}
		time.Sleep(renewInterval)
		synctest.Wait()
		store.setHold(nil)
		close(hold)
		synctest.Wait()

		checkMessages(t, f.spy.take(), "rejected r1")
		if owner := f.owner(t, id); owner != "b" {
			t.Errorf("租约属于 %q，期望仍属于 b", owner)
		}
		if n := f.host.LeasesLost(); n != 1 {
			t.Errorf("LeasesLost = %d，期望 1", n)
		}
		if err := f.host.send(t.Context(), id, envelope{input: command("r2", alice, room.Sync{})}, true); !errors.Is(err, errOwnedElsewhere) {
			t.Errorf("租约属于 b 时接管房间的错误 = %v，期望 errOwnedElsewhere", err)
		}
	})
}

type flakyLeases struct {
	port.Leases
	fail   atomic.Bool
	hang   atomic.Bool
	resume chan struct{}
}

func (l *flakyLeases) Renew(ctx context.Context, roomID string, ttl time.Duration) (bool, error) {
	if l.hang.Load() {
		<-l.resume
		return false, porttest.ErrRedisDown
	}
	if l.fail.Load() {
		return false, porttest.ErrRedisDown
	}
	return l.Leases.Renew(ctx, roomID, ttl)
}

func newFlakyFixture(t *testing.T) (*hostFixture, *flakyLeases) {
	t.Helper()
	store := porttest.New()
	songs := newSongs()
	redis := porttest.NewRedis()
	node := redis.Node("a")
	leases := &flakyLeases{Leases: node, resume: make(chan struct{})}
	f := startHost(t, &hostFixture{store: store, redis: redis, node: node, spy: spy{redis.Node("spy")}},
		newHost(store, store, songs.preparer, songs.music, Cluster{Instance: "a", Leases: leases, Liveness: node, Bus: node}, slog.New(slog.DiscardHandler)))
	return f, leases
}

func TestStopsTenSecondsAfterLastRenewal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, leases := newFlakyFixture(t)
		id := f.createRoom(t, room.Persistent)
		f.send(t, id, room.Join{Session: "s1", Member: alice})
		f.spy.take()
		leases.fail.Store(true)

		time.Sleep(ownerDeadline - time.Second)
		f.send(t, id, command("r1", alice, room.Sync{}))
		checkMessages(t, f.spy.take(), "reply r1 room.SyncResult")
		time.Sleep(time.Second)
		synctest.Wait()
		checkMessages(t, f.spy.take())
		if n := f.host.LeasesLost(); n != 1 {
			t.Errorf("LeasesLost = %d，期望 1", n)
		}
		if n := f.host.RoomsOwned(); n != 0 {
			t.Errorf("RoomsOwned = %d，期望 0", n)
		}
		other := f.redis.Node("b")
		if ok, err := other.Acquire(t.Context(), id, leaseTTL); err != nil || ok {
			t.Errorf("停止处理输入时 b 取得租约 = %v, %v，期望租约还没有过期", ok, err)
		}
		time.Sleep(leaseTTL - ownerDeadline)
		if ok, err := other.Acquire(t.Context(), id, leaseTTL); err != nil || !ok {
			t.Errorf("租约过期之后 b 取得租约 = %v, %v，期望 true", ok, err)
		}
	})
}

func TestStopsAtDeadlineWhileRenewalHangs(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f, leases := newFlakyFixture(t)
		id := f.createRoom(t, room.Persistent)
		f.send(t, id, room.Join{Session: "s1", Member: alice})
		f.spy.take()
		leases.hang.Store(true)

		time.Sleep(ownerDeadline + time.Second)
		go func() {
			_ = f.host.send(context.Background(), id, envelope{input: command("r1", alice, room.Sync{})}, true)
		}()
		synctest.Wait()
		checkMessages(t, f.spy.take(), "rejected r1")
		if n := f.host.LeasesLost(); n != 1 {
			t.Errorf("LeasesLost = %d，期望 1", n)
		}
		close(leases.resume)
	})
}

type slowTakeStore struct {
	*porttest.Store
	delay time.Duration
}

func (s *slowTakeStore) TakeRoom(ctx context.Context, id string) (uint32, room.Snapshot, error) {
	time.Sleep(s.delay)
	return s.Store.TakeRoom(ctx, id)
}

func TestSlowTakeRoomKeepsHealthyLease(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		f := newHostFixture(t, &slowTakeStore{Store: store, delay: renewInterval + time.Second}, store)
		id := f.createRoom(t, room.Persistent)
		start := time.Now()
		f.send(t, id, room.Join{Session: "s1", Member: alice})
		time.Sleep(ownerDeadline + 500*time.Millisecond - time.Since(start))
		f.send(t, id, command("r1", alice, room.Sync{}))
		checkMessages(t, f.spy.take(), "started 1", "1/1 room.MembersChanged", "reply r1 room.SyncResult")
		if n := f.host.LeasesLost(); n != 0 {
			t.Errorf("LeasesLost = %d，Redis 正常时不应失去租约", n)
		}
	})
}

func TestLeaseLostWhileTakingRoomPublishesNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		f := newHostFixture(t, &slowTakeStore{Store: store, delay: 2 * renewInterval}, store)
		id := f.createRoom(t, room.Persistent)
		sent := make(chan error, 1)
		go func() {
			sent <- f.host.send(t.Context(), id, envelope{input: room.Join{Session: "s1", Member: alice}}, true)
		}()
		time.Sleep(time.Second)
		f.redis.Flush()
		if ok, err := f.redis.Node("b").Acquire(t.Context(), id, leaseTTL); err != nil || !ok {
			t.Fatalf("清空之后 b 取得租约 = %v, %v", ok, err)
		}
		time.Sleep(2 * renewInterval)
		synctest.Wait()

		if err := <-sent; !errors.Is(err, ErrUnavailable) {
			t.Errorf("接管时租约被取走，输入的错误 = %v，期望 ErrUnavailable", err)
		}
		checkMessages(t, f.spy.take())
		if n := f.host.LeasesLost(); n != 1 {
			t.Errorf("LeasesLost = %d，期望 1", n)
		}
		if n := f.host.RoomsOwned(); n != 0 {
			t.Errorf("RoomsOwned = %d，期望 0", n)
		}
		if owner := f.owner(t, id); owner != "b" {
			t.Errorf("租约属于 %q，期望仍属于 b", owner)
		}
	})
}

func TestMembershipChangeDetectsLostLease(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		f := newHostFixture(t, store, store)
		id := f.createRoom(t, room.Persistent)
		f.send(t, id, room.Join{Session: "s1", Member: alice})
		f.spy.take()
		f.redis.Flush()
		if ok, err := f.redis.Node("b").Acquire(t.Context(), id, leaseTTL); err != nil || !ok {
			t.Fatalf("清空之后 b 取得租约 = %v, %v", ok, err)
		}

		f.send(t, id, room.Join{Session: "s2", Member: bob})
		checkMessages(t, f.spy.take())
		if n := f.host.LeasesLost(); n != 1 {
			t.Errorf("LeasesLost = %d，期望 1", n)
		}
	})
}

func TestTermExpiredCountsAsLostLease(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		f := newHostFixture(t, store, store)
		id := f.createRoom(t, room.Persistent)
		f.send(t, id, room.Join{Session: "s1", Member: alice})
		f.spy.take()
		if _, _, err := store.TakeRoom(t.Context(), id); err != nil {
			t.Fatal(err)
		}

		f.send(t, id, command("r1", alice, room.SetPlayMode{Mode: room.Random}))
		checkMessages(t, f.spy.take(), "rejected r1")
		if n := f.host.LeasesLost(); n != 1 {
			t.Errorf("LeasesLost = %d，期望 1", n)
		}
	})
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

func (b *logBuffer) entries(t *testing.T, msg string) []map[string]any {
	t.Helper()
	b.mu.Lock()
	text := b.buf.String()
	b.mu.Unlock()
	var found []map[string]any
	for line := range strings.Lines(strings.TrimSpace(text)) {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("日志不是 JSON：%v\n%s", err, line)
		}
		if entry["msg"] == msg {
			found = append(found, entry)
		}
	}
	return found
}

// 房间的释放通知发不出去时，其他网关要等下一次对账才接管房间，所以要留下日志。
type noReleaseNotice struct {
	port.Bus
}

func (b noReleaseNotice) PublishRoom(ctx context.Context, roomID string, m port.RoomMessage) error {
	if _, ok := m.(port.OwnerReleased); ok {
		return porttest.ErrRedisDown
	}
	return b.Bus.PublishRoom(ctx, roomID, m)
}

func TestFailedReleaseNoticeIsLogged(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		songs := newSongs()
		redis := porttest.NewRedis()
		node := redis.Node("a")
		var logs logBuffer
		f := startHost(t, &hostFixture{store: store, redis: redis, node: node, spy: spy{redis.Node("spy")}},
			newHost(store, store, songs.preparer, songs.music, Cluster{Instance: "a", Leases: node, Liveness: node, Bus: noReleaseNotice{node}}, slog.New(logctx.NewHandler(slog.NewJSONHandler(&logs, nil)))))
		id := f.createRoom(t, room.Persistent)
		f.send(t, id, room.Join{Session: "s1", Member: alice})
		if err := f.host.Shutdown(t.Context()); err != nil {
			t.Fatal(err)
		}

		entries := logs.entries(t, "发布房间的释放通知失败")
		if len(entries) != 1 {
			t.Fatalf("发布释放通知失败的日志有 %d 条，期望 1 条", len(entries))
		}
		if e := entries[0]; e["level"] != "ERROR" || e["room_id"] != id || e["error"] == nil {
			t.Errorf("日志 = %v，期望 ERROR 级别、带房间 ID 和错误", e)
		}
		if owner := f.owner(t, id); owner != "" {
			t.Errorf("释放之后租约属于 %q，期望已经删除", owner)
		}
	})
}

func TestRemoteInputsKeepTheirOrder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		f := newHostFixture(t, store, store)
		id := f.createRoom(t, room.Persistent)
		f.host.deliver(context.Background(), port.RoomInput{Room: id, Gateway: "b", Input: room.Join{Session: "b/s1", Member: bob}, Takeover: true})
		f.host.deliver(context.Background(), port.RoomInput{Room: id, Gateway: "b", Input: command("b/r1", bob, room.Sync{}), Takeover: true})
		synctest.Wait()
		msgs := f.spy.take()
		checkMessages(t, msgs, "started 1", "1/1 room.MembersChanged", "reply b/r1 room.SyncResult")
		if sessions := msgs[2].(port.Replied).Reply.Result.(room.SyncResult).View.Sessions; len(sessions) != 1 || sessions[0].ID != "b/s1" {
			t.Errorf("快照中的在线列表 = %+v，期望有刚加入的会话", sessions)
		}
	})
}

// 过了期限才轮到的请求不再交给房间，否则网关已经告诉客户端失败的请求，又会被房间执行一遍。
func TestForwardedRequestsExpireWhileQueued(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := &blockingStore{Store: porttest.New(), saving: make(chan struct{}, 1), release: make(chan struct{})}
		f := newHostFixture(t, store, store.Store)
		id := f.createRoom(t, room.Persistent)
		for _, in := range []room.Input{
			command("b/r1", bob, room.SetPlayMode{Mode: room.Random}),
			command("b/r2", bob, room.Sync{}),
			command("b/r3", bob, room.Chat{Text: "你好"}),
		} {
			f.host.deliver(context.Background(), port.RoomInput{Room: id, Gateway: "b", Input: in, Takeover: true})
		}

		// r1 卡在落盘。r2 在期限到达时被拒绝，r3 也一样：它同样是 10 秒之前到达的，轮到它时期限已经过了。
		time.Sleep(ownerReplyTimeout)
		synctest.Wait()
		checkMessages(t, f.spy.take(), "started 1", "rejected b/r2", "rejected b/r3")

		close(store.release)
		synctest.Wait()
		checkMessages(t, f.spy.take(), "1/1 room.ModeChanged", "reply b/r1 room.Done")
	})
}

// send 的两个 select 在多个分支同时就绪时随机选择一个，所以要多试几次：漏掉 ctx 检查时，几乎必然有一次会把输入交出去。
func TestSendIgnoresInputOfFinishedCaller(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		f := newHostFixture(t, store, store)
		id := f.createRoom(t, room.Persistent)
		f.send(t, id, room.Join{Session: "s1", Member: alice})
		f.spy.take()

		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		for range 64 {
			if err := f.host.send(ctx, id, envelope{input: command("r1", alice, room.Chat{Text: "你好"})}, true); !errors.Is(err, context.Canceled) {
				t.Fatalf("ctx 结束之后发送的错误 = %v，期望 context.Canceled", err)
			}
		}
		synctest.Wait()
		checkMessages(t, f.spy.take())
	})
}

func TestRemoteRequestsAreRejected(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		f := newHostFixture(t, store, store)
		elsewhere := f.createRoom(t, room.Persistent)
		if ok, err := f.redis.Node("c").Acquire(t.Context(), elsewhere, leaseTTL); err != nil || !ok {
			t.Fatalf("c 取得租约 = %v, %v", ok, err)
		}
		missing := "01a15778-1911-7a28-8b2e-e08c2a25f388"
		f.spy.watch(t, missing)
		f.host.deliver(context.Background(), port.RoomInput{Room: elsewhere, Gateway: "b", Input: command("b/r1", bob, room.Sync{}), Takeover: true})
		f.host.deliver(context.Background(), port.RoomInput{Room: missing, Gateway: "b", Patch: &port.InfoPatch{RequestID: "b/r2"}, Takeover: true})
		f.host.deliver(context.Background(), port.RoomInput{Room: missing, Gateway: "b", Input: room.Leave{Session: "b/s1"}})
		synctest.Wait()
		// 两个房间的输入各自排队，彼此之间没有先后。
		msgs := f.spy.take()
		slices.SortFunc(msgs, func(a, b port.RoomMessage) int { return strings.Compare(describe(a), describe(b)) })
		checkMessages(t, msgs, "rejected b/r1", "rejected b/r2 not found")
	})
}

type downStore struct {
	*porttest.Store
	down atomic.Bool
}

func (s *downStore) TakeRoom(ctx context.Context, id string) (uint32, room.Snapshot, error) {
	if s.down.Load() {
		return 0, room.Snapshot{}, errors.New("连接被拒绝")
	}
	return s.Store.TakeRoom(ctx, id)
}

func TestTakeoverFailuresAreLoggedWhenTheyStart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := &downStore{Store: porttest.New()}
		store.down.Store(true)
		songs := newSongs()
		redis := porttest.NewRedis()
		node := redis.Node("a")
		var logs logBuffer
		f := startHost(t, &hostFixture{store: store.Store, redis: redis, node: node, spy: spy{redis.Node("spy")}},
			newHost(store, store, songs.preparer, songs.music, cluster(node, "a"), slog.New(slog.NewJSONHandler(&logs, nil))))
		for range 3 {
			f.createRoom(t, room.Persistent)
		}
		ctx, stop := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() {
			defer close(done)
			f.host.Reconcile(ctx)
		}()
		t.Cleanup(func() {
			stop()
			<-done
		})

		time.Sleep(3 * (reconcileInterval + reconcileJitter))
		synctest.Wait()
		if n := len(logs.entries(t, "接管房间失败")); n != 1 {
			t.Fatalf("3 个房间在几次对账中都接管失败，记了 %d 条日志，期望 1 条", n)
		}
		store.down.Store(false)
		time.Sleep(reconcileInterval + reconcileJitter)
		synctest.Wait()
		if n := f.host.RoomsOwned(); n != 3 {
			t.Fatalf("存储恢复之后运行着 %d 个房间，期望 3 个", n)
		}
		store.down.Store(true)
		stuck := f.createRoom(t, room.Persistent)
		time.Sleep(reconcileInterval + reconcileJitter)
		synctest.Wait()
		if n := len(logs.entries(t, "接管房间失败")); n != 2 {
			t.Errorf("接管恢复正常之后又失败，日志共 %d 条，期望 2 条", n)
		}

		if ok, err := f.redis.Node("b").Acquire(t.Context(), stuck, 24*time.Hour); err != nil || !ok {
			t.Fatalf("b 取得租约 = %v, %v", ok, err)
		}
		store.down.Store(false)
		time.Sleep(2 * time.Minute)
		synctest.Wait()
		store.down.Store(true)
		f.createRoom(t, room.Persistent)
		time.Sleep(reconcileInterval + reconcileJitter)
		synctest.Wait()
		if n := len(logs.entries(t, "接管房间失败")); n != 3 {
			t.Errorf("存储恢复期间本实例没有接管成功过，两分钟之后又失败，日志共 %d 条，期望 3 条", n)
		}

		time.Sleep(time.Minute + reconcileInterval + reconcileJitter)
		synctest.Wait()
		if n := len(logs.entries(t, "接管房间失败")); n != 4 {
			t.Errorf("同一次故障持续了一分钟以上，日志共 %d 条，期望 4 条", n)
		}
	})
}

type recoveringLiveness struct {
	port.Liveness
	gateway     *porttest.Node
	unreachable string
	recovered   atomic.Bool
	failed      atomic.Bool
}

func (l *recoveringLiveness) Expired(ctx context.Context, ids []string) ([]string, error) {
	// 复查一次只查一个网关。
	if len(ids) == 1 && ids[0] == l.unreachable {
		l.failed.Store(true)
		return nil, errors.New("连接被拒绝")
	}
	expired, err := l.Liveness.Expired(ctx, ids)
	if err == nil && len(expired) > 0 && l.recovered.CompareAndSwap(false, true) {
		_, err = l.gateway.Heartbeat(ctx, aliveTTL)
	}
	return expired, err
}

// 复查读取失败时不知道网关是否还活着，不为它的会话发 Leave。
func TestRecoveredGatewayKeepsItsSessions(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		songs := newSongs()
		redis := porttest.NewRedis()
		node := redis.Node("a")
		gateway, unreachable := redis.Node("b"), redis.Node("c")
		liveness := &recoveringLiveness{Liveness: node, gateway: gateway, unreachable: "c"}
		f := startHost(t, &hostFixture{store: store, redis: redis, node: node, spy: spy{redis.Node("spy")}},
			newHost(store, store, songs.preparer, songs.music, Cluster{Instance: "a", Leases: node, Liveness: liveness, Bus: node}, slog.New(slog.DiscardHandler)))
		id := f.createRoom(t, room.Persistent)
		for _, g := range []*porttest.Node{gateway, unreachable} {
			if _, err := g.Heartbeat(t.Context(), aliveTTL); err != nil {
				t.Fatal(err)
			}
		}
		f.send(t, id, room.Join{Session: "a/s1", Member: alice})
		f.host.deliver(context.Background(), port.RoomInput{Room: id, Gateway: "b", Input: room.Join{Session: "b/s1", Member: bob}})
		f.host.deliver(context.Background(), port.RoomInput{Room: id, Gateway: "c", Input: room.Join{Session: "c/s1", Member: bob}})
		synctest.Wait()
		ctx, stop := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() {
			defer close(done)
			f.host.Reconcile(ctx)
		}()
		t.Cleanup(func() {
			stop()
			<-done
		})
		synctest.Wait()
		f.spy.take()

		time.Sleep(aliveTTL + reconcileInterval + reconcileJitter)
		synctest.Wait()
		if !liveness.recovered.Load() {
			t.Fatal("对账没有读到 b 的存活键过期")
		}
		if !liveness.failed.Load() {
			t.Fatal("对账没有复查 c 的存活键")
		}
		checkMessages(t, f.spy.take())
	})
}

func TestStoppedHostRejectsForwardedRequests(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		f := newHostFixture(t, store, store)
		id := f.createRoom(t, room.Persistent)
		if err := f.host.Shutdown(t.Context()); err != nil {
			t.Fatal(err)
		}
		f.host.deliver(context.Background(), port.RoomInput{Room: id, Gateway: "b", Input: command("b/r1", bob, room.Sync{}), Takeover: true})
		synctest.Wait()
		checkMessages(t, f.spy.take(), "rejected b/r1")
	})
}
