package realtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dihemoe/alisten/internal/app/catalog"
	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/member"
	"github.com/dihemoe/alisten/internal/domain/room"
	"github.com/dihemoe/alisten/internal/domain/track"
	"github.com/dihemoe/alisten/internal/lib/logctx"
)

const (
	ownerReplyTimeout = 10 * time.Second
	unsubscribeDelay  = 60 * time.Second
	retryInterval     = 5 * time.Second
	aliveTTL          = 15 * time.Second
	heartbeatInterval = 5 * time.Second
	presenceRetryMin  = time.Second
	presenceRetryMax  = 10 * time.Second
)

type Gateway struct {
	self     string
	host     *Host
	catalog  *catalog.Service
	leases   port.Leases
	liveness port.Liveness
	bus      port.Bus
	logger   *slog.Logger
	ids      atomic.Uint64

	mu       sync.Mutex
	closing  bool
	pending  map[room.RequestID]*pendingRequest
	sessions map[string]map[*Session]struct{}
	// 每个房间的事件流按打开的顺序排列。
	observers map[string][]*Observer
	links     map[string]*roomLink
	// 订阅连接每断开一次就加 1：新连接是否已经订阅了这些频道，要等确认才知道，所以之前确认过的房间订阅要重新确认。
	epoch   uint64
	active  sync.WaitGroup
	stopped chan struct{}
}

// RequestError 是网关为房间请求生成 RequestID 之后，该请求失败时返回的错误。owner 处理这个请求时的日志带有同一个 ID，
// 调用方记录失败时带上它，网关和 owner 两侧的日志就能对应起来。
type RequestError struct {
	ID  room.RequestID
	Err error
}

func (e *RequestError) Error() string { return e.Err.Error() }
func (e *RequestError) Unwrap() error { return e.Err }

func failed(id room.RequestID, err error) error {
	if err == nil {
		return nil
	}
	return &RequestError{ID: id, Err: err}
}

type pendingRequest struct {
	// 由从 pending 中取走请求的一方调用一次，然后关闭 done；在接收消息的 goroutine 上调用时不能阻塞。
	deliver func(room.Result, error)
	done    chan struct{}
}

type roomLink struct {
	users     int
	lastHTTP  time.Time
	timer     *time.Timer
	leaving   chan struct{}
	confirmed uint64
}

func New(snapshots port.RoomSnapshots, directory port.RoomDirectory, music *catalog.Service, preparer port.Preparer, cluster Cluster, logger *slog.Logger) (*Host, *Gateway) {
	h := newHost(snapshots, directory, preparer, music, cluster, logger)
	g := &Gateway{
		self:      cluster.Instance,
		host:      h,
		catalog:   music,
		leases:    cluster.Leases,
		liveness:  cluster.Liveness,
		bus:       cluster.Bus,
		logger:    logger,
		stopped:   make(chan struct{}),
		pending:   map[room.RequestID]*pendingRequest{},
		sessions:  map[string]map[*Session]struct{}{},
		observers: map[string][]*Observer{},
		links:     map[string]*roomLink{},
		epoch:     1,
	}
	return h, g
}

// 会话结束时必须调用 Leave。
func (g *Gateway) Join(ctx context.Context, roomID string, m member.Member) (*Session, error) {
	link, err := g.hold(ctx, roomID)
	if err != nil {
		return nil, err
	}
	s := newSession(room.SessionID(g.nextID("s")), roomID, m)
	s.link = link
	if !g.register(s) {
		g.unhold(roomID, link, false)
		return nil, ErrUnavailable
	}
	if err := g.route(ctx, roomID, envelope{input: room.Join{Session: s.id, Member: m}}, true); err != nil {
		g.Leave(s)
		return nil, err
	}
	// Join 没有回复，用紧接着的 Sync 回复中的快照作为握手的结果。
	id := g.newRequestID()
	g.request(ctx, roomID, id, envelope{input: room.Command{RequestID: id, Member: m, Action: room.Sync{}}}, false, func(res room.Result, e error) {
		if err = e; err == nil {
			s.welcome(res.(room.SyncResult).View)
		}
	})
	if err != nil {
		g.Leave(s)
		return nil, failed(id, err)
	}
	return s, nil
}

