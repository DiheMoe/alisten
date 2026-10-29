package port

import (
	"context"
	"io"
	"time"

	"github.com/dihemoe/alisten/internal/domain/track"
)

type QRLoginStatus string

const (
	QRLoginWaiting QRLoginStatus = "waiting"
	QRLoginScanned QRLoginStatus = "scanned"
	QRLoginSuccess QRLoginStatus = "success"
	QRLoginExpired QRLoginStatus = "expired"
	QRLoginFailed  QRLoginStatus = "failed"
)

type QRLoginSession struct {
	Key string
	// 二维码编码的登录页链接；平台只给图片时为空。
	LoginURL string
	// 浏览器可以直接显示的二维码图片：data: URI 或 https 地址；平台只给登录链接时为空。两者至少有一个。
	QRImage   string
	ExpiresAt time.Time
}

type QRLoginResult struct {
	Status  QRLoginStatus
	Message string
}

type SourceAccount struct {
	ID       string
	Name     *string
	VIP      *bool
	Verified bool
}

type QQLoginType string

const (
	QQLoginQQ     QQLoginType = "qq"
	QQLoginWeChat QQLoginType = "wx"
)

type NetEaseQRLoginSource interface {
	CreateQRLogin(context.Context) (QRLoginSession, error)
	CheckQRLogin(context.Context, string) (QRLoginResult, error)
	CurrentAccount(context.Context) (*SourceAccount, error)
}

type QQQRLoginSource interface {
	CreateQRLogin(context.Context, QQLoginType) (QRLoginSession, error)
	CheckQRLogin(context.Context, QQLoginType, string) (QRLoginResult, error)
	CurrentAccount(context.Context) (*SourceAccount, error)
}

// RejectedError 表示音源明确拒绝了这首歌，比如歌曲下架或没有版权：准备不再重试。
// 它的文本就是准备失败的原因。
type RejectedError struct {
	Err error
}

func (e *RejectedError) Error() string { return e.Err.Error() }
func (e *RejectedError) Unwrap() error { return e.Err }

type (
	Song struct {
		Track track.Track
		Meta  track.Meta
	}

	Playlist struct {
		ID          string
		Name        string
		CoverURL    string
		Creator     string
		Description string
		TrackCount  int64
		PlayCount   int64
	}

	// Audio 由调用方读完后关闭。
	Resolved struct {
		Meta  track.Meta
		Lyric string
		Audio io.ReadCloser
	}
)

type Source interface {
	SearchTracks(ctx context.Context, keyword string) ([]Song, error)
	SearchPlaylists(ctx context.Context, keyword string) ([]Playlist, error)
	TrackMeta(ctx context.Context, id string) (track.Meta, error)
	Resolve(ctx context.Context, id string) (Resolved, error)
}

// 只有网易云提供歌单内的歌曲和相似歌曲。
type NetEaseSource interface {
	Source
	PlaylistTracks(ctx context.Context, playlistID string) ([]Song, error)
	SimilarTracks(ctx context.Context, id string) ([]Song, error)
}

// 供 /media 跳转使用。播放地址有时效，调用方把取到的地址复用 1 分钟。
// 音源明确拒绝这首歌时返回 *RejectedError。实现要自己为对外请求设置期限。
type PlayURLs interface {
	PlayURL(ctx context.Context, id string) (string, error)
}
