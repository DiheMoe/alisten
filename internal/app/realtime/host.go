package realtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"math/rand/v2"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dihemoe/alisten/internal/app/catalog"
	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/room"
	"github.com/dihemoe/alisten/internal/domain/track"
	"github.com/dihemoe/alisten/internal/lib/logctx"
)

const (
	storeTimeout      = 10 * time.Second
	reconcileInterval = 5 * time.Second
	reconcileJitter   = time.Second
	leaseTTL          = 15 * time.Second
	renewInterval     = 5 * time.Second
	// 距上次续约成功超过这个时长就停止处理房间的输入。它早于租约过期，时钟正常时同一时刻只有一个 owner 在处理房间的输入。
	ownerDeadline = 10 * time.Second
	// 租约、存活键和实例间消息单次调用的期限，不能超过续约的间隔。
	clusterTimeout    = 5 * time.Second
	takeoverLogRepeat = time.Minute
)

var (
	ErrUnavailable    = errors.New("房间暂时不可用，请稍后再试。")
	errOwnedElsewhere = fmt.Errorf("房间由其他实例运行：%w", ErrUnavailable)
)

// Leases、Liveness 和 Bus 都要用 Instance 这个实例 ID 构造。
type Cluster struct {
	Instance string
	Leases   port.Leases
	Liveness port.Liveness
	Bus      port.Bus
}

type Host struct {
	self      string
	snapshots port.RoomSnapshots
	directory port.RoomDirectory
	preparer  port.Preparer
	catalog   *catalog.Service
	leases    port.Leases
	liveness  port.Liveness
	bus       port.Bus
	logger    *slog.Logger

	owned      atomic.Int64
	leasesLost atomic.Uint64
	// 上一条接管房间失败的 WARN 的时刻，接管成功之后清空。存储不可用时，每次对账都会为每个没有 owner 的房间尝试接管，且每次都失败：
	// takeoverLogRepeat 内只记一条 WARN，其余记 DEBUG，日志量不随房间数和对账次数增长。
	takeoverWarned atomic.Pointer[time.Time]

	mu         sync.Mutex
	state      hostState
	running    map[string]*hostedRoom
	forwarding map[string]*[]forwardedInput
	work       sync.WaitGroup
}

type forwardedInput struct {
	port.RoomInput
	ctx     context.Context
	arrived time.Time
}

type hostState uint8

const (
	idle hostState = iota
	accepting
	stopped
)

type envelope struct {
	input   room.Input
	patch   *port.InfoPatch
	gateway string
	ctx     context.Context
}

// 房间的一次运行。每次接管都新建一个，旧运行的计时器和推荐结果因此不会落到新的 Room 上。
type hostedRoom struct {
	id      string
	inbox   chan envelope
	started chan struct{}
	done    chan struct{}
	// 接管失败的原因，在 done 关闭之前写入。
	err    error
	ctx    context.Context
	cancel context.CancelFunc
	// 上次续约成功的时刻加上 ownerDeadline，Unix 纳秒。
	deadline atomic.Int64
	// 租约已经不属于本实例，或者无法确认还属于本实例：不再执行任何 effect，也不释放租约。
	lost      atomic.Bool
	releasing atomic.Bool

	mu sync.Mutex
	// 会话所在的网关只是运行时的记录，不进入领域状态。
	gateways map[room.SessionID]string

	// 以下字段只由房间的 goroutine 访问。
	acquired    bool
	stopKeeping context.CancelFunc
	room        *room.Room
	term        uint32
	info        room.Info
	closed      bool
	timers      map[room.Token]*time.Timer
}

func newHost(snapshots port.RoomSnapshots, directory port.RoomDirectory, preparer port.Preparer, music *catalog.Service, cluster Cluster, logger *slog.Logger) *Host {
	return &Host{
		self:       cluster.Instance,
		snapshots:  snapshots,
		directory:  directory,
		preparer:   preparer,
		catalog:    music,
		leases:     cluster.Leases,
		liveness:   cluster.Liveness,
		bus:        cluster.Bus,
		logger:     logger,
		running:    map[string]*hostedRoom{},
		forwarding: map[string]*[]forwardedInput{},
	}
}

// Start 之后才接管房间：数据库迁移到二进制要求的版本、并且订阅了本实例的频道之前，房间不能运行。
func (h *Host) Start() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.state == idle {
		h.state = accepting
	}
}

