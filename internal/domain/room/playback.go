package room

import (
	"slices"
	"time"

	"github.com/dihemoe/alisten/internal/domain/member"
	"github.com/dihemoe/alisten/internal/domain/track"
)

func (r *Room) advanceIfIdle(b *batch) {
	if r.current != nil || r.pending != nil || len(r.waiting) == 0 {
		return
	}
	i := 0
	if r.mode == Random {
		i = r.waitingIndex(r.next)
		if i < 0 {
			i = b.rand.IntN(len(r.waiting))
		}
		r.next = 0
	}
	next := r.waiting[i]
	r.waiting = slices.Delete(r.waiting, i, i+1)
	r.pending = &next
	if next.Ready {
		r.start(b)
	}
}

// 下一首只在有歌在播、并且已经准备好时才有。客户端要在当前这首放完之前加载它的音频，而没准备好的歌可能还没有音频，比如链接和 B 站的歌要先下载进对象存储。
// 顺序和轮流模式下是排序之后的第一首，随机模式下是 drawNext 抽好的那首。
func (r *Room) upNext() EntryID {
	if r.current == nil {
		return 0
	}
	i := 0
	if r.mode == Random {
		i = r.waitingIndex(r.next)
	}
	if i < 0 || i >= len(r.waiting) || !r.waiting[i].Ready {
		return 0
	}
	return r.waiting[i].ID
}

// 随机模式在有歌在播时提前抽好下一首，这样客户端能提前加载。抽中的歌一直留作下一首，直到它被选中、离开等待队列，或者房间换成别的模式。它没准备好时 upNext 返回 0，准备好之后才下发。
func (r *Room) drawNext(b *batch) {
	if r.mode != Random {
		r.next = 0
		return
	}
	if r.current == nil || len(r.waiting) == 0 || r.waitingIndex(r.next) >= 0 {
		return
	}
	r.next = r.waiting[b.rand.IntN(len(r.waiting))].ID
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
	votes, required := len(r.votes), max((len(r.sessions)+2)/3, 1)
	entry := r.current.Entry
	if votes < required {
		b.event(Activity{Kind: ActivityVoted, Member: by, Entry: entry, Votes: votes, Required: required})
		return VoteResult{Votes: votes, Required: required}, nil
	}
	b.event(Activity{Kind: ActivitySkipped, Member: by, Entry: entry, Votes: votes, Required: required})
	r.endCurrent(b, b.now)
	r.advanceIfIdle(b)
	return VoteResult{Votes: votes, Required: required, Skipped: true}, nil
}
