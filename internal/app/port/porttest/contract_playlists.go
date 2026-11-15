package porttest

import (
	"errors"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/member"
	"github.com/dihemoe/alisten/internal/domain/playlist"
	"github.com/dihemoe/alisten/internal/domain/track"
)

var (
	sunnyItem = port.PlaylistItem{Track: track.Track{Source: track.NetEase, ID: "186016"}, Meta: track.Meta{Title: "晴天", Artist: "周杰伦", Album: "叶惠美", CoverURL: "https://p1.music.126.net/a.jpg", PageURL: "https://music.163.com/#/song?id=186016", Duration: 269 * time.Second}}
	clipItem  = port.PlaylistItem{Track: track.Track{Source: track.Link, ID: "https://example.com/a.mp3"}, Meta: track.Meta{Title: "a.mp3", CoverKey: "covers/abc", PageURL: "https://example.com/a.mp3", Duration: time.Minute}}
)

func newAccount(t *testing.T, s ContractStore, username string) member.Member {
	t.Helper()
	m := newGuest(t, s, username, "游客-"+username)
	if err := s.Register(t.Context(), m.ID, username, "hash"); err != nil {
		t.Fatal(err)
	}
	return m
}

func favoritesOf(t *testing.T, s ContractStore, ownerID string) string {
	t.Helper()
	lists, err := s.MemberPlaylists(t.Context(), ownerID)
	if err != nil || len(lists) == 0 || !lists[0].Favorites {
		t.Fatalf("歌单 = %#v, %v，期望第一个是“我喜欢”", lists, err)
	}
	return lists[0].ID
}

func songItem(id string) port.PlaylistItem {
	return port.PlaylistItem{Track: track.Track{Source: track.NetEase, ID: id}, Meta: track.Meta{Title: "歌" + id, Artist: "艺人", CoverURL: "https://example.com/" + id + ".jpg", PageURL: "https://music.163.com/#/song?id=" + id, Duration: time.Minute}}
}

func displayIDs(t *testing.T, s ContractStore, ownerID, id string) []string {
	t.Helper()
	items, err := s.PlaylistItems(t.Context(), ownerID, id)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, len(items))
	for i, item := range items {
		ids[i] = item.Track.ID
	}
	return ids
}

// 依次加入 ids，显示顺序与 ids 相反。
func filled(t *testing.T, s ContractStore, ownerID, name string, ids ...string) string {
	t.Helper()
	p, err := s.CreatePlaylist(t.Context(), ownerID, name, 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if err := s.AddPlaylistItem(t.Context(), ownerID, p.ID, songItem(id), 1000); err != nil {
			t.Fatal(err)
		}
	}
	return p.ID
}

func updatedAt(t *testing.T, s ContractStore, ownerID, id string) time.Time {
	t.Helper()
	lists, err := s.MemberPlaylists(t.Context(), ownerID)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range lists {
		if l.ID == id {
			return l.UpdatedAt
		}
	}
	t.Fatalf("没有歌单 %s", id)
	return time.Time{}
}

func trackList(ids ...string) []track.Track {
	out := make([]track.Track, len(ids))
	for i, id := range ids {
		out[i] = track.Track{Source: track.NetEase, ID: id}
	}
	return out
}

