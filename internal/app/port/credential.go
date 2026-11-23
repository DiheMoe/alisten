package port

import (
	"context"
	"time"
)

type CredentialStatus struct {
	Set     bool
	Account *SourceAccount
	// 保存了凭据，但音乐平台确认登录已失效。这时 Account 为 nil。
	SignedOut bool
}

type YouTubeCookies interface {
	// cookies 为空字符串时删除已保存的 cookies。
	SetYouTubeCookies(context.Context, string) error
	YouTubeCredentialStatus(context.Context) (YouTubeCredentialStatus, error)
}

type YouTubeCredentialStatus struct {
	Set bool
	// 最近一次填写的时间，Set 为 false 时为零值。
	UpdatedAt time.Time
}
