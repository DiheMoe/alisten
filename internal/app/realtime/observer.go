package realtime

import (
	"context"
	"errors"
	"slices"

	"github.com/dihemoe/alisten/internal/domain/member"
	"github.com/dihemoe/alisten/internal/domain/room"
)

// 按本实例计数：限制的是每个实例为事件流占用的资源。
const observerLimit = 5

var ErrTooManyObservers = errors.New("这个房间的事件流超过 5 条，请先关闭不用的事件流")

// Observer 是房间的事件流：先收到房间的快照，之后是房间的事件；房间开始新的一次运行时事件重新编号，再收到一份新的快照。
type Observer struct {
	*sendQueue
	room       string
	link       *roomLink
	member     member.Member
	bot        bool
	credential string

	// 由 sendQueue 的 mu 保护：最近一次快照的任期，和收到过的 owner 变更通知中最大的任期。
	term, latest uint32
}

// Observe 订阅房间的事件但不加入房间：事件流不出现在在线列表中，不计入投票切歌所需的人数，也不让 AutoDJ 认为房间里有人。
// m 是凭证的持有者，bot 表示凭证是常驻房间的 API Key，credential 是凭证的哈希。名额用完时，同一凭证打开的最早的事件流以
// CloseReplaced 关闭，名额让给这一条；没有同一凭证的事件流时返回 ErrTooManyObservers。
// 返回时快照已经放进队列。结束时必须调用一次 Unobserve。
func (g *Gateway) Observe(ctx context.Context, roomID string, m member.Member, bot bool, credential string) (*Observer, error) {
	link, err := g.hold(ctx, roomID)
	if err != nil {
		return nil, err
	}
	o := &Observer{sendQueue: newSendQueue(), room: roomID, link: link, member: m, bot: bot, credential: credential}
	if err := g.watch(o); err != nil {
		g.unhold(roomID, link, false)
		return nil, err
	}
	if err := g.sync(ctx, o); err != nil {
		g.Unobserve(o)
		return nil, err
	}
	return o, nil
}

// 被替换的事件流已经不在名单中，只归还它对房间频道的占用。
func (g *Gateway) Unobserve(o *Observer) {
	g.mu.Lock()
	if rest := slices.DeleteFunc(g.observers[o.room], func(x *Observer) bool { return x == o }); len(rest) > 0 {
		g.observers[o.room] = rest
	} else {
		delete(g.observers, o.room)
	}
	g.mu.Unlock()
	// 中止正在进行的重新获取快照。
	o.cancel()
	g.unhold(o.room, o.link, false)
}

// 关闭被替换的事件流和占用它的名额在同一次加锁中完成：名额不会超出，同时到达的事件流也不会替换同一条。
func (g *Gateway) watch(o *Observer) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closing {
		return ErrUnavailable
	}
	set := g.observers[o.room]
	if len(set) == observerLimit {
		// 同一凭证打开的最早的事件流。
		i := slices.IndexFunc(set, func(x *Observer) bool { return x.credential == o.credential })
		if i < 0 {
			return ErrTooManyObservers
		}
		set[i].close(CloseReplaced)
		set = slices.Delete(set, i, i+1)
	}
	g.observers[o.room] = append(set, o)
	return nil
}

// 拿到的快照早于收到过的最新一次运行时重新获取。
func (g *Gateway) sync(ctx context.Context, o *Observer) error {
	for {
		id := g.newRequestID()
		var err error
		current := false
		g.request(ctx, o.room, id, envelope{input: room.Command{RequestID: id, Member: o.member, Action: room.Sync{}}}, false, func(res room.Result, e error) {
			if err = e; err == nil {
				current = o.snapshot(res.(room.SyncResult).View)
			}
		})
		if err != nil || current {
			return failed(id, err)
		}
	}
}

// 取不到新快照时关闭事件流，客户端重新连接之后以新快照为准。
func (g *Gateway) resync(o *Observer) {
	if g.sync(o.ctx, o) != nil {
		o.close(CloseRestart)
	}
}

// 在接收消息的 goroutine 上按房间频道的顺序调用：此后房间的事件才发给这个事件流，快照之前的事件都已包含在快照中。
// 返回 false 表示这份快照已经过时：之后又收到了更新一次运行的 owner 变更通知，从它接着发事件，编号就对不上。
func (o *Observer) snapshot(v room.View) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	if v.Term < o.latest {
		return false
	}
	o.term, o.subscribed = v.Term, true
	o.enqueue(Snapshot{View: v})
	return true
}

// 返回 true 表示要重新获取快照，在此之前不再发事件。正在获取快照时只记下任期，由获取快照的一方判断拿到的快照是否过时。
func (o *Observer) ownerChanged(term uint32) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.latest = max(o.latest, term)
	if !o.subscribed || o.term >= term {
		return false
	}
	o.subscribed = false
	return true
}
