package identity

import (
	"context"
	"log/slog"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
)

// 游客会话闲置超过 7 天就删除。存储记下的使用时间可能比实际早将近一天，所以实际闲置 6 到 7 天的会话也可能被删除。
// 没有会话的游客创建超过 1 天才删除，因为 port.Members.CreateMember 不保证成员和它的第一个会话同时写入，刚创建的成员可能还没有会话。
const (
	guestSessionIdle = 7 * 24 * time.Hour
	guestMemberGrace = 24 * time.Hour
)

type GuestCleanup struct {
	guests port.IdleGuests
	logger *slog.Logger
}

func NewGuestCleanup(guests port.IdleGuests, logger *slog.Logger) *GuestCleanup {
	return &GuestCleanup{guests: guests, logger: logger}
}

// 删除闲置的游客会话和没有会话的游客，账号不受影响。返回错误时什么也没有删除，可以直接重试。
func (c *GuestCleanup) Run(ctx context.Context, now time.Time) error {
	sessions, members, err := c.guests.DeleteIdleGuests(ctx, now.Add(-guestSessionIdle), now.Add(-guestMemberGrace))
	if err != nil {
		c.logger.WarnContext(ctx, "清理闲置的游客失败", "error", err)
		return err
	}
	c.logger.InfoContext(ctx, "已清理闲置的游客", "sessions", sessions, "members", members)
	return nil
}
