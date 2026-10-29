package port

import "context"

// 没有设置过品牌名时，BrandName 返回空字符串。
type Site interface {
	BrandName(ctx context.Context) (string, error)
	SetBrandName(ctx context.Context, name string) error
}
