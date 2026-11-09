package postgres

import (
	"errors"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/member"
	"github.com/dihemoe/alisten/internal/domain/track"
)

func registered(t *testing.T, store *Store, username string) member.Member {
	t.Helper()
	m := createMember(t, store, username, "游客-"+username)
	if err := store.Register(t.Context(), m.ID, username, "hash"); err != nil {
		t.Fatal(err)
	}
	return m
}

func favoritesOf(t *testing.T, store *Store, ownerID string) string {
	t.Helper()
	lists, err := store.MemberPlaylists(t.Context(), ownerID)
	if err != nil || len(lists) == 0 || !lists[0].Favorites {
		t.Fatalf("歌单 = %#v, %v，期望第一个是“我喜欢”", lists, err)
	}
	return lists[0].ID
}

var (
	sunnyItem = port.PlaylistItem{Track: track.Track{Source: track.NetEase, ID: "186016"}, Meta: track.Meta{Title: "晴天", Artist: "周杰伦", Album: "叶惠美", CoverURL: "https://p1.music.126.net/a.jpg", PageURL: "https://music.163.com/#/song?id=186016", Duration: 269 * time.Second}}
	clipItem  = port.PlaylistItem{Track: track.Track{Source: track.Link, ID: "https://example.com/a.mp3"}, Meta: track.Meta{Title: "a.mp3", CoverKey: "covers/abc", PageURL: "https://example.com/a.mp3", Duration: time.Minute}}
)

func TestRegisterCreatesFavorites(t *testing.T) {
	store, _ := newStore(t)
	alice := registered(t, store, "alice")
	lists, err := store.MemberPlaylists(t.Context(), alice.ID)
	if err != nil || len(lists) != 1 || !lists[0].Favorites || lists[0].Name != "我喜欢" || lists[0].TrackCount != 0 {
		t.Fatalf("注册之后的歌单 = %#v, %v，期望只有一个空的“我喜欢”", lists, err)
	}
	guest := createMember(t, store, "guest", "游客-1a2b")
	if lists, err := store.MemberPlaylists(t.Context(), guest.ID); err != nil || len(lists) != 0 {
		t.Errorf("游客的歌单 = %#v, %v，期望没有", lists, err)
	}
}

func TestPlaylistLifecycle(t *testing.T) {
	store, _ := newStore(t)
	alice := registered(t, store, "alice")
	bob := registered(t, store, "bob")
	favorites := favoritesOf(t, store, alice.ID)
	commute, err := store.CreatePlaylist(t.Context(), alice.ID, "通勤", 50)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RenamePlaylist(t.Context(), alice.ID, commute.ID, "上班路上"); err != nil {
		t.Fatal(err)
	}
	lists, err := store.MemberPlaylists(t.Context(), alice.ID)
	if err != nil || len(lists) != 2 || lists[1].ID != commute.ID || lists[1].Name != "上班路上" || lists[1].TrackCount != 0 || lists[1].Cover != nil {
		t.Errorf("歌单 = %#v, %v，期望“我喜欢”之后是改名后的 %s", lists, err, commute.ID)
	}
	if err := store.RenamePlaylist(t.Context(), alice.ID, favorites, "别的名字"); !errors.Is(err, port.ErrFavoritesFixed) {
		t.Errorf("“我喜欢”改名 = %v，期望 ErrFavoritesFixed", err)
	}
	if err := store.DeletePlaylist(t.Context(), alice.ID, favorites); !errors.Is(err, port.ErrFavoritesFixed) {
		t.Errorf("删除“我喜欢” = %v，期望 ErrFavoritesFixed", err)
	}
	for _, id := range []string{commute.ID, "not-a-uuid", "00000000-0000-7000-8000-000000000000"} {
		if err := store.RenamePlaylist(t.Context(), bob.ID, id, "x"); !errors.Is(err, port.ErrPlaylistNotFound) {
			t.Errorf("别人或不存在的歌单 %s 改名 = %v，期望 ErrPlaylistNotFound", id, err)
		}
		if err := store.DeletePlaylist(t.Context(), bob.ID, id); !errors.Is(err, port.ErrPlaylistNotFound) {
			t.Errorf("删除别人或不存在的歌单 %s = %v，期望 ErrPlaylistNotFound", id, err)
		}
		if _, err := store.PlaylistItems(t.Context(), bob.ID, id); !errors.Is(err, port.ErrPlaylistNotFound) {
			t.Errorf("读取别人或不存在的歌单 %s = %v，期望 ErrPlaylistNotFound", id, err)
		}
	}
	if err := store.DeletePlaylist(t.Context(), alice.ID, commute.ID); err != nil {
		t.Fatal(err)
	}
	if lists, err := store.MemberPlaylists(t.Context(), alice.ID); err != nil || len(lists) != 1 {
		t.Errorf("删除之后的歌单 = %#v, %v，期望只剩“我喜欢”", lists, err)
	}
}

