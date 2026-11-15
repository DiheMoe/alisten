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

var sunnyItem = port.PlaylistItem{Track: track.Track{Source: track.NetEase, ID: "186016"}, Meta: track.Meta{Title: "晴天", Artist: "周杰伦", Album: "叶惠美", CoverURL: "https://p1.music.126.net/a.jpg", PageURL: "https://music.163.com/#/song?id=186016", Duration: 269 * time.Second}}

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

func songItem(id string) port.PlaylistItem {
	return port.PlaylistItem{Track: track.Track{Source: track.NetEase, ID: id}, Meta: track.Meta{Title: "歌" + id, Artist: "艺人", CoverURL: "https://example.com/" + id + ".jpg", PageURL: "https://music.163.com/#/song?id=" + id, Duration: time.Minute}}
}

func displayIDs(t *testing.T, store *Store, ownerID, id string) []string {
	t.Helper()
	items, err := store.PlaylistItems(t.Context(), ownerID, id)
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
func filled(t *testing.T, store *Store, ownerID, name string, ids ...string) string {
	t.Helper()
	p, err := store.CreatePlaylist(t.Context(), ownerID, name, 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if err := store.AddPlaylistItem(t.Context(), ownerID, p.ID, songItem(id), 1000); err != nil {
			t.Fatal(err)
		}
	}
	return p.ID
}

func trackList(ids ...string) []track.Track {
	out := make([]track.Track, len(ids))
	for i, id := range ids {
		out[i] = track.Track{Source: track.NetEase, ID: id}
	}
	return out
}

func TestConcurrentCrossCopiesDoNotDeadlock(t *testing.T) {
	store, _ := newStore(t)
	alice := registered(t, store, "alice")
	a := filled(t, store, alice.ID, "甲", "1", "2")
	b := filled(t, store, alice.ID, "乙", "3", "4")
	errs := make(chan error, 40)
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			_, err := store.CopyPlaylistItems(t.Context(), alice.ID, a, b, trackList("1", "2"), 1000)
			errs <- err
		})
		wg.Go(func() {
			_, err := store.CopyPlaylistItems(t.Context(), alice.ID, b, a, trackList("3", "4"), 1000)
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	// PostgreSQL 发现死锁时只让其中一个事务报错，其余的照样把两个歌单补满，所以只看最后的曲目数发现不了死锁。
	for err := range errs {
		if err != nil {
			t.Errorf("并发互相复制 = %v，期望都成功", err)
		}
	}
	for _, id := range []string{a, b} {
		if got := displayIDs(t, store, alice.ID, id); len(got) != 4 {
			t.Errorf("互相复制之后有 %v，期望各 4 首", got)
		}
	}
}

func TestConcurrentRemovalsAndMovesDoNotDeadlock(t *testing.T) {
	store, _ := newStore(t)
	alice := registered(t, store, "alice")
	ids := make([]string, 40)
	for i := range ids {
		ids[i] = strconv.Itoa(i)
	}
	list := filled(t, store, alice.ID, "通勤", ids...)
	errs := make(chan error, 40)
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Go(func() { errs <- store.RemovePlaylistItems(t.Context(), alice.ID, list, trackList(ids[i])) })
		wg.Go(func() { errs <- store.MovePlaylistItems(t.Context(), alice.ID, list, trackList(ids[20+i]), nil) })
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("并发移出和移动 = %v，期望都成功", err)
		}
	}
	got := displayIDs(t, store, alice.ID, list)
	slices.Sort(got)
	want := slices.Clone(ids[20:])
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("并发移出和移动之后有 %v，期望只剩没有移出的 %v", got, want)
	}
}

func TestConcurrentRemovalsAndDeletesDoNotDeadlock(t *testing.T) {
	store, pool := newStore(t)
	alice := registered(t, store, "alice")
	lists := make([]string, 20)
	for i := range lists {
		lists[i] = filled(t, store, alice.ID, "歌单"+strconv.Itoa(i), "1", "2", "3")
	}
	removed, deleted := make(chan error, len(lists)), make(chan error, len(lists))
	var wg sync.WaitGroup
	for _, list := range lists {
		wg.Go(func() { removed <- store.RemovePlaylistItems(t.Context(), alice.ID, list, trackList("1", "2", "3")) })
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

func TestImportPlaylistRollsBackAsAWhole(t *testing.T) {
	store, pool := newStore(t)
	alice := registered(t, store, "alice")
	broken := songItem("3")
	broken.Meta.Title = "坏\x00标题"
	if _, err := store.ImportPlaylist(t.Context(), alice.ID, "写入失败", []port.PlaylistItem{songItem("1"), songItem("2"), broken}, 50); err == nil {
		t.Error("有一首写不进去时应报错")
	}
	if lists, err := store.MemberPlaylists(t.Context(), alice.ID); err != nil || len(lists) != 1 {
		t.Errorf("失败之后的歌单 = %#v, %v，期望只剩“我喜欢”", lists, err)
	}
	var count int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM playlist_items`).Scan(&count); err != nil || count != 0 {
		t.Errorf("失败之后的曲目数 = %d, %v，期望 0", count, err)
	}
}

func TestConcurrentImportsRespectLimit(t *testing.T) {
	store, _ := newStore(t)
	alice := registered(t, store, "alice")
	var wg sync.WaitGroup
	for i := range 10 {
		wg.Go(func() {
			_, _ = store.ImportPlaylist(t.Context(), alice.ID, "导入"+strconv.Itoa(i), []port.PlaylistItem{songItem(strconv.Itoa(i))}, 5)
		})
	}
	wg.Wait()
	if lists, err := store.MemberPlaylists(t.Context(), alice.ID); err != nil || len(lists) != 5 {
		t.Errorf("并发导入之后有 %d 个歌单, %v，期望正好 5 个（含“我喜欢”）", len(lists), err)
	}
}
