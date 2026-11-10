package playlist

import (
	"context"
	"crypto/sha256"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dihemoe/alisten/internal/app/catalog"
	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/app/port/porttest"
	"github.com/dihemoe/alisten/internal/domain/member"
	playlistorder "github.com/dihemoe/alisten/internal/domain/playlist"
	"github.com/dihemoe/alisten/internal/domain/track"
)

var (
	sunny     = track.Track{Source: track.NetEase, ID: "186016"}
	sunnyMeta = track.Meta{Title: "晴天", Artist: "周杰伦", Album: "叶惠美", CoverURL: "https://p1.music.126.net/a.jpg", PageURL: "https://music.163.com/#/song?id=186016", Duration: 269 * time.Second}
	clip      = track.Track{Source: track.Link, ID: "https://example.com/a.mp3"}
)

type tracksTable map[track.Track]port.PreparedTrack

func (t tracksTable) PreparedTrack(_ context.Context, k track.Track) (port.PreparedTrack, bool, error) {
	p, ok := t[k]
	return p, ok, nil
}

func (t tracksTable) TrackMetas(_ context.Context, ts []track.Track) (map[track.Track]track.Meta, error) {
	found := map[track.Track]track.Meta{}
	for _, k := range ts {
		if p, ok := t[k]; ok {
			found[k] = p.Meta
		}
	}
	return found, nil
}

func (tracksTable) SavePreparedTrack(context.Context, track.Track, port.PreparedTrack) error {
	return nil
}

type noHistory struct{}

func (noHistory) AutoDJSeeds(context.Context, string, bool) ([]track.Track, error) { return nil, nil }

func (noHistory) AutoDJExclusions(context.Context, string) ([]track.Track, error) { return nil, nil }

type roomQuota struct{}

func (roomQuota) TakeSearch(context.Context, string, int, time.Duration) (time.Duration, error) {
	return 0, nil
}

func (roomQuota) TakePick(context.Context, string, int, time.Duration) (time.Duration, error) {
	return 0, nil
}

type accountQuota struct {
	exhausted bool
	keys      []string
	// 最近一次取额度时给的额度。
	burst  int
	refill time.Duration
}

func (q *accountQuota) Take(_ context.Context, key string, burst int, refill time.Duration) (time.Duration, error) {
	q.keys = append(q.keys, key)
	q.burst, q.refill = burst, refill
	if q.exhausted {
		return time.Second, nil
	}
	return 0, nil
}

// 从短链接到解析结果的映射。不在表里的短链接解析失败。
type shortLinks map[string]string

func (l shortLinks) Resolve(_ context.Context, link string) (string, error) {
	if final, ok := l[link]; ok {
		return final, nil
	}
	return "", errors.New("跳转到了不受信任的主机")
}

type fixture struct {
	svc     *Service
	store   *porttest.Store
	tracks  tracksTable
	quota   *accountQuota
	netease *porttest.Source
	links   shortLinks
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	store := porttest.New()
	netease := porttest.NewSource(track.NetEase)
	netease.Add(sunny.ID, sunnyMeta, "", nil)
	bilibili := porttest.NewBilibili()
	tracks := tracksTable{}
	music := catalog.New(netease, porttest.NewSource(track.QQ), bilibili, bilibili, tracks, noHistory{}, store, roomQuota{})
	q := &accountQuota{}
	links := shortLinks{}
	return fixture{svc: New(store, store, tracks, music, q, links), store: store, tracks: tracks, quota: q, netease: netease, links: links}
}

func (f fixture) member(t *testing.T, username string) member.Member {
	t.Helper()
	hash := sha256.Sum256([]byte(username))
	m, err := f.store.CreateMember(t.Context(), hash[:], "游客-"+username)
	if err != nil {
		t.Fatal(err)
	}
	if username != "" {
		if err := f.store.Register(t.Context(), m.ID, username, "hash"); err != nil {
			t.Fatal(err)
		}
	}
	return m
}

