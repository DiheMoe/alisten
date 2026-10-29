package room

import (
	"slices"
	"testing"
	"time"

	"github.com/dihemoe/alisten/internal/domain/track"
)

func listenedRoom(t *testing.T) *Room {
	t.Helper()
	r := newRoom(t, Persistent)
	r.Handle(at(0), Join{Session: "s1", Member: alice})
	return r
}

func TestAutoDJAddIsNotAPick(t *testing.T) {
	r := listenedRoom(t)
	r.Handle(at(0), enqueue(alice, netease("a")))
	rec := find[Recommend](r.Handle(at(20*time.Second), Prepared{Track: netease("a"), Result: ready(3 * time.Minute)}))
	if len(rec) != 1 {
		t.Fatal("没有请求推荐")
	}
	r.Handle(at(21*time.Second), Recommended{Token: rec[0].Token, Track: netease("b")})
	r.Handle(at(22*time.Second), Prepared{Track: netease("b"), Result: ready(time.Minute)})

	effects := r.Handle(at(25*time.Second), command(alice, VoteSkip{}))
	if r.current == nil || r.current.Entry.Track != netease("b") || len(find[Recommend](effects)) != 1 {
		t.Fatalf("AutoDJ 加入的歌不算有人点歌，开播时应继续请求推荐：%#v", effects)
	}
}

func TestAutoDJIsOneRequester(t *testing.T) {
	r := listenedRoom(t)
	r.Handle(at(0), command(alice, SetPlayMode{Mode: Rotation}))
	r.Handle(at(0), enqueue(alice, netease("a")))
	rec := find[Recommend](r.Handle(at(20*time.Second), Prepared{Track: netease("a"), Result: ready(3 * time.Minute)}))
	if len(rec) != 1 {
		t.Fatal("没有请求推荐")
	}
	r.Handle(at(21*time.Second), Recommended{Token: rec[0].Token, Track: netease("b")})
	r.Handle(at(22*time.Second), Prepared{Track: netease("b"), Result: ready(time.Minute)})
	rec = find[Recommend](r.Handle(at(25*time.Second), command(alice, VoteSkip{})))
	if len(rec) != 1 {
		t.Fatal("AutoDJ 的歌开播时没有请求推荐")
	}
	r.Handle(at(26*time.Second), Recommended{Token: rec[0].Token, Track: netease("c")})
	r.Handle(at(27*time.Second), enqueue(bob, netease("d")))
	if got := ids(r.waiting); !slices.Equal(got, []EntryID{4, 3}) {
		t.Fatalf("上一首推荐还在播放，下一首推荐应排在乙这一轮的歌后面：%v", got)
	}
}

func TestAutoDJDoesNotRecommend(t *testing.T) {
	qq := track.Track{Source: track.QQ, ID: "a"}
	cases := []struct {
		name  string
		start func(r *Room) []Effect
	}{
		{"10 秒内有人点歌", func(r *Room) []Effect {
			r.Handle(at(0), enqueue(alice, netease("a")))
			return r.Handle(at(5*time.Second), Prepared{Track: netease("a"), Result: ready(time.Minute)})
		}},
		{"没有会话", func(r *Room) []Effect {
			r.Handle(at(0), Leave{Session: "s1"})
			r.Handle(at(0), enqueue(alice, netease("a")))
			return r.Handle(at(time.Minute), Prepared{Track: netease("a"), Result: ready(time.Minute)})
		}},
		{"不是网易云的歌", func(r *Room) []Effect {
			r.Handle(at(0), enqueue(alice, qq))
			return r.Handle(at(time.Minute), Prepared{Track: qq, Result: ready(time.Minute)})
		}},
		{"等待队列不为空", func(r *Room) []Effect {
			r.Handle(at(0), enqueue(alice, netease("a")))
			r.Handle(at(0), enqueue(alice, netease("b")))
			return r.Handle(at(time.Minute), Prepared{Track: netease("a"), Result: ready(time.Minute)})
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			effects := tc.start(listenedRoom(t))
			if len(events[PlaybackChanged](effects)) != 1 {
				t.Fatalf("没有开播：%#v", effects)
			}
			if len(find[Recommend](effects)) != 0 {
				t.Fatalf("不应请求推荐：%#v", effects)
			}
		})
	}
}

func TestRecommendedIsCheckedAgain(t *testing.T) {
	setup := func(t *testing.T) (*Room, Token) {
		r := listenedRoom(t)
		r.Handle(at(0), enqueue(alice, netease("a")))
		rec := find[Recommend](r.Handle(at(20*time.Second), Prepared{Track: netease("a"), Result: ready(3 * time.Minute)}))
		if len(rec) != 1 {
			t.Fatal("没有请求推荐")
		}
		return r, rec[0].Token
	}

	t.Run("token 不匹配", func(t *testing.T) {
		r, token := setup(t)
		if effects := r.Handle(at(21*time.Second), Recommended{Token: token + 100, Track: netease("b")}); len(effects) != 0 {
			t.Fatalf("应丢弃：%#v", effects)
		}
	})
	t.Run("推荐的就是正在播放的歌", func(t *testing.T) {
		r, token := setup(t)
		if effects := r.Handle(at(21*time.Second), Recommended{Token: token, Track: netease("a")}); len(effects) != 0 {
			t.Fatalf("应丢弃：%#v", effects)
		}
	})
	t.Run("等待推荐期间有人点歌", func(t *testing.T) {
		r, token := setup(t)
		r.Handle(at(25*time.Second), enqueue(bob, netease("c")))
		if effects := r.Handle(at(26*time.Second), Recommended{Token: token, Track: netease("b")}); len(effects) != 0 {
			t.Fatalf("应丢弃：%#v", effects)
		}
	})
	t.Run("同一个推荐结果再次送达", func(t *testing.T) {
		r, token := setup(t)
		r.Handle(at(21*time.Second), Recommended{Token: token, Track: netease("b")})
		// 推荐的歌被删掉之后等待队列又空了，如果再次送达的结果没有被丢弃，就会再入队一次。
		r.Handle(at(22*time.Second), command(alice, Remove{Entry: 2}))
		if effects := r.Handle(at(23*time.Second), Recommended{Token: token, Track: netease("b")}); len(effects) != 0 {
			t.Fatalf("应丢弃：%#v", effects)
		}
	})
	t.Run("发出推荐的歌被切掉了", func(t *testing.T) {
		r, token := setup(t)
		r.Handle(at(22*time.Second), command(alice, VoteSkip{}))
		if effects := r.Handle(at(23*time.Second), Recommended{Token: token, Track: netease("b")}); len(effects) != 0 {
			t.Fatalf("应丢弃：%#v", effects)
		}
	})
	t.Run("发出推荐的歌播完了", func(t *testing.T) {
		r, token := setup(t)
		r.Handle(at(3*time.Minute+21*time.Second), Timer{Token: r.endTimer})
		if effects := r.Handle(at(3*time.Minute+22*time.Second), Recommended{Token: token, Track: netease("b")}); len(effects) != 0 {
			t.Fatalf("应丢弃：%#v", effects)
		}
	})
}
