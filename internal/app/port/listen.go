package port

import (
	"context"
	"time"

	"github.com/dihemoe/alisten/internal/domain/track"
)

// 每个成员的播放历史最多保留的歌数，超出的部分从最早听到的删起。
const ListenLimit = 500

// 成员在房间里听到过的一首歌。元数据是听到时的快照。
type Listen struct {
	Track   track.Track
	Meta    track.Meta
	HeardAt time.Time
}

type ListenRecorder interface {
	// 只记给开启了记录的成员。同一首歌已有记录时 HeardAt 取较晚的，元数据用新的；
	// 每个成员超过 ListenLimit 首的部分随即删掉。
	RecordListen(ctx context.Context, memberIDs []string, l Listen) error
}

type Listens interface {
	// 最近听到的在前，最多 ListenLimit 首。
	MemberListens(ctx context.Context, memberID string) ([]Listen, error)
	// 不在历史里的歌忽略。
	DeleteListen(ctx context.Context, memberID string, t track.Track) error
	ClearListens(ctx context.Context, memberID string) error
	// 成员不存在时返回 ErrMemberNotFound。
	ListenRecording(ctx context.Context, memberID string) (bool, error)
	// 关闭之后已有的历史保留。成员不存在时返回 ErrMemberNotFound。
	SetListenRecording(ctx context.Context, memberID string, on bool) error
}