func (f fixture) favorites(t *testing.T, memberID string) string {
	t.Helper()
	lists, err := f.svc.List(t.Context(), memberID)
	if err != nil || len(lists) == 0 || !lists[0].Favorites {
		t.Fatalf("歌单 = %#v, %v", lists, err)
	}
	return lists[0].ID
}

func TestGuestCannotUsePlaylists(t *testing.T) {
	f := newFixture(t)
	guest := f.member(t, "")
	if _, err := f.svc.List(t.Context(), guest.ID); !errors.Is(err, ErrNotRegistered) {
		t.Errorf("游客读取歌单 = %v，期望 ErrNotRegistered", err)
	}
	if _, err := f.svc.Create(t.Context(), guest.ID, "通勤"); !errors.Is(err, ErrNotRegistered) {
		t.Errorf("游客新建歌单 = %v，期望 ErrNotRegistered", err)
	}
	if _, err := f.svc.Add(t.Context(), guest.ID, "any", sunny); !errors.Is(err, ErrNotRegistered) {
		t.Errorf("游客收藏 = %v，期望 ErrNotRegistered", err)
	}
	if _, err := f.svc.Containing(t.Context(), guest.ID, sunny); !errors.Is(err, ErrNotRegistered) {
		t.Errorf("游客查询所在的歌单 = %v，期望 ErrNotRegistered", err)
	}
	if err := f.svc.Remove(t.Context(), guest.ID, "any", []track.Track{sunny}); !errors.Is(err, ErrNotRegistered) {
		t.Errorf("游客批量移出 = %v，期望 ErrNotRegistered", err)
	}
	if _, err := f.svc.Copy(t.Context(), guest.ID, "from", "to", []track.Track{sunny}); !errors.Is(err, ErrNotRegistered) {
		t.Errorf("游客复制 = %v，期望 ErrNotRegistered", err)
	}
	if err := f.svc.Move(t.Context(), guest.ID, "any", []track.Track{sunny}, nil); !errors.Is(err, ErrNotRegistered) {
		t.Errorf("游客调整顺序 = %v，期望 ErrNotRegistered", err)
	}
	if _, _, err := f.svc.ImportFile(t.Context(), guest.ID, "导入", []ImportedTrack{{Track: sunny, Title: "晴天"}}); !errors.Is(err, ErrNotRegistered) {
		t.Errorf("游客导入文件 = %v，期望 ErrNotRegistered", err)
	}
	if _, _, err := f.svc.ResolveLink(t.Context(), guest.ID, "https://music.163.com/playlist?id=1"); !errors.Is(err, ErrNotRegistered) {
		t.Errorf("游客解析歌单链接 = %v，期望 ErrNotRegistered", err)
	}
	if _, _, err := f.svc.ImportPlatform(t.Context(), guest.ID, "导入", track.NetEase, "1"); !errors.Is(err, ErrNotRegistered) {
		t.Errorf("游客导入平台歌单 = %v，期望 ErrNotRegistered", err)
	}
	if len(f.quota.keys) != 0 {
		t.Errorf("游客不应消耗额度：%v", f.quota.keys)
	}
}

func TestGitHubAccountUsesPlaylists(t *testing.T) {
	f := newFixture(t)
	gh := f.member(t, "")
	if err := f.store.BindGitHub(t.Context(), gh.ID, 42); err != nil {
		t.Fatal(err)
	}
	lists, err := f.svc.List(t.Context(), gh.ID)
	if err != nil || len(lists) != 1 || !lists[0].Favorites {
		t.Errorf("只绑定了 GitHub 的账号的歌单 = %#v, %v，期望注册时建好的“我喜欢”", lists, err)
	}
}

