package port

import (
	"context"
	"errors"

	"github.com/dihemoe/alisten/internal/domain/track"
)

var ErrProviderFailed = errors.New("音乐平台没有正常响应，请稍后再试。")

// 房间宿主调用 Prepare 时，ctx 在房间停止运行时取消，结果不再交回房间；
// 实现要自己为每次对外请求设置期限。
type Preparer interface {
	// 返回错误表示准备失败，错误的文本就是广播给房间的失败原因。
	Prepare(ctx context.Context, t track.Track) (track.Meta, track.Prepared, error)
	// 各实例收到 tracks.prepared 频道上的结果时调用，不能阻塞：调用方是接收实例间消息的 goroutine。
	Finished(o PrepareOutcome)
}
