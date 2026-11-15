package playlist

import (
	"errors"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"testing"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/track"
)

var shareIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{22}$`)

func TestShareGeneratesANewLinkEachTime(t *testing.T) {
	f := newFixture(t)
	alice := f.account(t, "alice")
	bob := f.account(t, "bob")
	favorites := f.favorites(t, alice)
	item, err := f.svc.Add(t.Context(), alice, favorites, sunny)
	if err != nil {
		t.Fatal(err)
	}
	first, err := f.svc.Share(t.Context(), alice, favorites)
	if err != nil || !shareIDPattern.MatchString(first) {
		t.Fatalf("分享 ID = %q, %v，期望 22 个 base64url 字符", first, err)
	}
	shared, err := f.svc.Shared(t.Context(), first)
	want := port.SharedPlaylist{Name: "我喜欢", Favorites: true, OwnerName: "游客-alice", Items: []port.PlaylistItem{item}}
	if err != nil || !reflect.DeepEqual(shared, want) {
		t.Errorf("分享的歌单 = %#v, %v，期望 %#v", shared, err, want)
	}
	if lists, err := f.svc.List(t.Context(), alice); err != nil || lists[0].ShareID != first {
		t.Errorf("歌单 = %#v, %v，期望带上分享 ID", lists, err)
	}

	second, err := f.svc.Share(t.Context(), alice, favorites)
	if err != nil || !shareIDPattern.MatchString(second) || second == first {
		t.Fatalf("重新生成 = %q, %v，期望换一个新的分享 ID", second, err)
	}
	if _, err := f.svc.Shared(t.Context(), first); !errors.Is(err, port.ErrShareNotFound) {
		t.Errorf("旧链接 = %v，期望 ErrShareNotFound", err)
	}
	if _, err := f.svc.Shared(t.Context(), second); err != nil {
		t.Errorf("新链接 = %v", err)
	}

	if _, err := f.svc.Share(t.Context(), bob, favorites); !errors.Is(err, port.ErrPlaylistNotFound) {
		t.Errorf("分享别人的歌单 = %v，期望 ErrPlaylistNotFound", err)
	}
	if err := f.svc.Unshare(t.Context(), bob, favorites); !errors.Is(err, port.ErrPlaylistNotFound) {
		t.Errorf("停止别人的分享 = %v，期望 ErrPlaylistNotFound", err)
	}
	if err := f.svc.Unshare(t.Context(), alice, favorites); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Shared(t.Context(), second); !errors.Is(err, port.ErrShareNotFound) {
		t.Errorf("停止分享之后 = %v，期望 ErrShareNotFound", err)
	}
}

func TestCopySharedPlaylist(t *testing.T) {
	f := newFixture(t)
	alice := f.account(t, "alice")
	bob := f.account(t, "bob")
	rain := netease("185811")
	f.netease.Add(rain.ID, track.Meta{Title: "七里香", Artist: "周杰伦", PageURL: "https://music.163.com/#/song?id=185811"}, "", nil)
	commute, err := f.svc.Create(t.Context(), alice, "通勤")
	if err != nil {
		t.Fatal(err)
	}
	for _, song := range []track.Track{sunny, rain} {
		if _, err := f.svc.Add(t.Context(), alice, commute.ID, song); err != nil {
			t.Fatal(err)
		}
	}
	shareID, err := f.svc.Share(t.Context(), alice, commute.ID)
	if err != nil {
		t.Fatal(err)
	}
	keys := len(f.quota.keys)

	copied, err := f.svc.CopyShared(t.Context(), bob, shareID, "  抄来的  ")
	if err != nil || copied.Name != "抄来的" || copied.TrackCount != 2 || copied.ShareID != "" {
		t.Fatalf("复制 = %#v, %v，期望去掉名称首尾的空白、带 2 首、没有分享", copied, err)
	}
	got, err := f.svc.Items(t.Context(), bob, copied.ID)
	original, _ := f.svc.Items(t.Context(), alice, commute.ID)
	if err != nil || !slices.Equal(got, original) {
		t.Errorf("复制的曲目 = %#v, %v，期望与原歌单相同 %#v", got, err, original)
	}
	if len(f.quota.keys) != keys {
		t.Errorf("复制不应消耗额度：%v", f.quota.keys[keys:])
	}
	if _, err := f.svc.CopyShared(t.Context(), bob, shareID, "   "); !errors.Is(err, ErrInvalidName) {
		t.Errorf("名称为空白 = %v，期望 ErrInvalidName", err)
	}

	if err := f.svc.Unshare(t.Context(), alice, commute.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.CopyShared(t.Context(), bob, shareID, "抄来的"); !errors.Is(err, port.ErrShareNotFound) {
		t.Errorf("停止分享之后复制 = %v，期望 ErrShareNotFound", err)
	}

	shareID, err = f.svc.Share(t.Context(), alice, commute.ID)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 48 {
		if _, err := f.svc.Create(t.Context(), bob, "歌单"+strconv.Itoa(i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.svc.CopyShared(t.Context(), bob, shareID, "第 51 个"); !errors.Is(err, ErrTooManyPlaylists) {
		t.Errorf("第 51 个歌单 = %v，期望 ErrTooManyPlaylists", err)
	}
}
