package catalog

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/app/port/porttest"
	"github.com/dihemoe/alisten/internal/domain/room"
	"github.com/dihemoe/alisten/internal/domain/track"
)

type tracksTable map[track.Track]port.PreparedTrack

func (t tracksTable) PreparedTrack(_ context.Context, tr track.Track) (port.PreparedTrack, bool, error) {
	p, ok := t[tr]
	return p, ok, nil
}

func (t tracksTable) SavePreparedTrack(_ context.Context, tr track.Track, p port.PreparedTrack) error {
	t[tr] = p
	return nil
}

type history struct {
	seeds, queued, excluded []track.Track
}

func (h history) AutoDJSeeds(_ context.Context, _ string, includeQueue bool) ([]track.Track, error) {
	if includeQueue {
		return append(slices.Clone(h.seeds), h.queued...), nil
	}
	return h.seeds, nil
}

func (h history) AutoDJExclusions(context.Context, string) ([]track.Track, error) {
	return h.excluded, nil
}

type quota struct {
	mu    sync.Mutex
	waits []time.Duration
	calls int
}

func (q *quota) TakeSearch(_ context.Context, _ string, burst int, refill time.Duration) (time.Duration, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if burst != 10 || refill != time.Minute {
		return 0, errors.New("临时房间的额度是突发 10 次，之后每分钟恢复 1 次")
	}
	q.calls++
	if len(q.waits) == 0 {
		return 0, nil
	}
	wait := q.waits[0]
	q.waits = q.waits[1:]
	return wait, nil
}

type fixture struct {
	svc        *Service
	netease    *porttest.Source
	qq         *porttest.Source
	bilibili   *porttest.Bilibili
	tracks     tracksTable
	quota      *quota
	ephemeral  string
	persistent string
}

func song(source track.Source, id, title string) port.Song {
	return port.Song{Track: track.Track{Source: source, ID: id}, Meta: track.Meta{Title: title, Artist: "周杰伦", Duration: 4 * time.Minute}}
}

func newFixture(t *testing.T, h history) *fixture {
	t.Helper()
	f := &fixture{netease: porttest.NewSource(track.NetEase), qq: porttest.NewSource(track.QQ), bilibili: porttest.NewBilibili(), tracks: tracksTable{}, quota: &quota{}}
	rooms := porttest.New()
	var err error
	if f.ephemeral, err = rooms.CreateRoom(t.Context(), port.NewRoom{Kind: room.Ephemeral, Name: "临时"}); err != nil {
		t.Fatal(err)
	}
	if f.persistent, err = rooms.CreateRoom(t.Context(), port.NewRoom{Kind: room.Persistent, Name: "常驻"}); err != nil {
		t.Fatal(err)
	}
	f.svc = New(f.netease, f.qq, f.bilibili, f.bilibili, f.tracks, h, rooms, f.quota)
	return f
}

func (f *fixture) add(s *porttest.Source, songs ...port.Song) {
	for _, song := range songs {
		s.Add(song.Track.ID, song.Meta, "", nil)
	}
}

func TestSearchTracksPaginates(t *testing.T) {
	f := newFixture(t, history{})
	songs := []port.Song{song(track.NetEase, "1", "晴天"), song(track.NetEase, "2", "晴天 (Live)"), song(track.NetEase, "3", "晴天 (伴奏)")}
	f.add(f.netease, songs...)
	f.add(f.qq, song(track.QQ, "0039MnYb0qxYhV", "晴天"))

	cases := []struct {
		page, size int64
		want       []port.Song
	}{
		{1, 2, songs[:2]},
		{2, 2, songs[2:]},
		{3, 2, nil},
		{1 << 62, 100, nil},
	}
	for _, c := range cases {
		got, total, err := f.svc.SearchTracks(t.Context(), f.persistent, track.NetEase, "晴天", c.page, c.size)
		if err != nil || total != 3 || !reflect.DeepEqual(got, c.want) {
			t.Errorf("第 %d 页（每页 %d 条）= %v, %d, %v，期望 %v 和总数 3", c.page, c.size, got, total, err, c.want)
		}
	}
	if got, total, err := f.svc.SearchTracks(t.Context(), f.persistent, track.QQ, "晴天", 1, 10); err != nil || total != 1 || got[0].Track.ID != "0039MnYb0qxYhV" {
		t.Errorf("QQ 的搜索结果 = %v, %d, %v", got, total, err)
	}
	if _, _, err := f.svc.SearchTracks(t.Context(), f.persistent, track.Link, "晴天", 1, 10); !errors.Is(err, port.ErrProviderFailed) {
		t.Errorf("搜索链接的错误 = %v，期望 ErrProviderFailed", err)
	}
	f.netease.Fail(errors.New("网易云不可用"))
	if _, _, err := f.svc.SearchTracks(t.Context(), f.persistent, track.NetEase, "晴天", 1, 10); !errors.Is(err, port.ErrProviderFailed) {
		t.Errorf("上游出错时的错误 = %v，期望 ErrProviderFailed", err)
	}
}

