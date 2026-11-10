package media

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/app/port/porttest"
	"github.com/dihemoe/alisten/internal/domain/track"
)

func stored(tr track.Track) port.StoredMedia {
	key := Key(tr.ID)
	return port.StoredMedia{Key: key, Source: tr.Source, Link: tr.ID, Title: "晴天", Size: 3, CoverKey: track.ObjectKey(CoverObject(key, "jpg")), StoredAt: time.Now()}
}

// 每首歌的音频和封面都在对象存储中，另外还有一个不属于它们的对象。
func newLibrary(t *testing.T, used inRooms, media ...port.StoredMedia) (*Library, *porttest.MediaLibrary, *porttest.MediaStore) {
	t.Helper()
	lib := porttest.NewMediaLibrary(media...)
	store := porttest.NewMediaStore()
	objects := []string{AudioObject("other")}
	for _, m := range media {
		objects = append(objects, AudioObject(m.Key), string(m.CoverKey))
	}
	for _, object := range objects {
		if err := store.Put(t.Context(), object, strings.NewReader("data"), "application/octet-stream"); err != nil {
			t.Fatal(err)
		}
	}
	return NewLibrary(lib, used, store), lib, store
}

func storedKeys(t *testing.T, lib *porttest.MediaLibrary) []string {
	t.Helper()
	page, err := lib.ListStoredMedia(t.Context(), port.MediaQuery{Page: 1, Sort: port.MediaByStored})
	if err != nil {
		t.Fatal(err)
	}
	keys := make([]string, len(page.Items))
	for i, m := range page.Items {
		keys[i] = m.Key
	}
	slices.Sort(keys)
	return keys
}

func TestDeleteRemovesObjectsAndResult(t *testing.T) {
	l, lib, store := newLibrary(t, inRooms{}, stored(link))
	if err := l.Delete(t.Context(), Key(link.ID)); err != nil {
		t.Fatal(err)
	}
	if keys := store.Keys(); !slices.Equal(keys, []string{AudioObject("other")}) {
		t.Errorf("删除之后的对象 = %q，期望音频和封面都已删除", keys)
	}
	if keys := storedKeys(t, lib); len(keys) != 0 {
		t.Errorf("删除之后还有准备结果 %q", keys)
	}
	if err := l.Delete(t.Context(), Key(link.ID)); !errors.Is(err, ErrNotFound) {
		t.Errorf("再次删除 = %v，期望 ErrNotFound", err)
	}
}

func TestDeleteRefusesMediaInUse(t *testing.T) {
	video := track.Track{Source: track.Bilibili, ID: link.ID}
	l, lib, store := newLibrary(t, inRooms{video: true}, stored(video))
	if err := l.Delete(t.Context(), Key(link.ID)); !errors.Is(err, ErrInUse) {
		t.Errorf("删除仍被房间引用的歌 = %v，期望 ErrInUse", err)
	}
	if len(store.Keys()) != 3 || len(storedKeys(t, lib)) != 1 {
		t.Error("仍被房间引用的歌不应删除任何东西")
	}
}

func TestDeleteKeepsResultWhenObjectsCannotBeDeleted(t *testing.T) {
	l, lib, store := newLibrary(t, inRooms{}, stored(link))
	store.Fail(errors.New("连接被拒绝"))
	if err := l.Delete(t.Context(), Key(link.ID)); err == nil {
		t.Fatal("对象删除失败时应返回错误")
	}
	if len(storedKeys(t, lib)) != 1 {
		t.Error("对象删除失败时准备结果应保留，后台还能看到这首歌并重试")
	}
}

func TestDeleteManySkipsMediaInUseAndMissing(t *testing.T) {
	other := track.Track{Source: track.Link, ID: "https://example.com/b.mp3"}
	l, lib, store := newLibrary(t, inRooms{bilibili: true}, stored(link), stored(bilibili), stored(other))
	deleted, inUse, err := l.DeleteMany(t.Context(), []string{Key(bilibili.ID), Key(link.ID), Key("https://example.com/missing.mp3"), Key(other.ID)})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{Key(link.ID), Key(other.ID)}; !slices.Equal(deleted, want) {
		t.Errorf("删掉的 = %q，期望 %q", deleted, want)
	}
	if want := []string{Key(bilibili.ID)}; !slices.Equal(inUse, want) {
		t.Errorf("正在使用的 = %q，期望 %q", inUse, want)
	}
	if keys := storedKeys(t, lib); !slices.Equal(keys, []string{Key(bilibili.ID)}) {
		t.Errorf("剩下的准备结果 = %q，期望只剩正在使用的", keys)
	}
	if keys, want := store.Keys(), []string{AudioObject("other"), AudioObject(Key(bilibili.ID)), CoverObject(Key(bilibili.ID), "jpg")}; !slices.Equal(keys, slices.Sorted(slices.Values(want))) {
		t.Errorf("剩下的对象 = %q，期望 %q", keys, want)
	}
}

func TestDeleteManyDeletesAll(t *testing.T) {
	l, lib, _ := newLibrary(t, inRooms{}, stored(link), stored(bilibili))
	deleted, inUse, err := l.DeleteMany(t.Context(), []string{Key(link.ID), Key(bilibili.ID)})
	if err != nil || !slices.Equal(deleted, []string{Key(link.ID), Key(bilibili.ID)}) || len(inUse) != 0 {
		t.Errorf("DeleteMany = %q, %q, %v，期望两首都删掉", deleted, inUse, err)
	}
	if keys := storedKeys(t, lib); len(keys) != 0 {
		t.Errorf("删除之后还有准备结果 %q", keys)
	}
}

func TestDeleteManyStopsAtOtherErrors(t *testing.T) {
	l, lib, store := newLibrary(t, inRooms{}, stored(link), stored(bilibili))
	store.Fail(errors.New("连接被拒绝"))
	if _, _, err := l.DeleteMany(t.Context(), []string{Key(link.ID), Key(bilibili.ID)}); err == nil {
		t.Fatal("对象删除失败时应返回错误")
	}
	if len(storedKeys(t, lib)) != 2 {
		t.Error("对象删除失败时准备结果应保留")
	}
}
