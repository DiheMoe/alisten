package media

import (
	"context"
	"errors"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/track"
)

var (
	ErrNotFound = errors.New("媒体不存在")
	ErrInUse    = errors.New("这首歌还在房间的播放列表中，不能删除")
)

type Library struct {
	media port.MediaLibrary
	usage port.TrackUsage
	store port.MediaStore
}

func NewLibrary(media port.MediaLibrary, usage port.TrackUsage, store port.MediaStore) *Library {
	return &Library{media: media, usage: usage, store: store}
}

func (l *Library) List(ctx context.Context) ([]port.StoredMedia, error) {
	return l.media.ListStoredMedia(ctx)
}

// 先删除对象，再清除准备结果：中途失败时准备结果还在，后台仍能看到这首歌并重试；
// 音频已经不在的准备结果也不会被使用。
func (l *Library) Delete(ctx context.Context, key string) error {
	m, ok, err := l.media.StoredMedia(ctx, key)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	used, err := l.usage.TrackInUse(ctx, track.Track{Source: m.Source, ID: m.Link})
	if err != nil {
		return err
	}
	if used {
		return ErrInUse
	}
	objects := []string{AudioObject(m.Key)}
	if m.CoverKey != "" {
		objects = append(objects, string(m.CoverKey))
	}
	for _, object := range objects {
		if err := l.store.Delete(ctx, object); err != nil {
			return err
		}
	}
	return l.media.DeleteStoredMedia(ctx, m.Key)
}