// 释放全部房间，在下一次 Start 之前不再接管房间：订阅连接断开过，发给这些房间的输入可能已经丢失。
func (h *Host) pause() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.state == accepting {
		h.state = idle
	}
	for _, r := range h.running {
		r.release()
	}
}

func (h *Host) Shutdown(ctx context.Context) error {
	h.mu.Lock()
	h.state = stopped
	for _, r := range h.running {
		r.release()
	}
	h.mu.Unlock()
	done := make(chan struct{})
	go func() {
		h.work.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("等待房间停止：%w", ctx.Err())
	}
}

func (h *Host) RoomsOwned() int {
	return int(h.owned.Load())
}

func (h *Host) LeasesLost() uint64 {
	return h.leasesLost.Load()
}

// 每个未关闭的房间都要有 owner：常驻房间没有会话时时间线照常推进，临时房间无人 5 分钟后关闭。
func (h *Host) Reconcile(ctx context.Context) {
	for {
		h.checkGateways(ctx)
		h.reconcile(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(reconcileInterval + rand.N(reconcileJitter)):
		}
	}
}

func (h *Host) reconcile(ctx context.Context) {
	listCtx, cancel := context.WithTimeout(ctx, storeTimeout)
	rooms, err := h.directory.ListRooms(listCtx)
	cancel()
	if err != nil {
		if ctx.Err() == nil {
			h.logger.WarnContext(ctx, "对账时列出房间失败", "error", err)
		}
		return
	}
	h.mu.Lock()
	var others []string
	for _, r := range rooms {
		if _, ok := h.running[r.Info.ID]; !ok {
			others = append(others, r.Info.ID)
		}
	}
	h.mu.Unlock()
	if len(others) == 0 {
		return
	}
	ownersCtx, cancel := context.WithTimeout(ctx, clusterTimeout)
	owners, err := h.leases.Owners(ownersCtx, others)
	cancel()
	if err != nil {
		if ctx.Err() == nil {
			h.logger.WarnContext(ctx, "对账时读取房间的 owner 失败", "error", err)
		}
		return
	}
	for i, id := range others {
		if owners[i] == "" {
			h.ensure(id)
		}
	}
}

type roomSession struct {
	room    string
	session room.SessionID
}

func (h *Host) checkGateways(ctx context.Context) {
	h.mu.Lock()
	runs := slices.Collect(maps.Values(h.running))
	h.mu.Unlock()
	byGateway := map[string][]roomSession{}
	for _, r := range runs {
		r.mu.Lock()
		for s, g := range r.gateways {
			if g != h.self {
				byGateway[g] = append(byGateway[g], roomSession{room: r.id, session: s})
			}
		}
		r.mu.Unlock()
	}
	if len(byGateway) == 0 {
		return
	}
	checkCtx, cancel := context.WithTimeout(ctx, clusterTimeout)
	expired, err := h.liveness.Expired(checkCtx, slices.Sorted(maps.Keys(byGateway)))
	cancel()
	if err != nil {
		if ctx.Err() == nil {
			h.logger.WarnContext(ctx, "检查网关是否存活失败", "error", err)
		}
		return
	}
	for _, g := range expired {
		// 网关可能刚刚恢复了存活键，正在为会话重新 Join。Leave 排在重新加入之后时，会话会一直不在在线列表中，
		// 所以发 Leave 之前再查一次，缩小这个窗口。窗口无法消除：复查返回之后网关仍可能恢复。
		recheckCtx, cancel := context.WithTimeout(ctx, clusterTimeout)
		still, err := h.liveness.Expired(recheckCtx, []string{g})
		cancel()
		if err != nil || len(still) == 0 {
			continue
		}
		for _, s := range byGateway[g] {
			// 房间繁忙而没有收下的 Leave，下一次对账再发。
			sendCtx, cancel := context.WithTimeout(ctx, storeTimeout)
			_ = h.send(sendCtx, s.room, envelope{input: room.Leave{Session: s.session}}, false)
			cancel()
		}
	}
}

