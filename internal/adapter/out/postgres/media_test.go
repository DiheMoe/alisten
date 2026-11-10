package postgres

import (
	"cmp"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/room"
	"github.com/dihemoe/alisten/internal/domain/track"
)

func saveStored(t *testing.T, store *Store, tr track.Track, pageURL, key string, meta track.Meta, size int64) {
	t.Helper()
	meta.Title, meta.PageURL = "标题 "+key, pageURL
	p := port.PreparedTrack{
		Meta:      meta,
		Result:    track.Prepared{Duration: time.Minute, Gain: 1},
		MediaKey:  key,
		MediaSize: size,
	}
	if err := store.SavePreparedTrack(t.Context(), tr, p); err != nil {
		t.Fatal(err)
	}
}

func TestStoredMedia(t *testing.T) {
	store, _ := newStore(t)
	a := track.Track{Source: track.Link, ID: "https://example.com/a.mp3"}
	video := track.Track{Source: track.Bilibili, ID: "BV1xx411c7mD/2"}
	saveStored(t, store, a, a.ID, "aaa", track.Meta{Artist: "周杰伦", CoverKey: "link/aaa/cover.jpg"}, 100)
	saveStored(t, store, track.Track{Source: track.Link, ID: "https://example.com/b.mp3"}, "https://example.com/b.mp3", "bbb", track.Meta{CoverURL: "https://example.com/b.jpg"}, 200)
	saveStored(t, store, video, "https://www.bilibili.com/video/BV1xx411c7mD?p=2", "ccc", track.Meta{Artist: "某个 UP 主", CoverKey: "link/ccc/cover.jpg"}, 300)
	addTrack(t, store, netease("1"), track.Meta{Title: "晴天"}, track.Prepared{Duration: time.Minute, Gain: 1})

	id := createRoom(t, store, port.NewRoom{Kind: room.Persistent, Name: "大厅"})
	term, snap := takeRoom(t, store, id)
	played := room.Entry{ID: 1, Track: a, By: alice}
	saveRoom(t, store, term, snap, room.RecordPlay{Entry: played, StartedAt: t0, EndedAt: t0.Add(time.Minute)})

	page, err := store.ListStoredMedia(t.Context(), port.MediaQuery{Page: 1, Sort: port.MediaByStored})
	if err != nil {
		t.Fatal(err)
	}
	list := page.Items
	if len(list) != 3 || list[0].Key != "ccc" || list[1].Key != "bbb" || list[2].Key != "aaa" {
		t.Fatalf("已存储的媒体 = %+v，期望两首链接和一首 B 站的歌，后存入的在前", list)
	}
	got := list[2]
	if got.Source != track.Link || got.Link != a.ID || got.Title != "标题 aaa" || got.Artist != "周杰伦" || got.Size != 100 || got.CoverURL != "" || got.CoverKey != "link/aaa/cover.jpg" ||
		got.StoredAt.IsZero() || got.LastPlayedAt == nil || !got.LastPlayedAt.Equal(t0) {
		t.Errorf("播放过的链接 = %+v", got)
	}
	if b := list[1]; b.Source != track.Link || b.Artist != "" || b.CoverURL != "https://example.com/b.jpg" || b.CoverKey != "" || b.LastPlayedAt != nil {
		t.Errorf("没有歌手、封面不在对象存储里、没有播放过的链接 = %+v", b)
	}
	if c := list[0]; c.Source != track.Bilibili || c.Link != video.ID || c.Artist != "某个 UP 主" || c.CoverKey != "link/ccc/cover.jpg" || c.Size != 300 {
		t.Errorf("B 站的歌 = %+v", c)
	}

	for key, want := range map[string]port.StoredMedia{"aaa": got, "ccc": list[0]} {
		if found, ok, err := store.StoredMedia(t.Context(), key); err != nil || !ok || !reflect.DeepEqual(found, want) {
			t.Errorf("StoredMedia(%s) = %+v, %v, %v，期望 %+v", key, found, ok, err, want)
		}
	}
	for _, key := range []string{"aaa", "ccc"} {
		if err := store.DeleteStoredMedia(t.Context(), key); err != nil {
			t.Fatal(err)
		}
		if _, ok, err := store.StoredMedia(t.Context(), key); err != nil || ok {
			t.Errorf("清除之后 StoredMedia(%s) = %v, %v", key, ok, err)
		}
	}
	for _, tr := range []track.Track{a, video} {
		if _, ok, err := store.PreparedTrack(t.Context(), tr); err != nil || ok {
			t.Errorf("清除之后 %v 的准备结果 = %v, %v，期望没有", tr, ok, err)
		}
	}
	if page, err := store.ListStoredMedia(t.Context(), port.MediaQuery{Page: 1, Sort: port.MediaByStored}); err != nil || len(page.Items) != 1 || page.Total != 1 {
		t.Errorf("清除之后的列表 = %+v, %v", page, err)
	}
}

func mediaKeys(items []port.StoredMedia) []string {
	keys := make([]string, len(items))
	for i, m := range items {
		keys[i] = m.Key
	}
	slices.Sort(keys)
	return keys
}