func TestCreateValidatesName(t *testing.T) {
	f := newFixture(t)
	alice := f.member(t, "alice")
	p, err := f.svc.Create(t.Context(), alice.ID, "  通勤  ")
	if err != nil || p.Name != "通勤" {
		t.Fatalf("Create = %#v, %v，期望去掉首尾空白", p, err)
	}
	for _, name := range []string{"", "   ", strings.Repeat("歌", 51), "通\n勤", "\u200b"} {
		if _, err := f.svc.Create(t.Context(), alice.ID, name); !errors.Is(err, ErrInvalidName) {
			t.Errorf("Create(%q) = %v，期望 ErrInvalidName", name, err)
		}
		if err := f.svc.Rename(t.Context(), alice.ID, p.ID, name); !errors.Is(err, ErrInvalidName) {
			t.Errorf("Rename(%q) = %v，期望 ErrInvalidName", name, err)
		}
	}
	if _, err := f.svc.Create(t.Context(), alice.ID, strings.Repeat("歌", 50)); err != nil {
		t.Errorf("50 个字的歌单名 = %v，期望可以", err)
	}
}

func TestCreateStopsAtFiftyPlaylists(t *testing.T) {
	f := newFixture(t)
	alice := f.member(t, "alice")
	for i := range 49 {
		if _, err := f.svc.Create(t.Context(), alice.ID, "歌单"+strconv.Itoa(i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.svc.Create(t.Context(), alice.ID, "第 51 个"); !errors.Is(err, ErrTooManyPlaylists) {
		t.Errorf("第 51 个歌单 = %v，期望 ErrTooManyPlaylists", err)
	}
}

func TestAddTakesMetaFromTheServer(t *testing.T) {
	f := newFixture(t)
	alice := f.member(t, "alice")
	favorites := f.favorites(t, alice.ID)
	item, err := f.svc.Add(t.Context(), alice.ID, favorites, sunny)
	if want := (port.PlaylistItem{Track: sunny, Meta: sunnyMeta}); err != nil || item != want {
		t.Fatalf("Add = %#v, %v，期望 %#v", item, err, want)
	}
	if items, err := f.svc.Items(t.Context(), alice.ID, favorites); err != nil || !slices.Equal(items, []port.PlaylistItem{item}) {
		t.Errorf("Items = %#v, %v", items, err)
	}
	if want := []string{"playlist:member:" + alice.ID}; !slices.Equal(f.quota.keys, want) {
		t.Errorf("取的额度 = %v，期望 %v", f.quota.keys, want)
	}
	if err := f.svc.Remove(t.Context(), alice.ID, favorites, []track.Track{sunny}); err != nil {
		t.Fatal(err)
	}
	if items, _ := f.svc.Items(t.Context(), alice.ID, favorites); len(items) != 0 {
		t.Errorf("移除之后 = %#v，期望为空", items)
	}
}

func TestAddLinkNeedsPreparedTrack(t *testing.T) {
	f := newFixture(t)
	alice := f.member(t, "alice")
	favorites := f.favorites(t, alice.ID)
	if _, err := f.svc.Add(t.Context(), alice.ID, favorites, clip); !errors.Is(err, ErrTrackNotReady) {
		t.Errorf("没准备过的链接 = %v，期望 ErrTrackNotReady", err)
	}
	meta := track.Meta{Title: "a.mp3", CoverKey: "covers/abc", PageURL: clip.ID, Duration: time.Minute}
	f.tracks[clip] = port.PreparedTrack{Meta: meta}
	if item, err := f.svc.Add(t.Context(), alice.ID, favorites, clip); err != nil || item.Meta != meta {
		t.Errorf("准备过的链接 = %#v, %v，期望用曲目表里的元数据", item, err)
	}
}

func TestAddIsRateLimited(t *testing.T) {
	f := newFixture(t)
	alice := f.member(t, "alice")
	favorites := f.favorites(t, alice.ID)
	f.quota.exhausted = true
	if _, err := f.svc.Add(t.Context(), alice.ID, favorites, sunny); !errors.Is(err, ErrTooFast) {
		t.Errorf("额度用完时 = %v，期望 ErrTooFast", err)
	}
	if items, _ := f.svc.Items(t.Context(), alice.ID, favorites); len(items) != 0 {
		t.Errorf("额度用完时不应加入：%#v", items)
	}
}

type fullPlaylists struct{ *porttest.Store }

func (fullPlaylists) AddPlaylistItem(context.Context, string, string, port.PlaylistItem, int) error {
	return port.ErrPlaylistLimit
}

func (fullPlaylists) CopyPlaylistItems(context.Context, string, string, string, []track.Track, int) (int, error) {
	return 0, port.ErrPlaylistLimit
}

func TestAddReportsFullPlaylist(t *testing.T) {
	f := newFixture(t)
	alice := f.member(t, "alice")
	favorites := f.favorites(t, alice.ID)
	f.svc.playlists = fullPlaylists{f.store}
	if _, err := f.svc.Add(t.Context(), alice.ID, favorites, sunny); !errors.Is(err, ErrPlaylistFull) {
		t.Errorf("歌单满了时 = %v，期望 ErrPlaylistFull", err)
	}
}

func TestFavoritesStayFixed(t *testing.T) {
	f := newFixture(t)
	alice := f.member(t, "alice")
	favorites := f.favorites(t, alice.ID)
	if err := f.svc.Rename(t.Context(), alice.ID, favorites, "别的"); !errors.Is(err, port.ErrFavoritesFixed) {
		t.Errorf("“我喜欢”改名 = %v，期望 ErrFavoritesFixed", err)
	}
	if err := f.svc.Delete(t.Context(), alice.ID, favorites); !errors.Is(err, port.ErrFavoritesFixed) {
		t.Errorf("删除“我喜欢” = %v，期望 ErrFavoritesFixed", err)
	}
}

func TestCopyReportsFullPlaylist(t *testing.T) {
	f := newFixture(t)
	alice := f.member(t, "alice")
	favorites := f.favorites(t, alice.ID)
	commute, err := f.svc.Create(t.Context(), alice.ID, "通勤")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Add(t.Context(), alice.ID, favorites, sunny); err != nil {
		t.Fatal(err)
	}
	if n, err := f.svc.Copy(t.Context(), alice.ID, favorites, commute.ID, []track.Track{sunny}); err != nil || n != 1 {
		t.Errorf("复制 = %d, %v，期望 1", n, err)
	}
	if len(f.quota.keys) != 1 {
		t.Errorf("复制不应取收藏额度：%v", f.quota.keys)
	}
	f.svc.playlists = fullPlaylists{f.store}
	if _, err := f.svc.Copy(t.Context(), alice.ID, favorites, commute.ID, []track.Track{sunny}); !errors.Is(err, ErrPlaylistFull) {
		t.Errorf("歌单满了时 = %v，期望 ErrPlaylistFull", err)
	}
}

func TestMoveReordersAndReportsDomainErrors(t *testing.T) {
	f := newFixture(t)
	alice := f.member(t, "alice")
	favorites := f.favorites(t, alice.ID)
	if _, err := f.svc.Add(t.Context(), alice.ID, favorites, sunny); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Move(t.Context(), alice.ID, favorites, []track.Track{sunny}, &sunny); !errors.Is(err, playlistorder.ErrBeforeMoved) {
		t.Errorf("before 是被移动的歌 = %v，期望 ErrBeforeMoved", err)
	}
	missing := track.Track{Source: track.NetEase, ID: "1"}
	if err := f.svc.Move(t.Context(), alice.ID, favorites, []track.Track{sunny}, &missing); !errors.Is(err, playlistorder.ErrBeforeMissing) {
		t.Errorf("before 不在歌单里 = %v，期望 ErrBeforeMissing", err)
	}
	if err := f.svc.Move(t.Context(), alice.ID, favorites, []track.Track{sunny}, nil); err != nil {
		t.Errorf("移到最后 = %v", err)
	}
}
