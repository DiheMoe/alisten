package media

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/room"
	"github.com/dihemoe/alisten/internal/domain/track"
)

// 接管房间的期限是 10 秒，检查链接和 B 站的歌的音频只占用其中一部分，这样对象存储变慢时接管仍能完成。
const restoreCheckTimeout = 5 * time.Second

// Tracks 读取准备结果时，链接和 B 站的歌还要求音频仍在对象存储中。音频被清理或丢失之后，这首歌要重新下载。
type Tracks struct {
	port.Tracks
	store port.MediaStore
}

func NewTracks(tracks port.Tracks, store port.MediaStore) *Tracks {
	return &Tracks{Tracks: tracks, store: store}
}

// 检查对象存储失败时返回错误，由调用方稍后重试，不当作没有结果。否则对象存储短暂不可用时，已经存储的歌都会重新下载。
func (t *Tracks) PreparedTrack(ctx context.Context, tr track.Track) (port.PreparedTrack, bool, error) {
	p, ok, err := t.Tracks.PreparedTrack(ctx, tr)
	if err != nil || !ok || !Stored(tr.Source) {
		return p, ok, err
	}
	exists, err := t.store.Exists(ctx, AudioObject(p.MediaKey))
	if err != nil {
		return port.PreparedTrack{}, false, fmt.Errorf("检查 %s 的音频是否还在对象存储中：%w", tr.ID, err)
	}
	if !exists {
		return port.PreparedTrack{}, false, nil
	}
	return p, true, nil
}

// 只检查待播和等待队列，因为正在播放的歌已经开播了。
type Snapshots struct {
	port.RoomSnapshots
	store  port.MediaStore
	logger *slog.Logger
}

func NewSnapshots(snapshots port.RoomSnapshots, store port.MediaStore, logger *slog.Logger) *Snapshots {
	return &Snapshots{RoomSnapshots: snapshots, store: store, logger: logger}
}

// 检查出错的条目按曲目表恢复，因为对象存储不可用时房间仍要能被接管。任期已经加 1，所以检查出错时不再返回错误。
func (s *Snapshots) TakeRoom(ctx context.Context, id string) (uint32, room.Snapshot, error) {
	term, snap, err := s.RoomSnapshots.TakeRoom(ctx, id)
	if err != nil {
		return term, snap, err
	}
	ctx, cancel := context.WithTimeout(ctx, restoreCheckTimeout)
	defer cancel()
	entries := make([]*room.Entry, 0, len(snap.Waiting)+1)
	if snap.Pending != nil {
		entries = append(entries, snap.Pending)
	}
	for i := range snap.Waiting {
		entries = append(entries, &snap.Waiting[i])
	}
	var errs []error
	for _, e := range entries {
		if !e.Ready || !Stored(e.Track.Source) {
			continue
		}
		exists, err := s.store.Exists(ctx, AudioObject(Key(e.Track.ID)))
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if !exists {
			e.Ready, e.Prep = false, track.Prepared{}
		}
	}
	if len(errs) > 0 {
		s.logger.WarnContext(ctx, "检查曲目的音频失败，这些条目按曲目表恢复", "failed", len(errs), "error", errs[0])
	}
	return term, snap, nil
}
