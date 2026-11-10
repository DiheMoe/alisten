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
	Link   string
	Title  string
	Artist string
	// 音频的字节数。
	Size int64
	// 两者的含义与 track.Meta 的同名字段相同。
	CoverURL     string
	CoverKey     track.ObjectKey
	StoredAt     time.Time
	LastPlayedAt *time.Time
}

// 每种排序都以曲目 ID 作为最后的排序键，翻页时不重复也不遗漏。
type MediaSort uint8

const (
	// 存储时间从新到旧。
	MediaByStored MediaSort = iota + 1
	// 最近播放从早到晚，从没播放过的排最前。
	MediaByIdle
	// 从大到小。
	MediaBySize
)

const MediaPageSize = 50

type MediaQuery struct {
	// 从 1 开始，超出最后一页时返回空的一页。
	Page int
	Sort MediaSort
	// 标题包含它就算匹配，不区分大小写，按字面匹配；为空表示不搜索。
	Title string
}

type MediaPage struct {
	Items []StoredMedia
	// 匹配的全部歌曲的数量和音频的字节数，不只是这一页。
	Total      int
	TotalBytes int64
}

type MediaLibrary interface {
	ListStoredMedia(ctx context.Context, q MediaQuery) (MediaPage, error)
	StoredMedia(ctx context.Context, key string) (m StoredMedia, ok bool, err error)
	// 清除这首歌的准备结果。
	DeleteStoredMedia(ctx context.Context, key string) error
}
