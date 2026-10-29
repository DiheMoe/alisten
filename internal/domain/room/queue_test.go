package room

import (
	"fmt"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/dihemoe/alisten/internal/domain/member"
	"github.com/dihemoe/alisten/internal/domain/track"
)

func TestDuplicatePick(t *testing.T) {
	r := newRoom(t, Persistent)
	r.Handle(at(0), enqueue(alice, netease("a")))
	r.Handle(at(0), enqueue(alice, netease("b")))

	wantError(t, r.Handle(at(time.Second), enqueue(bob, netease("a"))), Conflict)
	wantError(t, r.Handle(at(time.Second), enqueue(bob, netease("b"))), Conflict)
}

func TestEphemeralRoomLimits(t *testing.T) {
	r := newRoom(t, Ephemeral)
	wantError(t, r.Handle(at(0), enqueue(alice, track.Track{Source: track.Link, ID: "https://example.com/a"})), Forbidden)

	// 每分钟点一首，额度不会用完；第一首成为待播，之后 10 首进入等待队列。
	for i := range 11 {
		res := reply(t, r.Handle(at(time.Duration(i+1)*time.Minute), enqueue(alice, netease(string(rune('a'+i))))))
		if res.Err != nil {
			t.Fatalf("第 %d 首点歌失败：%#v", i+1, res)
		}
	}
	wantError(t, r.Handle(at(13*time.Minute), enqueue(alice, netease("z"))), QueueFull)
}

func TestRateLimitsByRoomKind(t *testing.T) {
	cases := []struct {
		name  string
		kind  Kind
		burst int
	}{
		{"临时房间", Ephemeral, 5},
		{"常驻房间", Persistent, 30},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRoom(t, tc.kind)
			for i := range tc.burst {
				if res := reply(t, r.Handle(at(0), enqueue(alice, netease(fmt.Sprint(i))))); res.Err != nil {
					t.Fatalf("突发额度内的第 %d 次点歌失败：%#v", i+1, res)
				}
			}
			if len(r.waiting) != tc.burst-1 {
				t.Fatalf("等待队列有 %d 首，期望 %d 首：第一首成为待播，其余进入等待队列", len(r.waiting), tc.burst-1)
			}
			wantError(t, r.Handle(at(0), enqueue(alice, netease("x"))), RateLimited)
			wantError(t, r.Handle(at(59*time.Second), enqueue(alice, netease("x"))), RateLimited)
			wantError(t, r.Handle(at(59*time.Second), command(alice, Remove{Entry: 2})), RateLimited)

			if res := reply(t, r.Handle(at(time.Minute), enqueue(alice, netease("x")))); res.Err != nil {
				t.Fatalf("一分钟后恢复一次额度：%#v", res)
			}
			wantError(t, r.Handle(at(time.Minute), enqueue(alice, netease("y"))), RateLimited)
			wantError(t, r.Handle(at(time.Minute), command(alice, Remove{Entry: 2})), RateLimited)

			for i := range tc.burst {
				if res := reply(t, r.Handle(at(time.Minute), command(bob, Like{Entry: 2}))); res.Err != nil {
					t.Fatalf("点赞使用单独的额度，突发额度内的第 %d 次点赞失败：%#v", i+1, res)
				}
			}
			wantError(t, r.Handle(at(time.Minute), command(bob, Like{Entry: 2})), RateLimited)
		})
	}
}

func TestRotationTellsRequestersApartByIDAndName(t *testing.T) {
	cases := []struct {
		name          string
		first, second member.Member
	}{
		{"bot 替不同的人点歌", member.Member{ID: "bot", Name: "张三（bot）"}, member.Member{ID: "bot", Name: "李四（bot）"}},
		{"同名的不同成员", member.Member{ID: "m8", Name: "丁"}, member.Member{ID: "m9", Name: "丁"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRoom(t, Persistent)
			r.Handle(at(0), command(alice, SetPlayMode{Mode: Rotation}))
			playing(t, r, 0, netease("x"), time.Minute)
			r.Handle(at(time.Second), enqueue(tc.first, netease("a")))
			r.Handle(at(time.Second), enqueue(tc.first, netease("b")))
			r.Handle(at(time.Second), enqueue(tc.second, netease("c")))
			if got := ids(r.waiting); !slices.Equal(got, []EntryID{2, 4, 3}) {
				t.Fatalf("应算作两个点歌人轮流播放：%v", got)
			}
		})
	}
}

func TestRotationRemembersSongsThatHavePlayed(t *testing.T) {
	r := newRoom(t, Persistent)
	r.Handle(at(0), command(alice, SetPlayMode{Mode: Rotation}))
	pickReady(r, 0, alice, "a1")
	pickReady(r, 0, alice, "a2")
	pickReady(r, 0, bob, "b1")
	// 乙和丙轮流点歌，每次都等上一首放完才点：放过第 0 轮的人再点歌要排到第 1 轮，甲的第二首才不会一直被插队。
	var played []string
	for _, next := range []struct {
		by member.Member
		id string
	}{{carol, "c1"}, {bob, "b2"}, {carol, "c2"}, {bob, "b3"}, {carol, "c3"}} {
		now, song := finish(t, r)
		played = append(played, song)
		pickReady(r, now, next.by, next.id)
	}
	_, song := finish(t, r)
	if played = append(played, song); !slices.Equal(played, []string{"a1", "b1", "c1", "a2", "b2", "c2"}) {
		t.Fatalf("播放顺序 = %v，期望甲、乙、丙轮流", played)
	}
}

func TestRotationForgetsFinishedRounds(t *testing.T) {
	r := newRoom(t, Persistent)
	r.Handle(at(0), command(alice, SetPlayMode{Mode: Rotation}))
	pickReady(r, 0, alice, "a1")
	pickReady(r, 0, bob, "b1")
	pickReady(r, 0, alice, "a2")
	finish(t, r)
	now, _ := finish(t, r)
	pickReady(r, now, carol, "c1")
	if got := r.waiting[0].Round; got != 1 {
		t.Fatalf("第 0 轮放完之后新来的丙应排进第 1 轮：%d", got)
	}
	if want := map[requester]int{requesterOf(alice): 1}; !maps.Equal(r.served, want) {
		t.Fatalf("第 0 轮放完之后只需记着甲在第 1 轮开播：%v", r.served)
	}
}

func TestRotationStartsOverInAnEmptyRoom(t *testing.T) {
	r := newRoom(t, Persistent)
	r.Handle(at(0), command(alice, SetPlayMode{Mode: Rotation}))
	pickReady(r, 0, alice, "a1")
	pickReady(r, 0, alice, "a2")
	finish(t, r)
	now, _ := finish(t, r)
	pickReady(r, now, bob, "b1")
	pickReady(r, now, alice, "a3")
	pickReady(r, now, bob, "b2")
	if got := ids(r.waiting); !slices.Equal(got, []EntryID{4, 5}) {
		t.Fatalf("房间放空之后甲从第 0 轮重新开始，应排在乙的第二首前面：%v", got)
	}
}
