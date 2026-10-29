package port

import (
	"context"
	"io"
	"time"

	"github.com/dihemoe/alisten/internal/domain/track"
)

type MediaStore interface {
	// body 要能 Seek：签名要先读一遍算出摘要，失败重试时也要从头再读。
	Put(ctx context.Context, key string, body io.ReadSeeker, contentType string) error
	Exists(ctx context.Context, key string) (bool, error)
	// 对象不存在时同样成功。
	Delete(ctx context.Context, key string) error
}

type StoredMedia struct {
	// 曲目 ID 的 SHA-256（十六进制）。链接的曲目 ID 是 http(s) 地址，B 站的是 BV 号，两种 ID 不会相同，所以单凭它就能找到这首歌。
	Key    string
	Source track.Source
	// 曲目 ID：链接曲目是链接本身，B 站的歌是 BV 号。
	Link  string
	Title string
	// 音频的字节数。
	Size         int64
	CoverKey     track.ObjectKey
	StoredAt     time.Time
	LastPlayedAt *time.Time
}

type MediaLibrary interface {
	// 最近存入的在前。
	ListStoredMedia(ctx context.Context) ([]StoredMedia, error)
	StoredMedia(ctx context.Context, key string) (m StoredMedia, ok bool, err error)
	// 清除这首歌的准备结果。
	DeleteStoredMedia(ctx context.Context, key string) error
}
