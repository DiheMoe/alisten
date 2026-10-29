package room

import (
	"time"

	"github.com/dihemoe/alisten/internal/domain/member"
	"github.com/dihemoe/alisten/internal/domain/track"
)

type Effect interface{ effect() }

type Persist struct {
	Snapshot Snapshot
}

type Emit struct {
	Term  uint32
	Seq   uint64
	Event Event
}

type Reply struct {
	RequestID RequestID
	Result    Result
	Err       *Error
}

type Prepare struct {
	Track track.Track
}

type Recommend struct {
	Token Token
}

type Schedule struct {
	At    time.Time
	Token Token
}

type RecordPlay struct {
	Entry     Entry
	StartedAt time.Time
	EndedAt   time.Time
}

type Close struct{}

func (Persist) effect()    {}
func (Emit) effect()       {}
func (Reply) effect()      {}
func (Prepare) effect()    {}
func (Recommend) effect()  {}
func (Schedule) effect()   {}
func (RecordPlay) effect() {}
func (Close) effect()      {}

type Event interface{ event() }

type PlaybackChanged struct {
	Current *NowPlaying
}

type QueueChanged struct {
	Pending *Entry
	Waiting []Entry
}

type MembersChanged struct {
	Sessions []Session
}

type ModeChanged struct {
	Mode PlayMode
}

type InfoChanged struct {
	Name        string
	Description string
}

type ChatPosted struct {
	Member member.Member
	Text   string
	SentAt time.Time
}

type ActivityKind uint8

const (
	ActivityAdded ActivityKind = iota + 1
	ActivityRemoved
	ActivityLiked
	ActivityVoted
	ActivitySkipped
	ActivityFailed
)

type Activity struct {
	Kind     ActivityKind
	Member   member.Member
	Entry    Entry
	Votes    int
	Required int
	Reason   string
}

func (PlaybackChanged) event() {}
func (QueueChanged) event()    {}
func (MembersChanged) event()  {}
func (ModeChanged) event()     {}
func (InfoChanged) event()     {}
func (ChatPosted) event()      {}
func (Activity) event()        {}

type Result interface{ result() }

type Done struct{}

type EnqueueResult struct {
	Entry Entry
}

type LikeResult struct {
	Likes int
}

type VoteResult struct {
	Votes    int
	Required int
	Skipped  bool
}

type SyncResult struct {
	View View
}

func (Done) result()          {}
func (EnqueueResult) result() {}
func (LikeResult) result()    {}
func (VoteResult) result()    {}
func (SyncResult) result()    {}
