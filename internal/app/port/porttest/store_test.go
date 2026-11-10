package porttest

import (
	"errors"
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

func favoritesCount(t *testing.T, store *Store, memberID string) int {
	t.Helper()
	lists, err := store.MemberPlaylists(t.Context(), memberID)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, l := range lists {
		if l.Favorites {
			n++
		}
	}
	return n
}

// 与 postgres 适配器的 TestBindGitHub、TestAddCredentialsAfterGitHub 相同的场景。
func TestBindGitHub(t *testing.T) {
	store := New()
	alice, err := store.CreateMember(t.Context(), []byte("alice"), "甲")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := store.CreateMember(t.Context(), []byte("bob"), "乙")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UnbindGitHub(t.Context(), alice.ID); !errors.Is(err, port.ErrNoPassword) {
		t.Errorf("游客解绑 = %v，期望 ErrNoPassword", err)
	}
	if err := store.BindGitHub(t.Context(), alice.ID, 42); err != nil {
		t.Fatal(err)
	}
	if p, err := store.AccountProfile(t.Context(), alice.ID); err != nil || p != (port.AccountProfile{GitHub: true}) {
		t.Errorf("绑定之后的 AccountProfile = %#v, %v", p, err)
	}
	want := port.Account{Member: alice, GitHubID: 42}
	if got, err := store.AccountByGitHub(t.Context(), 42); err != nil || got != want {
		t.Errorf("AccountByGitHub = %#v, %v，期望 %#v", got, err, want)
	}
	if got, err := store.AccountByID(t.Context(), alice.ID); err != nil || got != want {
		t.Errorf("AccountByID = %#v, %v，期望 %#v", got, err, want)
	}
	if _, err := store.AccountByID(t.Context(), bob.ID); !errors.Is(err, port.ErrMemberNotFound) {
		t.Errorf("游客的 AccountByID = %v，期望 ErrMemberNotFound", err)
	}
	if err := store.BindGitHub(t.Context(), bob.ID, 42); !errors.Is(err, port.ErrGitHubTaken) {
		t.Errorf("GitHub 账号已经绑定了别人 = %v，期望 ErrGitHubTaken", err)
	}
	if err := store.BindGitHub(t.Context(), alice.ID, 43); !errors.Is(err, port.ErrGitHubAlreadyBound) {
		t.Errorf("已经绑定过的成员再绑定 = %v，期望 ErrGitHubAlreadyBound", err)
	}
	email := "alice@example.com"
	if _, err := store.UpdateMember(t.Context(), alice.ID, nil, nil, &email); err != nil {
		t.Fatal(err)
	}
	if p, _ := store.AccountProfile(t.Context(), alice.ID); p.Email != email {
		t.Errorf("只绑定了 GitHub 的账号保存邮箱之后 = %q", p.Email)
	}
	if err := store.UnbindGitHub(t.Context(), alice.ID); !errors.Is(err, port.ErrNoPassword) {
		t.Errorf("没有密码的账号解绑 = %v，期望 ErrNoPassword", err)
	}
	if err := store.Register(t.Context(), alice.ID, "alice", "hash-a"); !errors.Is(err, port.ErrAlreadyRegistered) {
		t.Errorf("只绑定了 GitHub 的账号用 Register 设置用户名 = %v，期望 ErrAlreadyRegistered", err)
	}
	if _, err := store.AddCredentials(t.Context(), bob.ID, "bob", "hash-b", []byte("bob")); !errors.Is(err, port.ErrAlreadyRegistered) {
		t.Errorf("游客用 AddCredentials 设置用户名 = %v，期望 ErrAlreadyRegistered", err)
	}
	if _, err := store.CreateMember(t.Context(), []byte("guest"), "丙"); err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceSession(t.Context(), []byte("guest"), alice.ID, []byte("alice-2"), ""); err != nil {
		t.Fatal(err)
	}
	deleted, err := store.AddCredentials(t.Context(), alice.ID, "alice", "hash-a", []byte("alice"))
	if err != nil || len(deleted) != 1 || string(deleted[0]) != "alice-2" {
		t.Fatalf("AddCredentials = %q, %v，期望删除 alice-2", deleted, err)
	}
	if m, err := store.MemberByToken(t.Context(), []byte("alice")); err != nil || m.ID != alice.ID {
		t.Errorf("发出请求的会话 = %#v, %v，期望保留", m, err)
	}
	if _, err := store.MemberByToken(t.Context(), []byte("alice-2")); !errors.Is(err, port.ErrMemberNotFound) {
		t.Errorf("其他会话 = %v，期望已删除", err)
	}
	if n := favoritesCount(t, store, alice.ID); n != 1 {
		t.Errorf("绑定 GitHub 再补设用户名之后有 %d 个“我喜欢”，期望 1 个", n)
	}
	if _, err := store.AddCredentials(t.Context(), alice.ID, "alice2", "hash-c", []byte("alice")); !errors.Is(err, port.ErrAlreadyRegistered) {
		t.Errorf("再补设一次 = %v，期望 ErrAlreadyRegistered", err)
	}
	if err := store.UnbindGitHub(t.Context(), alice.ID); err != nil {
		t.Fatal(err)
	}
	if p, _ := store.AccountProfile(t.Context(), alice.ID); p != (port.AccountProfile{Username: "alice", Email: email}) {
		t.Errorf("解绑之后的 AccountProfile = %#v", p)
	}
	if err := store.Register(t.Context(), bob.ID, "bob", "hash-b"); err != nil {
		t.Fatal(err)
	}
	if err := store.BindGitHub(t.Context(), bob.ID, 42); err != nil {
		t.Fatal(err)
	}
	if n := favoritesCount(t, store, bob.ID); n != 1 {
		t.Errorf("已注册的账号绑定 GitHub 之后有 %d 个“我喜欢”，期望 1 个", n)
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