// 可以重复调用。
func (g *Gateway) Leave(s *Session) {
	g.mu.Lock()
	set, ok := g.sessions[s.room]
	if _, joined := set[s]; !ok || !joined {
		g.mu.Unlock()
		return
	}
	delete(set, s)
	if len(set) == 0 {
		delete(g.sessions, s.room)
	}
	g.mu.Unlock()
	s.left.Store(true)
	// 第一次发送在调用方的 goroutine 上进行：Shutdown 会等会话 Leave，它返回时 Leave 已经交给 owner；发送失败的才交给后台重试。
	if leave := leaveOf(s); !g.sendPresence(s.room, false, leave) {
		go g.retryPresence(s.room, false, leave)
	}
	g.unhold(s.room, s.link, false)
	g.active.Done()
}

// 回复和房间的事件按房间产生的顺序放进会话的发送队列。
// 调用之前要先用 s.Begin 占用名额。
func (g *Gateway) Command(ctx context.Context, s *Session, tag any, a room.Action) error {
	id := g.newRequestID()
	var err error
	g.request(ctx, s.room, id, envelope{input: room.Command{RequestID: id, Member: s.Member(), Action: a}}, false, func(res room.Result, e error) {
		err = e
		s.reply(tag, res, e)
	})
	return failed(id, err)
}

func (g *Gateway) Do(ctx context.Context, roomID string, m member.Member, a room.Action) (room.Result, error) {
	id := g.newRequestID()
	var res room.Result
	var err error
	g.request(ctx, roomID, id, envelope{input: room.Command{RequestID: id, Member: m, Action: a}}, true, func(r room.Result, e error) {
		res, err = r, e
	})
	return res, failed(id, err)
}

// 参数为 nil 表示保持不变；房间落盘之后才返回。
func (g *Gateway) UpdateInfo(ctx context.Context, roomID string, name, description *string) error {
	id := g.newRequestID()
	var err error
	g.request(ctx, roomID, id, envelope{patch: &port.InfoPatch{RequestID: id, Name: name, Description: description}}, true, func(_ room.Result, e error) {
		err = e
	})
	return failed(id, err)
}

// 房间删除数据之后，各实例上这个房间的会话和事件流以 CloseRoomGone 关闭。
func (g *Gateway) Delete(ctx context.Context, roomID string) error {
	id := g.newRequestID()
	var err error
	g.request(ctx, roomID, id, envelope{input: room.Delete{RequestID: id}}, true, func(_ room.Result, e error) {
		err = e
	})
	return failed(id, err)
}

// Revoke 在房间成员的票据作废之后通知各实例，以 CloseRevoked 关闭这个房间的会话和成员打开的事件流。
func (g *Gateway) Revoke(ctx context.Context, roomID string) error {
	return g.announce(ctx, roomID, port.TicketsRevoked{})
}

// RevokeAPIKey 在常驻房间的 API Key 重置之后通知各实例，以 CloseRevoked 关闭用 Key 打开的事件流。
func (g *Gateway) RevokeAPIKey(ctx context.Context, roomID string) error {
	return g.announce(ctx, roomID, port.APIKeyReset{})
}

func (g *Gateway) announce(ctx context.Context, roomID string, m port.RoomMessage) error {
	ctx, cancel := context.WithTimeout(ctx, clusterTimeout)
	defer cancel()
	if err := g.bus.PublishRoom(ctx, roomID, m); err != nil {
		return unavailable(err)
	}
	return nil
}

func (g *Gateway) MemberUpdated(ctx context.Context, m member.Member) error {
	ctx, cancel := context.WithTimeout(ctx, clusterTimeout)
	defer cancel()
	if err := g.bus.PublishMember(ctx, m); err != nil {
		return unavailable(err)
	}
	return nil
}

// 广播失败时只记录日志：等待的一方会定期按任务 ID 核对。
func (g *Gateway) PublishPrepared(o port.PrepareOutcome) {
	ctx, cancel := context.WithTimeout(logctx.WithJob(context.Background(), o.JobID), clusterTimeout)
	defer cancel()
	if err := g.bus.PublishPrepared(ctx, o); err != nil {
		g.logger.WarnContext(ctx, "广播准备结果失败", "source", o.Track.Source, "track_id", o.Track.ID, "error", err)
	}
}

