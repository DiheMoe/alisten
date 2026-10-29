package port

import (
	"context"
	"time"
)

// 凭据未设置时，读取方法都返回空字符串。写入方法在具体的 Store 上，与网易云、QQ 的 SetCookie 相同。
type Credentials interface {
	NeteaseCookie(ctx context.Context) (string, error)
	QQCookie(ctx context.Context) (string, error)
	BilibiliCredential(ctx context.Context) (string, error)
}

type CredentialStatus struct {
	Set     bool
	Account *SourceAccount
}

type YouTubeCookies interface {
	// cookies 为空字符串时删除已保存的 cookies。
	SetCookies(context.Context, string) error
	Status(context.Context) (YouTubeCookiesStatus, error)
}

type YouTubeCookiesStatus struct {
	Set bool
	// 最近一次填写的时间，Set 为 false 时为零值。
	UpdatedAt time.Time
}
