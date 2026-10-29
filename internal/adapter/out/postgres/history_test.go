package postgres

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/room"
	"github.com/dihemoe/alisten/internal/domain/track"
)

func picked(id room.EntryID, t track.Track) room.Entry {
	return room.Entry{ID: id, Track: t, By: alice, AddedAt: t0}
}

func autoPicked(id room.EntryID, t track.Track) room.Entry {
	return room.Entry{ID: id, Track: t, Auto: true, AddedAt: t0}
}

func played(e room.Entry, minute int) room.RecordPlay {
	start := t0.Add(time.Duration(minute) * time.Minute)
	return room.RecordPlay{Entry: e, StartedAt: start, EndedAt: start.Add(time.Minute)}
}

func trackIDs(tracks []track.Track) []string {
	ids := make([]string, len(tracks))
	for i, t := range tracks {
		if t.Source == track.NetEase {
			ids[i] = t.ID
		} else {
			ids[i] = string(t.Source) + ":" + t.ID
		}
	}
	return ids
}

func TestAutoDJSeeds(t *testing.T) {
	store, _ := newStore(t)
	id := createRoom(t, store, port.NewRoom{Kind: room.Persistent, Name: "大厅"})
	other := createRoom(t, store, port.NewRoom{Kind: room.Persistent, Name: "二厅"})
	term, snap := takeRoom(t, store, id)
	otherTerm, otherSnap := takeRoom(t, store, other)
	saveRoom(t, store, otherTerm, otherSnap, played(picked(1, netease("elsewhere")), 9))

	snap.EntrySeq = 12
	snap.Current = &room.NowPlaying{Entry: picked(6, netease("c")), StartedAt: t0.Add(5 * time.Minute)}
	snap.Pending = &room.Entry{ID: 7, Track: netease("p"), By: alice, AddedAt: t0}
	w1, w2, again := picked(8, netease("w1")), picked(9, netease("w2")), picked(10, netease("a"))
	w2.Likes = 1
	snap.Waiting = []room.Entry{w2, w1, again, autoPicked(11, netease("w3")), picked(12, track.Track{Source: track.QQ, ID: "w4"})}
	saveRoom(t, store, term, snap,
		played(picked(1, netease("a")), 0),
		played(picked(2, netease("b")), 1),
		played(autoPicked(3, netease("x")), 2),
		played(picked(4, track.Track{Source: track.QQ, ID: "q"}), 3),
		played(picked(5, netease("a")), 4),
	)

	seeds, err := store.AutoDJSeeds(t.Context(), id, false)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := trackIDs(seeds), []string{"c", "a", "b"}; !slices.Equal(got, want) {
		t.Errorf("自动补歌的种子 = %v，期望 %v", got, want)
	}
	seeds, err = store.AutoDJSeeds(t.Context(), id, true)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := trackIDs(seeds), []string{"c", "p", "w2", "w1", "a", "b"}; !slices.Equal(got, want) {
		t.Errorf("推荐列表的种子 = %v，期望 %v", got, want)
	}

	// 轮流播放时等待队列中点赞数相同的先比轮次，种子的顺序与队列相同。
	snap.Mode = room.Rotation
	snap.Waiting[1].Round, snap.Waiting[2].Round = 2, 1
	saveRoom(t, store, term, snap)
	seeds, err = store.AutoDJSeeds(t.Context(), id, true)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := trackIDs(seeds), []string{"c", "p", "w2", "a", "w1", "b"}; !slices.Equal(got, want) {
		t.Errorf("轮流播放时推荐列表的种子 = %v，期望 %v", got, want)
	}
}

func TestAutoDJSeedsKeepsMostRecent32(t *testing.T) {
	store, _ := newStore(t)
	id := createRoom(t, store, port.NewRoom{Kind: room.Persistent, Name: "大厅"})
	term, snap := takeRoom(t, store, id)
	var plays []room.RecordPlay
	for i := range 40 {
		plays = append(plays, played(picked(room.EntryID(i+1), netease(fmt.Sprint(i))), i))
	}
	snap.EntrySeq = 41
	snap.Current = &room.NowPlaying{Entry: picked(41, netease("c")), StartedAt: t0.Add(time.Hour)}
	saveRoom(t, store, term, snap, plays...)

	seeds, err := store.AutoDJSeeds(t.Context(), id, false)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"c"}
	for i := 39; len(want) < 32; i-- {
		want = append(want, fmt.Sprint(i))
	}
	if got := trackIDs(seeds); !slices.Equal(got, want) {
		t.Errorf("种子 = %v，期望正在播放的加上最近 31 首 %v", got, want)
	}
}