func (h *Host) ensure(id string) *hostedRoom {
	h.mu.Lock()
	defer h.mu.Unlock()
	if r, ok := h.running[id]; ok {
		return r
	}
	if h.state != accepting {
		return nil
	}
	ctx, cancel := context.WithCancel(logctx.WithRoom(context.Background(), id))
	r := &hostedRoom{
		id:       id,
		inbox:    make(chan envelope),
		started:  make(chan struct{}),
		done:     make(chan struct{}),
		ctx:      ctx,
		cancel:   cancel,
		gateways: map[room.SessionID]string{},
		timers:   map[room.Token]*time.Timer{},
	}
	h.running[id] = r
	h.work.Go(func() { h.run(r) })
	return r
}

// takeover 为假时，房间不在本实例运行就丢弃输入：重新接管后的 Room
// 从落盘的快照恢复，在线列表由网关重新加入会话来重建。
func (h *Host) send(ctx context.Context, roomID string, e envelope, takeover bool) error {
	// 调用方已经放弃的输入不再交给房间：下面两个 select 的多个分支同时就绪时，Go 随机选择其一，仅靠过期的 ctx 无法阻止输入被交出。
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}
	var r *hostedRoom
	if takeover {
		if r = h.ensure(roomID); r == nil {
			return ErrUnavailable
		}
	} else {
		h.mu.Lock()
		r = h.running[roomID]
		h.mu.Unlock()
		if r == nil {
			return nil
		}
	}
	select {
	case <-r.started:
	case <-r.done:
		if r.err != nil {
			return r.err
		}
		return ErrUnavailable
	case <-ctx.Done():
		return context.Cause(ctx)
	}
	e.ctx = ctx
	select {
	case r.inbox <- e:
		return nil
	case <-r.ctx.Done():
		return ErrUnavailable
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

// 在接收消息的 goroutine 上调用，不能等房间收下输入：
// 同一房间的输入按到达的顺序排队，由这个房间的转发 goroutine 依次交给房间。
func (h *Host) deliver(ctx context.Context, in port.RoomInput) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.state == stopped {
		// Shutdown 已经在等待 work，不能再向其中添加 goroutine。
		go h.reject(ctx, in.Room, (envelope{input: in.Input, patch: in.Patch}).requestID(), ErrUnavailable)
		return
	}
	item := forwardedInput{RoomInput: in, ctx: ctx, arrived: time.Now()}
	if queue, ok := h.forwarding[in.Room]; ok {
		*queue = append(*queue, item)
		return
	}
	queue := &[]forwardedInput{item}
	h.forwarding[in.Room] = queue
	h.work.Go(func() { h.forward(in.Room, queue) })
}

func (h *Host) forward(roomID string, queue *[]forwardedInput) {
	for {
		h.mu.Lock()
		if len(*queue) == 0 {
			delete(h.forwarding, roomID)
			h.mu.Unlock()
			return
		}
		in := (*queue)[0]
		*queue = (*queue)[1:]
		h.mu.Unlock()
		e := envelope{input: in.Input, patch: in.Patch, gateway: in.Gateway}
		ctx, cancel := forwardContext(in.ctx, e.requestID(), in.arrived)
		if err := h.send(ctx, roomID, e, in.Takeover); err != nil {
			h.reject(ctx, roomID, e.requestID(), err)
		}
		cancel()
	}
}

// 网关最多等待回复 ownerReplyTimeout，超过这个时间的请求不必再交给房间。期限从请求到达本实例时算起：
// 排在忙碌房间后面的请求不能因为轮到得晚而多得一份期限。会话的输入不设期限，与本实例的网关一致。
func forwardContext(received context.Context, id room.RequestID, arrived time.Time) (context.Context, context.CancelFunc) {
	if id == "" {
		return context.WithCancel(received)
	}
	return context.WithDeadline(received, arrived.Add(ownerReplyTimeout))
}

// 让房间没有处理的请求立即失败，发出请求的网关不必等到超时。请求的 ctx 可能已经结束。
func (h *Host) reject(ctx context.Context, roomID string, id room.RequestID, err error) {
	if id == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), clusterTimeout)
	defer cancel()
	_ = h.bus.PublishRoom(ctx, roomID, port.Rejected{RequestID: id, NotFound: errors.Is(err, port.ErrRoomNotFound)})
}

