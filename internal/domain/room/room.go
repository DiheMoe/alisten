package room

import (
	"math/rand/v2"
	"slices"
	"time"

	"github.com/dihemoe/alisten/internal/domain/track"
)

const (
	// 开播时间比切歌晚 startLead。没预加载上的客户端收到切歌之后才开始加载音频，留出这段时间，网络正常的客户端加载好时还没到开播时间，就能和其他人同时从头放。
	startLead = 1500 * time.Millisecond
	// 刚点的歌在 prepareGrace 之内还没准备好，轮到它时也等它。过了这段时间还没准备好，就先放后面已经准备好的歌。
	// 网易云和 QQ 音乐的歌正常几秒就能准备好，所以连续点歌时，顺序不会被准备得快的歌打乱。准备得慢的歌，比如要先下载的链接，
	// 或者音源出错要重试的歌，不能让房间一直等它。
	prepareGrace        = 15 * time.Second
	closeDelay          = 5 * time.Minute
	autoDJQuiet         = 10 * time.Second
	RefillInterval      = time.Minute
	EphemeralBurst      = 5
	persistentBurst     = 30
	ephemeralQueueLimit = 10
	// 等待队列每次变化都要整体落盘，并向所有会话广播完整的队列，所以常驻房间也要有上限。
	persistentQueueLimit = 200
	chatLimit            = 500
	// 聊天和切换播放模式按成员限流，bot 也一样。每条聊天都要广播给房间里的每个连接，每次切换模式都要整个房间落盘一次，
	// 按成员计数是为了一个人刷屏时不挡住别人。
	chatBurst  = 10
	chatRefill = time.Second
	modeBurst  = 3
	modeRefill = 10 * time.Second
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
	chatLimits     memberLimits
	modeLimits     memberLimits
	lastPick       time.Time
	token          Token
	endTimer       Token
	closeTimer     Token
	graceTimer     Token
	graceAt        time.Time
	recommendation Token
	// 上一批输入的时间。begin 先用它算出处理前的下一首，再把它更新为这一批的时间。歌是否还在 prepareGrace 之内按它判断，
	// 所以处理前的下一首就是上一批广播过的那首，只因为时间过去而变的下一首也能比较出来。
	now time.Time
	// 随机模式下提前抽好的下一首。它和 served 一样只在内存中，换 owner 之后重新抽。
	next EntryID
	// 各点歌人已开播的最高轮次。served 只在内存中，所以换 owner 之后，刚放过歌的点歌人可能提前一轮。已在队列中的歌不受影响。
	served map[requester]int
}

// 每次 Restore 之后，token 都从 1 重新编号。所以放弃一个房间再恢复时，调用方必须先作废旧 Room
// 还没触发的定时器和还没返回的推荐，否则它们的 token 会和新 Room 的撞号，被新 Room 当成自己的。
// 返回的 effect 和 Handle 返回的一样，按同样的规则执行。
func Restore(env Env, term uint32, s Snapshot) (*Room, []Effect) {
	r := &Room{
		term:       term,
		info:       s.Info,
		mode:       s.Mode,
		entrySeq:   s.EntrySeq,
		waiting:    slices.Clone(s.Waiting),
		pending:    clonePtr(s.Pending),
		current:    clonePtr(s.Current),
		served:     map[requester]int{},
		chatLimits: memberLimits{},
		modeLimits: memberLimits{},
		now:        env.Now,
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
	}
	r.advanceIfIdle(b)
	r.drawNext(b)
	r.watchGrace(b)
	if r.info.Kind == Ephemeral {
		r.closeTimer = r.schedule(b, env.Now.Add(closeDelay))
	}
	return r, r.commit(b)
}

