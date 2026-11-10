package port

import "context"

type ShortLinks interface {
	// 返回跳转之后的最终地址；跳转到不受信任的主机、跳转过多或超时都返回错误。
	Resolve(ctx context.Context, link string) (string, error)
}