func (h *Host) run(r *hostedRoom) {
	defer h.finish(r)
	if !h.acquire(r) {
		return
	}
	// 续约从取得租约时开始，不等接管结束：接管最多耗时 storeTimeout，而期限从取得租约时算起，等接管结束再续约会超过期限。
	keepCtx, stopKeeping := context.WithCancel(context.Background())
	r.stopKeeping = stopKeeping
	h.work.Go(func() { h.keep(keepCtx, r) })
	ctx, cancel := context.WithTimeout(r.ctx, storeTimeout)
	term, snap, err := h.snapshots.TakeRoom(ctx, r.id)
	cancel()
	if err != nil {
		r.err = ErrUnavailable
		if errors.Is(err, port.ErrRoomNotFound) {
			r.err = port.ErrRoomNotFound
		} else if r.ctx.Err() == nil {
			level := slog.LevelDebug
			if last := h.takeoverWarned.Load(); last == nil || time.Since(*last) >= takeoverLogRepeat {
				if now := time.Now(); h.takeoverWarned.CompareAndSwap(last, &now) {
					level = slog.LevelWarn
				}
			}
			h.logger.Log(r.ctx, level, "接管房间失败", "error", err)
		}
		return
	}
	h.takeoverWarned.Store(nil)
	var effects []room.Effect
	r.room, effects = room.Restore(roomEnv(), term, snap)
	r.term, r.info = term, snap.Info
	h.owned.Add(1)
	close(r.started)
	if !h.holding(r.ctx, r) || !h.publish(r.ctx, r, port.OwnerChanged{Term: term}) || !h.execute(r.ctx, r, effects, "") {
		return
	}
	for {
		select {
		case <-r.ctx.Done():
			return
		case e := <-r.inbox:
			ctx := r.inputContext(e)
			// 释放房间时只处理完当前的输入。
			if r.ctx.Err() != nil || !h.holding(ctx, r) {
				h.reject(ctx, r.id, e.requestID(), ErrUnavailable)
				return
			}
			in := r.input(e)
			r.track(in, e.gateway, h.self)
			if t, ok := in.(room.Timer); ok {
				delete(r.timers, t.Token)
			}
			if !h.execute(ctx, r, r.room.Handle(roomEnv(), in), e.requestID()) {
				return
			}
		}
	}
}

func (h *Host) acquire(r *hostedRoom) bool {
	start := time.Now()
	ctx, cancel := context.WithTimeout(r.ctx, clusterTimeout)
	ok, err := h.leases.Acquire(ctx, r.id, leaseTTL)
	cancel()
	switch {
	case err != nil:
		r.err = ErrUnavailable
		if r.ctx.Err() == nil {
			h.logger.WarnContext(r.ctx, "取得房间的租约失败", "error", err)
		}
		return false
	case !ok:
		r.err = errOwnedElsewhere
		return false
	}
	r.acquired = true
	r.deadline.Store(start.Add(ownerDeadline).UnixNano())
	return true
}

// 续约不经过房间的 goroutine：房间正在落盘时也要按时续约。
func (h *Host) keep(ctx context.Context, r *hostedRoom) {
	ticker := time.NewTicker(renewInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		start := time.Now()
		renewCtx, cancel := context.WithTimeout(ctx, clusterTimeout)
		ok, err := h.leases.Renew(renewCtx, r.id, leaseTTL)
		cancel()
		switch {
		case ctx.Err() != nil:
			return
		case err == nil && ok:
			r.deadline.Store(start.Add(ownerDeadline).UnixNano())
		case err == nil:
			h.lose(r.ctx, r, "租约已不属于本实例，放弃房间")
			return
		case time.Now().UnixNano() >= r.deadline.Load():
			h.lose(r.ctx, r, "超过 10 秒没有续约成功，放弃房间", "error", err)
			return
		}
	}
}

func (h *Host) holding(ctx context.Context, r *hostedRoom) bool {
	if r.lost.Load() {
		return false
	}
	if time.Now().UnixNano() < r.deadline.Load() {
		return true
	}
	h.lose(ctx, r, "超过 10 秒没有续约成功，放弃房间")
	return false
}

func (h *Host) lose(ctx context.Context, r *hostedRoom, msg string, args ...any) {
	if r.lost.CompareAndSwap(false, true) {
		h.leasesLost.Add(1)
		h.logger.WarnContext(ctx, msg, args...)
		r.cancel()
	}
}