func (g *Gateway) Shutdown(ctx context.Context) error {
	g.mu.Lock()
	if !g.closing {
		g.closing = true
		close(g.stopped)
	}
	g.mu.Unlock()
	g.closeAll(CloseRestart)
	done := make(chan struct{})
	go func() {
		g.active.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("等待会话关闭：%w", ctx.Err())
	}
}

func (g *Gateway) Sessions() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	n := 0
	for _, set := range g.sessions {
		n += len(set)
	}
	return n
}

var ErrRequesterNotAllowed = errors.New("requester：只有用常驻房间的 API Key 点歌时才能填写")

type Pick struct {
	Track   track.Track
	Source  track.Source
	Keyword string
	// 点歌人的名字，由 bot 代填。
	Requester string
}

// by 是发出请求的成员，bot 表示他是常驻房间的 bot 成员。返回的成员是条目的点歌人，房间命令以他的名义发出。
func (g *Gateway) Resolve(ctx context.Context, by member.Member, bot bool, p Pick) (room.Enqueue, member.Member, error) {
	if p.Requester != "" {
		if !bot {
			return room.Enqueue{}, member.Member{}, ErrRequesterNotAllowed
		}
		by = member.Member{ID: by.ID, Name: p.Requester + "（bot）"}
	}
	switch {
	case p.Keyword != "":
		t, m, err := g.catalog.FirstMatch(ctx, p.Source, p.Keyword)
		return room.Enqueue{Track: t, Meta: m}, by, err
	case p.Track.Source == track.Link:
		t, m, err := g.catalog.ResolveLink(ctx, p.Track.ID)
		return room.Enqueue{Track: t, Meta: m}, by, err
	default:
		m, err := g.catalog.TrackMeta(ctx, p.Track)
		return room.Enqueue{Track: p.Track, Meta: m}, by, err
	}
}

// deliver 恰好被调用一次：收到回复时在接收消息的 goroutine 上调用，失败时在这里调用；返回时 deliver 已经调用过。
// 网关先确认订阅了房间频道再发出输入，因为回复和事件都经由房间频道到达。
func (g *Gateway) request(ctx context.Context, roomID string, id room.RequestID, e envelope, http bool, deliver func(room.Result, error)) {
	p := &pendingRequest{deliver: deliver, done: make(chan struct{})}
	g.mu.Lock()
	g.pending[id] = p
	g.mu.Unlock()
	ctx, cancel := context.WithTimeoutCause(ctx, ownerReplyTimeout, ErrUnavailable)
	defer cancel()
	link, err := g.hold(ctx, roomID)
	if err == nil {
		defer g.unhold(roomID, link, http)
		err = g.route(ctx, roomID, e, true)
	}
	if err != nil {
		g.settle(id, nil, err)
	} else {
		select {
		case <-p.done:
		case <-ctx.Done():
			g.settle(id, nil, context.Cause(ctx))
		}
	}
	<-p.done
}

func (g *Gateway) route(ctx context.Context, roomID string, e envelope, takeover bool) error {
	for retried := false; ; retried = true {
		ownersCtx, cancel := context.WithTimeout(ctx, clusterTimeout)
		owners, err := g.leases.Owners(ownersCtx, []string{roomID})
		cancel()
		if err != nil {
			return unavailable(err)
		}
		switch owner := owners[0]; {
		case owner == "" && !takeover:
			return nil
		case owner == "" || owner == g.self:
			err := g.host.send(ctx, roomID, e, takeover)
			// 读到没有 owner 之后、本实例接管之前，其他实例先取得了租约。
			if errors.Is(err, errOwnedElsewhere) && !retried {
				continue
			}
			return err
		default:
			sendCtx, cancel := context.WithTimeout(ctx, clusterTimeout)
			delivered, err := g.bus.SendInput(sendCtx, owner, port.RoomInput{Room: roomID, Gateway: g.self, Input: e.input, Patch: e.patch, Takeover: takeover})
			cancel()
			if err != nil {
				return unavailable(err)
			}
			// owner 所在的实例没有订阅自己的频道：它已经停止，但租约还没有过期。
			if !delivered {
				return ErrUnavailable
			}
			return nil
		}
	}
}

