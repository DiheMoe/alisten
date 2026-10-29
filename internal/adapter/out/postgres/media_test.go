package postgres

import (
	"reflect"
	"testing"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/room"
	"github.com/dihemoe/alisten/internal/domain/track"
)

func saveStored(t *testing.T, store *Store, tr track.Track, pageURL, key, cover string, size int64) {
	t.Helper()
	p := port.PreparedTrack{
		Meta:      track.Meta{Title: "标题 " + key, CoverKey: track.ObjectKey(cover), PageURL: pageURL},
		Result:    track.Prepared{Duration: time.Minute, Gain: 1},
		MediaKey:  key,
		MediaSize: size,
	}
	if err := store.SavePreparedTrack(t.Context(), tr, p); err != nil {
		t.Fatal(err)
	}
}

func TestStoredMedia(t *testing.T) {
	store, _ := newStore(t)
	a := track.Track{Source: track.Link, ID: "https://example.com/a.mp3"}
	video := track.Track{Source: track.Bilibili, ID: "BV1xx411c7mD/2"}
	saveStored(t, store, a, a.ID, "aaa", "link/aaa/cover.jpg", 100)
	saveStored(t, store, track.Track{Source: track.Link, ID: "https://example.com/b.mp3"}, "https://example.com/b.mp3", "bbb", "", 200)
	saveStored(t, store, video, "https://www.bilibili.com/video/BV1xx411c7mD?p=2", "ccc", "link/ccc/cover.jpg", 300)
	addTrack(t, store, netease("1"), track.Meta{Title: "晴天"}, track.Prepared{Duration: time.Minute, Gain: 1})

	id := createRoom(t, store, port.NewRoom{Kind: room.Persistent, Name: "大厅"})
	term, snap := takeRoom(t, store, id)
	played := room.Entry{ID: 1, Track: a, By: alice}
	saveRoom(t, store, term, snap, room.RecordPlay{Entry: played, StartedAt: t0, EndedAt: t0.Add(time.Minute)})

	list, err := store.ListStoredMedia(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 || list[0].Key != "ccc" || list[1].Key != "bbb" || list[2].Key != "aaa" {
		t.Fatalf("已存储的媒体 = %+v，期望两首链接和一首 B 站的歌，后存入的在前", list)
	}
	got := list[2]
	if got.Source != track.Link || got.Link != a.ID || got.Title != "标题 aaa" || got.Size != 100 || got.CoverKey != "link/aaa/cover.jpg" ||
		got.StoredAt.IsZero() || got.LastPlayedAt == nil || !got.LastPlayedAt.Equal(t0) {
		t.Errorf("播放过的链接 = %+v", got)
	}
	if b := list[1]; b.Source != track.Link || b.CoverKey != "" || b.LastPlayedAt != nil {
		t.Errorf("没有封面、没有播放过的链接 = %+v", b)
	}
	if c := list[0]; c.Source != track.Bilibili || c.Link != video.ID || c.CoverKey != "link/ccc/cover.jpg" || c.Size != 300 {
		t.Errorf("B 站的歌 = %+v", c)
	}

	for key, want := range map[string]port.StoredMedia{"aaa": got, "ccc": list[0]} {
		if found, ok, err := store.StoredMedia(t.Context(), key); err != nil || !ok || !reflect.DeepEqual(found, want) {
			t.Errorf("StoredMedia(%s) = %+v, %v, %v，期望 %+v", key, found, ok, err, want)
		}
	}
	for _, key := range []string{"aaa", "ccc"} {
		if err := store.DeleteStoredMedia(t.Context(), key); err != nil {
			t.Fatal(err)
		}
		if _, ok, err := store.StoredMedia(t.Context(), key); err != nil || ok {
			t.Errorf("清除之后 StoredMedia(%s) = %v, %v", key, ok, err)
		}
	}
	for _, tr := range []track.Track{a, video} {
		if _, ok, err := store.PreparedTrack(t.Context(), tr); err != nil || ok {
			t.Errorf("清除之后 %v 的准备结果 = %v, %v，期望没有", tr, ok, err)
		}
	}
	if list, err := store.ListStoredMedia(t.Context()); err != nil || len(list) != 1 {
		t.Errorf("清除之后的列表 = %+v, %v", list, err)
	}
}
