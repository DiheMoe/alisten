package room

import (
	"slices"
	"testing"
	"time"

	"github.com/dihemoe/alisten/internal/domain/track"
)

func TestCurrentSongCanBePickedAgain(t *testing.T) {
	r := newRoom(t, Persistent)
	playing(t, r, 0, netease("a"), 3*time.Minute)
	if res := reply(t, r.Handle(at(time.Second), enqueue(bob, netease("a")))); res.Err != nil {
		t.Fatalf("正在播放的歌可以再点：%#v", res)
	}
}

func TestPreparedForWaitingEntryOnlyMarksReady(t *testing.T) {
	r := newRoom(t, Persistent)
	playing(t, r, 0, netease("a"), 3*time.Minute)
	r.Handle(at(time.Second), enqueue(alice, netease("b")))

	effects := r.Handle(at(2*time.Second), Prepared{Track: netease("b"), Result: ready(time.Minute)})
	if len(events[PlaybackChanged](effects)) != 0 || !r.waiting[0].Ready {
		t.Fatalf("等待队列中的歌只标记为已就绪：%#v", effects)
	}
	marked := r.waiting[0]

	again := Prepared{Track: netease("b"), Meta: track.Meta{Title: "B2"}, Result: ready(2 * time.Minute)}
	if effects := r.Handle(at(3*time.Second), again); len(effects) != 0 || r.waiting[0] != marked {
		t.Fatalf("已就绪的条目忽略重复的结果：%#v", effects)
	}
	if effects := r.Handle(at(3*time.Second), PrepareFailed{Track: netease("b"), Reason: "x"}); len(effects) != 0 || len(r.waiting) != 1 || r.waiting[0] != marked {
		t.Fatalf("已就绪的条目忽略准备失败的结果：%#v", effects)
	}
	if effects := r.Handle(at(3*time.Second), Prepared{Track: netease("x"), Result: ready(time.Minute)}); len(effects) != 0 {
		t.Fatalf("不在队列中的曲目忽略结果：%#v", effects)
	}
}

func TestSongEndRecordsPlayAndAdvances(t *testing.T) {
	r := newRoom(t, Persistent)
	playing(t, r, 0, netease("a"), 3*time.Minute)
	r.Handle(at(time.Second), enqueue(alice, netease("b")))
	r.Handle(at(time.Second), Prepared{Track: netease("b"), Result: ready(time.Minute)})
	first := *r.current

	effects := r.Handle(at(3*time.Minute+time.Second), Timer{Token: r.endTimer})
	plays := recorded(effects)
	if len(plays) != 1 || plays[0].Entry.ID != first.Entry.ID || !plays[0].EndedAt.Equal(first.EndsAt()) {
		t.Fatalf("播完应记录播放历史，结束时间为应播完的时间：%#v", effects)
	}
	got := events[PlaybackChanged](effects)
	if len(got) != 1 || got[0].Current == nil || got[0].Current.Entry.Track != netease("b") ||
		!got[0].Current.StartedAt.Equal(t0.Add(3*time.Minute+time.Second+startLead)) {
		t.Fatalf("应接着播放下一首：%#v", effects)
	}

	effects = r.Handle(at(5*time.Minute), Timer{Token: r.endTimer})
	if got := events[PlaybackChanged](effects); len(got) != 1 || got[0].Current != nil {
		t.Fatalf("等待队列为空时应进入空闲：%#v", effects)
	}
}

func TestPrepareFailedWaitingEntryIsRemoved(t *testing.T) {
	r := newRoom(t, Persistent)
	playing(t, r, 0, netease("a"), 3*time.Minute)
	r.Handle(at(time.Second), enqueue(alice, netease("b")))
	r.Handle(at(time.Second), enqueue(alice, netease("c")))
	current := *r.current

	effects := r.Handle(at(2*time.Second), PrepareFailed{Track: netease("b"), Reason: "歌曲已下架"})
	if !persistsFirst(effects) {
		t.Fatalf("状态变化时第一个 effect 应是 Persist：%#v", effects)
	}
	if got := ids(r.waiting); !slices.Equal(got, []EntryID{3}) {
		t.Fatalf("准备失败的条目应移出等待队列：%v", got)
	}
	failed := events[Activity](effects)
	if len(failed) != 1 || failed[0].Kind != ActivityFailed || failed[0].Entry.ID != 2 || failed[0].Reason != "歌曲已下架" {
		t.Fatalf("应广播准备失败的原因：%#v", effects)
	}
	if queue := events[QueueChanged](effects); len(queue) != 1 || !slices.Equal(ids(queue[0].Waiting), []EntryID{3}) {
		t.Fatalf("广播的等待队列不应再包含失败的条目：%#v", effects)
	}
	if len(events[PlaybackChanged](effects)) != 0 || *r.current != current {
		t.Fatalf("准备失败不应影响正在播放的歌：%#v", effects)
	}
}

