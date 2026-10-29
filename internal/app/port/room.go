package port

import (
	"context"
	"errors"

	"github.com/dihemoe/alisten/internal/domain/room"
)

var (
	ErrRoomNotFound = errors.New("房间不存在")
	ErrSlugTaken    = errors.New("别名已被占用")
)

// 别名或密码哈希为空字符串表示没有。
type (
	RoomRecord struct {
		Info         room.Info
		Slug         string
		PasswordHash string
	}

	NewRoom struct {
		Kind         room.Kind
		Name         string
		Description  string
		Slug         string
		PasswordHash string
	}
)

// nil 表示不修改，空字符串表示取消。
type AccessChange struct {
	Slug         *string
	PasswordHash *string
}

type RoomDirectory interface {
	CreateRoom(ctx context.Context, r NewRoom) (string, error)
	ListRooms(ctx context.Context) ([]RoomRecord, error)
	// ref 是房间的 UUID 或别名。
	FindRoom(ctx context.Context, ref string) (RoomRecord, error)
	// 设置或修改密码时同时删除房间成员的票据，bot 成员的票据（API Key）保留；调用方随后还要让这个房间的在线会话断开。
	UpdateRoomAccess(ctx context.Context, id string, c AccessChange) error
}
