package port

import (
	"context"
	"time"
)

// 常驻房间的 API Key 是房间 bot 成员的票据。
type NewAPIKey struct {
	TokenHash []byte
	// 房间还没有 bot 成员时用它创建：对应的随机值不交给任何人，bot 因此没有可用的成员 token。
	BotTokenHash []byte
}

type APIKeyStatus struct {
	Set bool
	// Key 的生成时间，Set 为 false 时为零值。
	CreatedAt time.Time
}

// roomID 必须是常驻房间。每个房间至多一把 Key；bot 成员在第一次生成时创建，此后一直不变。
type APIKeys interface {
	RoomAPIKeyStatus(ctx context.Context, roomID string) (APIKeyStatus, error)
	// 还没有 Key 时生成，已经有时旧 Key 立即失效。
	ResetRoomAPIKey(ctx context.Context, roomID string, k NewAPIKey) error
}
