package port

import (
	"context"
	"time"
)

// Leases 是房间的 owner 租约，值是实例 ID。实现绑定在本实例上：取得、续期和释放的都是本实例的租约。
// 房间的在线人数附在租约上，与租约一起续期、一起过期。
type Leases interface {
	// 租约不存在或已经属于本实例时取得它，在线人数记为 0；返回 false 表示租约属于其他实例。
	Acquire(ctx context.Context, roomID string, ttl time.Duration) (bool, error)
	// 租约仍属于本实例时续期；返回 false 表示租约已不属于本实例。
	Renew(ctx context.Context, roomID string, ttl time.Duration) (bool, error)
	// 租约仍属于本实例时写入在线人数；返回 false 表示租约已不属于本实例。
	SetOnline(ctx context.Context, roomID string, online int) (bool, error)
	// 只删除属于本实例的租约。
	Release(ctx context.Context, roomID string) error
	// 按 roomIDs 的顺序返回各房间的 owner，没有 owner 时为空字符串。
	Owners(ctx context.Context, roomIDs []string) ([]string, error)
}

type OnlineCounts interface {
	// 按 roomIDs 的顺序返回各房间的在线人数，没有 owner 的房间为 0。
	OnlineCounts(ctx context.Context, roomIDs []string) ([]int, error)
}

// Liveness 是各实例的存活键。实现绑定在本实例上：Heartbeat 刷新的是本实例的存活键。
type Liveness interface {
	// 返回 false 表示刷新之前存活键已经不存在：owner 可能已经让本实例的会话离开了在线列表。
	Heartbeat(ctx context.Context, ttl time.Duration) (bool, error)
	Expired(ctx context.Context, instanceIDs []string) ([]string, error)
}
