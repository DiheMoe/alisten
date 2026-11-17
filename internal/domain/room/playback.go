package room

import (
	"slices"
	"time"

	"github.com/dihemoe/alisten/internal/domain/member"
	"github.com/dihemoe/alisten/internal/domain/track"
)

func (r *Room) advanceIfIdle(b *batch) {
	if r.current != nil {
		return
	}
	if r.pending != nil && !r.pending.Ready {
		if r.holdsTurn()(*r.pending) {
			return
		}
		// 待播过了宽限期还没准备好，等待队列里又有准备好的歌，就把它放回等待队列，重新选一首。它准备好之后，下次轮到它时再放。
		r.waiting = append(r.waiting, *r.pending)
		r.sortWaiting()
		r.pending = nil
	}
	if r.pending == nil && len(r.waiting) > 0 {
		i := r.choose(b)
		next := r.waiting[i]
		r.waiting = slices.Delete(r.waiting, i, i+1)
		r.pending = &next
	}
	if r.pending != nil && r.pending.Ready {
		r.start(b)
	}
}

// 空闲时选出轮到的歌，返回它在等待队列中的位置。等待队列不能为空。
func (r *Room) choose(b *batch) int {
	i := r.upcoming()
	if r.mode != Random {
		return i
	}
	if i < 0 || !r.holdsTurn()(r.waiting[i]) {
		i = r.drawHeld(b)
	}
	r.next = 0
	return i
}

// 轮到一首歌时，它占着位置，房间就等它，后面的歌不能先放。准备好的歌和点了还不到 prepareGrace 的歌都占着位置。
// 过了宽限期还没准备好的歌，只在等待队列里有准备好的歌时才让出位置，因为没有别的歌能先放时，跳过它也没有用。
// 所以等待队列不为空时，总有一首歌占着位置。
// holdsTurn 返回判断函数。等待队列里有没有准备好的歌在调用 holdsTurn 时就算好了，这样逐首判断时不用每首都扫一遍队列。等待队列变了之后要重新调用。
func (r *Room) holdsTurn() func(Entry) bool {
	anyReady := r.anyReady()
	return func(e Entry) bool {
		return e.Ready || r.now.Before(e.AddedAt.Add(prepareGrace)) || !anyReady
	}
}

func (r *Room) anyReady() bool {
	return slices.ContainsFunc(r.waiting, func(e Entry) bool { return e.Ready })
}

// 当前这首放完时要选的歌在等待队列中的位置，没有时为 -1。顺序和轮流模式下是排序之后第一首占着位置的歌，随机模式下是 drawNext 抽好的那首。
func (r *Room) upcoming() int {
	if r.mode == Random {
		return r.waitingIndex(r.next)
	}
	return slices.IndexFunc(r.waiting, r.holdsTurn())
}

// 下一首只在有歌在播、并且已经准备好时才有。客户端要在当前这首放完之前加载它的音频，而没准备好的歌可能还没有音频，比如链接和 B 站的歌要先下载进对象存储。
func (r *Room) upNext() EntryID {
	if r.current == nil {
		return 0
	}
	i := r.upcoming()
	if i < 0 || !r.waiting[i].Ready {
		return 0
	}
	return r.waiting[i].ID
}

// 随机模式在有歌在播时提前抽好下一首，这样客户端能提前加载。只在占着位置的歌里抽。抽中的歌一直留作下一首，直到它被选中、离开等待队列、
// 过了宽限期还没准备好，或者房间换成别的模式。它没准备好时 upNext 返回 0，准备好之后才下发。
func (r *Room) drawNext(b *batch) {
	if r.mode != Random {
		r.next = 0
		return
	}
	if r.current == nil {
		return
	}
	if i := r.waitingIndex(r.next); i >= 0 && r.holdsTurn()(r.waiting[i]) {
		return
	}
	r.next = 0
	if i := r.drawHeld(b); i >= 0 {
		r.next = r.waiting[i].ID
	}
}

// 从占着位置的歌里随机抽一首，返回它在等待队列中的位置。等待队列为空时返回 -1。
func (r *Room) drawHeld(b *batch) int {
	var held []int
	holds := r.holdsTurn()
	for i, e := range r.waiting {
		if holds(e) {
			held = append(held, i)
		}
	}
	if len(held) == 0 {
		return -1
	}
	return held[b.rand.IntN(len(held))]
}

