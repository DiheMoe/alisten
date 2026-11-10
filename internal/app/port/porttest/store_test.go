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

func TestRecordListenKeepsWhatPostgreSQLKeeps(t *testing.T) {
	store := New()
	m, err := store.CreateMember(t.Context(), []byte("token"), "甲")
	if err != nil {
		t.Fatal(err)
	}
	heard := time.Date(2026, 11, 10, 20, 0, 0, 1234567, time.UTC)
	l := port.Listen{Track: track.Track{Source: "netease", ID: "1"}, Meta: track.Meta{Title: "歌", Duration: 3*time.Minute + 1234*time.Microsecond}, HeardAt: heard}
	if err := store.RecordListen(t.Context(), []string{m.ID}, l); err != nil {
		t.Fatal(err)
	}
	listens, err := store.MemberListens(t.Context(), m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(listens) != 1 || listens[0].Meta.Duration != 3*time.Minute+time.Millisecond || !listens[0].HeardAt.Equal(heard.Truncate(time.Microsecond)) {
		t.Errorf("历史 = %#v，期望时长按毫秒、时间按微秒截断", listens)
	}
	l.Meta.Title = "坏\x00标题"
	if err := store.RecordListen(t.Context(), []string{m.ID}, l); err == nil {
		t.Error("含 NUL 的标题应报错")
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

func TestSetPlaylistShareRejectsDuplicates(t *testing.T) {
	store := New()
	m, err := store.CreateMember(t.Context(), []byte("token"), "甲")
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.CreatePlaylist(t.Context(), m.ID, "一", 10)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreatePlaylist(t.Context(), m.ID, "二", 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetPlaylistShare(t.Context(), m.ID, first.ID, "share"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetPlaylistShare(t.Context(), m.ID, second.ID, "share"); err == nil {
		t.Error("同一个分享 ID 设给第二个歌单时应报错")
	}
	if err := store.SetPlaylistShare(t.Context(), m.ID, first.ID, "share"); err != nil {
		t.Errorf("重新设成自己已有的分享 ID = %v，期望成功", err)
	}
}
