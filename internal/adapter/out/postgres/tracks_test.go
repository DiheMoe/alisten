package postgres

import (
	"testing"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/room"
	"github.com/dihemoe/alisten/internal/domain/track"
)

func TestPreparedTrackRoundTrip(t *testing.T) {
	store, _ := newStore(t)
	song := track.Track{Source: track.QQ, ID: "0039MnYb0qxYhV"}
	if _, ok, err := store.PreparedTrack(t.Context(), song); err != nil || ok {
		t.Fatalf("还没有准备过的歌 = %v, %v，期望没有记录", ok, err)
	}

	saved := port.PreparedTrack{
		Meta: track.Meta{
			Title: "晴天", Artist: "周杰伦", Album: "叶惠美", CoverURL: "https://y.gtimg.cn/c.jpg",
			PageURL: "https://y.qq.com/n/ryqq/songDetail/0039MnYb0qxYhV", Duration: 269 * time.Second,
		},
		Result: track.Prepared{Duration: 269500 * time.Millisecond, Gain: 0.75, Lyric: "[00:01.00]故事的小黄花"},
	}
	if err := store.SavePreparedTrack(t.Context(), song, saved); err != nil {
		t.Fatal(err)
	}
	got, ok, err := store.PreparedTrack(t.Context(), song)
	if err != nil || !ok {
		t.Fatalf("保存之后读取 = %v, %v", ok, err)
	}
	want := saved
	want.Meta.Duration = saved.Result.Duration
	if got != want {
		t.Errorf("读出 %#v\n期望 %#v：元数据中的时长取准备得到的时长", got, want)
	}
}

func TestSavePreparedTrackOverwritesResult(t *testing.T) {
	store, pool := newStore(t)
	song := netease("1")
	addTrack(t, store, song, track.Meta{Title: "旧"}, track.Prepared{Duration: time.Minute, Gain: 1})
	id := createRoom(t, store, port.NewRoom{Kind: room.Persistent, Name: "大厅"})
	term, snap := takeRoom(t, store, id)
	saveRoom(t, store, term, snap, room.RecordPlay{Entry: room.Entry{ID: 1, Track: song, By: alice}, StartedAt: t0, EndedAt: t0.Add(time.Minute)})

	again := port.PreparedTrack{Meta: track.Meta{Title: "新"}, Result: track.Prepared{Duration: 2 * time.Minute, Gain: 0.5, Lyric: "词"}}
	addTrack(t, store, song, again.Meta, again.Result)
	got, _, err := store.PreparedTrack(t.Context(), song)
	if err != nil {
		t.Fatal(err)
	}
	if got.Meta.Title != "新" || got.Result != again.Result {
		t.Errorf("再次准备之后 = %#v，期望以后一次的结果为准", got)
	}
	var lastPlayed time.Time
	if err := pool.QueryRow(t.Context(), "SELECT last_played_at FROM tracks WHERE track_id = '1'").Scan(&lastPlayed); err != nil {
		t.Fatal(err)
	}
	if !lastPlayed.Equal(t0) {
		t.Errorf("last_played_at = %v，再次准备不应清除最近一次播放的时间", lastPlayed)
	}
}

func TestPreparedTrackStoresMissingValuesAsNull(t *testing.T) {
	store, pool := newStore(t)
	song := netease("1")
	saved := port.PreparedTrack{
		Meta:   track.Meta{Title: "无名", PageURL: "https://music.163.com/#/song?id=1", Duration: time.Minute},
		Result: track.Prepared{Duration: time.Minute, Gain: 1},
	}
	if err := store.SavePreparedTrack(t.Context(), song, saved); err != nil {
		t.Fatal(err)
	}
	var missing string
	err := pool.QueryRow(t.Context(), `SELECT concat_ws(',', CASE WHEN artist IS NULL THEN 'artist' END, CASE WHEN album IS NULL THEN 'album' END,
		CASE WHEN cover_url IS NULL THEN 'cover_url' END, CASE WHEN cover_key IS NULL THEN 'cover_key' END, CASE WHEN lyric IS NULL THEN 'lyric' END)
		FROM tracks WHERE track_id = '1'`).Scan(&missing)
	if err != nil {
		t.Fatal(err)
	}
	if missing != "artist,album,cover_url,cover_key,lyric" {
		t.Errorf("为 NULL 的列 = %s，期望 artist,album,cover_url,cover_key,lyric", missing)
	}
	if got, ok, err := store.PreparedTrack(t.Context(), song); err != nil || !ok || got != saved {
		t.Errorf("读出 %#v, %v, %v\n期望 %#v", got, ok, err, saved)
	}
}

func TestLinkTrackRoundTrip(t *testing.T) {
	store, pool := newStore(t)
	link := track.Track{Source: track.Link, ID: "https://example.com/a.mp3"}
	saved := port.PreparedTrack{
		Meta:      track.Meta{Title: "晴天", Artist: "周杰伦", CoverKey: "link/abc/cover.webp", PageURL: link.ID, Duration: 3 * time.Second},
		Result:    track.Prepared{Duration: 3 * time.Second, Gain: 1},
		MediaKey:  "abc",
		MediaSize: 12290,
	}
	if err := store.SavePreparedTrack(t.Context(), link, saved); err != nil {
		t.Fatal(err)
	}
	if got, ok, err := store.PreparedTrack(t.Context(), link); err != nil || !ok || got != saved {
		t.Errorf("读出 %#v, %v, %v\n期望 %#v", got, ok, err, saved)
	}
	var coverURL, coverKey string
	if err := pool.QueryRow(t.Context(), "SELECT coalesce(cover_url, 'NULL'), cover_key FROM tracks WHERE track_id = $1", link.ID).Scan(&coverURL, &coverKey); err != nil {
		t.Fatal(err)
	}
	if coverURL != "NULL" || coverKey != "link/abc/cover.webp" {
		t.Errorf("cover_url = %q，cover_key = %q", coverURL, coverKey)
	}

	id := createRoom(t, store, port.NewRoom{Kind: room.Persistent, Name: "大厅"})
	term, snap := takeRoom(t, store, id)
	snap.EntrySeq = 1
	snap.Waiting = []room.Entry{{ID: 1, Track: link, Meta: track.Meta{Title: link.ID, PageURL: link.ID}, By: alice, AddedAt: t0}}
	saveRoom(t, store, term, snap)
	if _, snap = takeRoom(t, store, id); !snap.Waiting[0].Ready || snap.Waiting[0].Meta != saved.Meta {
		t.Errorf("恢复出的条目 = %+v，期望已就绪，元数据取自曲目表", snap.Waiting[0])
	}
}