func runPlaylistsContract(t *testing.T, open func(t *testing.T) ContractStore) {
	t.Run("注册时建出空的“我喜欢”", func(t *testing.T) {
		s := open(t)
		alice := newAccount(t, s, "alice")
		lists, err := s.MemberPlaylists(t.Context(), alice.ID)
		if err != nil || len(lists) != 1 || !lists[0].Favorites || lists[0].Name != "我喜欢" || lists[0].TrackCount != 0 {
			t.Fatalf("注册之后的歌单 = %#v, %v，期望只有一个空的“我喜欢”", lists, err)
		}
		guest := newGuest(t, s, "guest", "游客-1a2b")
		if lists, err := s.MemberPlaylists(t.Context(), guest.ID); err != nil || len(lists) != 0 {
			t.Errorf("游客的歌单 = %#v, %v，期望没有", lists, err)
		}
	})

	t.Run("新建、改名和删除歌单", func(t *testing.T) {
		s := open(t)
		alice := newAccount(t, s, "alice")
		bob := newAccount(t, s, "bob")
		favorites := favoritesOf(t, s, alice.ID)
		commute, err := s.CreatePlaylist(t.Context(), alice.ID, "通勤", 50)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.RenamePlaylist(t.Context(), alice.ID, commute.ID, "上班路上"); err != nil {
			t.Fatal(err)
		}
		lists, err := s.MemberPlaylists(t.Context(), alice.ID)
		if err != nil || len(lists) != 2 || lists[1].ID != commute.ID || lists[1].Name != "上班路上" || lists[1].TrackCount != 0 || lists[1].Cover != nil {
			t.Errorf("歌单 = %#v, %v，期望“我喜欢”之后是改名后的 %s", lists, err, commute.ID)
		}
		if err := s.RenamePlaylist(t.Context(), alice.ID, favorites, "别的名字"); !errors.Is(err, port.ErrFavoritesFixed) {
			t.Errorf("“我喜欢”改名 = %v，期望 ErrFavoritesFixed", err)
		}
		if err := s.DeletePlaylist(t.Context(), alice.ID, favorites); !errors.Is(err, port.ErrFavoritesFixed) {
			t.Errorf("删除“我喜欢” = %v，期望 ErrFavoritesFixed", err)
		}
		for _, id := range []string{commute.ID, missingID} {
			if err := s.RenamePlaylist(t.Context(), bob.ID, id, "x"); !errors.Is(err, port.ErrPlaylistNotFound) {
				t.Errorf("别人或不存在的歌单 %s 改名 = %v，期望 ErrPlaylistNotFound", id, err)
			}
			if err := s.DeletePlaylist(t.Context(), bob.ID, id); !errors.Is(err, port.ErrPlaylistNotFound) {
				t.Errorf("删除别人或不存在的歌单 %s = %v，期望 ErrPlaylistNotFound", id, err)
			}
			if _, err := s.PlaylistItems(t.Context(), bob.ID, id); !errors.Is(err, port.ErrPlaylistNotFound) {
				t.Errorf("读取别人或不存在的歌单 %s = %v，期望 ErrPlaylistNotFound", id, err)
			}
		}
		if err := s.DeletePlaylist(t.Context(), alice.ID, commute.ID); err != nil {
			t.Fatal(err)
		}
		if lists, err := s.MemberPlaylists(t.Context(), alice.ID); err != nil || len(lists) != 1 {
			t.Errorf("删除之后的歌单 = %#v, %v，期望只剩“我喜欢”", lists, err)
		}
	})

	t.Run("歌单上限把“我喜欢”算在内", func(t *testing.T) {
		s := open(t)
		alice := newAccount(t, s, "alice")
		if _, err := s.CreatePlaylist(t.Context(), alice.ID, "一", 2); err != nil {
			t.Fatal(err)
		}
		if _, err := s.CreatePlaylist(t.Context(), alice.ID, "二", 2); !errors.Is(err, port.ErrPlaylistLimit) {
			t.Errorf("超过上限时 = %v，期望 ErrPlaylistLimit", err)
		}
	})

	t.Run("加入和移出曲目", func(t *testing.T) {
		s := open(t)
		alice := newAccount(t, s, "alice")
		bob := newAccount(t, s, "bob")
		favorites := favoritesOf(t, s, alice.ID)
		for _, item := range []port.PlaylistItem{sunnyItem, clipItem, sunnyItem} {
			if err := s.AddPlaylistItem(t.Context(), alice.ID, favorites, item, 2); err != nil {
				t.Fatal(err)
			}
		}
		items, err := s.PlaylistItems(t.Context(), alice.ID, favorites)
		if want := []port.PlaylistItem{clipItem, sunnyItem}; err != nil || !slices.Equal(items, want) {
			t.Errorf("曲目 = %#v, %v，期望新加入的在前、重复加入不变 %#v", items, err, want)
		}
		if lists, _ := s.MemberPlaylists(t.Context(), alice.ID); lists[0].TrackCount != 2 {
			t.Errorf("曲目数 = %d，期望 2", lists[0].TrackCount)
		}
		third := port.PlaylistItem{Track: track.Track{Source: track.NetEase, ID: "185811"}, Meta: track.Meta{Title: "七里香", PageURL: "https://music.163.com/#/song?id=185811"}}
		if err := s.AddPlaylistItem(t.Context(), alice.ID, favorites, third, 2); !errors.Is(err, port.ErrPlaylistLimit) {
			t.Errorf("歌单满了时 = %v，期望 ErrPlaylistLimit", err)
		}
		if err := s.AddPlaylistItem(t.Context(), bob.ID, favorites, third, 2); !errors.Is(err, port.ErrPlaylistNotFound) {
			t.Errorf("加入别人的歌单 = %v，期望 ErrPlaylistNotFound", err)
		}
		for range 2 {
			if err := s.RemovePlaylistItems(t.Context(), alice.ID, favorites, []track.Track{sunnyItem.Track}); err != nil {
				t.Fatal(err)
			}
		}
		if items, err := s.PlaylistItems(t.Context(), alice.ID, favorites); err != nil || !slices.Equal(items, []port.PlaylistItem{clipItem}) {
			t.Errorf("移除之后的曲目 = %#v, %v", items, err)
		}
		if err := s.RemovePlaylistItems(t.Context(), bob.ID, favorites, []track.Track{clipItem.Track}); !errors.Is(err, port.ErrPlaylistNotFound) {
			t.Errorf("从别人的歌单移除 = %v，期望 ErrPlaylistNotFound", err)
		}
	})

	t.Run("只有改名和曲目增减才推进更新时间", func(t *testing.T) {
		s := open(t)
		alice := newAccount(t, s, "alice")
		created, err := s.CreatePlaylist(t.Context(), alice.ID, "通勤", 50)
		if err != nil || created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() || created.Cover != nil {
			t.Fatalf("新建 = %#v, %v，期望带创建和更新时间、没有封面", created, err)
		}
		read := func() port.PlaylistRecord {
			t.Helper()
			lists, err := s.MemberPlaylists(t.Context(), alice.ID)
			if err != nil || len(lists) != 2 {
				t.Fatalf("歌单 = %#v, %v", lists, err)
			}
			return lists[1]
		}
		before := read()
		if !before.CreatedAt.Equal(created.CreatedAt) || !before.UpdatedAt.Equal(created.UpdatedAt) {
			t.Errorf("读出的时间 %v/%v，期望与新建时一致 %v/%v", before.CreatedAt, before.UpdatedAt, created.CreatedAt, created.UpdatedAt)
		}
		steps := []struct {
			name    string
			do      func() error
			changed bool
		}{
			{"改名", func() error { return s.RenamePlaylist(t.Context(), alice.ID, created.ID, "上班路上") }, true},
			{"加入新曲目", func() error { return s.AddPlaylistItem(t.Context(), alice.ID, created.ID, sunnyItem, 10) }, true},
			{"重复加入", func() error { return s.AddPlaylistItem(t.Context(), alice.ID, created.ID, sunnyItem, 10) }, false},
			{"移除曲目", func() error {
				return s.RemovePlaylistItems(t.Context(), alice.ID, created.ID, []track.Track{sunnyItem.Track})
			}, true},
			{"移除不存在的曲目", func() error {
				return s.RemovePlaylistItems(t.Context(), alice.ID, created.ID, []track.Track{sunnyItem.Track})
			}, false},
		}
		for _, step := range steps {
			if err := step.do(); err != nil {
				t.Fatalf("%s：%v", step.name, err)
			}
			after := read()
			if got := after.UpdatedAt.After(before.UpdatedAt); got != step.changed {
				t.Errorf("%s之后更新时间推进 = %v，期望 %v", step.name, got, step.changed)
			}
			if !after.CreatedAt.Equal(before.CreatedAt) {
				t.Errorf("%s之后创建时间变了：%v -> %v", step.name, before.CreatedAt, after.CreatedAt)
			}
			before = after
		}
	})

	t.Run("封面取排在最前、有封面的歌", func(t *testing.T) {
		s := open(t)
		alice := newAccount(t, s, "alice")
		favorites := favoritesOf(t, s, alice.ID)
		add := func(source track.Source, id, coverURL string, coverKey track.ObjectKey) {
			t.Helper()
			item := port.PlaylistItem{
				Track: track.Track{Source: source, ID: id},
				Meta:  track.Meta{Title: id, CoverURL: coverURL, CoverKey: coverKey, PageURL: "https://example.com/" + id},
			}
			if err := s.AddPlaylistItem(t.Context(), alice.ID, favorites, item, 100); err != nil {
				t.Fatal(err)
			}
		}
		for i := range 3 {
			add(track.NetEase, strconv.Itoa(i), "https://img.example/"+strconv.Itoa(i)+".jpg", "")
		}
		add(track.Link, "https://example.com/a.mp3", "", "covers/a.jpg")
		add(track.NetEase, "no-cover", "", "")
		lists, err := s.MemberPlaylists(t.Context(), alice.ID)
		if err != nil || len(lists) != 1 {
			t.Fatalf("歌单 = %#v, %v", lists, err)
		}
		if got, want := lists[0].Cover, (port.Cover{Key: "covers/a.jpg"}); got == nil || *got != want {
			t.Errorf("封面 = %+v，期望跳过没有封面的歌，取排在最前的 %+v", got, want)
		}
	})

	t.Run("批量移出时忽略不在歌单里的歌", func(t *testing.T) {
		s := open(t)
		alice := newAccount(t, s, "alice")
		list := filled(t, s, alice.ID, "通勤", "1", "2", "3", "4")
		if err := s.RemovePlaylistItems(t.Context(), alice.ID, list, trackList("2", "9", "4", "2")); err != nil {
			t.Fatal(err)
		}
		if got := displayIDs(t, s, alice.ID, list); !slices.Equal(got, []string{"3", "1"}) {
			t.Errorf("批量移出之后 = %v，期望 [3 1]", got)
		}
	})

	t.Run("复制曲目到另一个歌单", func(t *testing.T) {
		s := open(t)
		alice := newAccount(t, s, "alice")
		bob := newAccount(t, s, "bob")
		from := filled(t, s, alice.ID, "来源", "1", "2", "3", "4")
		to := filled(t, s, alice.ID, "目标", "3", "9")
		before := updatedAt(t, s, alice.ID, to)

		n, err := s.CopyPlaylistItems(t.Context(), alice.ID, from, to, trackList("4", "x", "3", "1", "4"), 1000)
		if err != nil || n != 2 {
			t.Fatalf("复制 = %d, %v，期望跳过已有的、不存在的和重复的，复制 2 首", n, err)
		}
		if got := displayIDs(t, s, alice.ID, to); !slices.Equal(got, []string{"4", "1", "9", "3"}) {
			t.Errorf("复制之后 = %v，期望新复制的在最前且保持给定的先后 [4 1 9 3]", got)
		}
		items, _ := s.PlaylistItems(t.Context(), alice.ID, to)
		if want := songItem("4"); items[0] != want {
			t.Errorf("复制的曲目 = %#v，期望带着元数据快照 %#v", items[0], want)
		}
		if !updatedAt(t, s, alice.ID, to).After(before) {
			t.Error("复制之后目标歌单的更新时间没有推进")
		}

		if err := s.AddPlaylistItem(t.Context(), alice.ID, to, songItem("5"), 1000); err != nil {
			t.Fatal(err)
		}
		if got := displayIDs(t, s, alice.ID, to); got[0] != "5" {
			t.Errorf("复制之后新加入的歌 = %v，期望仍在最前", got)
		}

		before = updatedAt(t, s, alice.ID, to)
		if n, err := s.CopyPlaylistItems(t.Context(), alice.ID, from, to, trackList("1", "x"), 1000); err != nil || n != 0 {
			t.Errorf("没有可复制的歌 = %d, %v，期望 0", n, err)
		}
		if !updatedAt(t, s, alice.ID, to).Equal(before) {
			t.Error("没有复制任何歌，更新时间却变了")
		}

		if n, err := s.CopyPlaylistItems(t.Context(), alice.ID, from, to, trackList("2"), 5); !errors.Is(err, port.ErrPlaylistLimit) || n != 0 {
			t.Errorf("装不下时 = %d, %v，期望 ErrPlaylistLimit", n, err)
		}
		if n, err := s.CopyPlaylistItems(t.Context(), alice.ID, from, to, trackList("2"), 6); err != nil || n != 1 {
			t.Errorf("正好装满时 = %d, %v，期望复制 1 首", n, err)
		}

		full := filled(t, s, alice.ID, "满", "1", "2")
		if _, err := s.CopyPlaylistItems(t.Context(), alice.ID, from, full, trackList("3", "4", "1"), 3); !errors.Is(err, port.ErrPlaylistLimit) {
			t.Fatalf("超限 = %v，期望 ErrPlaylistLimit", err)
		}
		if got := displayIDs(t, s, alice.ID, full); !slices.Equal(got, []string{"2", "1"}) {
			t.Errorf("整批拒绝之后 = %v，期望一首也没有复制", got)
		}

		other := filled(t, s, bob.ID, "别人的", "7")
		for name, call := range map[string]func() (int, error){
			"来源是别人的": func() (int, error) {
				return s.CopyPlaylistItems(t.Context(), alice.ID, other, to, trackList("7"), 1000)
			},
			"目标是别人的": func() (int, error) {
				return s.CopyPlaylistItems(t.Context(), alice.ID, from, other, trackList("1"), 1000)
			},
			"来源不存在": func() (int, error) {
				return s.CopyPlaylistItems(t.Context(), alice.ID, missingID, to, trackList("1"), 1000)
			},
			"目标不存在": func() (int, error) {
				return s.CopyPlaylistItems(t.Context(), alice.ID, from, missingID, trackList("1"), 1000)
			},
		} {
			if n, err := call(); !errors.Is(err, port.ErrPlaylistNotFound) || n != 0 {
				t.Errorf("%s = %d, %v，期望 ErrPlaylistNotFound", name, n, err)
			}
		}
	})

	t.Run("查询曲目所在的歌单只看本人的", func(t *testing.T) {
		s := open(t)
		alice := newAccount(t, s, "alice")
		bob := newAccount(t, s, "bob")
		favorites := favoritesOf(t, s, alice.ID)
		commute := filled(t, s, alice.ID, "通勤", "1")
		filled(t, s, alice.ID, "空", "2")
		bobFavorites := favoritesOf(t, s, bob.ID)
		if err := s.AddPlaylistItem(t.Context(), alice.ID, favorites, songItem("1"), 10); err != nil {
			t.Fatal(err)
		}
		if err := s.AddPlaylistItem(t.Context(), bob.ID, bobFavorites, songItem("1"), 10); err != nil {
			t.Fatal(err)
		}
		// 端口没有规定 TrackPlaylists 的顺序，所以排序之后再比较。
		ids, err := s.TrackPlaylists(t.Context(), alice.ID, songItem("1").Track)
		want := []string{favorites, commute}
		slices.Sort(ids)
		slices.Sort(want)
		if err != nil || !slices.Equal(ids, want) {
			t.Errorf("所在的歌单 = %v, %v，期望只有本人的 %v", ids, err, want)
		}
		ids, err = s.TrackPlaylists(t.Context(), alice.ID, songItem("3").Track)
		if err != nil || len(ids) != 0 {
			t.Errorf("不在任何歌单里的歌 = %#v, %v，期望没有歌单", ids, err)
		}
	})

	t.Run("调整曲目的顺序", func(t *testing.T) {
		s := open(t)
		alice := newAccount(t, s, "alice")
		bob := newAccount(t, s, "bob")
		list := filled(t, s, alice.ID, "通勤", "5", "4", "3", "2", "1")
		at := func(id string) *track.Track { return &trackList(id)[0] }
		steps := []struct {
			name   string
			moved  []track.Track
			before *track.Track
			want   []string
		}{
			{"移到最前", trackList("4"), at("1"), []string{"4", "1", "2", "3", "5"}},
			{"移到最后", trackList("4"), nil, []string{"1", "2", "3", "5", "4"}},
			{"移到中间", trackList("1"), at("5"), []string{"2", "3", "1", "5", "4"}},
			{"多首保持给定的先后", trackList("4", "2"), at("3"), []string{"4", "2", "3", "1", "5"}},
			{"忽略不在歌单里的歌", trackList("x", "5"), at("2"), []string{"4", "5", "2", "3", "1"}},
		}
		for _, step := range steps {
			if err := s.MovePlaylistItems(t.Context(), alice.ID, list, step.moved, step.before); err != nil {
				t.Fatalf("%s：%v", step.name, err)
			}
			if got := displayIDs(t, s, alice.ID, list); !slices.Equal(got, step.want) {
				t.Errorf("%s之后 = %v，期望 %v", step.name, got, step.want)
			}
		}

		if err := s.AddPlaylistItem(t.Context(), alice.ID, list, songItem("6"), 1000); err != nil {
			t.Fatal(err)
		}
		if got := displayIDs(t, s, alice.ID, list); got[0] != "6" {
			t.Errorf("移动之后新加入的歌 = %v，期望仍在最前", got)
		}

		errs := []struct {
			name   string
			moved  []track.Track
			before *track.Track
			want   error
		}{
			{"before 是被移动的歌", trackList("1", "2"), at("2"), playlist.ErrBeforeMoved},
			{"before 不在歌单里", trackList("1"), at("x"), playlist.ErrBeforeMissing},
		}
		for _, e := range errs {
			if err := s.MovePlaylistItems(t.Context(), alice.ID, list, e.moved, e.before); !errors.Is(err, e.want) {
				t.Errorf("%s = %v，期望 %v", e.name, err, e.want)
			}
		}
		if err := s.MovePlaylistItems(t.Context(), bob.ID, list, trackList("1"), nil); !errors.Is(err, port.ErrPlaylistNotFound) {
			t.Errorf("移动别人的歌单 = %v，期望 ErrPlaylistNotFound", err)
		}
		if err := s.MovePlaylistItems(t.Context(), alice.ID, missingID, trackList("1"), nil); !errors.Is(err, port.ErrPlaylistNotFound) {
			t.Errorf("移动不存在的歌单 = %v，期望 ErrPlaylistNotFound", err)
		}
	})

	t.Run("顺序变了才推进更新时间", func(t *testing.T) {
		s := open(t)
		alice := newAccount(t, s, "alice")
		list := filled(t, s, alice.ID, "通勤", "3", "2", "1")
		before := updatedAt(t, s, alice.ID, list)
		if err := s.MovePlaylistItems(t.Context(), alice.ID, list, trackList("x"), nil); err != nil {
			t.Fatal(err)
		}
		if !updatedAt(t, s, alice.ID, list).Equal(before) {
			t.Error("没有歌被移动，更新时间却变了")
		}
		if err := s.MovePlaylistItems(t.Context(), alice.ID, list, trackList("1"), nil); err != nil {
			t.Fatal(err)
		}
		if !updatedAt(t, s, alice.ID, list).After(before) {
			t.Error("移动之后更新时间没有推进")
		}
	})

	t.Run("导入的歌单与来源同序", func(t *testing.T) {
		s := open(t)
		alice := newAccount(t, s, "alice")
		items := []port.PlaylistItem{songItem("3"), clipItem, songItem("1")}
		imported, err := s.ImportPlaylist(t.Context(), alice.ID, "导入的歌单", items, 50)
		if err != nil {
			t.Fatal(err)
		}
		if got, err := s.PlaylistItems(t.Context(), alice.ID, imported.ID); err != nil || !slices.Equal(got, items) {
			t.Errorf("导入之后的曲目 = %#v, %v，期望与来源同序 %#v", got, err, items)
		}
		lists, err := s.MemberPlaylists(t.Context(), alice.ID)
		if err != nil || len(lists) != 2 {
			t.Fatalf("歌单 = %#v, %v，期望“我喜欢”和导入的歌单", lists, err)
		}
		read := lists[1]
		if imported.ID != read.ID || imported.Name != "导入的歌单" || imported.Favorites || imported.TrackCount != 3 ||
			!imported.CreatedAt.Equal(read.CreatedAt) || !imported.UpdatedAt.Equal(read.UpdatedAt) {
			t.Errorf("导入返回 %#v，期望与读出的 %#v 一致、共 3 首", imported, read)
		}
		if want := (port.Cover{URL: songItem("3").Meta.CoverURL}); imported.Cover == nil || *imported.Cover != want || read.Cover == nil || *read.Cover != want {
			t.Errorf("封面 = %+v / %+v，期望排在最前的 %+v", imported.Cover, read.Cover, want)
		}
		if err := s.AddPlaylistItem(t.Context(), alice.ID, imported.ID, songItem("9"), 1000); err != nil {
			t.Fatal(err)
		}
		if got := displayIDs(t, s, alice.ID, imported.ID); !slices.Equal(got, []string{"9", "3", clipItem.Track.ID, "1"}) {
			t.Errorf("导入之后再加入 = %v，期望新加入的排在最前", got)
		}
	})

	t.Run("导入空列表建出空歌单", func(t *testing.T) {
		s := open(t)
		alice := newAccount(t, s, "alice")
		imported, err := s.ImportPlaylist(t.Context(), alice.ID, "空的", nil, 50)
		if err != nil || imported.Name != "空的" || imported.TrackCount != 0 || imported.Cover != nil {
			t.Fatalf("导入空列表 = %#v, %v，期望建出一个没有曲目的歌单", imported, err)
		}
		if got, err := s.PlaylistItems(t.Context(), alice.ID, imported.ID); err != nil || len(got) != 0 {
			t.Errorf("曲目 = %#v, %v，期望为空", got, err)
		}
	})

	t.Run("歌单数到了上限时不导入", func(t *testing.T) {
		s := open(t)
		alice := newAccount(t, s, "alice")
		if _, err := s.ImportPlaylist(t.Context(), alice.ID, "超限", []port.PlaylistItem{songItem("1"), songItem("2")}, 1); !errors.Is(err, port.ErrPlaylistLimit) {
			t.Errorf("歌单数到了上限 = %v，期望 ErrPlaylistLimit", err)
		}
		if lists, err := s.MemberPlaylists(t.Context(), alice.ID); err != nil || len(lists) != 1 {
			t.Errorf("失败之后的歌单 = %#v, %v，期望只剩“我喜欢”", lists, err)
		}
	})

	t.Run("分享和停止分享歌单", func(t *testing.T) {
		s := open(t)
		alice := newAccount(t, s, "alice")
		bob := newAccount(t, s, "bob")
		hash := "0bc83cb571cd1c50ba6f3e8a78ef1346"
		if _, err := s.UpdateMember(t.Context(), alice.ID, nil, &hash, nil); err != nil {
			t.Fatal(err)
		}
		favorites := favoritesOf(t, s, alice.ID)
		if err := s.AddPlaylistItem(t.Context(), alice.ID, favorites, sunnyItem, 1000); err != nil {
			t.Fatal(err)
		}
		if err := s.AddPlaylistItem(t.Context(), alice.ID, favorites, clipItem, 1000); err != nil {
			t.Fatal(err)
		}
		before := updatedAt(t, s, alice.ID, favorites)
		if err := s.SetPlaylistShare(t.Context(), alice.ID, favorites, "share-1"); err != nil {
			t.Fatal(err)
		}
		if lists, err := s.MemberPlaylists(t.Context(), alice.ID); err != nil || lists[0].ShareID != "share-1" {
			t.Errorf("歌单 = %#v, %v，期望带上分享 ID", lists, err)
		}
		if !updatedAt(t, s, alice.ID, favorites).Equal(before) {
			t.Error("分享改变了歌单的更新时间")
		}
		shared, err := s.SharedPlaylist(t.Context(), "share-1")
		want := port.SharedPlaylist{Name: "我喜欢", Favorites: true, OwnerName: alice.Name, OwnerAvatarHash: hash, Items: []port.PlaylistItem{clipItem, sunnyItem}}
		if err != nil || shared.Name != want.Name || shared.Favorites != want.Favorites || shared.OwnerName != want.OwnerName ||
			shared.OwnerAvatarHash != want.OwnerAvatarHash || !slices.Equal(shared.Items, want.Items) {
			t.Errorf("分享的歌单 = %#v, %v，期望 %#v", shared, err, want)
		}

		if err := s.SetPlaylistShare(t.Context(), alice.ID, favorites, "share-2"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.SharedPlaylist(t.Context(), "share-1"); !errors.Is(err, port.ErrShareNotFound) {
			t.Errorf("重新生成之后的旧链接 = %v，期望 ErrShareNotFound", err)
		}
		if shared, err := s.SharedPlaylist(t.Context(), "share-2"); err != nil || len(shared.Items) != 2 {
			t.Errorf("新链接 = %#v, %v", shared, err)
		}
		for _, id := range []string{favorites, missingID} {
			if err := s.SetPlaylistShare(t.Context(), bob.ID, id, "share-3"); !errors.Is(err, port.ErrPlaylistNotFound) {
				t.Errorf("分享别人或不存在的歌单 %s = %v，期望 ErrPlaylistNotFound", id, err)
			}
			if err := s.SetPlaylistShare(t.Context(), bob.ID, id, ""); !errors.Is(err, port.ErrPlaylistNotFound) {
				t.Errorf("停止分享别人或不存在的歌单 %s = %v，期望 ErrPlaylistNotFound", id, err)
			}
		}

		for range 2 {
			if err := s.SetPlaylistShare(t.Context(), alice.ID, favorites, ""); err != nil {
				t.Fatalf("停止分享 = %v，期望重复停止也成功", err)
			}
		}
		if _, err := s.SharedPlaylist(t.Context(), "share-2"); !errors.Is(err, port.ErrShareNotFound) {
			t.Errorf("停止分享之后 = %v，期望 ErrShareNotFound", err)
		}
		if lists, err := s.MemberPlaylists(t.Context(), alice.ID); err != nil || lists[0].ShareID != "" {
			t.Errorf("停止分享之后的歌单 = %#v, %v，期望没有分享 ID", lists, err)
		}
	})

	t.Run("分享 ID 不能给第二个歌单", func(t *testing.T) {
		s := open(t)
		alice := newAccount(t, s, "alice")
		bob := newAccount(t, s, "bob")
		favorites := favoritesOf(t, s, alice.ID)
		if err := s.SetPlaylistShare(t.Context(), alice.ID, favorites, "share-1"); err != nil {
			t.Fatal(err)
		}
		if err := s.SetPlaylistShare(t.Context(), bob.ID, favoritesOf(t, s, bob.ID), "share-1"); err == nil {
			t.Error("同一个分享 ID 设给第二个歌单时应报错")
		}
		if err := s.SetPlaylistShare(t.Context(), alice.ID, favorites, "share-1"); err != nil {
			t.Errorf("重新设成自己已有的分享 ID = %v，期望成功", err)
		}
		if shared, err := s.SharedPlaylist(t.Context(), "share-1"); err != nil || shared.OwnerName != alice.Name {
			t.Errorf("分享 = %#v, %v，期望仍是第一个歌单", shared, err)
		}
	})

	t.Run("删除的歌单不再分享", func(t *testing.T) {
		s := open(t)
		alice := newAccount(t, s, "alice")
		list := filled(t, s, alice.ID, "通勤", "1")
		if err := s.SetPlaylistShare(t.Context(), alice.ID, list, "share-1"); err != nil {
			t.Fatal(err)
		}
		if err := s.DeletePlaylist(t.Context(), alice.ID, list); err != nil {
			t.Fatal(err)
		}
		if _, err := s.SharedPlaylist(t.Context(), "share-1"); !errors.Is(err, port.ErrShareNotFound) {
			t.Errorf("删除歌单之后 = %v，期望 ErrShareNotFound", err)
		}
	})

	t.Run("复制分享的歌单", func(t *testing.T) {
		s := open(t)
		alice := newAccount(t, s, "alice")
		bob := newAccount(t, s, "bob")
		source := filled(t, s, alice.ID, "通勤", "1", "2", "3")
		if err := s.MovePlaylistItems(t.Context(), alice.ID, source, trackList("1"), &trackList("3")[0]); err != nil {
			t.Fatal(err)
		}
		if err := s.SetPlaylistShare(t.Context(), alice.ID, source, "share-1"); err != nil {
			t.Fatal(err)
		}
		original := displayIDs(t, s, alice.ID, source)

		copied, err := s.CopySharedPlaylist(t.Context(), bob.ID, "share-1", "抄来的", 50)
		if err != nil {
			t.Fatal(err)
		}
		if want := (port.Cover{URL: songItem("1").Meta.CoverURL}); copied.Name != "抄来的" || copied.Favorites || copied.TrackCount != 3 ||
			copied.ShareID != "" || copied.Cover == nil || *copied.Cover != want {
			t.Errorf("复制返回 %#v，期望 3 首、封面是排在最前的 %+v、没有分享", copied, want)
		}
		lists, err := s.MemberPlaylists(t.Context(), bob.ID)
		if err != nil || len(lists) != 2 {
			t.Fatalf("歌单 = %#v, %v，期望“我喜欢”和复制的歌单", lists, err)
		}
		if read := lists[1]; read.ID != copied.ID || read.Name != copied.Name || read.TrackCount != 3 || read.ShareID != "" ||
			!read.CreatedAt.Equal(copied.CreatedAt) || !read.UpdatedAt.Equal(copied.UpdatedAt) {
			t.Errorf("读出的 %#v，期望与复制返回的 %#v 一致", read, copied)
		}
		items, err := s.PlaylistItems(t.Context(), bob.ID, copied.ID)
		sourceItems, _ := s.PlaylistItems(t.Context(), alice.ID, source)
		if err != nil || !slices.Equal(items, sourceItems) {
			t.Errorf("复制的曲目 = %#v, %v，期望与原歌单相同 %#v", items, err, sourceItems)
		}
		if err := s.AddPlaylistItem(t.Context(), bob.ID, copied.ID, songItem("9"), 1000); err != nil {
			t.Fatal(err)
		}
		if got := displayIDs(t, s, bob.ID, copied.ID); !slices.Equal(got, append([]string{"9"}, original...)) {
			t.Errorf("复制之后再加入 = %v，期望新加入的排在最前、其余保持 %v", got, original)
		}
		if got := displayIDs(t, s, alice.ID, source); !slices.Equal(got, original) {
			t.Errorf("原歌单 = %v，期望不变 %v", got, original)
		}

		empty := filled(t, s, alice.ID, "空的")
		if err := s.SetPlaylistShare(t.Context(), alice.ID, empty, "share-2"); err != nil {
			t.Fatal(err)
		}
		if copied, err := s.CopySharedPlaylist(t.Context(), bob.ID, "share-2", "空的", 50); err != nil || copied.TrackCount != 0 || copied.Cover != nil {
			t.Errorf("复制空歌单 = %#v, %v，期望建出一个没有曲目的歌单", copied, err)
		}

		if _, err := s.CopySharedPlaylist(t.Context(), bob.ID, "share-1", "超限", 3); !errors.Is(err, port.ErrPlaylistLimit) {
			t.Errorf("歌单数到了上限 = %v，期望 ErrPlaylistLimit", err)
		}
		if _, err := s.CopySharedPlaylist(t.Context(), bob.ID, "share-x", "失效", 50); !errors.Is(err, port.ErrShareNotFound) {
			t.Errorf("读不到的分享 = %v，期望 ErrShareNotFound", err)
		}
		if lists, err := s.MemberPlaylists(t.Context(), bob.ID); err != nil || len(lists) != 3 {
			t.Errorf("失败之后有 %d 个歌单, %v，期望没有多建", len(lists), err)
		}
	})
}