func TestSearchQuota(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, history{})
		f.add(f.netease, song(track.NetEase, "1", "晴天"))

		f.quota.waits = []time.Duration{20 * time.Second}
		start := time.Now()
		if _, _, err := f.svc.SearchTracks(t.Context(), f.ephemeral, track.NetEase, "晴天", 1, 10); err != nil {
			t.Fatal(err)
		}
		if waited := time.Since(start); waited != 20*time.Second {
			t.Errorf("等了 %v，期望等到有额度为止", waited)
		}

		f.quota.waits = []time.Duration{30 * time.Second}
		start = time.Now()
		if _, err := f.svc.RecommendList(t.Context(), f.ephemeral); err != nil {
			t.Errorf("预计等待恰好 30 秒时的错误 = %v，期望等到有额度为止", err)
		}
		if waited := time.Since(start); waited != 30*time.Second {
			t.Errorf("等了 %v，期望 30 秒", waited)
		}

		f.quota.waits = []time.Duration{31 * time.Second}
		if _, err := f.svc.RecommendList(t.Context(), f.ephemeral); !errors.Is(err, ErrRateLimited) {
			t.Errorf("预计等待超过 30 秒时的错误 = %v，期望 ErrRateLimited", err)
		}

		calls := f.quota.calls
		if _, _, err := f.svc.SearchPlaylists(t.Context(), f.persistent, track.NetEase, "晴天", 1, 10); err != nil {
			t.Fatal(err)
		}
		if _, _, err := f.svc.FirstMatch(t.Context(), track.NetEase, "晴天"); err != nil {
			t.Fatal(err)
		}
		if f.quota.calls != calls {
			t.Error("常驻房间的搜索和按关键词点歌都不消耗额度")
		}
	})
}

func TestTrackMetaPrefersTracksTable(t *testing.T) {
	f := newFixture(t, history{})
	sunny := song(track.NetEase, "1", "晴天")
	f.add(f.netease, sunny)
	prepared := track.Meta{Title: "晴天", Artist: "周杰伦", PageURL: "https://music.163.com/#/song?id=1", Duration: 269500 * time.Millisecond}
	f.tracks[sunny.Track] = port.PreparedTrack{Meta: prepared}

	f.netease.Fail(errors.New("不应请求上游"))
	if got, err := f.svc.TrackMeta(t.Context(), sunny.Track); err != nil || got != prepared {
		t.Errorf("曲目表中已有的歌 = %v, %v，期望 %v", got, err, prepared)
	}
	f.netease.Fail(nil)
	delete(f.tracks, sunny.Track)
	if got, err := f.svc.TrackMeta(t.Context(), sunny.Track); err != nil || got != sunny.Meta {
		t.Errorf("曲目表中没有的歌 = %v, %v，期望上游的元数据 %v", got, err, sunny.Meta)
	}
	if _, err := f.svc.TrackMeta(t.Context(), track.Track{Source: track.NetEase, ID: "404"}); !errors.Is(err, port.ErrProviderFailed) {
		t.Errorf("上游没有的歌的错误 = %v，期望 ErrProviderFailed", err)
	}
}

func TestFirstMatch(t *testing.T) {
	f := newFixture(t, history{})
	f.add(f.qq, song(track.QQ, "a", "晴天"), song(track.QQ, "b", "晴天 (Live)"))
	if got, m, err := f.svc.FirstMatch(t.Context(), track.QQ, "晴天"); err != nil || got.ID != "a" || m.Title != "晴天" {
		t.Errorf("按关键词点歌 = %v, %v, %v，期望第一条搜索结果", got, m, err)
	}
	if _, _, err := f.svc.FirstMatch(t.Context(), track.QQ, "七里香"); !errors.Is(err, port.ErrProviderFailed) {
		t.Errorf("没有搜索结果时的错误 = %v，期望 ErrProviderFailed", err)
	}
}