func unavailable(err error) error {
	return fmt.Errorf("%w：%w", ErrUnavailable, err)
}

// 回复、超时和房间不再处理请求可能同时发生，先从 pending 中取走请求的一方负责送达。
func (g *Gateway) settle(id room.RequestID, res room.Result, err error) {
	g.mu.Lock()
	p, ok := g.pending[id]
	delete(g.pending, id)
	g.mu.Unlock()
	if ok {
		p.deliver(res, err)
		close(p.done)
	}
}

// 在接收消息的 goroutine 上按房间频道上的顺序调用，不能阻塞。
func (g *Gateway) deliver(roomID string, m port.RoomMessage) {
	switch m := m.(type) {
	case port.OwnerChanged:
		g.rejoin(roomID, false)
		for _, o := range g.roomObservers(roomID) {
			if o.ownerChanged(m.Term) {
				go g.resync(o)
			}
		}
	case port.OwnerReleased:
		// 房间暂时没有 owner，而这里还有它的会话，所以立即接管。
		g.rejoin(roomID, true)
	case port.Emitted:
		for _, q := range g.recipients(roomID) {
			q.event(m.Emit)
		}
	case port.Replied:
		var err error
		if m.Reply.Err != nil {
			err = m.Reply.Err
		}
		g.settle(m.Reply.RequestID, m.Reply.Result, err)
	case port.Rejected:
		err := ErrUnavailable
		if m.NotFound {
			err = port.ErrRoomNotFound
		}
		g.settle(m.RequestID, nil, err)
	case port.RoomClosed:
		for _, q := range g.recipients(roomID) {
			q.close(CloseRoomGone)
		}
	case port.TicketsRevoked:
		for _, s := range g.roomSessions(roomID) {
			s.close(CloseRevoked)
		}
		for _, o := range g.roomObservers(roomID) {
			if !o.bot {
				o.close(CloseRevoked)
			}
		}
	case port.APIKeyReset:
		for _, o := range g.roomObservers(roomID) {
			if o.bot {
				o.close(CloseRevoked)
			}
		}
	}
}

// 房间的新一次运行从空的在线列表开始，本地会话重新加入，在线列表由此重建。不能在接收消息的 goroutine 上等待房间收下输入。
func (g *Gateway) rejoin(roomID string, takeover bool) {
	sessions := g.roomSessions(roomID)
	if len(sessions) == 0 {
		return
	}
	go func() {
		for _, s := range sessions {
			g.deliverPresence(roomID, takeover, joinOf(s))
			// 会话在重新加入的途中离开时，它的 Leave 可能排在这次 Join 之前，所以补发一次 Leave，以免它留在在线列表中。
			if s.left.Load() {
				g.deliverPresence(roomID, false, leaveOf(s))
			}
		}
	}()
}

// 在接收消息的 goroutine 上调用，不能阻塞。
func (g *Gateway) memberChanged(m member.Member) {
	g.mu.Lock()
	var rooms []string
	for roomID, set := range g.sessions {
		updated := false
		for s := range set {
			if s.update(m) {
				updated = true
			}
		}
		if updated {
			rooms = append(rooms, roomID)
		}
	}
	g.mu.Unlock()
	if len(rooms) == 0 {
		return
	}
	go func() {
		for _, roomID := range rooms {
			g.deliverPresence(roomID, false, g.memberUpdateOf(roomID, m.ID))
		}
	}()
}

// owner 靠 Join、Leave 和 MemberUpdated 知道谁在线，这几条消息丢失后没有别的办法补回：离开的会话会一直留在在线列表中，
// 临时房间也因此无法关闭；仍然在线的会话则不在列表中。所以送不到 owner 时要一直重试，直到送达。
// next 在每次尝试前调用，返回这一次要发的输入；返回 false 表示这条消息已经不必再发。
func (g *Gateway) sendPresence(roomID string, takeover bool, next func() (room.Input, bool)) (done bool) {
	in, ok := next()
	return !ok || g.route(context.Background(), roomID, envelope{input: in}, takeover) == nil
}