func TestAutoDJExclusions(t *testing.T) {
	store, _ := newStore(t)
	id := createRoom(t, store, port.NewRoom{Kind: room.Persistent, Name: "大厅"})
	other := createRoom(t, store, port.NewRoom{Kind: room.Persistent, Name: "二厅"})
	term, snap := takeRoom(t, store, id)
	otherTerm, otherSnap := takeRoom(t, store, other)
	// 其他房间自动播放的和正在播放的歌比本房间的所有记录都新，查询如果去掉 room_id 条件，它们就会挤进最近 128 首。
	otherSnap.EntrySeq = 3
	otherSnap.Current = &room.NowPlaying{Entry: autoPicked(3, netease("elsewhere-current")), StartedAt: t0.Add(600 * time.Minute)}
	otherSnap.Waiting = []room.Entry{picked(2, netease("elsewhere-waiting"))}
	saveRoom(t, store, otherTerm, otherSnap, played(autoPicked(1, netease("elsewhere-auto")), 500))

	var plays []room.RecordPlay
	for i := range 130 {
		plays = append(plays, played(autoPicked(room.EntryID(i+1), netease(fmt.Sprintf("x%03d", i))), i))
	}
	plays = append(plays, played(picked(131, netease("m")), 130))
	snap.EntrySeq = 135
	snap.Current = &room.NowPlaying{Entry: autoPicked(132, netease("y")), StartedAt: t0.Add(3 * time.Hour)}
	snap.Pending = &room.Entry{ID: 133, Track: netease("p"), By: alice, AddedAt: t0}
	snap.Waiting = []room.Entry{picked(134, track.Track{Source: track.QQ, ID: "w"}), autoPicked(135, netease("z"))}
	saveRoom(t, store, term, snap, plays...)

	excluded, err := store.AutoDJExclusions(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	got := trackIDs(excluded)
	for _, want := range []string{"y", "x129", "x003", "p", "qq:w", "z"} {
		if !slices.Contains(got, want) {
			t.Errorf("排除项中缺少 %s", want)
		}
	}
	for _, unwanted := range []string{"x002", "m", "elsewhere-auto", "elsewhere-current", "elsewhere-waiting"} {
		if slices.Contains(got, unwanted) {
			t.Errorf("排除项不应包含 %s", unwanted)
		}
	}
	if len(got) != 128+3 {
		t.Errorf("排除项有 %d 首，期望最近 128 首 AutoDJ 的歌加上待播和等待队列的 3 首", len(got))
	}
}

func TestAutoDJLooksAtLast1000Plays(t *testing.T) {
	store, _ := newStore(t)
	id := createRoom(t, store, port.NewRoom{Kind: room.Persistent, Name: "大厅"})
	term, snap := takeRoom(t, store, id)
	var plays []room.RecordPlay
	add := func(e room.Entry) { plays = append(plays, played(e, len(plays))) }
	for i := range 50 {
		add(picked(1, netease(fmt.Sprint("old-", i))))
		add(autoPicked(1, netease(fmt.Sprint("old-auto-", i))))
	}
	// 最近的第 1002、1001 次播放在窗口之外，第 1000、999 次在窗口之中。
	add(picked(1, netease("outside")))
	add(autoPicked(1, netease("outside-auto")))
	add(picked(1, netease("edge")))
	add(autoPicked(1, netease("edge-auto")))
	for len(plays) < 1102 {
		switch len(plays) {
		case 500:
			add(picked(1, netease("inside")))
		case 600:
			add(autoPicked(1, netease("inside-auto")))
		default:
			add(picked(1, track.Track{Source: track.QQ, ID: fmt.Sprint(len(plays))}))
		}
	}
	snap.EntrySeq = 1
	snap.Current = &room.NowPlaying{Entry: picked(1, netease("c")), StartedAt: t0.Add(2000 * time.Minute)}
	saveRoom(t, store, term, snap, plays...)

	seeds, err := store.AutoDJSeeds(t.Context(), id, false)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := trackIDs(seeds), []string{"c", "inside", "edge"}; !slices.Equal(got, want) {
		t.Errorf("种子 = %v，期望正在播放的歌加上最近 1000 次播放中成员点的网易云歌曲 %v", got, want)
	}
	excluded, err := store.AutoDJExclusions(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := trackIDs(excluded), []string{"edge-auto", "inside-auto"}; !slices.Equal(got, want) {
		t.Errorf("排除项 = %v，期望最近 1000 次播放中 AutoDJ 加入的歌 %v", got, want)
	}
}

func TestTrackInUse(t *testing.T) {
	store, _ := newStore(t)
	id := createRoom(t, store, port.NewRoom{Kind: room.Persistent, Name: "大厅"})
	term, snap := takeRoom(t, store, id)
	link := track.Track{Source: track.Link, ID: "https://example.com/a.mp3"}
	pending := picked(3, netease("3"))
	snap.EntrySeq = 3
	snap.Current = &room.NowPlaying{Entry: picked(1, link), StartedAt: t0}
	snap.Waiting = []room.Entry{picked(2, netease("1"))}
	snap.Pending = &pending
	saveRoom(t, store, term, snap)

	for tr, want := range map[track.Track]bool{
		link:                        true,
		netease("1"):                true,
		netease("3"):                true,
		netease("2"):                false,
		{Source: track.QQ, ID: "1"}: false,
	} {
		got, err := store.TrackInUse(t.Context(), tr)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("TrackInUse(%v) = %v，期望 %v", tr, got, want)
		}
	}

	snap.Current, snap.Pending, snap.Waiting = nil, nil, nil
	saveRoom(t, store, term, snap)
	for _, tr := range []track.Track{link, netease("1"), netease("3")} {
		if used, err := store.TrackInUse(t.Context(), tr); err != nil || used {
			t.Errorf("条目移出房间后 TrackInUse(%v) = %v, %v，期望 false", tr, used, err)
		}
	}
}
