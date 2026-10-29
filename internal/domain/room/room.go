package room

import (
	"math/rand/v2"
	"slices"
	"time"

	"github.com/dihemoe/alisten/internal/domain/track"
)

const (
	startLead           = 200 * time.Millisecond
	closeDelay          = 5 * time.Minute
	autoDJQuiet         = 10 * time.Second
	refillInterval      = time.Minute
	ephemeralBurst      = 5
	persistentBurst     = 30
	ephemeralQueueLimit = 10
	chatLimit           = 500
)

// Room 内部没有锁，同一个 Room 的 Handle 必须由调用方串行调用。
type Room struct {
	term           uint32
	seq            uint64
	info           Info
	mode           PlayMode
	entrySeq       EntryID
	waiting        []Entry
	pending        *Entry
	current        *NowPlaying
	sessions       []Session
	votes          []string
	pickLimit      limiter
	likeLimit      limiter
	lastPick       time.Time
	token          Token
	endTimer       Token
	closeTimer     Token
	recommendation Token
	// 各点歌人已开播的最高轮次，只在内存中：换 owner 之后刚放过歌的点歌人可能提前一轮，已在队列中的歌不受影响。
	served map[requester]int
}

// token 每次 Restore 后都从 1 重新开始。放弃房间后再次恢复时，调用方必须作废旧 Room
// 尚未触发的定时器和尚未返回的推荐，否则旧 token 会被新 Room 当成自己的。
// 返回的 effect 与 Handle 的要求相同。
func Restore(env Env, term uint32, s Snapshot) (*Room, []Effect) {
	r := &Room{
		term:     term,
		info:     s.Info,
		mode:     s.Mode,
		entrySeq: s.EntrySeq,
		waiting:  slices.Clone(s.Waiting),
		pending:  clonePtr(s.Pending),
		current:  clonePtr(s.Current),
		served:   map[requester]int{},
	}
	// 存储返回的条目顺序不保证符合排序规则。
	r.sortWaiting()
	b := r.begin(env)
	if r.pending != nil && !r.pending.Ready {
		b.prepare(r.pending.Track)
	}
	for _, e := range r.waiting {
		if !e.Ready {
			b.prepare(e.Track)
		}
	}
	switch {
	case r.current != nil && env.Now.Before(r.current.EndsAt()):
		r.endTimer = r.schedule(b, r.current.EndsAt())
	case r.current != nil:
		r.endCurrent(b, r.current.EndsAt())
	case r.pending != nil && r.pending.Ready:
		r.start(b)
	}
	r.advanceIfIdle(b)
	if r.info.Kind == Ephemeral {
		r.closeTimer = r.schedule(b, env.Now.Add(closeDelay))
	}
	return r, r.commit(b)
}

// 返回的 effect 必须按顺序执行：Persist 失败时丢弃本批剩余的 effect 并放弃该房间，
// 否则客户端会看到没有落盘的状态。
// effect 只能读，不能修改：同一批 effect 共用切片和指针，Reply.Err 指向包级的错误值。
// 输出 Close 之后必须丢弃这个 Room：关闭时只结束正在播放的歌，不再开播下一首，它的状态已不是运行中房间的状态。
func (r *Room) Handle(env Env, in Input) []Effect {
	b := r.begin(env)
	switch in := in.(type) {
	case Join:
		r.join(in)
	case Leave:
		r.leave(b, in)
	case MemberUpdated:
		r.memberUpdated(in)
	case UpdateInfo:
		r.info.Name, r.info.Description = in.Name, in.Description
		b.respond(in.RequestID, Done{}, nil)
	case Delete:
		r.stopPlayback(b)
		b.closed = true
		b.respond(in.RequestID, Done{}, nil)
	case Command:
		r.command(b, in)
	case Prepared:
		r.prepared(b, in)
	case PrepareFailed:
		r.prepareFailed(b, in)
	case Recommended:
		r.recommended(b, in)
	case Timer:
		r.timer(b, in)
	}
	// 入队、点赞和切换播放模式都会改变等待队列的顺序，各条规则不必各自排序。
	r.sortWaiting()
	return r.commit(b)
}