// 120 首：存储时间和最近播放时间各不相同，其中 10 首没有播放过；每 3 首大小相同，要靠曲目 ID 分出先后。
// 有 3 首的标题含 50%_off；另外两首只在 % 或 _ 被当作通配符时才会匹配它。
func TestStoredMediaPages(t *testing.T) {
	store, pool := newStore(t)
	titles := map[int]string{5: "Summer 50%_off", 50: "50%_OFF 特价", 95: "全场50%_Off", 20: "50abc_off", 30: "50%Xoff", 40: `C:\music`}
	all := make([]port.StoredMedia, 120)
	var totalBytes int64
	for i := range all {
		// 曲目 ID 的顺序与存入的顺序不同，大小相同时是否按曲目 ID 排才看得出来。
		link := fmt.Sprintf("https://example.com/%03d.mp3", i*7%120)
		m := port.StoredMedia{
			Key: fmt.Sprintf("key%03d", i), Source: track.Link, Link: link, Title: cmp.Or(titles[i], fmt.Sprintf("第 %d 首", i)),
			Size: int64(1000 + i*11%40), StoredAt: t0.Add(time.Duration(i*13%120) * time.Minute),
		}
		if i%12 != 0 {
			played := t0.Add(time.Duration(i*17%120) * time.Hour)
			m.LastPlayedAt = &played
		}
		saveStored(t, store, track.Track{Source: track.Link, ID: link}, link, m.Key, track.Meta{}, m.Size)
		if _, err := pool.Exec(t.Context(), "UPDATE tracks SET title = $1, prepared_at = $2, last_played_at = $3 WHERE media_key = $4", m.Title, m.StoredAt, m.LastPlayedAt, m.Key); err != nil {
			t.Fatal(err)
		}
		all[i] = m
		totalBytes += m.Size
	}
	list := func(q port.MediaQuery) port.MediaPage {
		t.Helper()
		page, err := store.ListStoredMedia(t.Context(), q)
		if err != nil {
			t.Fatal(err)
		}
		return page
	}
	// 每一页都报告全部的数量和大小；翻过最后一页是空的。
	pages := func(sort port.MediaSort) []port.StoredMedia {
		t.Helper()
		var items []port.StoredMedia
		for i, want := range []int{50, 50, 20, 0} {
			page := list(port.MediaQuery{Page: i + 1, Sort: sort})
			if len(page.Items) != want || page.Total != 120 || page.TotalBytes != totalBytes {
				t.Fatalf("排序 %d 的第 %d 页有 %d 首，共 %d 首 %d 字节，期望 %d 首，共 120 首 %d 字节", sort, i+1, len(page.Items), page.Total, page.TotalBytes, want, totalBytes)
			}
			items = append(items, page.Items...)
		}
		if got, want := mediaKeys(items), mediaKeys(all); !slices.Equal(got, want) {
			t.Fatalf("排序 %d 依次翻页 = %q，期望不重复也不遗漏", sort, got)
		}
		return items
	}
	inOrder := func(name string, items []port.StoredMedia, compare func(a, b port.StoredMedia) int) {
		t.Helper()
		for i := 1; i < len(items); i++ {
			if compare(items[i-1], items[i]) >= 0 {
				t.Fatalf("%s：%+v 排在了 %+v 之前", name, items[i-1], items[i])
			}
		}
	}
	byLink := func(a, b port.StoredMedia) int { return strings.Compare(a.Link, b.Link) }

	inOrder("按存储时间", pages(port.MediaByStored), func(a, b port.StoredMedia) int { return b.StoredAt.Compare(a.StoredAt) })
	idle := pages(port.MediaByIdle)
	for i, m := range idle {
		if (m.LastPlayedAt == nil) != (i < 10) {
			t.Fatalf("按最近播放的第 %d 首 = %+v，期望前 10 首是没有播放过的", i+1, m)
		}
	}
	inOrder("没有播放过的", idle[:10], byLink)
	inOrder("按最近播放", idle[10:], func(a, b port.StoredMedia) int { return a.LastPlayedAt.Compare(*b.LastPlayedAt) })
	inOrder("按大小", pages(port.MediaBySize), func(a, b port.StoredMedia) int { return cmp.Or(cmp.Compare(b.Size, a.Size), byLink(a, b)) })
	if page := list(port.MediaQuery{Page: math.MaxInt, Sort: port.MediaBySize}); len(page.Items) != 0 || page.Total != 120 || page.TotalBytes != totalBytes {
		t.Errorf("页码极大时 = %d 首，共 %d 首 %d 字节，期望没有，总数照常", len(page.Items), page.Total, page.TotalBytes)
	}

	for _, tt := range []struct {
		title string
		want  []int
	}{
		{"50%_off", []int{5, 50, 95}},
		{"OFF", []int{5, 20, 30, 50, 95}},
		{`:\m`, []int{40}},
	} {
		var want []port.StoredMedia
		var bytes int64
		for _, i := range tt.want {
			want = append(want, all[i])
			bytes += all[i].Size
		}
		page := list(port.MediaQuery{Page: 1, Sort: port.MediaByStored, Title: tt.title})
		if got := mediaKeys(page.Items); !slices.Equal(got, mediaKeys(want)) || page.Total != len(want) || page.TotalBytes != bytes {
			t.Errorf("搜索 %q = %q，共 %d 首 %d 字节，期望 %q，共 %d 首 %d 字节", tt.title, got, page.Total, page.TotalBytes, mediaKeys(want), len(want), bytes)
		}
	}
}