func TestNextIsTheFirstReadyWaitingEntry(t *testing.T) {
	r := newRoom(t, Persistent)
	playing(t, r, 0, netease("a"), 3*time.Minute)
	effects := r.Handle(at(time.Second), enqueue(alice, netease("b")))
	if got := events[QueueChanged](effects); len(got) != 1 || got[0].Next != 0 {
		t.Fatalf("准备好之前不应有下一首：%#v", got)
	}
	effects = r.Handle(at(2*time.Second), Prepared{Track: netease("b"), Meta: track.Meta{Title: "b"}, Result: ready(time.Minute)})
	if got := events[QueueChanged](effects); len(got) != 1 || got[0].Next != 2 {
		t.Fatalf("准备好之后下一首应是 #2：%#v", got)
	}
	if v := r.view(); v.Next != 2 {
		t.Fatalf("快照里的下一首 = %d，期望 2", v.Next)
	}
}

func TestNoNextWhenNothingPlays(t *testing.T) {
	r := newRoom(t, Persistent)
	effects := r.Handle(at(0), enqueue(alice, netease("a")))
	if got := events[QueueChanged](effects); len(got) != 1 || got[0].Next != 0 {
		t.Fatalf("没有歌在播时不应有下一首：%#v", got)
	}
}

func TestStartingThePendingTrackAnnouncesTheNext(t *testing.T) {
	r := newRoom(t, Persistent)
	r.Handle(at(0), enqueue(alice, netease("a")))
	pickReady(r, time.Second, bob, "b")
	effects := r.Handle(at(2*time.Second), Prepared{Track: netease("a"), Meta: track.Meta{Title: "a"}, Result: ready(time.Minute)})
	if got := events[QueueChanged](effects); len(got) != 1 || got[0].Next != 2 {
		t.Fatalf("待播开播之后应广播下一首 #2：%#v", effects)
	}
}

func TestModeSwitchAnnouncesTheNext(t *testing.T) {
	r := newRoom(t, Persistent)
	r.Handle(at(0), command(alice, SetPlayMode{Mode: Random}))
	playing(t, r, 0, netease("a"), time.Minute)
	pickReady(r, time.Second, alice, "b")
	pickReady(r, time.Second, alice, "c")
	// 让抽中的不是排在第一的 #2，切回顺序模式时下一首才一定会变。这时等待队列和待播都没变，只有单独比较下一首才会广播。
	r.next = 3
	effects := r.Handle(at(2*time.Second), command(alice, SetPlayMode{Mode: Sequential}))
	if q := events[QueueChanged](effects); len(q) != 1 || q[0].Next != 2 {
		t.Fatalf("下一首从 #3 变成 #2 时应广播：%#v", effects)
	}
}

func TestRandomPlaysTheDrawnNext(t *testing.T) {
	r := newRoom(t, Persistent)
	r.Handle(at(0), command(alice, SetPlayMode{Mode: Random}))
	playing(t, r, 0, netease("a"), time.Minute)
	for _, id := range []string{"b", "c", "d"} {
		pickReady(r, time.Second, alice, id)
	}
	// b 入队时等待队列里只有它，所以抽中的是 #2。切歌时等待队列有 4 首，种子为 0 时当场随机会选到 #4，所以这个用例能区分两种做法。
	drawn := r.upNext()
	if drawn == 0 {
		t.Fatal("随机模式下有歌在播时应抽好下一首")
	}
	pickReady(r, 2*time.Second, bob, "e")
	if got := r.upNext(); got != drawn {
		t.Fatalf("新点的歌不应改变抽好的下一首：%d，原来 %d", got, drawn)
	}
	r.Handle(at(time.Minute+2*time.Second), Timer{Token: r.endTimer})
	if r.current == nil || r.current.Entry.ID != drawn {
		t.Fatalf("切歌时应放抽好的 #%d，实际 %#v", drawn, r.current)
	}
	if got := r.upNext(); got == 0 || got == drawn {
		t.Fatalf("开播之后应抽好新的下一首，实际 %d", got)
	}
}

func TestRandomRedrawsWhenTheDrawnEntryIsRemoved(t *testing.T) {
	r := newRoom(t, Persistent)
	r.Handle(at(0), command(alice, SetPlayMode{Mode: Random}))
	playing(t, r, 0, netease("a"), time.Minute)
	pickReady(r, time.Second, alice, "b")
	pickReady(r, time.Second, alice, "c")
	drawn := r.upNext()
	effects := r.Handle(at(2*time.Second), command(alice, Remove{Entry: drawn}))
	got := r.upNext()
	if got == 0 || got == drawn {
		t.Fatalf("抽好的歌被删掉之后应重新抽，实际 %d", got)
	}
	if q := events[QueueChanged](effects); len(q) != 1 || q[0].Next != got {
		t.Fatalf("重新抽签之后应广播新的下一首：%#v", effects)
	}
}

func TestOtherModesClearTheDraw(t *testing.T) {
	r := newRoom(t, Persistent)
	r.Handle(at(0), command(alice, SetPlayMode{Mode: Random}))
	playing(t, r, 0, netease("a"), time.Minute)
	pickReady(r, time.Second, alice, "b")
	pickReady(r, time.Second, alice, "c")
	r.Handle(at(2*time.Second), command(alice, SetPlayMode{Mode: Sequential}))
	if r.next != 0 {
		t.Fatalf("顺序模式不应留着抽好的下一首：%d", r.next)
	}
	if got := r.upNext(); got != r.waiting[0].ID {
		t.Fatalf("顺序模式的下一首应是第一首 #%d，实际 %d", r.waiting[0].ID, got)
	}
}