func TestDeletePlaylistRemovesItsItems(t *testing.T) {
	store, pool := newStore(t)
	alice := registered(t, store, "alice")
	commute, err := store.CreatePlaylist(t.Context(), alice.ID, "通勤", 50)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddPlaylistItem(t.Context(), alice.ID, commute.ID, sunnyItem, 1000); err != nil {
		t.Fatal(err)
	}
	if err := store.DeletePlaylist(t.Context(), alice.ID, commute.ID); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM playlist_items WHERE playlist_id = $1`, commute.ID).Scan(&count); err != nil || count != 0 {
		t.Errorf("删除歌单之后的曲目数 = %d, %v，期望 0", count, err)
	}
}

func TestAccountHasOneFavorites(t *testing.T) {
	store, pool := newStore(t)
	alice := registered(t, store, "alice")
	_, err := pool.Exec(t.Context(), `INSERT INTO playlists (owner_id, kind, name) VALUES ($1, 'favorites', '我喜欢')`, alice.ID)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != uniqueViolation {
		t.Errorf("第二个“我喜欢” = %v，期望唯一约束错误", err)
	}
}

func TestPlaylistLimitCountsFavorites(t *testing.T) {
	store, _ := newStore(t)
	alice := registered(t, store, "alice")
	if _, err := store.CreatePlaylist(t.Context(), alice.ID, "一", 2); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreatePlaylist(t.Context(), alice.ID, "二", 2); !errors.Is(err, port.ErrPlaylistLimit) {
		t.Errorf("超过上限时 = %v，期望 ErrPlaylistLimit", err)
	}
}

func TestPlaylistItems(t *testing.T) {
	store, _ := newStore(t)
	alice := registered(t, store, "alice")
	bob := registered(t, store, "bob")
	favorites := favoritesOf(t, store, alice.ID)
	for _, item := range []port.PlaylistItem{sunnyItem, clipItem, sunnyItem} {
		if err := store.AddPlaylistItem(t.Context(), alice.ID, favorites, item, 2); err != nil {
			t.Fatal(err)
		}
	}
	items, err := store.PlaylistItems(t.Context(), alice.ID, favorites)
	if want := []port.PlaylistItem{clipItem, sunnyItem}; err != nil || !slices.Equal(items, want) {
		t.Errorf("曲目 = %#v, %v，期望新加入的在前、重复加入不变 %#v", items, err, want)
	}
	if lists, _ := store.MemberPlaylists(t.Context(), alice.ID); lists[0].TrackCount != 2 {
		t.Errorf("曲目数 = %d，期望 2", lists[0].TrackCount)
	}
	third := port.PlaylistItem{Track: track.Track{Source: track.NetEase, ID: "185811"}, Meta: track.Meta{Title: "七里香", PageURL: "https://music.163.com/#/song?id=185811"}}
	if err := store.AddPlaylistItem(t.Context(), alice.ID, favorites, third, 2); !errors.Is(err, port.ErrPlaylistLimit) {
		t.Errorf("歌单满了时 = %v，期望 ErrPlaylistLimit", err)
	}
	if err := store.AddPlaylistItem(t.Context(), bob.ID, favorites, third, 2); !errors.Is(err, port.ErrPlaylistNotFound) {
		t.Errorf("加入别人的歌单 = %v，期望 ErrPlaylistNotFound", err)
	}
	for range 2 {
		if err := store.RemovePlaylistItem(t.Context(), alice.ID, favorites, sunnyItem.Track); err != nil {
			t.Fatal(err)
		}
	}
	if items, err := store.PlaylistItems(t.Context(), alice.ID, favorites); err != nil || !slices.Equal(items, []port.PlaylistItem{clipItem}) {
		t.Errorf("移除之后的曲目 = %#v, %v", items, err)
	}
	if err := store.RemovePlaylistItem(t.Context(), bob.ID, favorites, clipItem.Track); !errors.Is(err, port.ErrPlaylistNotFound) {
		t.Errorf("从别人的歌单移除 = %v，期望 ErrPlaylistNotFound", err)
	}
}

func TestConcurrentPlaylistAddsRespectLimit(t *testing.T) {
	store, pool := newStore(t)
	alice := registered(t, store, "alice")
	favorites := favoritesOf(t, store, alice.ID)
	var wg sync.WaitGroup
	for i := range 10 {
		wg.Go(func() {
			item := port.PlaylistItem{Track: track.Track{Source: track.NetEase, ID: strconv.Itoa(i)}, Meta: track.Meta{Title: "歌", PageURL: "https://music.163.com/"}}
			_ = store.AddPlaylistItem(t.Context(), alice.ID, favorites, item, 5)
		})
	}
	wg.Wait()
	if items, err := store.PlaylistItems(t.Context(), alice.ID, favorites); err != nil || len(items) != 5 {
		t.Errorf("并发加入之后有 %d 首, %v，期望正好 5 首", len(items), err)
	}
	var distinct, seq int64
	var largest int64
	if err := pool.QueryRow(t.Context(), `SELECT count(DISTINCT i.position), max(i.position), max(p.item_seq)
FROM playlist_items i JOIN playlists p ON p.id = i.playlist_id WHERE i.playlist_id = $1`, favorites).Scan(&distinct, &largest, &seq); err != nil {
		t.Fatal(err)
	}
	if distinct != 5 || largest > seq {
		t.Errorf("排序值有 %d 种、最大 %d，歌单的 item_seq 是 %d，期望 5 首的排序值两两不同且都不大于 item_seq", distinct, largest, seq)
	}
}

func TestConcurrentPlaylistCreatesRespectLimit(t *testing.T) {
	store, _ := newStore(t)
	alice := registered(t, store, "alice")
	var wg sync.WaitGroup
	for i := range 10 {
		wg.Go(func() {
			_, _ = store.CreatePlaylist(t.Context(), alice.ID, "歌单"+strconv.Itoa(i), 5)
		})
	}
	wg.Wait()
	if lists, err := store.MemberPlaylists(t.Context(), alice.ID); err != nil || len(lists) != 5 {
		t.Errorf("并发新建之后有 %d 个歌单, %v，期望正好 5 个（含“我喜欢”）", len(lists), err)
	}
}

func TestPlaylistTimestamps(t *testing.T) {
	store, _ := newStore(t)
	alice := registered(t, store, "alice")
	created, err := store.CreatePlaylist(t.Context(), alice.ID, "通勤", 50)
	if err != nil || created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() || created.Cover != nil {
		t.Fatalf("新建 = %#v, %v，期望带创建和更新时间、没有封面", created, err)
	}
	read := func() port.PlaylistRecord {
		t.Helper()
		lists, err := store.MemberPlaylists(t.Context(), alice.ID)
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
		{"改名", func() error { return store.RenamePlaylist(t.Context(), alice.ID, created.ID, "上班路上") }, true},
		{"加入新曲目", func() error { return store.AddPlaylistItem(t.Context(), alice.ID, created.ID, sunnyItem, 10) }, true},
		{"重复加入", func() error { return store.AddPlaylistItem(t.Context(), alice.ID, created.ID, sunnyItem, 10) }, false},
		{"移除曲目", func() error { return store.RemovePlaylistItem(t.Context(), alice.ID, created.ID, sunnyItem.Track) }, true},
		{"移除不存在的曲目", func() error { return store.RemovePlaylistItem(t.Context(), alice.ID, created.ID, sunnyItem.Track) }, false},
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
}

func TestPlaylistCoverIsTheFirstSongWithOne(t *testing.T) {
	store, _ := newStore(t)
	alice := registered(t, store, "alice")
	favorites := favoritesOf(t, store, alice.ID)
	add := func(source track.Source, id, coverURL string, coverKey track.ObjectKey) {
		t.Helper()
		item := port.PlaylistItem{
			Track: track.Track{Source: source, ID: id},
			Meta:  track.Meta{Title: id, CoverURL: coverURL, CoverKey: coverKey, PageURL: "https://example.com/" + id},
		}
		if err := store.AddPlaylistItem(t.Context(), alice.ID, favorites, item, 100); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 3 {
		add(track.NetEase, strconv.Itoa(i), "https://img.example/"+strconv.Itoa(i)+".jpg", "")
	}
	add(track.Link, "https://example.com/a.mp3", "", "covers/a.jpg")
	add(track.NetEase, "no-cover", "", "")
	lists, err := store.MemberPlaylists(t.Context(), alice.ID)
	if err != nil || len(lists) != 1 {
		t.Fatalf("歌单 = %#v, %v", lists, err)
	}
	if got, want := lists[0].Cover, (port.Cover{Key: "covers/a.jpg"}); got == nil || *got != want {
		t.Errorf("封面 = %+v，期望跳过没有封面的歌，取排在最前的 %+v", got, want)
	}
}

func TestConcurrentRemovalsAndDeletesDoNotDeadlock(t *testing.T) {
	store, pool := newStore(t)
	alice := registered(t, store, "alice")
	lists := make([]string, 20)
	for i := range lists {
		p, err := store.CreatePlaylist(t.Context(), alice.ID, "歌单"+strconv.Itoa(i), 50)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.AddPlaylistItem(t.Context(), alice.ID, p.ID, sunnyItem, 1000); err != nil {
			t.Fatal(err)
		}
		lists[i] = p.ID
	}
	removed, deleted := make(chan error, len(lists)), make(chan error, len(lists))
	var wg sync.WaitGroup
	for _, list := range lists {
		wg.Go(func() { removed <- store.RemovePlaylistItem(t.Context(), alice.ID, list, sunnyItem.Track) })
		wg.Go(func() { deleted <- store.DeletePlaylist(t.Context(), alice.ID, list) })
	}
	wg.Wait()
	close(removed)
	close(deleted)
	for err := range removed {
		if err != nil && !errors.Is(err, port.ErrPlaylistNotFound) {
			t.Errorf("与删除歌单并发移出 = %v，期望成功或 ErrPlaylistNotFound", err)
		}
	}
	for err := range deleted {
		if err != nil {
			t.Errorf("与移出并发删除歌单 = %v，期望成功", err)
		}
	}
	if lists, err := store.MemberPlaylists(t.Context(), alice.ID); err != nil || len(lists) != 1 {
		t.Errorf("删除之后的歌单 = %#v, %v，期望只剩“我喜欢”", lists, err)
	}
	var count int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM playlist_items`).Scan(&count); err != nil || count != 0 {
		t.Errorf("删除歌单之后还有 %d 首曲目, %v，期望 0", count, err)
	}
}
