package media

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/app/port/porttest"
	"github.com/dihemoe/alisten/internal/domain/track"
)

type library struct {
	media map[string]port.StoredMedia
	used  map[track.Track]bool
}

func (l *library) ListStoredMedia(context.Context) ([]port.StoredMedia, error) {
	var list []port.StoredMedia
	for _, m := range l.media {
		list = append(list, m)
	}
	return list, nil
}

func (l *library) StoredMedia(_ context.Context, key string) (port.StoredMedia, bool, error) {
	m, ok := l.media[key]
	return m, ok, nil
}

func (l *library) DeleteStoredMedia(_ context.Context, key string) error {
	delete(l.media, key)
	return nil
}

func (l *library) TrackInUse(_ context.Context, t track.Track) (bool, error) {
	return l.used[t], nil
}

func newLibrary(t *testing.T) (*Library, *library, *porttest.MediaStore) {
	t.Helper()
	key := Key(link.ID)
	lib := &library{
		media: map[string]port.StoredMedia{key: {Key: key, Source: track.Link, Link: link.ID, Title: "晴天", Size: 3, CoverKey: track.ObjectKey(CoverObject(key, "jpg")), StoredAt: time.Now()}},
		used:  map[track.Track]bool{},
	}
	store := porttest.NewMediaStore()
	for _, object := range []string{AudioObject(key), CoverObject(key, "jpg"), AudioObject("other")} {
		if err := store.Put(t.Context(), object, strings.NewReader("data"), "application/octet-stream"); err != nil {
			t.Fatal(err)
		}
	}
	return NewLibrary(lib, lib, store), lib, store
}

func TestDeleteRemovesObjectsAndResult(t *testing.T) {
	l, lib, store := newLibrary(t)
	if err := l.Delete(t.Context(), Key(link.ID)); err != nil {
		t.Fatal(err)
	}
	if keys := store.Keys(); !slices.Equal(keys, []string{AudioObject("other")}) {
		t.Errorf("删除之后的对象 = %q，期望音频和封面都已删除", keys)
	}
	if len(lib.media) != 0 {
		t.Error("删除之后准备结果应已清除")
	}
	if err := l.Delete(t.Context(), Key(link.ID)); !errors.Is(err, ErrNotFound) {
		t.Errorf("再次删除 = %v，期望 ErrNotFound", err)
	}
}

func TestDeleteRefusesMediaInUse(t *testing.T) {
	l, lib, store := newLibrary(t)
	stored := lib.media[Key(link.ID)]
	stored.Source = track.Bilibili
	lib.media[Key(link.ID)] = stored
	lib.used[track.Track{Source: track.Bilibili, ID: link.ID}] = true
	if err := l.Delete(t.Context(), Key(link.ID)); !errors.Is(err, ErrInUse) {
		t.Errorf("删除仍被房间引用的歌 = %v，期望 ErrInUse", err)
	}
	if len(store.Keys()) != 3 || len(lib.media) != 1 {
		t.Error("仍被房间引用的歌不应删除任何东西")
	}
}

func TestDeleteKeepsResultWhenObjectsCannotBeDeleted(t *testing.T) {
	l, lib, store := newLibrary(t)
	store.Fail(errors.New("连接被拒绝"))
	if err := l.Delete(t.Context(), Key(link.ID)); err == nil {
		t.Fatal("对象删除失败时应返回错误")
	}
	if len(lib.media) != 1 {
		t.Error("对象删除失败时准备结果应保留，后台还能看到这首歌并重试")
	}
}