func (h *Host) finish(r *hostedRoom) {
	r.cancel()
	if r.stopKeeping != nil {
		r.stopKeeping()
	}
	for _, t := range r.timers {
		t.Stop()
	}
	if r.room != nil {
		h.owned.Add(-1)
	}
	// 先删除租约，再发出释放通知：收到通知的网关接管房间时，租约已经空出来了。因出错而放弃房间时不发通知：
	// 否则落盘一直失败时，网关收到通知就接管、接管之后又落盘失败，会反复接管同一个房间。
	if r.acquired && !r.lost.Load() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.ctx), clusterTimeout)
		if err := h.leases.Release(ctx, r.id); err != nil {
			h.logger.WarnContext(ctx, "释放房间的租约失败", "error", err)
		}
		if r.room != nil && !r.closed && r.releasing.Load() {
			if err := h.bus.PublishRoom(ctx, r.id, port.OwnerReleased{}); err != nil {
				h.logger.ErrorContext(ctx, "发布房间的释放通知失败", "error", err)
			}
		}
		cancel()
	}
	h.mu.Lock()
	if h.running[r.id] == r {
		delete(h.running, r.id)
	}
	h.mu.Unlock()
	close(r.done)
}

// 调用都带着 ctx 的值，但不随它取消。返回 false 表示这次运行结束：落盘失败、租约不再属于本实例或者消息发布失败时放弃房间，
// 不再执行剩下的 effect，request 还没有回复时让它立即失败；输出 Close 之后丢弃 Room。
func (h *Host) execute(ctx context.Context, r *hostedRoom, effects []room.Effect, request room.RequestID) (ok bool) {
	replied := false
	defer func() {
		if !ok && !replied {
			h.reject(ctx, r.id, request, ErrUnavailable)
		}
	}()
	for i, e := range effects {
		if !h.holding(ctx, r) {
			return false
		}
		switch e := e.(type) {
		case room.Persist:
			if err := h.persist(ctx, r, e.Snapshot, plays(effects[i+1:])); err != nil {
				h.abandon(ctx, r, "落盘失败，放弃房间", err)
				return false
			}
			r.info = e.Snapshot.Info
		case room.RecordPlay:
			// 已经和 Persist 在同一个事务中写入。
		case room.Emit:
			if m, ok := e.Event.(room.MembersChanged); ok && !h.setOnline(ctx, r, len(m.Sessions)) {
				return false
			}
			if !h.publish(ctx, r, port.Emitted{Emit: e}) {
				return false
			}
		case room.Close:
			if err := h.remove(ctx, r); err != nil {
				h.abandon(ctx, r, "删除房间失败，放弃房间", err)
				return false
			}
			r.closed = true
			if !h.publish(ctx, r, port.RoomClosed{}) {
				return false
			}
		case room.Reply:
			if !h.publish(ctx, r, port.Replied{Reply: e}) {
				return false
			}
			replied = true
		case room.Prepare:
			h.prepare(ctx, r, e.Track)
		case room.Recommend:
			h.recommend(ctx, r, e.Token)
		case room.Schedule:
			token := e.Token
			r.timers[token] = time.AfterFunc(time.Until(e.At), func() { r.post(room.Timer{Token: token}) })
		}
	}
	return !r.closed
}

// 在线人数在广播在线列表之前写入：看到在线列表变化的客户端，在房间列表中看到的也是新的人数。
// 写入失败只影响房间列表，不放弃房间：真的连不上时续约也会失败，房间随之停止。
func (h *Host) setOnline(ctx context.Context, r *hostedRoom, n int) bool {
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), clusterTimeout)
	ok, err := h.leases.SetOnline(writeCtx, r.id, n)
	cancel()
	switch {
	case err != nil:
		h.logger.WarnContext(ctx, "写入在线人数失败", "error", err)
	case !ok:
		h.lose(ctx, r, "租约已不属于本实例，放弃房间")
		return false
	}
	return true
}

// 任期过期说明房间已经由其他实例接管，计为失去租约。
func (h *Host) abandon(ctx context.Context, r *hostedRoom, msg string, err error) {
	if errors.Is(err, port.ErrTermExpired) {
		h.lose(ctx, r, msg, "error", err)
		return
	}
	h.logger.ErrorContext(ctx, msg, "error", err)
}

// 房间的消息发布失败时，订阅的网关可能漏掉了它，所以放弃房间：下一个 owner 从落盘的快照恢复房间，各网关重新加入会话。
func (h *Host) publish(ctx context.Context, r *hostedRoom, m port.RoomMessage) bool {
	publishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), clusterTimeout)
	defer cancel()
	if err := h.bus.PublishRoom(publishCtx, r.id, m); err != nil {
		h.logger.ErrorContext(ctx, "发布房间消息失败，放弃房间", "error", err)
		return false
	}
	return true
}

