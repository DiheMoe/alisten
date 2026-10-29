package room

import (
	"time"

	"github.com/dihemoe/alisten/internal/domain/member"
	"github.com/dihemoe/alisten/internal/domain/track"
)

type Kind uint8

const (
	Ephemeral Kind = iota + 1
	Persistent
)

type PlayMode uint8

const (
	Sequential PlayMode = iota + 1
	Random
	Rotation
)

type (
	EntryID   uint64
	SessionID string
	RequestID string
	Token     uint64
)

type Env struct {
	Now  time.Time
	Seed uint64
}

type Info struct {
	ID          string
	Name        string
	Description string
	Kind        Kind
}

type Entry struct {
	ID      EntryID
	Track   track.Track
	Meta    track.Meta
	By      member.Member
	Auto    bool
	Likes   int
	Round   int
	Ready   bool
	Prep    track.Prepared
	AddedAt time.Time
}

type NowPlaying struct {
	Entry     Entry
	StartedAt time.Time
}

func (p NowPlaying) EndsAt() time.Time {
	return p.StartedAt.Add(p.Entry.Prep.Duration)
}

type Session struct {
	ID     SessionID
	Member member.Member
}

type Snapshot struct {
	Info     Info
	Mode     PlayMode
	EntrySeq EntryID
	Waiting  []Entry
	Pending  *Entry
	Current  *NowPlaying
}

type View struct {
	Term     uint32
	Seq      uint64
	Snapshot Snapshot
	Sessions []Session
}
