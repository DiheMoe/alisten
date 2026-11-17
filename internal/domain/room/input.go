package room

import (
	"github.com/dihemoe/alisten/internal/domain/member"
	"github.com/dihemoe/alisten/internal/domain/track"
)

type Input interface{ input() }

type Join struct {
	RequestID RequestID
	Session   SessionID
	Member    member.Member
	// Rejoin 为 true 表示会话已经进过房间，网关把它重新加入，比如房间换实例开始了新的一次运行。重新加入不检查上限，也不回复。
	// 网关把本地已有的会话逐个重新加入，检查上限的话，先重新加入的会话会占满名额，后面的就被拒，换实例就成了随机踢人。
	Rejoin bool
}

type Leave struct {
	Session SessionID
}

type MemberUpdated struct {
	Member member.Member
}

// Name 和 Description 为 nil 表示保持不变，因为管理接口只修改给出的字段。
type UpdateInfo struct {
	RequestID   RequestID
	Name        *string
	Description *string
}

type Delete struct {
	RequestID RequestID
}

type Command struct {
	RequestID RequestID
	Member    member.Member
	Action    Action
}

type Prepared struct {
	Track  track.Track
	Meta   track.Meta
	Result track.Prepared
}

type PrepareFailed struct {
	Track  track.Track
	Reason string
}

type Recommended struct {
	Token Token
	Track track.Track
	Meta  track.Meta
}

type Timer struct {
	Token Token
}

func (Join) input()          {}
func (Leave) input()         {}
func (MemberUpdated) input() {}
func (UpdateInfo) input()    {}
func (Delete) input()        {}
func (Command) input()       {}
func (Prepared) input()      {}
func (PrepareFailed) input() {}
func (Recommended) input()   {}
func (Timer) input()         {}

type Action interface{ action() }

type Enqueue struct {
	Track track.Track
	Meta  track.Meta
	// bot 代点时替谁点的歌，是 bot 填的名字。成员自己点的歌为空。
	Requester string
}

type Remove struct {
	Entry EntryID
}

type Like struct {
	Entry EntryID
}

type VoteSkip struct{}

type SetPlayMode struct {
	Mode PlayMode
}

type Chat struct {
	Text string
}

type Sync struct{}

func (Enqueue) action()     {}
func (Remove) action()      {}
func (Like) action()        {}
func (VoteSkip) action()    {}
func (SetPlayMode) action() {}
func (Chat) action()        {}
func (Sync) action()        {}
