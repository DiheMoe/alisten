package room

import "fmt"

type ErrorKind uint8

const (
	NotFound ErrorKind = iota + 1
	Forbidden
	Conflict
	QueueFull
	RateLimited
	NothingPlaying
)

type Error struct {
	Kind    ErrorKind
	Message string
}

func (e *Error) Error() string { return e.Message }

var (
	errRateLimited    = &Error{RateLimited, "操作过于频繁，请稍后再试。"}
	errLinkForbidden  = &Error{Forbidden, "临时房间只能点哔哩哔哩的链接。"}
	errQueueFull      = &Error{QueueFull, fmt.Sprintf("临时房间的播放列表最多 %d 首歌，等播放几首之后再点。", ephemeralQueueLimit)}
	errDuplicatePick  = &Error{Conflict, "这首歌已经在播放列表里了。"}
	errNotInQueue     = &Error{NotFound, "这首歌已经不在播放列表里了。"}
	errNothingPlaying = &Error{NothingPlaying, "现在没有在放的歌。"}
	errDuplicateVote  = &Error{Conflict, "你已经投过票了。"}
)