func TestTrackMetaOfBilibili(t *testing.T) {
	f := newFixture(t, history{})
	video := track.Track{Source: track.Bilibili, ID: "BV1xx411c7mD/2"}
	f.bilibili.AddVideo(video.ID, track.Meta{Title: "晴天（钢琴版）", Artist: "某 UP 主", Duration: 2 * time.Minute}, nil, nil, "")
	want := track.Meta{Title: "晴天（钢琴版）", Artist: "某 UP 主", PageURL: "https://www.bilibili.com/video/BV1xx411c7mD?p=2", Duration: 2 * time.Minute}
	if got, err := f.svc.TrackMeta(t.Context(), video); err != nil || got != want {
		t.Errorf("B 站的元数据 = %+v, %v，期望 %+v：页面地址按 ID 拼出", got, err, want)
	}
	prepared := track.Meta{Title: "晴天（钢琴版）", CoverKey: "link/k/cover.jpg", PageURL: want.PageURL, Duration: 2 * time.Minute}
	f.tracks[video] = port.PreparedTrack{Meta: prepared}
	f.bilibili.Fail(errors.New("不应请求上游"))
	if got, err := f.svc.TrackMeta(t.Context(), video); err != nil || got != prepared {
		t.Errorf("曲目表中已有的 B 站的歌 = %+v, %v，期望 %+v", got, err, prepared)
	}
}

func TestResolveLink(t *testing.T) {
	f := newFixture(t, history{})
	const (
		video   = "https://www.bilibili.com/video/BV1xx411c7mD?p=2"
		short   = "https://b23.tv/qingtian"
		bangumi = "https://www.bilibili.com/bangumi/play/ep1"
		youtube = "https://www.youtube.com/watch?v=x"
	)
	f.bilibili.AddVideo("BV1xx411c7mD/2", track.Meta{Title: "晴天（钢琴版）", Artist: "某 UP 主"}, nil, nil, "")
	f.bilibili.AddLink(video, "BV1xx411c7mD/2")
	f.bilibili.AddLink(short, "BV1xx411c7mD/2")
	f.bilibili.RejectLink(bangumi, "哔哩哔哩的番剧不能点播")
	want := track.Track{Source: track.Bilibili, ID: "BV1xx411c7mD/2"}
	for _, link := range []string{video, short} {
		got, m, err := f.svc.ResolveLink(t.Context(), link)
		if err != nil || got != want || m.Title != "晴天（钢琴版）" || m.PageURL != "https://www.bilibili.com/video/BV1xx411c7mD?p=2" {
			t.Errorf("ResolveLink(%s) = %v, %+v, %v，期望 B 站的 %v", link, got, m, err, want)
		}
	}
	got, m, err := f.svc.ResolveLink(t.Context(), youtube)
	if err != nil || got != (track.Track{Source: track.Link, ID: youtube}) || m != (track.Meta{Title: youtube, PageURL: youtube}) {
		t.Errorf("其他链接 = %v, %+v, %v，期望仍是链接曲目，标题和页面都是链接本身", got, m, err)
	}
	var rejected *RejectedLinkError
	if _, _, err := f.svc.ResolveLink(t.Context(), bangumi); !errors.As(err, &rejected) || err.Error() != "哔哩哔哩的番剧不能点播" {
		t.Errorf("番剧的链接 = %v，期望 RejectedLinkError，文本是音源给出的原因", err)
	}
}

func TestResolveLinkFailures(t *testing.T) {
	f := newFixture(t, history{})
	const link = "https://www.bilibili.com/video/BV1xx411c7mD"
	// 音源里也登记这个 ID：不检查格式时，它会被当作 B 站的曲目点进队列。
	f.bilibili.AddVideo("BV1xx411c7mD/1", track.Meta{Title: "第 1 P"}, nil, nil, "")
	f.bilibili.AddLink(link, "BV1xx411c7mD/1")
	if _, _, err := f.svc.ResolveLink(t.Context(), link); !errors.Is(err, port.ErrProviderFailed) {
		t.Errorf("音源给出的曲目 ID 格式不对时 = %v，期望 ErrProviderFailed", err)
	}
	f.bilibili.AddLink(link, "BV1xx411c7mD")
	if _, _, err := f.svc.ResolveLink(t.Context(), link); !errors.Is(err, port.ErrProviderFailed) {
		t.Errorf("音源中查不到这个视频时 = %v，期望 ErrProviderFailed", err)
	}
	f.bilibili.Fail(errors.New("连接被重置"))
	var rejected *RejectedLinkError
	if _, _, err := f.svc.ResolveLink(t.Context(), link); !errors.Is(err, port.ErrProviderFailed) || errors.As(err, &rejected) {
		t.Errorf("识别链接出错时 = %v，期望 ErrProviderFailed", err)
	}
}

