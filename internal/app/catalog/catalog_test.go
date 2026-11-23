package catalog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"slices"
	"strings"
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

func (h history) AutoDJSeeds(_ context.Context, _ string, includeQueue bool, _ port.AutoDJWindow) ([]track.Track, error) {
	if includeQueue {
		return append(slices.Clone(h.seeds), h.queued...), nil
	}
	return h.seeds, nil
}

func (h history) AutoDJExclusions(context.Context, string, port.AutoDJWindow) ([]track.Track, error) {
	return h.excluded, nil
}

type quota struct {
	mu        sync.Mutex
	room      string
	waits     []time.Duration
	calls     int
	pickWait  time.Duration
	pickCalls int
}

func (q *quota) Take(_ context.Context, key string, burst int, refill time.Duration) (time.Duration, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	switch key {
	case "room:" + q.room + ":search":
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
	case "room:" + q.room + ":pick":
		if burst != 5 || refill != time.Minute {
			return 0, errors.New("临时房间的点歌额度与房间的点歌限流相同：突发 5 次，之后每分钟恢复 1 次")
		}
		q.pickCalls++
		return q.pickWait, nil
	}
	return 0, fmt.Errorf("取了 %q 的额度，期望是房间 %s 的搜索额度或点歌额度", key, q.room)
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
	if f.ephemeral, err = rooms.CreateRoom(t.Context(), port.NewRoom{Kind: room.Ephemeral, Name: "临时", Capacity: 16}); err != nil {
		t.Fatal(err)
	}
	if f.persistent, err = rooms.CreateRoom(t.Context(), port.NewRoom{Kind: room.Persistent, Name: "常驻", Capacity: 64}); err != nil {
		t.Fatal(err)
	}
	f.quota.room = f.ephemeral
	f.svc = New(f.netease, f.qq, f.bilibili, f.bilibili, f.tracks, h, rooms, f.quota, slog.New(slog.DiscardHandler))
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
	if _, _, err := f.svc.SearchTracks(t.Context(), f.persistent, track.Link, "晴天", 1, 10); !errors.Is(err, ErrUnsupported) {
		t.Errorf("搜索链接的错误 = %v，期望 ErrUnsupported", err)
	}
	f.netease.Fail(errors.New("网易云不可用"))
	if _, _, err := f.svc.SearchTracks(t.Context(), f.persistent, track.NetEase, "晴天", 1, 10); !errors.Is(err, port.ErrSourceFailed) {
		t.Errorf("上游出错时的错误 = %v，期望 ErrSourceFailed", err)
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

func TestTakePick(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, history{})
		if err := f.svc.TakePick(t.Context(), f.ephemeral); err != nil || f.quota.pickCalls != 1 {
			t.Fatalf("临时房间 = %v，取了 %d 次点歌额度，期望取到 1 次", err, f.quota.pickCalls)
		}

		f.quota.pickWait = time.Minute
		start := time.Now()
		var roomErr *room.Error
		if err := f.svc.TakePick(t.Context(), f.ephemeral); !errors.As(err, &roomErr) || roomErr.Kind != room.RateLimited || roomErr.Message != "这个房间点歌和移除的次数用完了，60 秒后再试。" {
			t.Errorf("点歌额度用完时的错误 = %v，期望和房间一样说明是房间的额度、60 秒后再试", err)
		}
		if waited := time.Since(start); waited != 0 {
			t.Errorf("等了 %v，期望与房间的点歌限流一样立即拒绝", waited)
		}

		calls := f.quota.pickCalls
		if err := f.svc.TakePick(t.Context(), f.persistent); err != nil || f.quota.pickCalls != calls {
			t.Errorf("常驻房间 = %v，取了 %d 次点歌额度，期望不取", err, f.quota.pickCalls-calls)
		}
		if f.quota.calls != 0 {
			t.Errorf("取了 %d 次搜索额度，点歌只取点歌额度", f.quota.calls)
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
	if _, err := f.svc.TrackMeta(t.Context(), track.Track{Source: track.NetEase, ID: "404"}); !errors.Is(err, port.ErrSourceFailed) {
		t.Errorf("上游没有的歌的错误 = %v，期望 ErrSourceFailed", err)
	}
}

func TestFirstMatch(t *testing.T) {
	f := newFixture(t, history{})
	f.add(f.qq, song(track.QQ, "a", "晴天"), song(track.QQ, "b", "晴天 (Live)"))
	if got, m, err := f.svc.FirstMatch(t.Context(), track.QQ, "晴天"); err != nil || got.ID != "a" || m.Title != "晴天" {
		t.Errorf("按关键词点歌 = %v, %v, %v，期望第一条搜索结果", got, m, err)
	}
	if _, _, err := f.svc.FirstMatch(t.Context(), track.QQ, "七里香"); !errors.Is(err, ErrNoMatch) {
		t.Errorf("没有搜索结果时的错误 = %v，期望 ErrNoMatch", err)
	}
	if _, _, err := f.svc.FirstMatch(t.Context(), track.Bilibili, "晴天"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("在 B 站按关键词点歌 = %v，期望 ErrUnsupported", err)
	}
	f.qq.Fail(errors.New("QQ 音乐不可用"))
	if _, _, err := f.svc.FirstMatch(t.Context(), track.QQ, "晴天"); !errors.Is(err, port.ErrSourceFailed) {
		t.Errorf("上游出错时的错误 = %v，期望 ErrSourceFailed", err)
	}
}

func TestPlaylistSongsSkipsRoomQuota(t *testing.T) {
	f := newFixture(t, history{})
	songs := []port.Song{song(track.NetEase, "1", "晴天"), song(track.NetEase, "2", "七里香")}
	f.add(f.netease, songs...)
	f.netease.AddPlaylist(port.Playlist{ID: "p1", Name: "周杰伦"}, "2", "1")
	if got, err := f.svc.PlaylistSongs(t.Context(), track.NetEase, "p1"); err != nil || !reflect.DeepEqual(got, []port.Song{songs[1], songs[0]}) {
		t.Errorf("歌单内的歌曲 = %v, %v，期望按歌单的顺序", got, err)
	}
	if f.quota.calls != 0 {
		t.Errorf("取了 %d 次房间的搜索额度，导入不走房间的额度", f.quota.calls)
	}
	qqSongs := []port.Song{song(track.QQ, "0039MnYb0qxYhV", "晴天"), song(track.QQ, "004Z8Ihr0JIu5s", "七里香")}
	f.add(f.qq, qqSongs...)
	f.qq.AddPlaylist(port.Playlist{ID: "q1", Name: "周杰伦"}, "004Z8Ihr0JIu5s", "0039MnYb0qxYhV")
	if got, err := f.svc.PlaylistSongs(t.Context(), track.QQ, "q1"); err != nil || !reflect.DeepEqual(got, []port.Song{qqSongs[1], qqSongs[0]}) {
		t.Errorf("QQ 音乐歌单内的歌曲 = %v, %v，期望按歌单的顺序", got, err)
	}
	if _, err := f.svc.PlaylistSongs(t.Context(), track.Bilibili, "p1"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("B 站的歌单 = %v，期望 ErrUnsupported", err)
	}
	f.netease.Fail(errors.New("网易云不可用"))
	if _, err := f.svc.PlaylistSongs(t.Context(), track.NetEase, "p1"); !errors.Is(err, port.ErrSourceFailed) {
		t.Errorf("上游出错时的错误 = %v，期望 ErrSourceFailed", err)
	}
}

func TestPlaylistTracksTakesRoomQuota(t *testing.T) {
	f := newFixture(t, history{})
	songs := []port.Song{song(track.QQ, "0039MnYb0qxYhV", "晴天")}
	f.add(f.qq, songs...)
	f.qq.AddPlaylist(port.Playlist{ID: "q1", Name: "周杰伦"}, "0039MnYb0qxYhV")
	if got, err := f.svc.PlaylistTracks(t.Context(), f.ephemeral, track.QQ, "q1"); err != nil || !reflect.DeepEqual(got, songs) || f.quota.calls != 1 {
		t.Errorf("在临时房间里打开 QQ 音乐的歌单 = %v, %v，取了 %d 次额度，期望取到歌曲、取 1 次额度", got, err, f.quota.calls)
	}
	if _, err := f.svc.PlaylistTracks(t.Context(), f.ephemeral, track.Bilibili, "q1"); !errors.Is(err, ErrUnsupported) || f.quota.calls != 1 {
		t.Errorf("B 站的歌单 = %v，取了 %d 次额度，期望 ErrUnsupported，而且不取额度", err, f.quota.calls)
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
		t.Errorf("番剧的链接 = %v，期望 RejectedLinkError，文本是音乐平台给出的原因", err)
	}
}

func TestResolveLinkFailures(t *testing.T) {
	f := newFixture(t, history{})
	const link = "https://www.bilibili.com/video/BV1xx411c7mD"
	// 音乐平台里也登记这个 ID，这样不检查格式的话，这个 ID 就会被当作 B 站的曲目点进队列。
	f.bilibili.AddVideo("BV1xx411c7mD/1", track.Meta{Title: "第 1 P"}, nil, nil, "")
	f.bilibili.AddLink(link, "BV1xx411c7mD/1")
	if _, _, err := f.svc.ResolveLink(t.Context(), link); !errors.Is(err, port.ErrSourceFailed) {
		t.Errorf("音乐平台给出的曲目 ID 格式不对时 = %v，期望 ErrSourceFailed", err)
	}
	f.bilibili.AddLink(link, "BV1xx411c7mD")
	if _, _, err := f.svc.ResolveLink(t.Context(), link); !errors.Is(err, port.ErrSourceFailed) {
		t.Errorf("音乐平台上查不到这个视频时 = %v，期望 ErrSourceFailed", err)
	}
	f.bilibili.Fail(errors.New("连接被重置"))
	var rejected *RejectedLinkError
	if _, _, err := f.svc.ResolveLink(t.Context(), link); !errors.Is(err, port.ErrSourceFailed) || errors.As(err, &rejected) {
		t.Errorf("识别链接出错时 = %v，期望 ErrSourceFailed", err)
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
	slices.SortFunc(ids, func(a, b track.Track) int { return strings.Compare(a.ID, b.ID) })
	if want := append(want, netease("f")); !slices.Equal(ids, want) {
		t.Errorf("推荐列表 = %v，期望是 %v 这几首：种子还包括队列中的歌，去掉排除项和种子本身", ids, want)
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
	// 推荐列表最多 10 首。候选凑够 10 首就不再获取下一个种子的相似歌曲，不够时才继续。
	for _, tt := range []struct {
		candidates int
		seeds      []string
	}{
		{26, []string{"s1"}},
		{10, []string{"s1"}},
		{9, []string{"s1", "s2"}},
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
		similarOf := map[string][]string{"s1": similar, "s2": {"late"}}
		fetched := map[string]bool{}
		for _, seed := range tt.seeds {
			for _, id := range similarOf[seed] {
				fetched[id] = true
			}
		}
		listed := map[string]bool{}
		for _, s := range list {
			if fetched[s.Track.ID] {
				listed[s.Track.ID] = true
			}
		}
		if len(list) != 10 || len(listed) != 10 {
			t.Errorf("%d 首候选的推荐列表 = %v，期望是已经获取的相似歌曲里不重复的 10 首", tt.candidates, list)
		}
		if !slices.Equal(calls.seeds, tt.seeds) {
			t.Errorf("%d 首候选之后获取了 %v 的相似歌曲，期望 %v", tt.candidates, calls.seeds, tt.seeds)
		}
	}
}

func TestRecommendListDrawsFromAllCandidates(t *testing.T) {
	// 推荐列表每次从全部候选里随机抽，自动补歌仍只从前 10 首里挑。
	f := newFixture(t, history{seeds: []track.Track{netease("s1")}})
	var similar []string
	for i := range 26 {
		id := string(rune('A' + i))
		f.add(f.netease, song(track.NetEase, id, id))
		similar = append(similar, id)
	}
	f.netease.SetSimilar("s1", similar...)

	// 每次抽中某一首的概率是 10/26，50 次里有任何一首从没抽中的概率不到 1e-9。
	listed := map[string]bool{}
	for range 50 {
		list, err := f.svc.RecommendList(t.Context(), f.persistent)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range list {
			listed[s.Track.ID] = true
		}
	}
	if len(listed) != len(similar) {
		t.Errorf("获取 50 次推荐列表，出现过 %d 首候选，期望全部 %d 首都出现过", len(listed), len(similar))
	}
	for range 50 {
		if got, _, ok, err := f.svc.Recommend(t.Context(), f.persistent); err != nil || !ok || got.ID > "J" {
			t.Fatalf("自动补歌 = %v, %v, %v，期望从前 10 首 A 到 J 里挑", got, ok, err)
		}
	}
}

func TestRecommendListLogsSourceFailuresWhenTheyStart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newFixture(t, history{seeds: []track.Track{netease("a")}})
		var logs bytes.Buffer
		f.svc.logger = slog.New(slog.NewJSONHandler(&logs, nil))
		warnings := func() []map[string]any {
			t.Helper()
			var found []map[string]any
			for line := range strings.Lines(strings.TrimSpace(logs.String())) {
				var entry map[string]any
				if err := json.Unmarshal([]byte(line), &entry); err != nil {
					t.Fatalf("日志不是 JSON：%v\n%s", err, line)
				}
				if entry["level"] == "WARN" {
					found = append(found, entry)
				}
			}
			return found
		}

		cancelled, cancel := context.WithCancel(t.Context())
		cancel()
		f.netease.Fail(context.Canceled)
		if _, err := f.svc.RecommendList(cancelled, f.persistent); err != nil {
			t.Fatal(err)
		}
		if got := warnings(); len(got) != 0 {
			t.Fatalf("请求被取消时 WARN = %v，期望不记", got)
		}
		f.netease.Fail(errors.New("网易云不可用"))
		for range 2 {
			if list, err := f.svc.RecommendList(t.Context(), f.persistent); err != nil || len(list) != 0 {
				t.Fatalf("接口出错时的推荐列表 = %v, %v，期望为空", list, err)
			}
		}
		if got := warnings(); len(got) != 1 || !strings.Contains(fmt.Sprint(got[0]["error"]), "网易云不可用") {
			t.Fatalf("接口连续出错两次，WARN = %v，期望一条带着错误的 WARN", got)
		}
		f.netease.Fail(nil)
		if _, err := f.svc.RecommendList(t.Context(), f.persistent); err != nil {
			t.Fatal(err)
		}
		f.netease.Fail(errors.New("网易云不可用"))
		if _, err := f.svc.RecommendList(t.Context(), f.persistent); err != nil {
			t.Fatal(err)
		}
		if n := len(warnings()); n != 2 {
			t.Errorf("接口恢复之后又出错，WARN 共 %d 条，期望 2 条", n)
		}
	})
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
