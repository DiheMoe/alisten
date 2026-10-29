package room

import (
	"testing"
	"time"

	"github.com/dihemoe/alisten/internal/domain/member"
	"github.com/dihemoe/alisten/internal/domain/track"
)

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

var (
	alice = member.Member{ID: "m1", Name: "甲"}
	bob   = member.Member{ID: "m2", Name: "乙"}
	carol = member.Member{ID: "m3", Name: "丙"}
)

func at(d time.Duration) Env { return Env{Now: t0.Add(d)} }

func netease(id string) track.Track { return track.Track{Source: track.NetEase, ID: id} }

func ready(d time.Duration) track.Prepared { return track.Prepared{Duration: d, Gain: 1} }

func newRoom(t *testing.T, kind Kind) *Room {
	t.Helper()
	r, _ := Restore(at(0), 1, Snapshot{Info: Info{ID: "r1", Name: "房间", Kind: kind}, Mode: Sequential})
	return r
}

func command(m member.Member, a Action) Command {
	return Command{RequestID: "q", Member: m, Action: a}
}

func enqueue(m member.Member, t track.Track) Command {
	return command(m, Enqueue{Track: t, Meta: track.Meta{Title: t.ID}})
}

func find[T Effect](effects []Effect) []T {
	var out []T
	for _, e := range effects {
		if v, ok := e.(T); ok {
			out = append(out, v)
		}
	}
	return out
}

func events[T Event](effects []Effect) []T {
	var out []T
	for _, emit := range find[Emit](effects) {
		if v, ok := emit.Event.(T); ok {
			out = append(out, v)
		}
	}
	return out
}

func persistsFirst(effects []Effect) bool {
	if len(effects) == 0 {
		return false
	}
	_, ok := effects[0].(Persist)
	return ok
}

func reply(t *testing.T, effects []Effect) Reply {
	t.Helper()
	replies := find[Reply](effects)
	if len(replies) != 1 {
		t.Fatalf("期望一个回复，实际 %d 个：%#v", len(replies), effects)
	}
	return replies[0]
}

func wantError(t *testing.T, effects []Effect, kind ErrorKind) {
	t.Helper()
	if r := reply(t, effects); r.Err == nil || r.Err.Kind != kind || len(effects) != 1 {
		t.Fatalf("期望只回复错误 %d，实际 %#v", kind, effects)
	}
}

func ids(entries []Entry) []EntryID {
	out := make([]EntryID, len(entries))
	for i, e := range entries {
		out[i] = e.ID
	}
	return out
}

func playing(t *testing.T, r *Room, now time.Duration, tr track.Track, d time.Duration) {
	t.Helper()
	r.Handle(at(now), enqueue(alice, tr))
	r.Handle(at(now), Prepared{Track: tr, Meta: track.Meta{Title: tr.ID}, Result: ready(d)})
	if r.current == nil || r.current.Entry.Track != tr {
		t.Fatalf("%v 没有开始播放", tr)
	}
}

// 点的歌立即准备好，每首一分钟。
func pickReady(r *Room, now time.Duration, by member.Member, id string) {
	r.Handle(at(now), enqueue(by, netease(id)))
	r.Handle(at(now), Prepared{Track: netease(id), Meta: track.Meta{Title: id}, Result: ready(time.Minute)})
}

// 放完正在播放的歌，返回放完的时间和这首歌。
func finish(t *testing.T, r *Room) (time.Duration, string) {
	t.Helper()
	if r.current == nil {
		t.Fatal("没有正在播放的歌")
	}
	song, end := r.current.Entry.Track.ID, r.current.EndsAt().Sub(t0)
	r.Handle(at(end), Timer{Token: r.endTimer})
	return end, song
}
