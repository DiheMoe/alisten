package playlist

import (
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/track"
)

func netease(id string) track.Track { return track.Track{Source: track.NetEase, ID: id} }

func qq(id string) track.Track { return track.Track{Source: track.QQ, ID: id} }

func itemTracks(items []port.PlaylistItem) []track.Track {
	tracks := make([]track.Track, len(items))
	for i, item := range items {
		tracks[i] = item.Track
	}
	return tracks
}

func TestImportFileChoosesMeta(t *testing.T) {
	f := newFixture(t)
	alice := f.member(t, "alice")
	clipMeta := track.Meta{Title: "a.mp3", CoverKey: "link/abc/cover.webp", PageURL: clip.ID, Duration: time.Minute}
	knownMeta := track.Meta{Title: "已知", PageURL: "https://y.qq.com/n/ryqq/songDetail/known"}
	f.tracks[sunny] = port.PreparedTrack{Meta: sunnyMeta}
	f.tracks[clip] = port.PreparedTrack{Meta: clipMeta}
	f.tracks[qq("known")] = port.PreparedTrack{Meta: knownMeta}
	video := track.Track{Source: track.Bilibili, ID: "BV1xx411c7mD/2"}
	other := track.Track{Source: track.Link, ID: "https://example.com/b.mp3"}

	cases := []struct {
		name string
		in   ImportedTrack
		want *track.Meta // nil 表示跳过
	}{
		{"曲目表里有：完全用服务端的元数据",
			ImportedTrack{Track: sunny, Title: "文件里的标题", Artist: "别人", CoverURL: "https://p1.music.126.net/other.jpg", Duration: time.Second},
			&sunnyMeta},
		{"链接曲目在曲目表里：用服务端的封面",
			ImportedTrack{Track: clip, Title: "文件里的标题", CoverURL: "https://evil.example/a.jpg"},
			&clipMeta},
		{"曲目表里有：文件里的标题为空也不跳过",
			ImportedTrack{Track: qq("known")},
			&knownMeta},
		{"网易云：平台的 https 地址保留",
			ImportedTrack{Track: netease("185811"), Title: "七里香", Artist: "周杰伦", Album: "七里香", CoverURL: "https://p2.music.126.net/b.jpg", PageURL: "https://music.163.com/song?id=185811", Duration: 299 * time.Second},
			&track.Meta{Title: "七里香", Artist: "周杰伦", Album: "七里香", CoverURL: "https://p2.music.126.net/b.jpg", PageURL: "https://music.163.com/song?id=185811", Duration: 299 * time.Second}},
		{"网易云：http 和伪装的域名丢弃，页面链接由 ID 推出",
			ImportedTrack{Track: netease("1"), Title: "一", CoverURL: "http://p1.music.126.net/a.jpg", PageURL: "https://music.163.com.evil.example/song?id=1"},
			&track.Meta{Title: "一", PageURL: "https://music.163.com/#/song?id=1"}},
		{"网易云：封面只认 music.126.net 的子域名，带 userinfo 的地址丢弃",
			ImportedTrack{Track: netease("2"), Title: "二", CoverURL: "https://music.126.net.evil.example/a.jpg", PageURL: "https://user@music.163.com/song?id=2"},
			&track.Meta{Title: "二", PageURL: "https://music.163.com/#/song?id=2"}},
		{"网易云：封面主机只是字面上以 music.126.net 结尾的丢弃",
			ImportedTrack{Track: netease("6"), Title: "六", CoverURL: "https://evilmusic.126.net/a.jpg", PageURL: "https://music.163.com/song?id=6"},
			&track.Meta{Title: "六", PageURL: "https://music.163.com/song?id=6"}},
		{"网易云：带端口的地址丢弃",
			ImportedTrack{Track: netease("3"), Title: "三", CoverURL: "https://p1.music.126.net:8443/a.jpg", PageURL: "https://music.163.com:8443/song?id=3"},
			&track.Meta{Title: "三", PageURL: "https://music.163.com/#/song?id=3"}},
		{"QQ 音乐：平台的 https 地址保留，域名不区分大小写",
			ImportedTrack{Track: qq("0039MnYb0qxYhV"), Title: "晴天", CoverURL: "https://Y.GTIMG.CN/music/a.jpg", PageURL: "https://y.qq.com/n/ryqq/songDetail/0039MnYb0qxYhV"},
			&track.Meta{Title: "晴天", CoverURL: "https://Y.GTIMG.CN/music/a.jpg", PageURL: "https://y.qq.com/n/ryqq/songDetail/0039MnYb0qxYhV"}},
		{"QQ 音乐：别家平台的地址也丢弃",
			ImportedTrack{Track: qq("002"), Title: "二", CoverURL: "https://p1.music.126.net/a.jpg", PageURL: "https://music.163.com/#/song?id=2"},
			&track.Meta{Title: "二", PageURL: "https://y.qq.com/n/ryqq/songDetail/002"}},
		{"B 站：页面链接由 ID 推出，封面丢弃",
			ImportedTrack{Track: video, Title: "晴天（钢琴版）", Artist: "某 UP 主", CoverURL: "https://i0.hdslb.com/a.jpg", PageURL: "https://www.bilibili.com/video/BV1xx411c7mD", Duration: 2 * time.Minute},
			&track.Meta{Title: "晴天（钢琴版）", Artist: "某 UP 主", PageURL: "https://www.bilibili.com/video/BV1xx411c7mD?p=2", Duration: 2 * time.Minute}},
		{"链接曲目：页面链接就是 ID，封面丢弃",
			ImportedTrack{Track: other, Title: "b.mp3", CoverURL: "https://example.com/b.jpg", PageURL: "https://evil.example/"},
			&track.Meta{Title: "b.mp3", PageURL: other.ID}},
		{"曲目表里没有、标题为空：跳过",
			ImportedTrack{Track: netease("4"), Artist: "无名"},
			nil},
		{"曲目表里没有、标题只有空白：跳过",
			ImportedTrack{Track: netease("5"), Title: " \t"},
			nil},
	}
	in := make([]ImportedTrack, len(cases))
	var want []track.Track
	for i, c := range cases {
		in[i] = c.in
		if c.want != nil {
			want = append(want, c.in.Track)
		}
	}
	p, n, err := f.svc.ImportFile(t.Context(), alice.ID, "导入", in)
	if err != nil || n != len(want) || p.TrackCount != len(want) {
		t.Fatalf("ImportFile = %#v, %d, %v，期望导入 %d 首", p, n, err, len(want))
	}
	items, err := f.svc.Items(t.Context(), alice.ID, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := itemTracks(items); !slices.Equal(got, want) {
		t.Errorf("导入的曲目 = %v，期望与文件同序 %v", got, want)
	}
	for _, c := range cases {
		i := slices.IndexFunc(items, func(item port.PlaylistItem) bool { return item.Track == c.in.Track })
		switch {
		case c.want == nil && i >= 0:
			t.Errorf("%s：导入了 %#v", c.name, items[i].Meta)
		case c.want != nil && i < 0:
			t.Errorf("%s：没有导入", c.name)
		case c.want != nil && items[i].Meta != *c.want:
			t.Errorf("%s：元数据 = %#v\n期望 %#v", c.name, items[i].Meta, *c.want)
		}
	}
}

func TestImportFileTruncatesThenKeepsFirstOccurrences(t *testing.T) {
	f := newFixture(t)
	alice := f.member(t, "alice")
	in := []ImportedTrack{{Track: netease("b"), Title: "B"}, {Track: netease("a"), Title: "A"}, {Track: netease("b"), Title: "又一个 B"}}
	for i := range 999 {
		in = append(in, ImportedTrack{Track: netease(strconv.Itoa(i)), Title: "歌"})
	}
	p, n, err := f.svc.ImportFile(t.Context(), alice.ID, "导入", in)
	if err != nil || n != 999 {
		t.Fatalf("ImportFile = %#v, %d, %v，期望前 1000 首去掉 1 首重复的，导入 999 首", p, n, err)
	}
	items, err := f.svc.Items(t.Context(), alice.ID, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if items[0].Meta.Title != "B" || items[1].Track != netease("a") || items[2].Track != netease("0") || items[len(items)-1].Track != netease("996") {
		t.Errorf("导入的曲目以 %v 开头、%v 结尾，期望重复的歌留在第一次出现的位置、第 1000 首之后的不导入", itemTracks(items[:3]), items[len(items)-1].Track)
	}
}

func TestImportFileFallsBackToDefaultName(t *testing.T) {
	f := newFixture(t)
	alice := f.member(t, "alice")
	song := []ImportedTrack{{Track: sunny, Title: "晴天"}}
	for _, name := range []string{"", "   ", strings.Repeat("歌", 51), "通\n勤", "\u200b"} {
		if p, _, err := f.svc.ImportFile(t.Context(), alice.ID, name, song); err != nil || p.Name != "导入的歌单" {
			t.Errorf("文件里的名字 %q：%#v, %v，期望改用“导入的歌单”", name, p, err)
		}
	}
	if p, _, err := f.svc.ImportFile(t.Context(), alice.ID, "  通勤  ", song); err != nil || p.Name != "通勤" {
		t.Errorf("合法的名字 = %#v, %v，期望去掉首尾空白后照用", p, err)
	}
}

func TestImportFileIsNotRateLimitedButRespectsPlaylistLimit(t *testing.T) {
	f := newFixture(t)
	alice := f.member(t, "alice")
	f.quota.exhausted = true
	for i := range 49 {
		if _, _, err := f.svc.ImportFile(t.Context(), alice.ID, "导入"+strconv.Itoa(i), nil); err != nil {
			t.Fatalf("第 %d 次导入 = %v", i+1, err)
		}
	}
	if len(f.quota.keys) != 0 {
		t.Errorf("导入文件不访问外部，不应取额度：%v", f.quota.keys)
	}
	if _, _, err := f.svc.ImportFile(t.Context(), alice.ID, "第 51 个", nil); !errors.Is(err, ErrTooManyPlaylists) {
		t.Errorf("第 51 个歌单 = %v，期望 ErrTooManyPlaylists", err)
	}
}

func TestResolveLink(t *testing.T) {
	f := newFixture(t)
	alice := f.member(t, "alice")
	f.links["https://163cn.tv/abc"] = "https://music.163.com/playlist?id=456&userid=1"
	f.links["https://163cn.tv/song"] = "https://music.163.com/song?id=1"
	f.links["https://163cn.tv/stuck"] = "https://163cn.tv/stuck"
	f.links["https://c6.y.qq.com/base/fcgi-bin/u?__=abc"] = "https://i.y.qq.com/n2/m/share/details/taoge.html?id=789&hosteuin=1"
	linkQuota := "playlist-link:member:" + alice.ID
	cases := []struct {
		raw    string
		source track.Source
		id     string
		err    error
		quota  []string
	}{
		{raw: "https://music.163.com/#/playlist?id=123", source: track.NetEase, id: "123"},
		{raw: "分享歌单：通勤 https://163cn.tv/abc (来自网易云音乐)", source: track.NetEase, id: "456", quota: []string{linkQuota}},
		{raw: "https://163cn.tv/broken", err: ErrShortLinkFailed, quota: []string{linkQuota}},
		{raw: "https://163cn.tv/stuck", err: ErrShortLinkFailed, quota: []string{linkQuota}},
		{raw: "https://163cn.tv/song", err: ErrInvalidPlaylistLink, quota: []string{linkQuota}},
		{raw: "https://example.com/playlist?id=1", err: ErrInvalidPlaylistLink},
		{raw: "随便一段文字", err: ErrInvalidPlaylistLink},
		{raw: "https://y.qq.com/n/ryqq/playlist/789", source: track.QQ, id: "789"},
		{raw: "https://c6.y.qq.com/base/fcgi-bin/u?__=abc", source: track.QQ, id: "789", quota: []string{linkQuota}},
	}
	for _, c := range cases {
		before := len(f.quota.keys)
		source, id, err := f.svc.ResolveLink(t.Context(), alice.ID, c.raw)
		if c.err != nil && !errors.Is(err, c.err) {
			t.Errorf("ResolveLink(%q) = %v，期望 %v", c.raw, err, c.err)
		}
		if c.err == nil && (err != nil || source != c.source || id != c.id) {
			t.Errorf("ResolveLink(%q) = %q, %q, %v，期望 %s 的歌单 %s", c.raw, source, id, err, c.source, c.id)
		}
		if taken := f.quota.keys[before:]; !slices.Equal(taken, c.quota) {
			t.Errorf("ResolveLink(%q) 取的额度 = %v，期望 %v：长链接在本地识别，只有解析短链接才取链接额度", c.raw, taken, c.quota)
		}
	}
	if f.quota.burst != 10 || f.quota.refill != 6*time.Minute {
		t.Errorf("链接额度是突发 %d 次、每 %v 恢复 1 次，期望突发 10 次、每 6 分钟恢复 1 次", f.quota.burst, f.quota.refill)
	}
}

func TestImportPlatform(t *testing.T) {
	f := newFixture(t)
	alice := f.member(t, "alice")
	sevenMeta := track.Meta{Title: "七里香", Artist: "周杰伦", PageURL: "https://music.163.com/#/song?id=185811", Duration: 299 * time.Second}
	f.netease.Add("185811", sevenMeta, "", nil)
	f.netease.AddPlaylist(port.Playlist{ID: "p1", Name: "周杰伦"}, "185811", sunny.ID, "185811")

	p, n, err := f.svc.ImportPlatform(t.Context(), alice.ID, "  周杰伦  ", track.NetEase, "p1")
	if err != nil || n != 2 || p.Name != "周杰伦" || p.TrackCount != 2 {
		t.Fatalf("ImportPlatform = %#v, %d, %v，期望去掉重复的、导入 2 首", p, n, err)
	}
	items, err := f.svc.Items(t.Context(), alice.ID, p.ID)
	want := []port.PlaylistItem{{Track: netease("185811"), Meta: sevenMeta}, {Track: sunny, Meta: sunnyMeta}}
	if err != nil || !slices.Equal(items, want) {
		t.Errorf("导入的曲目 = %#v, %v\n期望与平台同序、用音源的元数据 %#v", items, err, want)
	}
	if want := []string{"playlist-import:member:" + alice.ID}; !slices.Equal(f.quota.keys, want) || f.quota.burst != 10 || f.quota.refill != 6*time.Minute {
		t.Errorf("额度 = %v（突发 %d，每 %v 恢复 1 次），期望 %v（突发 10，每 6 分钟恢复 1 次）", f.quota.keys, f.quota.burst, f.quota.refill, want)
	}
}

func TestImportPlatformFromQQ(t *testing.T) {
	f := newFixture(t)
	alice := f.member(t, "alice")
	meta := track.Meta{Title: "晴天", Artist: "周杰伦", PageURL: "https://y.qq.com/n/ryqq/songDetail/0039MnYb0qxYhV", Duration: 269 * time.Second}
	f.qq.Add("0039MnYb0qxYhV", meta, "", nil)
	f.qq.AddPlaylist(port.Playlist{ID: "789", Name: "周杰伦"}, "0039MnYb0qxYhV")

	p, n, err := f.svc.ImportPlatform(t.Context(), alice.ID, "周杰伦", track.QQ, "789")
	if err != nil || n != 1 {
		t.Fatalf("ImportPlatform = %#v, %d, %v，期望导入 1 首", p, n, err)
	}
	items, err := f.svc.Items(t.Context(), alice.ID, p.ID)
	want := []port.PlaylistItem{{Track: track.Track{Source: track.QQ, ID: "0039MnYb0qxYhV"}, Meta: meta}}
	if err != nil || !slices.Equal(items, want) {
		t.Errorf("导入的曲目 = %#v, %v\n期望用 QQ 音乐的元数据 %#v", items, err, want)
	}
}

func TestImportPlatformTruncatesThenDedupes(t *testing.T) {
	f := newFixture(t)
	alice := f.member(t, "alice")
	ids := []string{"0"}
	for i := range 1001 {
		id := strconv.Itoa(i)
		f.netease.Add(id, track.Meta{Title: "歌" + id}, "", nil)
		ids = append(ids, id)
	}
	f.netease.AddPlaylist(port.Playlist{ID: "big"}, ids...)
	p, n, err := f.svc.ImportPlatform(t.Context(), alice.ID, "大歌单", track.NetEase, "big")
	if err != nil || n != 999 {
		t.Fatalf("ImportPlatform = %#v, %d, %v，期望前 1000 首去掉 1 首重复的，导入 999 首", p, n, err)
	}
	items, err := f.svc.Items(t.Context(), alice.ID, p.ID)
	if err != nil || items[0].Track != netease("0") || items[len(items)-1].Track != netease("998") {
		t.Errorf("导入的曲目 = %d 首, %v，期望从 0 到 998", len(items), err)
	}
}

func TestImportPlatformRejects(t *testing.T) {
	f := newFixture(t)
	alice := f.member(t, "alice")
	f.netease.AddPlaylist(port.Playlist{ID: "p1"}, sunny.ID)
	if _, _, err := f.svc.ImportPlatform(t.Context(), alice.ID, "B 站", track.Bilibili, "p1"); !errors.Is(err, ErrUnsupportedPlaylist) {
		t.Errorf("B 站的歌单 = %v，期望 ErrUnsupportedPlaylist", err)
	}
	if _, _, err := f.svc.ImportPlatform(t.Context(), alice.ID, " ", track.NetEase, "p1"); !errors.Is(err, ErrInvalidName) {
		t.Errorf("空的歌单名 = %v，期望 ErrInvalidName", err)
	}
	if len(f.quota.keys) != 0 {
		t.Errorf("被拒绝的请求不应取额度：%v", f.quota.keys)
	}
	if _, _, err := f.svc.ImportPlatform(t.Context(), alice.ID, "不存在", track.NetEase, "missing"); !errors.Is(err, port.ErrProviderFailed) {
		t.Errorf("音源里没有的歌单 = %v，期望 ErrProviderFailed", err)
	}
	for i := range 49 {
		if _, err := f.svc.Create(t.Context(), alice.ID, "歌单"+strconv.Itoa(i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := f.svc.ImportPlatform(t.Context(), alice.ID, "第 51 个", track.NetEase, "p1"); !errors.Is(err, ErrTooManyPlaylists) {
		t.Errorf("第 51 个歌单 = %v，期望 ErrTooManyPlaylists", err)
	}
}

func TestPlatformImportsAreRateLimited(t *testing.T) {
	f := newFixture(t)
	alice := f.member(t, "alice")
	f.netease.AddPlaylist(port.Playlist{ID: "p1"}, sunny.ID)
	f.links["https://163cn.tv/abc"] = "https://music.163.com/playlist?id=1"
	f.quota.exhausted = true
	if source, id, err := f.svc.ResolveLink(t.Context(), alice.ID, "https://music.163.com/playlist?id=1"); err != nil || source != track.NetEase || id != "1" {
		t.Errorf("额度用完时解析长链接 = %q, %q, %v，期望照常识别", source, id, err)
	}
	if _, _, err := f.svc.ResolveLink(t.Context(), alice.ID, "https://163cn.tv/abc"); !errors.Is(err, ErrTooFast) {
		t.Errorf("额度用完时解析短链接 = %v，期望 ErrTooFast", err)
	}
	if _, _, err := f.svc.ImportPlatform(t.Context(), alice.ID, "周杰伦", track.NetEase, "p1"); !errors.Is(err, ErrTooFast) {
		t.Errorf("额度用完时导入 = %v，期望 ErrTooFast", err)
	}
	if want := []string{"playlist-link:member:" + alice.ID, "playlist-import:member:" + alice.ID}; !slices.Equal(f.quota.keys, want) {
		t.Errorf("取的额度 = %v，期望短链接取链接额度、导入取导入额度 %v", f.quota.keys, want)
	}
	if lists, _ := f.svc.List(t.Context(), alice.ID); len(lists) != 1 {
		t.Errorf("额度用完时不应建歌单：%#v", lists)
	}
}
