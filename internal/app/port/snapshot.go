package port

import (
	"context"
	"errors"

	"github.com/dihemoe/alisten/internal/domain/room"
)

// 房间已由新的 owner 接管或已被删除，调用方必须放弃这个房间。
var ErrTermExpired = errors.New("任期已过期")

type RoomSnapshots interface {
	// 任期加 1 之后，之前任期的写入都会失败，所以只有接管房间的实例才能调用。
	// 条目是否就绪只按曲目表判断；链接和 B 站的歌的音频是否还在对象存储中，由调用方检查。
	TakeRoom(ctx context.Context, id string) (uint32, room.Snapshot, error)
	SaveRoom(ctx context.Context, term uint32, s room.Snapshot, plays []room.RecordPlay) error
	DeleteRoom(ctx context.Context, id string, term uint32) error
}
