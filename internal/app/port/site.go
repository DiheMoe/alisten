package port

import "context"

type SiteSettings struct {
	// 没有设置过品牌名时为空字符串。
	BrandName        string
	RegistrationOpen bool
	// 没有设置过临时房间最多人数时为 0。
	EphemeralRoomCapacity int
}

type Site interface {
	Settings(ctx context.Context) (SiteSettings, error)
	SetBrandName(ctx context.Context, name string) error
	SetRegistrationOpen(ctx context.Context, open bool) error
	SetEphemeralRoomCapacity(ctx context.Context, capacity int) error
}
