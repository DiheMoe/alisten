package porttest

import (
	"testing"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/track"
)

func TestUpdateMemberRejectsTextPostgreSQLCannotStore(t *testing.T) {
	store := New()
	m, err := store.CreateMember(t.Context(), []byte("token"), "甲")
	if err != nil {
		t.Fatal(err)
	}
	bad := "a\x00@example.com"
	if _, err := store.UpdateMember(t.Context(), m.ID, nil, nil, &bad); err == nil {
		t.Error("含 NUL 的邮箱应报错")
	}
}

func TestAddPlaylistItemKeepsMilliseconds(t *testing.T) {
	store := New()
	m, err := store.CreateMember(t.Context(), []byte("token"), "甲")
	if err != nil {
		t.Fatal(err)
	}
	list, err := store.CreatePlaylist(t.Context(), m.ID, "歌单", 10)
	if err != nil {
		t.Fatal(err)
	}
	item := port.PlaylistItem{
		Track: track.Track{Source: "netease", ID: "1"},
		Meta:  track.Meta{Title: "歌", Duration: 3*time.Minute + 1234*time.Microsecond + 567*time.Nanosecond},
	}
	if err := store.AddPlaylistItem(t.Context(), m.ID, list.ID, item, 10); err != nil {
		t.Fatal(err)
	}
	items, err := store.PlaylistItems(t.Context(), m.ID, list.ID)
	if err != nil {
		t.Fatal(err)
	}
	if want := 3*time.Minute + time.Millisecond; items[0].Meta.Duration != want {
		t.Errorf("Duration = %v，期望 %v", items[0].Meta.Duration, want)
	}
}

func TestImportPlaylistRejectsWhatPostgreSQLRejects(t *testing.T) {
	store := New()
	m, err := store.CreateMember(t.Context(), []byte("token"), "甲")
	if err != nil {
		t.Fatal(err)
	}
	song := port.PlaylistItem{Track: track.Track{Source: "netease", ID: "1"}, Meta: track.Meta{Title: "歌"}}
	broken := port.PlaylistItem{Track: track.Track{Source: "netease", ID: "2"}, Meta: track.Meta{Title: "坏\x00标题"}}
	for name, items := range map[string][]port.PlaylistItem{"重复的歌": {song, song}, "含 NUL 的标题": {song, broken}} {
		if _, err := store.ImportPlaylist(t.Context(), m.ID, "导入", items, 10); err == nil {
			t.Errorf("%s应报错", name)
		}
	}
	if lists, _ := store.MemberPlaylists(t.Context(), m.ID); len(lists) != 0 {
		t.Errorf("失败之后的歌单 = %#v，期望一个也没有建", lists)
	}
}