// 房间在等的歌。空闲时是待播，有歌在播时是当前这首放完时要选的那首。
func (r *Room) awaited() *Entry {
	if r.current == nil {
		return r.pending
	}
	if i := r.upcoming(); i >= 0 {
		return &r.waiting[i]
	}
	return nil
}

// 房间在等一首还在宽限期内的歌，等待队列里又有准备好的歌时，宽限期一到就要改放准备好的歌，所以在那一刻设一个定时器。
// 没有准备好的歌时，宽限期到了它也不让出位置，所以不设定时器。
func (r *Room) watchGrace(b *batch) {
	var at time.Time
	if e := r.awaited(); e != nil && !e.Ready && r.anyReady() {
		if end := e.AddedAt.Add(prepareGrace); end.After(b.now) {
			at = end
		}
	}
	if at.Equal(r.graceAt) {
		return
	}
	r.graceAt, r.graceTimer = at, 0
	if !at.IsZero() {
		r.graceTimer = r.schedule(b, at)
	}
}

func (r *Room) start(b *batch) {
	r.current = &NowPlaying{Entry: *r.pending, StartedAt: b.now.Add(startLead)}
	r.pending = nil
	r.votes = nil
	r.endTimer = r.schedule(b, r.current.EndsAt())
	by := requesterOf(r.current.Entry)
	r.served[by] = max(r.served[by], r.current.Entry.Round)
	// 推荐基于网易云的相似歌曲。
	if r.current.Entry.Track.Source == track.NetEase && r.autoDJReady(b.now) {
		r.recommendation = r.issue()
		b.tasks = append(b.tasks, Recommend{Token: r.recommendation})
	}
}

func (r *Room) autoDJReady(now time.Time) bool {
	return len(r.waiting) == 0 && len(r.sessions) > 0 && now.Sub(r.lastPick) >= autoDJQuiet
}

func (r *Room) endCurrent(b *batch, at time.Time) {
	c := *r.current
	r.current = nil
	r.endTimer = 0
	// 推荐只对发出它的那首歌有效。所以 token 有效时 current 一定非空，recommended 也依赖这一点。
	r.recommendation = 0
	// 开播前的 startLead 之内被切掉时，结束时间不能早于开始时间。
	if at.Before(c.StartedAt) {
		at = c.StartedAt
	}
	b.plays = append(b.plays, RecordPlay{Entry: c.Entry, StartedAt: c.StartedAt, EndedAt: at})
}

// 房间关闭时还在播放的歌也算结束，要写播放记录。关闭可能在歌曲原定的结束时间之后才送达，所以记录不能比歌曲本身还长。
func (r *Room) stopPlayback(b *batch) {
	if r.current == nil {
		return
	}
	at := r.current.EndsAt()
	if b.now.Before(at) {
		at = b.now
	}
	r.endCurrent(b, at)
}

func (r *Room) voteSkip(b *batch, by member.Member) (Result, *Error) {
	if r.current == nil {
		return nil, errNothingPlaying
	}
	if slices.Contains(r.votes, by.ID) {
		return nil, errDuplicateVote
	}
	r.votes = append(r.votes, by.ID)
	votes, required := len(r.votes), r.voteThreshold()
	entry := r.current.Entry
	if votes < required {
		b.event(Activity{Kind: ActivityVoted, Member: by, Entry: entry, Votes: votes, Required: required})
		return VoteResult{Votes: votes, Required: required}, nil
	}
	b.event(Activity{Kind: ActivitySkipped, Member: by, Entry: entry, Votes: votes, Required: required})
	r.endCurrent(b, b.now)
	return VoteResult{Votes: votes, Required: required, Skipped: true}, nil
}

// 一人一票，所以门槛也按人数算，否则开着多个连接的成员会让票数永远凑不够。
func (r *Room) voteThreshold() int {
	return max((Headcount(r.sessions)+2)/3, 1)
}

// 有人离开后门槛会变低，已有的票数够了就直接切歌，否则投过票的人再投只会得到“已经投过票”，这首歌就切不掉了。
// 离开的人投过的票仍然算数，因为票一直有效到这首歌结束。这次切歌不是哪一票促成的，所以动态里没有成员。
func (r *Room) recountVotes(b *batch) {
	if r.current == nil || len(r.votes) == 0 {
		return
	}
	votes, required := len(r.votes), r.voteThreshold()
	if votes < required {
		return
	}
	b.event(Activity{Kind: ActivitySkipped, Entry: r.current.Entry, Votes: votes, Required: required})
	r.endCurrent(b, b.now)
}
