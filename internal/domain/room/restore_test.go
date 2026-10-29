package room

import (
	"cmp"
	"slices"
	"testing"
	"time"
)

func TestRestore(t *testing.T) {
	playingEntry := Entry{ID: 1, Track: netease("a"), Ready: true, Prep: ready(3 * time.Minute)}
	snapshot := Snapshot{
		Info:     Info{ID: "r1", Kind: Persistent},
		Mode:     Sequential,
		EntrySeq: 3,
		Current:  &NowPlaying{Entry: playingEntry, StartedAt: t0},
		// 存储返回的顺序不保证符合排序规则。
		Waiting: []Entry{
			{ID: 2, Track: netease("b"), Ready: true, Prep: ready(time.Minute)},
			{ID: 3, Track: netease("c"), Likes: 1},
		},
	}

	t.Run("当前歌没放完", func(t *testing.T) {
		r, effects := Restore(at(time.Minute), 2, snapshot)
		if len(find[Persist](effects)) != 0 {
			t.Fatalf("状态没变不落盘：%#v", effects)
		}
		if prep := find[Prepare](effects); len(prep) != 1 || prep[0].Track != netease("c") {
			t.Fatalf("未就绪的条目重新准备：%#v", effects)
		}
		if s := find[Schedule](effects); len(s) != 1 || !s[0].At.Equal(t0.Add(3*time.Minute)) {
			t.Fatalf("重新设定播完的定时器：%#v", effects)
		}
		if got := ids(r.waiting); !slices.Equal(got, []EntryID{3, 2}) {
			t.Fatalf("等待队列应按点赞数排序：%v", got)
		}
	})

	t.Run("当前歌已经放完", func(t *testing.T) {
		r, effects := Restore(at(5*time.Minute), 2, snapshot)
		if !persistsFirst(effects) {
			t.Fatalf("状态变化时先落盘：%#v", effects)
		}
		if plays := find[RecordPlay](effects); len(plays) != 1 || !plays[0].EndedAt.Equal(t0.Add(3*time.Minute)) {
			t.Fatalf("补记已经放完的歌：%#v", effects)
		}
		if r.pending == nil || r.pending.ID != 3 {
			t.Fatalf("应选下一首（点赞数最多的未就绪条目）：%#v", r.pending)
		}
	})

	t.Run("重新设定的定时器能结束当前歌", func(t *testing.T) {
		current := &NowPlaying{Entry: playingEntry, StartedAt: t0}
		r, effects := Restore(at(time.Minute), 2, Snapshot{Info: snapshot.Info, Mode: Sequential, EntrySeq: 1, Current: current})
		timers := find[Schedule](effects)
		if len(timers) != 1 {
			t.Fatalf("重新设定播完的定时器：%#v", effects)
		}

		effects = r.Handle(at(3*time.Minute), Timer{Token: timers[0].Token})
		if plays := find[RecordPlay](effects); len(plays) != 1 || !plays[0].EndedAt.Equal(current.EndsAt()) {
			t.Fatalf("到点应结束当前歌并记录：%#v", effects)
		}
		if got := events[PlaybackChanged](effects); len(got) != 1 || got[0].Current != nil {
			t.Fatalf("等待队列为空时应进入空闲：%#v", effects)
		}
	})
}

func TestRestoreKeepsTheRotation(t *testing.T) {
	r := newRoom(t, Persistent)
	r.Handle(at(0), command(alice, SetPlayMode{Mode: Rotation}))
	for _, id := range []string{"a1", "a2", "a3"} {
		pickReady(r, 0, alice, id)
	}
	pickReady(r, 0, bob, "b1")
	now, _ := finish(t, r)
	pickReady(r, now, carol, "c1")
	now, _ = finish(t, r)
	// 乙的第一首已经放完，第二首按已开播的轮次排进第 1 轮。
	pickReady(r, now, bob, "b2")
	s := r.snapshot()
	// 存储按条目 ID 返回等待队列。
	slices.SortFunc(s.Waiting, func(a, b Entry) int { return cmp.Compare(a.ID, b.ID) })

	restored, _ := Restore(at(now), 2, s)
	if got, want := ids(restored.waiting), ids(r.waiting); !slices.Equal(got, want) {
		t.Fatalf("恢复后的等待队列 = %v，期望与恢复前相同：%v", got, want)
	}
	if len(restored.served) != 0 {
		t.Fatalf("已开播的轮次只在内存中，恢复后应为空：%v", restored.served)
	}
}