// 网关停止之后不再重试：进程即将退出，本实例的存活键过期之后，owner 会让这里的会话离开。
func (g *Gateway) retryPresence(roomID string, takeover bool, next func() (room.Input, bool)) {
	for delay := presenceRetryMin; ; delay = min(2*delay, presenceRetryMax) {
		select {
		case <-g.stopped:
			return
		case <-time.After(delay):
		}
		if g.sendPresence(roomID, takeover, next) {
			return
		}
	}
}

func (g *Gateway) deliverPresence(roomID string, takeover bool, next func() (room.Input, bool)) {
	if !g.sendPresence(roomID, takeover, next) {
		g.retryPresence(roomID, takeover, next)
	}
}

func joinOf(s *Session) func() (room.Input, bool) {
	return func() (room.Input, bool) {
		if s.ended() {
			return nil, false
		}
		return room.Join{Session: s.id, Member: s.Member()}, true
	}
}

func leaveOf(s *Session) func() (room.Input, bool) {
	return func() (room.Input, bool) { return room.Leave{Session: s.id}, true }
}

// 每次都发送这个成员在本地会话记录中的最新资料，而不是触发这次发送的那一份：否则资料再次修改之后，
// 先前那次发送的重试如果晚到，会把旧资料写回房间。
func (g *Gateway) memberUpdateOf(roomID, memberID string) func() (room.Input, bool) {
	return func() (room.Input, bool) {
		g.mu.Lock()
		defer g.mu.Unlock()
		for s := range g.sessions[roomID] {
			if m := s.Member(); m.ID == memberID {
				return room.MemberUpdated{Member: m}, true
			}
		}
		return nil, false
	}
}

// 订阅连接断开过：会话和事件流可能漏掉了房间的事件，等待中的请求可能收不到回复。会话和事件流以 1012 断开，
// 客户端重连之后以新快照为准。
func (g *Gateway) restart() {
	g.mu.Lock()
	g.epoch++
	g.mu.Unlock()
	g.closeAll(CloseRestart)
	g.mu.Lock()
	var ids []room.RequestID
	for id := range g.pending {
		ids = append(ids, id)
	}
	g.mu.Unlock()
	for _, id := range ids {
		g.settle(id, nil, ErrUnavailable)
	}
}

func (g *Gateway) closeAll(reason CloseReason) {
	g.mu.Lock()
	var all []*sendQueue
	for _, set := range g.sessions {
		for s := range set {
			all = append(all, s.sendQueue)
		}
	}
	for _, set := range g.observers {
		for _, o := range set {
			all = append(all, o.sendQueue)
		}
	}
	g.mu.Unlock()
	for _, q := range all {
		q.close(reason)
	}
}

// 订阅本实例的频道并刷新存活键：此后其他实例的网关才能把输入转发到这里，owner 也不会认为本实例的网关已经退出。
func (g *Gateway) open(ctx context.Context) error {
	for {
		attemptCtx, cancel := context.WithTimeout(ctx, clusterTimeout)
		err := g.bus.SubscribeInstance(attemptCtx)
		if err == nil {
			_, err = g.liveness.Heartbeat(attemptCtx, aliveTTL)
		}
		cancel()
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		g.logger.WarnContext(ctx, "订阅本实例的频道失败，稍后重试", "error", err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(retryInterval):
		}
	}
}

func (g *Gateway) heartbeat(ctx context.Context) {
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		beatCtx, cancel := context.WithTimeout(ctx, clusterTimeout)
		alive, err := g.liveness.Heartbeat(beatCtx, aliveTTL)
		cancel()
		// 存活键曾经过期（例如 Redis 被清空），owner 可能已经让本地会话离开了在线列表。
		if err == nil && !alive {
			for _, roomID := range g.joinedRooms() {
				g.rejoin(roomID, false)
			}
		}
	}
}

func (g *Gateway) joinedRooms() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Collect(maps.Keys(g.sessions))
}

