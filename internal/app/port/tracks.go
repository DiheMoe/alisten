package port

import (
	"context"

	"github.com/dihemoe/alisten/internal/domain/track"
)

// 曲目表中只有准备得到的时长，读出的 Meta.Duration 就是 Result.Duration。
type PreparedTrack struct {
	Meta   track.Meta
	Result track.Prepared
	// 链接和 B 站的歌的 MediaKey 是曲目 ID 的 SHA-256（十六进制），对象存储中音频和封面的对象键由它得出；
	// MediaSize 是音频的字节数。其他来源为空。
	MediaKey  string
	MediaSize int64
}

type Tracks interface {
	PreparedTrack(ctx context.Context, t track.Track) (p PreparedTrack, ok bool, err error)
	SavePreparedTrack(ctx context.Context, t track.Track, p PreparedTrack) error
}
