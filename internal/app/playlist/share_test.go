package playlist

import (
	"context"
	"errors"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/app/port/porttest"
	"github.com/dihemoe/alisten/internal/domain/track"
)

var shareIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{22}$`)

func TestGuestCannotShareOrCopy(t *testing.T) {
	f := newFixture(t)
	guest := f.member(t, "")
	alice := f.member(t, "alice")
	favorites := f.favorites(t, alice.ID)
	if _, err := f.svc.Share(t.Context(), guest.ID, favorites); !errors.Is(err, ErrNotRegistered) {
		t.Errorf("游客开启分享 = %v，期望 ErrNotRegistered", err)
	}
	if err := f.svc.Unshare(t.Context(), guest.ID, favorites); !errors.Is(err, ErrNotRegistered) {
		t.Errorf("游客停止分享 = %v，期望 ErrNotRegistered", err)
	}
	shareID, err := f.svc.Share(t.Context(), alice.ID, favorites)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.CopyShared(t.Context(), guest.ID, shareID, "抄来的"); !errors.Is(err, ErrNotRegistered) {
		t.Errorf("游客复制 = %v，期望 ErrNotRegistered", err)
	}
	if _, err := f.svc.Shared(t.Context(), shareID); err != nil {
		t.Errorf("不需要身份就能读取：%v", err)
	}
	if len(f.quota.keys) != 0 {
		t.Errorf("分享和复制不应消耗额度：%v", f.quota.keys)
	}
}

func TestShareGeneratesANewLinkEachTime(t *testing.T) {
	f := newFixture(t)
	alice := f.member(t, "alice")
	bob := f.member(t, "bob")
	favorites := f.favorites(t, alice.ID)
	item, err := f.svc.Add(t.Context(), alice.ID, favorites, sunny)
	if err != nil {
		t.Fatal(err)
	}
	first, err := f.svc.Share(t.Context(), alice.ID, favorites)
	if err != nil || !shareIDPattern.MatchString(first) {
		t.Fatalf("分享 ID = %q, %v，期望 22 个 base64url 字符", first, err)
	}
	shared, err := f.svc.Shared(t.Context(), first)
	want := port.SharedPlaylist{Name: "我喜欢", Favorites: true, OwnerName: alice.Name, Items: []port.PlaylistItem{item}}
	if err != nil || !reflect.DeepEqual(shared, want) {
		t.Errorf("分享的歌单 = %#v, %v，期望 %#v", shared, err, want)
	}
	if lists, err := f.svc.List(t.Context(), alice.ID); err != nil || lists[0].ShareID != first {
		t.Errorf("歌单 = %#v, %v，期望带上分享 ID", lists, err)
	}

	second, err := f.svc.Share(t.Context(), alice.ID, favorites)
	if err != nil || !shareIDPattern.MatchString(second) || second == first {
		t.Fatalf("重新生成 = %q, %v，期望换一个新的分享 ID", second, err)
	}
	if _, err := f.svc.Shared(t.Context(), first); !errors.Is(err, port.ErrShareNotFound) {
		t.Errorf("旧链接 = %v，期望 ErrShareNotFound", err)
	}
	if _, err := f.svc.Shared(t.Context(), second); err != nil {
		t.Errorf("新链接 = %v", err)
	}

	if _, err := f.svc.Share(t.Context(), bob.ID, favorites); !errors.Is(err, port.ErrPlaylistNotFound) {
		t.Errorf("分享别人的歌单 = %v，期望 ErrPlaylistNotFound", err)
	}
	if err := f.svc.Unshare(t.Context(), bob.ID, favorites); !errors.Is(err, port.ErrPlaylistNotFound) {
		t.Errorf("停止别人的分享 = %v，期望 ErrPlaylistNotFound", err)
	}
	if err := f.svc.Unshare(t.Context(), alice.ID, favorites); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Shared(t.Context(), second); !errors.Is(err, port.ErrShareNotFound) {
		t.Errorf("停止分享之后 = %v，期望 ErrShareNotFound", err)
	}
}

type shareLookups struct {
	*porttest.Store
	lookups int
}

func (s *shareLookups) SharedPlaylist(ctx context.Context, shareID string) (port.SharedPlaylist, error) {
	s.lookups++
	return s.Store.SharedPlaylist(ctx, shareID)
}

func (s *shareLookups) CopySharedPlaylist(ctx context.Context, ownerID, shareID, name string, limit int) (port.PlaylistRecord, error) {
	s.lookups++
	return s.Store.CopySharedPlaylist(ctx, ownerID, shareID, name, limit)
}

func TestMalformedShareIDsAreNotLookedUp(t *testing.T) {
	f := newFixture(t)
	alice := f.member(t, "alice")
	lookups := &shareLookups{Store: f.store}
	f.svc.playlists = lookups
	valid := strings.Repeat("a", 21)
	for _, id := range []string{"", "abc", valid, valid + "aa", valid + "\x00", valid + "=", valid + "+", valid + "/", "\xff" + valid} {
		if _, err := f.svc.Shared(t.Context(), id); !errors.Is(err, port.ErrShareNotFound) {
			t.Errorf("读取 %q = %v，期望 ErrShareNotFound", id, err)
		}
		if _, err := f.svc.CopyShared(t.Context(), alice.ID, id, "抄来的"); !errors.Is(err, port.ErrShareNotFound) {
			t.Errorf("复制 %q = %v，期望 ErrShareNotFound", id, err)
		}
	}
	if lookups.lookups != 0 {
		t.Errorf("格式不对的分享 ID 查了 %d 次存储，期望不查", lookups.lookups)
	}
	if _, err := f.svc.Shared(t.Context(), valid+"a"); !errors.Is(err, port.ErrShareNotFound) || lookups.lookups != 1 {
		t.Errorf("格式正确却不存在的分享 = %v，查了 %d 次，期望查一次后 ErrShareNotFound", err, lookups.lookups)
	}
}

func TestCopySharedPlaylist(t *testing.T) {
	f := newFixture(t)
	alice := f.member(t, "alice")
	bob := f.member(t, "bob")
	rain := netease("185811")
	f.netease.Add(rain.ID, track.Meta{Title: "七里香", Artist: "周杰伦", PageURL: "https://music.163.com/#/song?id=185811"}, "", nil)
	commute, err := f.svc.Create(t.Context(), alice.ID, "通勤")
	if err != nil {
		t.Fatal(err)
	}
	for _, song := range []track.Track{sunny, rain} {
		if _, err := f.svc.Add(t.Context(), alice.ID, commute.ID, song); err != nil {
			t.Fatal(err)
		}
	}
	shareID, err := f.svc.Share(t.Context(), alice.ID, commute.ID)
	if err != nil {
		t.Fatal(err)
	}
	keys := len(f.quota.keys)

	copied, err := f.svc.CopyShared(t.Context(), bob.ID, shareID, "  抄来的  ")
	if err != nil || copied.Name != "抄来的" || copied.TrackCount != 2 || copied.ShareID != "" {
		t.Fatalf("复制 = %#v, %v，期望去掉名称首尾的空白、带 2 首、没有分享", copied, err)
	}
	got, err := f.svc.Items(t.Context(), bob.ID, copied.ID)
	original, _ := f.svc.Items(t.Context(), alice.ID, commute.ID)
	if err != nil || !slices.Equal(got, original) {
		t.Errorf("复制的曲目 = %#v, %v，期望与原歌单相同 %#v", got, err, original)
	}
	if len(f.quota.keys) != keys {
		t.Errorf("复制不应消耗额度：%v", f.quota.keys[keys:])
	}
	if _, err := f.svc.CopyShared(t.Context(), bob.ID, shareID, "   "); !errors.Is(err, ErrInvalidName) {
		t.Errorf("名称为空白 = %v，期望 ErrInvalidName", err)
	}

	if err := f.svc.Unshare(t.Context(), alice.ID, commute.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.CopyShared(t.Context(), bob.ID, shareID, "抄来的"); !errors.Is(err, port.ErrShareNotFound) {
		t.Errorf("停止分享之后复制 = %v，期望 ErrShareNotFound", err)
	}

	shareID, err = f.svc.Share(t.Context(), alice.ID, commute.ID)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 48 {
		if _, err := f.svc.Create(t.Context(), bob.ID, "歌单"+strconv.Itoa(i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.svc.CopyShared(t.Context(), bob.ID, shareID, "第 51 个"); !errors.Is(err, ErrTooManyPlaylists) {
		t.Errorf("第 51 个歌单 = %v，期望 ErrTooManyPlaylists", err)
	}
}