type batch struct {
	now      time.Time
	rand     *rand.Rand
	before   Snapshot
	sessions []Session
	events   []Event
	plays    []RecordPlay
	closed   bool
	answer   *Reply
	tasks    []Effect
}

func (r *Room) begin(env Env) *batch {
	return &batch{
		now:      env.Now,
		rand:     rand.New(rand.NewPCG(env.Seed, 0)),
		before:   r.snapshot(),
		sessions: slices.Clone(r.sessions),
	}
}

func (b *batch) event(e Event) {
	b.events = append(b.events, e)
}

func (b *batch) prepare(t track.Track) {
	b.tasks = append(b.tasks, Prepare{Track: t})
}

func (b *batch) respond(id RequestID, res Result, err *Error) {
	b.answer = &Reply{RequestID: id, Result: res, Err: err}
}

// 状态事件由处理前后的差异得出，不由各条规则各自发出，这样任何状态变化都不会漏掉广播。
// Persist 排在最前：运行时落盘成功后才执行其余的 effect，客户端看到的都是已落盘的状态。
func (r *Room) commit(b *batch) []Effect {
	after := r.snapshot()
	var out []Effect
	if !sameSnapshot(b.before, after) {
		out = append(out, Persist{Snapshot: after})
	}
	for _, p := range b.plays {
		out = append(out, p)
	}
	events := b.events
	if after.Info != b.before.Info {
		events = append(events, InfoChanged{Name: after.Info.Name, Description: after.Info.Description})
	}
	if after.Mode != b.before.Mode {
		events = append(events, ModeChanged{Mode: after.Mode})
	}
	if !slices.Equal(b.sessions, r.sessions) {
		events = append(events, MembersChanged{Sessions: slices.Clone(r.sessions)})
	}
	if !samePtr(b.before.Pending, after.Pending) || !slices.Equal(b.before.Waiting, after.Waiting) {
		events = append(events, QueueChanged{Pending: after.Pending, Waiting: after.Waiting})
	}
	if !samePtr(b.before.Current, after.Current) {
		events = append(events, PlaybackChanged{Current: after.Current})
	}
	for _, e := range events {
		r.seq++
		out = append(out, Emit{Term: r.term, Seq: r.seq, Event: e})
	}
	if b.closed {
		out = append(out, Close{})
	}
	if b.answer != nil {
		out = append(out, *b.answer)
	}
	return append(out, b.tasks...)
}

func (r *Room) timer(b *batch, in Timer) {
	switch in.Token {
	// 没有定时器时 endTimer 和 closeTimer 都是 0。
	case 0:
	case r.endTimer:
		r.endCurrent(b, r.current.EndsAt())
		r.advanceIfIdle(b)
	case r.closeTimer:
		r.closeTimer = 0
		r.stopPlayback(b)
		b.closed = true
	}
}

func (r *Room) snapshot() Snapshot {
	return Snapshot{
		Info:     r.info,
		Mode:     r.mode,
		EntrySeq: r.entrySeq,
		Waiting:  slices.Clone(r.waiting),
		Pending:  clonePtr(r.pending),
		Current:  clonePtr(r.current),
	}
}

func (r *Room) view() View {
	return View{Term: r.term, Seq: r.seq, Snapshot: r.snapshot(), Sessions: slices.Clone(r.sessions)}
}

func (r *Room) issue() Token {
	r.token++
	return r.token
}

func (r *Room) schedule(b *batch, at time.Time) Token {
	t := r.issue()
	b.tasks = append(b.tasks, Schedule{At: at, Token: t})
	return t
}

func sameSnapshot(a, b Snapshot) bool {
	return a.Info == b.Info && a.Mode == b.Mode && a.EntrySeq == b.EntrySeq &&
		slices.Equal(a.Waiting, b.Waiting) && samePtr(a.Pending, b.Pending) && samePtr(a.Current, b.Current)
}

func samePtr[T comparable](a, b *T) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func clonePtr[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}