// 同一批中的 RecordPlay 都排在 Persist 之后，与它在同一个事务中写入。
func plays(effects []room.Effect) []room.RecordPlay {
	var out []room.RecordPlay
	for _, e := range effects {
		if p, ok := e.(room.RecordPlay); ok {
			out = append(out, p)
		}
	}
	return out
}

// 落盘不随 ctx 取消：释放房间时，正在处理的输入要处理完。
func (h *Host) persist(ctx context.Context, r *hostedRoom, s room.Snapshot, plays []room.RecordPlay) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), storeTimeout)
	defer cancel()
	return h.snapshots.SaveRoom(ctx, r.term, s, plays)
}

func (h *Host) remove(ctx context.Context, r *hostedRoom) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), storeTimeout)
	defer cancel()
	return h.snapshots.DeleteRoom(ctx, r.id, r.term)
}

func (h *Host) prepare(ctx context.Context, r *hostedRoom, t track.Track) {
	h.work.Go(func() {
		ctx, cancel := r.detach(ctx)
		defer cancel()
		meta, result, err := h.preparer.Prepare(ctx, t)
		if err != nil {
			r.post(room.PrepareFailed{Track: t, Reason: err.Error()})
			return
		}
		r.post(room.Prepared{Track: t, Meta: meta, Result: result})
	})
}

func (h *Host) recommend(ctx context.Context, r *hostedRoom, token room.Token) {
	h.work.Go(func() {
		ctx, cancel := r.detach(ctx)
		defer cancel()
		t, m, ok, err := h.catalog.Recommend(ctx, r.id)
		if err != nil && r.ctx.Err() == nil {
			h.logger.WarnContext(ctx, "AutoDJ 推荐失败", "error", err)
		}
		if ok {
			r.post(room.Recommended{Token: token, Track: t, Meta: m})
		}
	})
}

func (r *hostedRoom) release() {
	r.releasing.Store(true)
	r.cancel()
}

func (r *hostedRoom) detach(ctx context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	stop := context.AfterFunc(r.ctx, cancel)
	return ctx, func() {
		stop()
		cancel()
	}
}

func (r *hostedRoom) post(in room.Input) {
	select {
	case r.inbox <- envelope{input: in}:
	case <-r.ctx.Done():
	}
}

func (r *hostedRoom) input(e envelope) room.Input {
	if e.patch == nil {
		return e.input
	}
	in := room.UpdateInfo{RequestID: e.patch.RequestID, Name: r.info.Name, Description: r.info.Description}
	if e.patch.Name != nil {
		in.Name = *e.patch.Name
	}
	if e.patch.Description != nil {
		in.Description = *e.patch.Description
	}
	return in
}

func (r *hostedRoom) track(in room.Input, gateway, self string) {
	if gateway == "" {
		gateway = self
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	switch in := in.(type) {
	case room.Join:
		r.gateways[in.Session] = gateway
	case room.Leave:
		delete(r.gateways, in.Session)
	}
}

// 请求和成员的 ID 取自输入本身：其他实例转发来的输入只带着追踪上下文。
func (r *hostedRoom) inputContext(e envelope) context.Context {
	ctx := r.ctx
	if e.ctx != nil {
		ctx = logctx.WithRoom(context.WithoutCancel(e.ctx), r.id)
	}
	if id := e.requestID(); id != "" {
		ctx = logctx.WithRequest(ctx, string(id))
	}
	if id := e.memberID(); id != "" {
		ctx = logctx.WithMember(ctx, id)
	}
	return ctx
}

func (e envelope) requestID() room.RequestID {
	if e.patch != nil {
		return e.patch.RequestID
	}
	switch in := e.input.(type) {
	case room.Command:
		return in.RequestID
	case room.UpdateInfo:
		return in.RequestID
	case room.Delete:
		return in.RequestID
	}
	return ""
}

func (e envelope) memberID() string {
	switch in := e.input.(type) {
	case room.Command:
		return in.Member.ID
	case room.Join:
		return in.Member.ID
	case room.MemberUpdated:
		return in.Member.ID
	}
	return ""
}

func roomEnv() room.Env {
	return room.Env{Now: time.Now(), Seed: rand.Uint64()}
}