// 返回的 effect 必须按顺序执行。Persist 失败时要丢弃这一批剩下的 effect，并放弃这个房间，
// 否则客户端会看到没有落盘的状态。
// effect 只能读，不能修改，因为同一批 effect 共用切片和指针，Reply.Err 也指向包级的错误值。
// 输出 Close 之后必须丢弃这个 Room。关闭时只结束正在播放的歌，不再开播下一首，所以 Room 的状态已经不是运行中房间的状态。
func (r *Room) Handle(env Env, in Input) []Effect {
	b := r.begin(env)
	switch in := in.(type) {
	case Join:
		r.join(b, in)
	case Leave:
		r.leave(b, in)
	case MemberUpdated:
		r.memberUpdated(in)
	case UpdateInfo:
		if in.Name != nil {
			r.info.Name = *in.Name
		}
		if in.Description != nil {
			r.info.Description = *in.Description
		}
		if in.Capacity != nil {
			r.info.Capacity = *in.Capacity
		}
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
	// 入队、点赞和切换播放模式都会改变等待队列的顺序。处理完统一排序，各条规则就不必各自排序。
	r.sortWaiting()
	// 选歌也在处理完之后统一做。宽限期会随时间结束，所以任何一批输入都可能让轮到的歌变化，不只是入队、准备结果和切歌。
	// 房间关闭时不再开播下一首。
	if !b.closed {
		r.advanceIfIdle(b)
	}
	r.drawNext(b)
	r.watchGrace(b)
	return r.commit(b)
}

type batch struct {
	now        time.Time
	rand       *rand.Rand
	before     Snapshot
	beforeNext EntryID
	sessions   []Session
	events     []Event
	plays      []RecordPlay
	closed     bool
	answer     *Reply
	tasks      []Effect
}

func (r *Room) begin(env Env) *batch {
	b := &batch{
		now:        env.Now,
		rand:       rand.New(rand.NewPCG(env.Seed, 0)),
		before:     r.snapshot(),
		beforeNext: r.upNext(),
		sessions:   slices.Clone(r.sessions),
	}
	r.now = env.Now
	return b
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
// Persist 排在最前，运行时落盘成功后才执行其余的 effect，这样客户端看到的都是已落盘的状态。
func (r *Room) commit(b *batch) []Effect {
	after := r.snapshot()
	var out []Effect
	if !sameSnapshot(b.before, after) || len(b.plays) > 0 {
		out = append(out, Persist{Snapshot: after, Plays: b.plays})
	}
	// RecordListen 排在广播之前，这样客户端看到开播时，听歌记录已经写好。
	if l, ok := r.listened(b); ok {
		out = append(out, l)
	}
	events := b.events
	if after.Info != b.before.Info {
		events = append(events, InfoChanged{Name: after.Info.Name, Description: after.Info.Description, Capacity: after.Info.Capacity})
	}
	if after.Mode != b.before.Mode {
		events = append(events, ModeChanged{Mode: after.Mode})
	}
	if !slices.Equal(b.sessions, r.sessions) {
		events = append(events, MembersChanged{Sessions: slices.Clone(r.sessions)})
	}
	next := r.upNext()
	// 开播变化排在队列变化前面。切歌时客户端先看到开播，才能换上预加载好的那首。先看到队列变化的话，下一首已经换了，预加载的那首会被当成作废的丢掉。
	if !samePtr(b.before.Current, after.Current) {
		events = append(events, PlaybackChanged{Current: after.Current})
	}
	// 待播开播、随机模式重新抽签，或者排在前面的歌过了宽限期时，等待队列可能没变，下一首却变了，所以要单独比较。
	if !samePtr(b.before.Pending, after.Pending) || !slices.Equal(b.before.Waiting, after.Waiting) || next != b.beforeNext {
		events = append(events, QueueChanged{Pending: after.Pending, Waiting: after.Waiting, Next: next})
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

// 开播了一首新歌时，听歌记录记给所有在线成员。正在播放的歌没变时，只记给这一批处理之前还没有会话的成员。
func (r *Room) listened(b *batch) (RecordListen, bool) {
	c := r.current
	if c == nil {
		return RecordListen{}, false
	}
	started := b.before.Current == nil || b.before.Current.Entry.ID != c.Entry.ID
	var members []string
	for _, s := range r.sessions {
		id := s.Member.ID
		if slices.Contains(members, id) || !started && slices.ContainsFunc(b.sessions, func(old Session) bool { return old.Member.ID == id }) {
			continue
		}
		members = append(members, id)
	}
	if len(members) == 0 {
		return RecordListen{}, false
	}
	return RecordListen{Members: members, Entry: c.Entry, StartedAt: c.StartedAt}, true
}

func (r *Room) timer(b *batch, in Timer) {
	switch in.Token {
	// 没有定时器时 endTimer、graceTimer 和 closeTimer 都是 0。
	case 0:
	case r.endTimer:
		r.endCurrent(b, r.current.EndsAt())
	// 宽限期结束时重新选歌，选歌在 Handle 处理完之后统一做。
	case r.graceTimer:
		r.graceTimer, r.graceAt = 0, time.Time{}
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
	return View{Term: r.term, Seq: r.seq, Snapshot: r.snapshot(), Sessions: slices.Clone(r.sessions), Next: r.upNext()}
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
