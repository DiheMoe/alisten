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
	plays := find[RecordPlay](effects)
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
