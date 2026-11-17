package room

import (
	"fmt"
	"time"
)

type ErrorKind uint8

const (
	NotFound ErrorKind = iota + 1
	Forbidden
	Conflict
	QueueFull
	RateLimited
	NothingPlaying
	RoomFull
	TooManySessions
)

type Error struct {
	Kind    ErrorKind
	Message string
}

func (e *Error) Error() string { return e.Message }

var (
	errLinkForbidden       = &Error{Forbidden, "临时房间只能点哔哩哔哩的链接。"}
	errEphemeralQueueFull  = &Error{QueueFull, fmt.Sprintf("临时房间的播放列表最多 %d 首歌，等播放几首之后再点。", ephemeralQueueLimit)}
	errPersistentQueueFull = &Error{QueueFull, fmt.Sprintf("播放列表最多 %d 首歌，等播放几首之后再点。", persistentQueueLimit)}
	errDuplicatePick       = &Error{Conflict, "这首歌已经在播放列表里了。"}
	errNotInQueue          = &Error{NotFound, "这首歌已经不在播放列表里了。"}
	errNothingPlaying      = &Error{NothingPlaying, "现在没有在放的歌。"}
	errDuplicateVote       = &Error{Conflict, "你已经投过票了。"}
	errTooManySessions     = &Error{TooManySessions, fmt.Sprintf("你在这个房间已经开了 %d 个连接。关掉一个，这里就会自动连上。", maxSessionsPerMember)}
)

func errRoomFull(capacity int) *Error {
	return &Error{RoomFull, fmt.Sprintf("房间已满，最多 %d 人。", capacity)}
}

// 点歌和移除共用整个房间的额度，所以文案写明是房间的额度，否则没操作过的人收到它也会以为是自己点得太快。
func PickQuotaExhausted(wait time.Duration) *Error {
	return &Error{RateLimited, fmt.Sprintf("这个房间点歌和移除的次数用完了，%d 秒后再试。", seconds(wait))}
}

func errLikeQuota(wait time.Duration) *Error {
	return &Error{RateLimited, fmt.Sprintf("这个房间点赞的次数用完了，%d 秒后再试。", seconds(wait))}
}

func errChatQuota(wait time.Duration) *Error {
	return &Error{RateLimited, fmt.Sprintf("消息发得太快了，%d 秒后再发。", seconds(wait))}
}

func errModeQuota(wait time.Duration) *Error {
	return &Error{RateLimited, fmt.Sprintf("播放模式切换得太频繁了，%d 秒后再试。", seconds(wait))}
}

// 不足一秒的部分算一秒，免得用户照着提示的时间重试时额度还没恢复。
func seconds(d time.Duration) int {
	return int((d + time.Second - 1) / time.Second)
}
