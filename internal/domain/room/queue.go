package room

import (
	"cmp"
	"maps"
	"slices"

	"github.com/dihemoe/alisten/internal/domain/member"
	"github.com/dihemoe/alisten/internal/domain/track"
)

func (r *Room) command(b *batch, c Command) {
	var res Result
	var err *Error
	switch a := c.Action.(type) {
	case Enqueue:
		res, err = r.enqueue(b, c.Member, a)
	case Remove:
		res, err = r.remove(b, c.Member, a)
	case Like:
		res, err = r.like(b, c.Member, a)
	case VoteSkip:
		res, err = r.voteSkip(b, c.Member)
	case SetPlayMode:
		r.mode = a.Mode
		res = Done{}
	case Chat:
		b.event(ChatPosted{Member: c.Member, Text: truncate(a.Text, chatLimit), SentAt: b.now})
		res = Done{}
	case Sync:
		res = SyncResult{View: r.view()}
	}
	b.respond(c.RequestID, res, err)
}

func (r *Room) burst() int {
	if r.info.Kind == Persistent {
		return persistentBurst
	}
	return ephemeralBurst
}

func (r *Room) enqueue(b *batch, by member.Member, a Enqueue) (Result, *Error) {
	if !r.pickLimit.allow(b.now, r.burst(), refillInterval) {
		return nil, errRateLimited
	}
	if a.Track.Source == track.Link && r.info.Kind == Ephemeral {
		return nil, errLinkForbidden
	}
	if r.info.Kind == Ephemeral && len(r.waiting) >= ephemeralQueueLimit {
		return nil, errQueueFull
	}
	if r.queued(a.Track) {
		return nil, errDuplicatePick
	}
	e := r.add(b, a.Track, a.Meta, by, false)
	r.lastPick = b.now
	b.event(Activity{Kind: ActivityAdded, Member: by, Entry: e})
	r.advanceIfIdle(b)
	return EnqueueResult{Entry: e}, nil
}

func (r *Room) remove(b *batch, by member.Member, a Remove) (Result, *Error) {
	if !r.pickLimit.allow(b.now, r.burst(), refillInterval) {
		return nil, errRateLimited
	}
	i := r.waitingIndex(a.Entry)
	if i < 0 {
		return nil, errNotInQueue
	}
	e := r.waiting[i]
	r.waiting = slices.Delete(r.waiting, i, i+1)
	b.event(Activity{Kind: ActivityRemoved, Member: by, Entry: e})
	return Done{}, nil
}

func (r *Room) like(b *batch, by member.Member, a Like) (Result, *Error) {
	if !r.likeLimit.allow(b.now, r.burst(), refillInterval) {
		return nil, errRateLimited
	}
	i := r.waitingIndex(a.Entry)
	if i < 0 {
		return nil, errNotInQueue
	}
	r.waiting[i].Likes++
	e := r.waiting[i]
	b.event(Activity{Kind: ActivityLiked, Member: by, Entry: e})
	return LikeResult{Likes: e.Likes}, nil
}

func (r *Room) add(b *batch, t track.Track, m track.Meta, by member.Member, auto bool) Entry {
	r.entrySeq++
	e := Entry{ID: r.entrySeq, Track: t, Meta: m, By: by, Auto: auto, Round: r.nextRound(by), AddedAt: b.now}
	r.waiting = append(r.waiting, e)
	b.prepare(t)
	return e
}

// 轮次在入队时定下，之后不再改变：新歌排进最早还没放完的一轮；点歌人还有歌在队列中，或者已经有歌开播，就至少排到这些歌中最高一轮的下一轮。
// 最早的一轮不一定是正在播放的那一轮，点赞会让靠后轮次的歌先放。
// 点歌人按成员 ID 和名字区分：bot 用同一个成员替不同的人点歌；AutoDJ 加入的歌没有点歌人，算作同一个点歌人。
func (r *Room) nextRound(by member.Member) int {
	entries := slices.Clone(r.waiting)
	if r.pending != nil {
		entries = append(entries, *r.pending)
	}
	if r.current != nil {
		entries = append(entries, r.current.Entry)
	}
	if len(entries) == 0 {
		// 房间放空之后轮次从 0 重新开始，之前开播的记录作废。
		clear(r.served)
		return 0
	}
	round := slices.MinFunc(entries, func(a, b Entry) int { return cmp.Compare(a.Round, b.Round) }).Round
	// 早于最早一轮的记录不会再影响轮次；bot 代填的点歌人名字不受限制，不删就会一直增长。
	maps.DeleteFunc(r.served, func(_ requester, served int) bool { return served < round })
	key := requesterOf(by)
	if served, ok := r.served[key]; ok {
		round = max(round, served+1)
	}
	for _, e := range entries {
		if requesterOf(e.By) == key {
			round = max(round, e.Round+1)
		}
	}
	return round
}

type requester struct{ id, name string }

func requesterOf(m member.Member) requester { return requester{m.ID, m.Name} }

func (r *Room) prepared(b *batch, in Prepared) {
	e := r.unready(in.Track)
	if e == nil {
		return
	}
	e.Meta, e.Ready, e.Prep = in.Meta, true, in.Result
	if e == r.pending {
		r.start(b)
	}
}

func (r *Room) prepareFailed(b *batch, in PrepareFailed) {
	e := r.unready(in.Track)
	if e == nil {
		return
	}
	failed := *e
	if e == r.pending {
		r.pending = nil
	} else {
		r.waiting = slices.DeleteFunc(r.waiting, func(w Entry) bool { return w.ID == failed.ID })
	}
	b.event(Activity{Kind: ActivityFailed, Entry: failed, Reason: in.Reason})
	r.advanceIfIdle(b)
}

func (r *Room) recommended(b *batch, in Recommended) {
	if in.Token == 0 || in.Token != r.recommendation {
		return
	}
	r.recommendation = 0
	if !r.autoDJReady(b.now) || r.current.Entry.Track == in.Track {
		return
	}
	e := r.add(b, in.Track, in.Meta, member.Member{}, true)
	b.event(Activity{Kind: ActivityAdded, Entry: e})
}

func (r *Room) queued(t track.Track) bool {
	return r.pending != nil && r.pending.Track == t ||
		slices.ContainsFunc(r.waiting, func(e Entry) bool { return e.Track == t })
}

// 待播一就绪就开播，所以待播总是在准备中。
func (r *Room) unready(t track.Track) *Entry {
	if r.pending != nil && r.pending.Track == t {
		return r.pending
	}
	i := slices.IndexFunc(r.waiting, func(e Entry) bool { return !e.Ready && e.Track == t })
	if i < 0 {
		return nil
	}
	return &r.waiting[i]
}

func (r *Room) waitingIndex(id EntryID) int {
	return slices.IndexFunc(r.waiting, func(e Entry) bool { return e.ID == id })
}

func truncate(s string, n int) string {
	if runes := []rune(s); len(runes) > n {
		return string(runes[:n])
	}
	return s
}

func (r *Room) sortWaiting() {
	slices.SortFunc(r.waiting, compareWaiting(r.mode))
}

func compareWaiting(mode PlayMode) func(a, b Entry) int {
	return func(a, b Entry) int {
		c := cmp.Compare(b.Likes, a.Likes)
		if mode == Rotation {
			c = cmp.Or(c, cmp.Compare(a.Round, b.Round))
		}
		return cmp.Or(c, cmp.Compare(a.ID, b.ID))
	}
}