// 订阅连接断开时只通知 lost，不在这里处理：恢复订阅要等这里收到确认。
func (g *Gateway) receive(ctx context.Context, lost chan<- struct{}) {
	for {
		d, err := g.bus.Receive(ctx)
		if err != nil {
			return
		}
		switch d := d.(type) {
		case port.RoomDelivery:
			g.deliver(d.Room, d.Message)
		case port.InputDelivery:
			g.host.deliver(d.Context, d.Input)
		case port.MemberDelivery:
			g.memberChanged(d.Member)
		case port.PreparedDelivery:
			g.host.preparer.Finished(d.Outcome)
		case port.SubscriptionLost:
			select {
			case lost <- struct{}{}:
			default:
			}
		}
	}
}

// 返回时订阅已经生效。确认过的订阅不再重复订阅和等待确认，Redis 不可用时请求也不必等待。
// 用完之后调用 unhold。
func (g *Gateway) hold(ctx context.Context, roomID string) (*roomLink, error) {
	for {
		g.mu.Lock()
		link := g.links[roomID]
		if link != nil && link.leaving != nil {
			leaving := link.leaving
			g.mu.Unlock()
			select {
			case <-leaving:
				continue
			case <-ctx.Done():
				return nil, context.Cause(ctx)
			}
		}
		if link == nil {
			link = &roomLink{}
			g.links[roomID] = link
		}
		link.users++
		if link.timer != nil {
			link.timer.Stop()
			link.timer = nil
		}
		epoch := g.epoch
		confirmed := link.confirmed == epoch
		g.mu.Unlock()
		if confirmed {
			return link, nil
		}
		subscribeCtx, cancel := context.WithTimeout(ctx, clusterTimeout)
		err := g.bus.SubscribeRoom(subscribeCtx, roomID)
		cancel()
		if err != nil {
			g.unhold(roomID, link, false)
			return nil, unavailable(err)
		}
		g.mu.Lock()
		link.confirmed = max(link.confirmed, epoch)
		g.mu.Unlock()
		return link, nil
	}
}

func (g *Gateway) unhold(roomID string, link *roomLink, http bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	link.users--
	if http {
		link.lastHTTP = time.Now()
	}
	if link.users == 0 {
		link.timer = time.AfterFunc(time.Until(link.lastHTTP.Add(unsubscribeDelay)), func() { g.unsubscribe(roomID, link) })
	}
}

func (g *Gateway) unsubscribe(roomID string, link *roomLink) {
	g.mu.Lock()
	if g.links[roomID] != link || link.users > 0 || link.leaving != nil {
		g.mu.Unlock()
		return
	}
	link.leaving = make(chan struct{})
	g.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), clusterTimeout)
	_ = g.bus.UnsubscribeRoom(ctx, roomID)
	cancel()
	g.mu.Lock()
	delete(g.links, roomID)
	close(link.leaving)
	g.mu.Unlock()
}

func (g *Gateway) register(s *Session) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closing {
		return false
	}
	set := g.sessions[s.room]
	if set == nil {
		set = map[*Session]struct{}{}
		g.sessions[s.room] = set
	}
	set[s] = struct{}{}
	g.active.Add(1)
	return true
}

func (g *Gateway) roomSessions(roomID string) []*Session {
	g.mu.Lock()
	defer g.mu.Unlock()
	sessions := make([]*Session, 0, len(g.sessions[roomID]))
	for s := range g.sessions[roomID] {
		sessions = append(sessions, s)
	}
	return sessions
}

func (g *Gateway) roomObservers(roomID string) []*Observer {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.observers[roomID])
}

// 会话和事件流都收到房间的事件和关闭通知。
func (g *Gateway) recipients(roomID string) []*sendQueue {
	g.mu.Lock()
	defer g.mu.Unlock()
	var all []*sendQueue
	for s := range g.sessions[roomID] {
		all = append(all, s.sendQueue)
	}
	for _, o := range g.observers[roomID] {
		all = append(all, o.sendQueue)
	}
	return all
}

func (g *Gateway) newRequestID() room.RequestID {
	return room.RequestID(g.nextID("r"))
}

func (g *Gateway) nextID(prefix string) string {
	return g.self + "/" + prefix + strconv.FormatUint(g.ids.Add(1), 10)
}
