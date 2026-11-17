package playlist

import (
	"context"
	"crypto/sha256"
	"errors"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dihemoe/alisten/internal/app/catalog"
	"github.com/dihemoe/alisten/internal/app/identity"
	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/app/port/porttest"
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

func (noHistory) AutoDJSeeds(context.Context, string, bool, port.AutoDJWindow) ([]track.Track, error) {
	return nil, nil
}

func (noHistory) AutoDJExclusions(context.Context, string, port.AutoDJWindow) ([]track.Track, error) {
	return nil, nil
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
	svc      *Service
	identity *identity.Service
	store    *porttest.Store
	tracks   tracksTable
	quota    *accountQuota
	netease  *porttest.Source
	qq       *porttest.Source
	links    shortLinks
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	store := porttest.New()
	netease := porttest.NewSource(track.NetEase)
	netease.Add(sunny.ID, sunnyMeta, "", nil)
	qq := porttest.NewSource(track.QQ)
	bilibili := porttest.NewBilibili()
	tracks := tracksTable{}
	music := catalog.New(netease, qq, bilibili, bilibili, tracks, noHistory{}, store, porttest.Quota{}, slog.New(slog.DiscardHandler))
	q := &accountQuota{}
	links := shortLinks{}
	return fixture{svc: New(store, tracks, music, q, links), identity: identity.New(store, store, store, porttest.Quota{}, nil), store: store, tracks: tracks, quota: q, netease: netease, qq: qq, links: links}
}

// 用户名兼作成员 token，所以按用户名的 SHA-256 建会话。
func (f fixture) account(t *testing.T, username string) identity.AccountOwner {
	t.Helper()
	hash := sha256.Sum256([]byte(username))
	m, err := f.store.CreateMember(t.Context(), hash[:], "游客-"+username)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.Register(t.Context(), m.ID, username, "hash"); err != nil {
		t.Fatal(err)
	}
	caller, err := f.identity.Authenticate(t.Context(), username)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := caller.AccountOwner()
	if err != nil {
		t.Fatal(err)
	}
	return owner
}

func (f fixture) favorites(t *testing.T, owner identity.AccountOwner) string {
	t.Helper()
	lists, err := f.svc.List(t.Context(), owner)
	if err != nil || len(lists) == 0 || !lists[0].Favorites {
		t.Fatalf("歌单 = %#v, %v", lists, err)
	}
	return lists[0].ID
}

func TestCreateValidatesName(t *testing.T) {
	f := newFixture(t)
	alice := f.account(t, "alice")
	p, err := f.svc.Create(t.Context(), alice, "  通勤  ")
	if err != nil || p.Name != "通勤" {
		t.Fatalf("Create = %#v, %v，期望去掉首尾空白", p, err)
	}
	for _, name := range []string{"", "   ", strings.Repeat("歌", 51), "通\n勤", "\u200b"} {
		if _, err := f.svc.Create(t.Context(), alice, name); !errors.Is(err, ErrInvalidName) {
			t.Errorf("Create(%q) = %v，期望 ErrInvalidName", name, err)
		}
		if err := f.svc.Rename(t.Context(), alice, p.ID, name); !errors.Is(err, ErrInvalidName) {
			t.Errorf("Rename(%q) = %v，期望 ErrInvalidName", name, err)
		}
	}
	if _, err := f.svc.Create(t.Context(), alice, strings.Repeat("歌", 50)); err != nil {
		t.Errorf("50 个字的歌单名 = %v，期望可以", err)
	}
}

func TestCreateStopsAtFiftyPlaylists(t *testing.T) {
	f := newFixture(t)
	alice := f.account(t, "alice")
	for i := range 49 {
		if _, err := f.svc.Create(t.Context(), alice, "歌单"+strconv.Itoa(i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.svc.Create(t.Context(), alice, "第 51 个"); !errors.Is(err, ErrTooManyPlaylists) {
		t.Errorf("第 51 个歌单 = %v，期望 ErrTooManyPlaylists", err)
	}
}

func TestAddTakesMetaFromTheServer(t *testing.T) {
	f := newFixture(t)
	alice := f.account(t, "alice")
	favorites := f.favorites(t, alice)
	item, err := f.svc.Add(t.Context(), alice, favorites, sunny)
	if want := (port.PlaylistItem{Track: sunny, Meta: sunnyMeta}); err != nil || item != want {
		t.Fatalf("Add = %#v, %v，期望 %#v", item, err, want)
	}
	if items, err := f.svc.Items(t.Context(), alice, favorites); err != nil || !slices.Equal(items, []port.PlaylistItem{item}) {
		t.Errorf("Items = %#v, %v", items, err)
	}
	if want := []string{"playlist:member:" + alice.MemberID()}; !slices.Equal(f.quota.keys, want) {
		t.Errorf("取的额度 = %v，期望 %v", f.quota.keys, want)
	}
	if err := f.svc.Remove(t.Context(), alice, favorites, []track.Track{sunny}); err != nil {
		t.Fatal(err)
	}
	if items, _ := f.svc.Items(t.Context(), alice, favorites); len(items) != 0 {
		t.Errorf("移除之后 = %#v，期望为空", items)
	}
}

func TestAddLinkNeedsPreparedTrack(t *testing.T) {
	f := newFixture(t)
	alice := f.account(t, "alice")
	favorites := f.favorites(t, alice)
	if _, err := f.svc.Add(t.Context(), alice, favorites, clip); !errors.Is(err, ErrTrackNotReady) {
		t.Errorf("没准备过的链接 = %v，期望 ErrTrackNotReady", err)
	}
	meta := track.Meta{Title: "a.mp3", CoverKey: "covers/abc", PageURL: clip.ID, Duration: time.Minute}
	f.tracks[clip] = port.PreparedTrack{Meta: meta}
	if item, err := f.svc.Add(t.Context(), alice, favorites, clip); err != nil || item.Meta != meta {
		t.Errorf("准备过的链接 = %#v, %v，期望用曲目表里的元数据", item, err)
	}
}

func TestAddIsRateLimited(t *testing.T) {
	f := newFixture(t)
	alice := f.account(t, "alice")
	favorites := f.favorites(t, alice)
	f.quota.exhausted = true
	if _, err := f.svc.Add(t.Context(), alice, favorites, sunny); !errors.Is(err, ErrTooFast) {
		t.Errorf("额度用完时 = %v，期望 ErrTooFast", err)
	}
	if items, _ := f.svc.Items(t.Context(), alice, favorites); len(items) != 0 {
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
	alice := f.account(t, "alice")
	favorites := f.favorites(t, alice)
	f.svc.playlists = fullPlaylists{f.store}
	if _, err := f.svc.Add(t.Context(), alice, favorites, sunny); !errors.Is(err, ErrPlaylistFull) {
		t.Errorf("歌单满了时 = %v，期望 ErrPlaylistFull", err)
	}
}

func TestFavoritesStayFixed(t *testing.T) {
	f := newFixture(t)
	alice := f.account(t, "alice")
	favorites := f.favorites(t, alice)
	if err := f.svc.Rename(t.Context(), alice, favorites, "别的"); !errors.Is(err, port.ErrFavoritesFixed) {
		t.Errorf("“我喜欢”改名 = %v，期望 ErrFavoritesFixed", err)
	}
	if err := f.svc.Delete(t.Context(), alice, favorites); !errors.Is(err, port.ErrFavoritesFixed) {
		t.Errorf("删除“我喜欢” = %v，期望 ErrFavoritesFixed", err)
	}
}

func TestCopyReportsFullPlaylist(t *testing.T) {
	f := newFixture(t)
	alice := f.account(t, "alice")
	favorites := f.favorites(t, alice)
	commute, err := f.svc.Create(t.Context(), alice, "通勤")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Add(t.Context(), alice, favorites, sunny); err != nil {
		t.Fatal(err)
	}
	if n, err := f.svc.Copy(t.Context(), alice, favorites, commute.ID, []track.Track{sunny}); err != nil || n != 1 {
		t.Errorf("复制 = %d, %v，期望 1", n, err)
	}
	if len(f.quota.keys) != 1 {
		t.Errorf("复制不应取收藏额度：%v", f.quota.keys)
	}
	f.svc.playlists = fullPlaylists{f.store}
	if _, err := f.svc.Copy(t.Context(), alice, favorites, commute.ID, []track.Track{sunny}); !errors.Is(err, ErrPlaylistFull) {
		t.Errorf("歌单满了时 = %v，期望 ErrPlaylistFull", err)
	}
}

func TestMoveReordersAndReportsDomainErrors(t *testing.T) {
	f := newFixture(t)
	alice := f.account(t, "alice")
	favorites := f.favorites(t, alice)
	if _, err := f.svc.Add(t.Context(), alice, favorites, sunny); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Move(t.Context(), alice, favorites, []track.Track{sunny}, &sunny); !errors.Is(err, playlistorder.ErrBeforeMoved) {
		t.Errorf("before 是被移动的歌 = %v，期望 ErrBeforeMoved", err)
	}
	missing := track.Track{Source: track.NetEase, ID: "1"}
	if err := f.svc.Move(t.Context(), alice, favorites, []track.Track{sunny}, &missing); !errors.Is(err, playlistorder.ErrBeforeMissing) {
		t.Errorf("before 不在歌单里 = %v，期望 ErrBeforeMissing", err)
	}
	if err := f.svc.Move(t.Context(), alice, favorites, []track.Track{sunny}, nil); err != nil {
		t.Errorf("移到最后 = %v", err)
	}
}
