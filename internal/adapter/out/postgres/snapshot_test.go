package postgres

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/member"
	"github.com/dihemoe/alisten/internal/domain/room"
	"github.com/dihemoe/alisten/internal/domain/track"
)

// PostgreSQL 的时间精确到微秒。
var t0 = time.Date(2026, 1, 1, 0, 0, 0, 1000, time.UTC)

var alice = member.Member{ID: "01a15778-1911-7a28-8b2e-e08c2a25f381", Name: "甲", AvatarHash: "0bc83cb571cd1c50ba6f3e8a78ef1346"}

func netease(id string) track.Track { return track.Track{Source: track.NetEase, ID: id} }

func takeRoom(t *testing.T, store *Store, id string) (uint32, room.Snapshot) {
	t.Helper()
	term, snap, err := store.TakeRoom(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return term, snap
}

func saveRoom(t *testing.T, store *Store, term uint32, snap room.Snapshot, plays ...room.RecordPlay) {
	t.Helper()
	if err := store.SaveRoom(t.Context(), term, snap, plays); err != nil {
		t.Fatal(err)
	}
}

func addTrack(t *testing.T, store *Store, tr track.Track, m track.Meta, p track.Prepared) {
	t.Helper()
	if err := store.SavePreparedTrack(t.Context(), tr, port.PreparedTrack{Meta: m, Result: p}); err != nil {
		t.Fatal(err)
	}
}

func TestTakeRoomIncrementsTerm(t *testing.T) {
	store, _ := newStore(t)
	id := createRoom(t, store, port.NewRoom{Kind: room.Ephemeral, Name: "临时", Description: "描述"})

	term, snap := takeRoom(t, store, id)
	want := room.Snapshot{Info: room.Info{ID: id, Name: "临时", Description: "描述", Kind: room.Ephemeral}, Mode: room.Sequential}
	if term != 1 || !reflect.DeepEqual(snap, want) {
		t.Fatalf("新房间第一次接管 = %d, %#v，期望任期 1 和 %#v", term, snap, want)
	}
	if term, _ := takeRoom(t, store, id); term != 2 {
		t.Errorf("再次接管的任期 = %d，期望 2", term)
	}

	for _, missing := range []string{"01a15778-1911-7a28-8b2e-e08c2a25f388", "r1"} {
		if _, _, err := store.TakeRoom(t.Context(), missing); !errors.Is(err, port.ErrRoomNotFound) {
			t.Errorf("TakeRoom(%q) 的错误 = %v，期望 ErrRoomNotFound", missing, err)
		}
	}
}

func TestSavedSnapshotIsRestored(t *testing.T) {
	store, _ := newStore(t)
	id := createRoom(t, store, port.NewRoom{Kind: room.Persistent, Name: "大厅"})
	term, _ := takeRoom(t, store, id)

	playing := track.Prepared{Duration: 3 * time.Minute, Gain: 0.8, Lyric: "[00:00.00]歌词"}
	queued := track.Prepared{Duration: 2*time.Minute + 500*time.Millisecond, Gain: 1.2}
	link := track.Track{Source: track.Link, ID: "https://example.com/a.mp3"}
	meta := track.Meta{Title: "歌", Artist: "人", Album: "专辑", CoverURL: "https://p.example/c.jpg", PageURL: "https://music.163.com/#/song?id=1", Duration: playing.Duration}
	linkMeta := track.Meta{Title: "链接里的歌", PageURL: link.ID, Duration: queued.Duration}
	addTrack(t, store, netease("1"), meta, playing)
	addTrack(t, store, link, linkMeta, queued)

	// 准备中的链接曲目也可能带有封面的对象键：例如音频已不在对象存储中、要重新准备的歌。
	unprepared := track.Track{Source: track.Link, ID: "https://example.com/c.mp3"}
	saved := room.Snapshot{
		Info:     room.Info{ID: id, Name: "新名字", Description: "新描述", Kind: room.Persistent},
		Mode:     room.Random,
		EntrySeq: 5,
		Current: &room.NowPlaying{
			Entry:     room.Entry{ID: 1, Track: netease("1"), Meta: meta, By: alice, Round: 1, Ready: true, Prep: playing, AddedAt: t0},
			StartedAt: t0.Add(time.Second),
		},
		Pending: &room.Entry{
			ID: 2, Track: track.Track{Source: track.QQ, ID: "2"}, By: alice, Round: 2, AddedAt: t0,
			Meta: track.Meta{Title: "待播", PageURL: "https://y.qq.com/n/ryqq/songDetail/2", Duration: time.Minute},
		},
		Waiting: []room.Entry{
			{ID: 3, Track: link, Meta: linkMeta, By: alice, Likes: 2, Round: 3, Ready: true, Prep: queued, AddedAt: t0},
			{ID: 4, Track: netease("4"), Auto: true, Round: 1, AddedAt: t0.Add(time.Minute)},
			{ID: 5, Track: unprepared, Meta: track.Meta{Title: unprepared.ID, CoverKey: "link/c/cover.png", PageURL: unprepared.ID}, By: alice, Round: 4, AddedAt: t0},
		},
	}
	saveRoom(t, store, term, saved)

	term, restored := takeRoom(t, store, id)
	if term != 2 {
		t.Errorf("任期 = %d，期望 2", term)
	}
	if !reflect.DeepEqual(restored, saved) {
		t.Fatalf("恢复出的快照与落盘的不同：\n得到 %#v\n期望 %#v", restored, saved)
	}
}

func TestPlayModesAreStored(t *testing.T) {
	store, _ := newStore(t)
	for _, mode := range []room.PlayMode{room.Sequential, room.Random, room.Rotation} {
		id := createRoom(t, store, port.NewRoom{Kind: room.Persistent, Name: "大厅"})
		term, snap := takeRoom(t, store, id)
		snap.Mode = mode
		saveRoom(t, store, term, snap)
		if _, restored := takeRoom(t, store, id); restored.Mode != mode {
			t.Errorf("落盘的播放模式 %d 恢复为 %d", mode, restored.Mode)
		}
	}
}

func TestSnapshotStoresMissingValuesAsNull(t *testing.T) {
	store, pool := newStore(t)
	id := createRoom(t, store, port.NewRoom{Kind: room.Persistent, Name: "大厅", Description: "介绍"})
	term, _ := takeRoom(t, store, id)
	bob := member.Member{ID: "01a15778-1911-7a28-8b2e-e08c2a25f382", Name: "乙"}
	saved := room.Snapshot{
		Info:     room.Info{ID: id, Name: "大厅", Kind: room.Persistent},
		Mode:     room.Sequential,
		EntrySeq: 2,
		Waiting: []room.Entry{
			{ID: 1, Track: netease("1"), Meta: track.Meta{Title: "无名", PageURL: "https://music.163.com/#/song?id=1"}, By: bob, AddedAt: t0},
			{ID: 2, Track: netease("2"), Meta: track.Meta{Title: "推荐", PageURL: "https://music.163.com/#/song?id=2"}, Auto: true, AddedAt: t0},
		},
	}
	saveRoom(t, store, term, saved)

	var description *string
	if err := pool.QueryRow(t.Context(), "SELECT description FROM rooms WHERE id = $1", id).Scan(&description); err != nil {
		t.Fatal(err)
	}
	if description == nil || *description != "" {
		t.Errorf("清空的房间描述 = %v，期望空串", description)
	}
	rows, err := pool.Query(t.Context(), `SELECT entry_id, concat_ws(',', CASE WHEN artist IS NULL THEN 'artist' END, CASE WHEN album IS NULL THEN 'album' END,
		CASE WHEN cover_url IS NULL THEN 'cover_url' END, CASE WHEN by_name IS NULL THEN 'by_name' END, CASE WHEN by_avatar_hash IS NULL THEN 'by_avatar_hash' END)
		FROM room_entries WHERE room_id = $1 ORDER BY entry_id`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var missing []string
	for rows.Next() {
		var entry int
		var columns string
		if err := rows.Scan(&entry, &columns); err != nil {
			t.Fatal(err)
		}
		missing = append(missing, fmt.Sprintf("%d:%s", entry, columns))
	}
	if want := []string{"1:artist,album,cover_url,by_avatar_hash", "2:artist,album,cover_url,by_name,by_avatar_hash"}; !slices.Equal(missing, want) {
		t.Errorf("各条目为 NULL 的列 = %v，期望 %v", missing, want)
	}
	if _, restored := takeRoom(t, store, id); !reflect.DeepEqual(restored, saved) {
		t.Errorf("恢复出的快照与落盘的不同：\n得到 %#v\n期望 %#v", restored, saved)
	}
}

func TestReadinessComesFromTracks(t *testing.T) {
	store, _ := newStore(t)
	id := createRoom(t, store, port.NewRoom{Kind: room.Persistent, Name: "大厅"})
	term, snap := takeRoom(t, store, id)

	prepared := track.Prepared{Duration: time.Minute, Gain: 1}
	link := track.Track{Source: track.Link, ID: "https://example.com/b.mp3"}
	snap.EntrySeq = 2
	snap.Waiting = []room.Entry{
		{ID: 1, Track: netease("removed"), Ready: true, Prep: prepared, AddedAt: t0},
		{ID: 2, Track: link, Meta: track.Meta{Title: link.ID, PageURL: link.ID}, AddedAt: t0},
	}
	saveRoom(t, store, term, snap)
	meta := track.Meta{Title: "链接里的歌", Artist: "人", PageURL: link.ID, Duration: prepared.Duration}
	addTrack(t, store, link, meta, prepared)

	_, restored := takeRoom(t, store, id)
	if got := restored.Waiting[0]; got.Ready || got.Prep != (track.Prepared{}) {
		t.Errorf("曲目表中没有的条目应恢复为准备中：%#v", got)
	}
	if got := restored.Waiting[1]; !got.Ready || got.Prep != prepared || got.Meta != meta {
		t.Errorf("曲目表中已有结果的条目应恢复为已就绪，元数据取自曲目表：%#v", got)
	}
}

func TestSaveRoomRequiresCurrentTerm(t *testing.T) {
	store, _ := newStore(t)
	id := createRoom(t, store, port.NewRoom{Kind: room.Persistent, Name: "大厅"})
	stale, snap := takeRoom(t, store, id)
	takeRoom(t, store, id)

	snap.Info.Name = "旧 owner 改的名字"
	if err := store.SaveRoom(t.Context(), stale, snap, nil); !errors.Is(err, port.ErrTermExpired) {
		t.Fatalf("过期任期落盘的错误 = %v，期望 ErrTermExpired", err)
	}
	if _, got := takeRoom(t, store, id); got.Info.Name != "大厅" {
		t.Errorf("过期任期的落盘不应生效：%#v", got.Info)
	}
}

func TestFailedSaveKeepsLastSnapshot(t *testing.T) {
	store, _ := newStore(t)
	id := createRoom(t, store, port.NewRoom{Kind: room.Persistent, Name: "大厅"})
	term, snap := takeRoom(t, store, id)
	snap.EntrySeq = 1
	snap.Waiting = []room.Entry{{ID: 1, Track: netease("1"), AddedAt: t0}}
	saveRoom(t, store, term, snap)

	// 超过 btree 索引项的上限，插入条目时失败。
	var long strings.Builder
	long.WriteString("https://example.com/")
	for i := 0; long.Len() < 4096; i++ {
		sum := sha256.Sum256([]byte{byte(i)})
		long.WriteString(hex.EncodeToString(sum[:]))
	}
	failed := snap
	failed.Info.Name = "不会落盘"
	failed.EntrySeq = 2
	failed.Waiting = append(failed.Waiting, room.Entry{ID: 2, Track: track.Track{Source: track.Link, ID: long.String()}, AddedAt: t0})
	if err := store.SaveRoom(t.Context(), term, failed, nil); err == nil {
		t.Fatal("超长的曲目 ID 应让落盘失败")
	}

	_, restored := takeRoom(t, store, id)
	if !reflect.DeepEqual(restored, snap) {
		t.Fatalf("落盘失败后应保留上一次的快照：\n得到 %#v\n期望 %#v", restored, snap)
	}
}

func TestSaveRoomRecordsPlays(t *testing.T) {
	store, pool := newStore(t)
	first := createRoom(t, store, port.NewRoom{Kind: room.Persistent, Name: "一厅"})
	second := createRoom(t, store, port.NewRoom{Kind: room.Persistent, Name: "二厅"})
	addTrack(t, store, netease("1"), track.Meta{}, track.Prepared{Duration: time.Minute, Gain: 1})
	firstTerm, firstSnap := takeRoom(t, store, first)
	secondTerm, secondSnap := takeRoom(t, store, second)

	song := room.Entry{ID: 1, Track: netease("1"), By: alice, AddedAt: t0}
	saveRoom(t, store, secondTerm, secondSnap, room.RecordPlay{Entry: song, StartedAt: t0.Add(2 * time.Minute), EndedAt: t0.Add(3 * time.Minute)})
	saveRoom(t, store, firstTerm, firstSnap,
		room.RecordPlay{Entry: song, StartedAt: t0, EndedAt: t0.Add(time.Minute)},
		room.RecordPlay{Entry: room.Entry{ID: 2, Track: netease("2"), Auto: true}, StartedAt: t0.Add(time.Minute), EndedAt: t0.Add(2 * time.Minute)},
	)

	var lastPlayed time.Time
	if err := pool.QueryRow(t.Context(), "SELECT last_played_at FROM tracks WHERE track_id = '1'").Scan(&lastPlayed); err != nil {
		t.Fatal(err)
	}
	if !lastPlayed.Equal(t0.Add(2 * time.Minute)) {
		t.Errorf("last_played_at = %v，期望最近一次开播的时间，不被后写入的较早记录覆盖", lastPlayed)
	}

	rows, err := pool.Query(t.Context(), `
		SELECT track_id, coalesce(by_member_id::text, 'NULL'), coalesce(by_name, 'NULL'), coalesce(by_avatar_hash, 'NULL'), auto, started_at, ended_at
		FROM plays WHERE room_id = $1 ORDER BY started_at`, first)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type play struct {
		track, by, name, avatar string
		auto                    bool
		started, ended          time.Time
	}
	var got []play
	for rows.Next() {
		var p play
		if err := rows.Scan(&p.track, &p.by, &p.name, &p.avatar, &p.auto, &p.started, &p.ended); err != nil {
			t.Fatal(err)
		}
		p.started, p.ended = p.started.UTC(), p.ended.UTC()
		got = append(got, p)
	}
	want := []play{
		{"1", alice.ID, alice.Name, alice.AvatarHash, false, t0, t0.Add(time.Minute)},
		{"2", "NULL", "NULL", "NULL", true, t0.Add(time.Minute), t0.Add(2 * time.Minute)},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("播放记录 = %v，期望 %v", got, want)
	}
}

func TestDeleteRoom(t *testing.T) {
	store, pool := newStore(t)
	id := createRoom(t, store, port.NewRoom{Kind: room.Ephemeral, Name: "临时"})
	addTicket(t, pool, id)
	addTrack(t, store, netease("1"), track.Meta{}, track.Prepared{Duration: time.Minute, Gain: 1})
	stale, _ := takeRoom(t, store, id)
	term, snap := takeRoom(t, store, id)
	song := room.Entry{ID: 1, Track: netease("1"), By: alice, AddedAt: t0}
	snap.EntrySeq = 1
	snap.Waiting = []room.Entry{song}
	saveRoom(t, store, term, snap, room.RecordPlay{Entry: song, StartedAt: t0, EndedAt: t0.Add(time.Minute)})

	if err := store.DeleteRoom(t.Context(), id, stale); !errors.Is(err, port.ErrTermExpired) {
		t.Fatalf("过期任期删除房间的错误 = %v，期望 ErrTermExpired", err)
	}
	if err := store.DeleteRoom(t.Context(), id, term); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.TakeRoom(t.Context(), id); !errors.Is(err, port.ErrRoomNotFound) {
		t.Errorf("删除后接管的错误 = %v，期望 ErrRoomNotFound", err)
	}

	var entries, tickets, plays, tracks int
	err := pool.QueryRow(t.Context(), `SELECT
		(SELECT count(*) FROM room_entries), (SELECT count(*) FROM room_tickets),
		(SELECT count(*) FROM plays), (SELECT count(*) FROM tracks)`).Scan(&entries, &tickets, &plays, &tracks)
	if err != nil {
		t.Fatal(err)
	}
	if entries != 0 || tickets != 0 || plays != 0 {
		t.Errorf("删除房间后应级联删除条目、票据和播放记录：条目 %d、票据 %d、播放记录 %d", entries, tickets, plays)
	}
	if tracks != 1 {
		t.Error("曲目表不随房间删除")
	}
}
