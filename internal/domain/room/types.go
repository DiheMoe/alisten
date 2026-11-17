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
	// 房间最多能进多少人，人数按成员去重计算。
	Capacity int
}

type Entry struct {
	ID    EntryID
	Track track.Track
	Meta  track.Meta
	By    member.Member
	// bot 代点时替谁点的歌，是 bot 填的名字。成员自己点的歌为空。
	Requester string
	Auto      bool
	Likes     int
	Round     int
	Ready     bool
	Prep      track.Prepared
	AddedAt   time.Time
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
	// 下一首的条目 ID，没有时为 0，规则见 upNext。
	Next EntryID
}