func netease(id string) track.Track { return track.Track{Source: track.NetEase, ID: id} }

func TestRecommend(t *testing.T) {
	h := history{seeds: []track.Track{netease("a"), netease("b")}, queued: []track.Track{netease("q")}, excluded: []track.Track{netease("x")}}
	f := newFixture(t, h)
	for _, id := range []string{"a", "b", "q", "x", "c", "d", "e", "f"} {
		f.add(f.netease, song(track.NetEase, id, "歌"+id))
	}
	f.netease.SetSimilar("a", "x", "c", "b", "d")
	f.netease.SetSimilar("b", "c", "e")
	f.netease.SetSimilar("q", "f")

	want := []track.Track{netease("c"), netease("d"), netease("e")}
	got, m, ok, err := f.svc.Recommend(t.Context(), f.persistent)
	if err != nil || !ok || !slices.Contains(want, got) || m.Title != "歌"+got.ID {
		t.Errorf("推荐 = %v, %v, %v, %v，期望从 %v 中选一首", got, m, ok, err, want)
	}

	list, err := f.svc.RecommendList(t.Context(), f.persistent)
	if err != nil {
		t.Fatal(err)
	}
	var ids []track.Track
	for _, s := range list {
		ids = append(ids, s.Track)
	}
	if want := append(want, netease("f")); !slices.Equal(ids, want) {
		t.Errorf("推荐列表 = %v，期望 %v：种子还包括队列中的歌，去掉排除项和种子本身", ids, want)
	}
}

type similarCalls struct {
	port.NetEaseSource
	seeds []string
}

func (s *similarCalls) SimilarTracks(ctx context.Context, id string) ([]port.Song, error) {
	s.seeds = append(s.seeds, id)
	return s.NetEaseSource.SimilarTracks(ctx, id)
}

func TestRecommendLimits(t *testing.T) {
	for _, tt := range []struct {
		candidates int
		seeds      []string
	}{
		{26, []string{"s1"}},
	} {
		f := newFixture(t, history{seeds: []track.Track{netease("s1"), netease("s2")}})
		var similar []string
		for i := range tt.candidates {
			id := string(rune('A' + i))
			f.add(f.netease, song(track.NetEase, id, id))
			similar = append(similar, id)
		}
		f.add(f.netease, song(track.NetEase, "late", "late"))
		f.netease.SetSimilar("s1", similar...)
		f.netease.SetSimilar("s2", "late")
		calls := &similarCalls{NetEaseSource: f.netease}
		f.svc.netease = calls

		list, err := f.svc.RecommendList(t.Context(), f.persistent)
		if err != nil {
			t.Fatal(err)
		}
		if len(list) != 10 || list[0].Track.ID != "A" || list[9].Track.ID != "J" {
			t.Errorf("%d 首候选的推荐列表 = %v，期望前 10 首候选", tt.candidates, list)
		}
		if !slices.Equal(calls.seeds, tt.seeds) {
			t.Errorf("%d 首候选之后获取了 %v 的相似歌曲，期望 %v", tt.candidates, calls.seeds, tt.seeds)
		}
	}
}

func TestNoRecommendation(t *testing.T) {
	f := newFixture(t, history{})
	if _, _, ok, err := f.svc.Recommend(t.Context(), f.persistent); ok || err != nil {
		t.Errorf("没有种子时 = %v, %v，期望没有推荐结果", ok, err)
	}

	f = newFixture(t, history{seeds: []track.Track{netease("a")}})
	f.netease.Fail(errors.New("网易云不可用"))
	if _, _, ok, err := f.svc.Recommend(t.Context(), f.persistent); ok || err == nil {
		t.Errorf("接口出错时 = %v, %v，期望没有推荐结果，错误用于记录日志", ok, err)
	}
	if list, err := f.svc.RecommendList(t.Context(), f.persistent); err != nil || len(list) != 0 {
		t.Errorf("接口出错时的推荐列表 = %v, %v，期望为空", list, err)
	}
}
