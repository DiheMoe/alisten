package room

import (
	"testing"
	"time"

	"github.com/dihemoe/alisten/internal/domain/track"
)

func TestChatIsLimitedPerMember(t *testing.T) {
	r := newRoom(t, Persistent)
	chat := func(now time.Duration, m Command) Reply {
		t.Helper()
		return reply(t, r.Handle(at(now), m))
	}
	for i := range chatBurst {
		if res := chat(0, command(alice, Chat{Text: "你好"})); res.Err != nil {
			t.Fatalf("第 %d 条聊天被拒：%v", i+1, res.Err)
		}
	}
	if res := chat(0, command(alice, Chat{Text: "你好"})); res.Err != ErrRateLimited {
		t.Fatalf("连发 %d 条之后再发 = %v，期望 ErrRateLimited", chatBurst, res.Err)
	}
	if res := chat(0, command(bob, Chat{Text: "你好"})); res.Err != nil {
		t.Fatalf("别的成员发聊天 = %v，期望不受影响", res.Err)
	}
	if res := chat(chatRefill, command(alice, Chat{Text: "你好"})); res.Err != nil {
		t.Fatalf("过了 %v 再发 = %v，期望补回一条", chatRefill, res.Err)
	}
	if res := chat(chatRefill, command(alice, Chat{Text: "你好"})); res.Err != ErrRateLimited {
		t.Fatalf("补回的一条用掉之后再发 = %v，期望 ErrRateLimited", res.Err)
	}
}

func TestPlayModeIsLimitedPerMember(t *testing.T) {
	r := newRoom(t, Persistent)
	modes := []PlayMode{Random, Sequential, Rotation, Random}
	for i := range modeBurst {
		if res := reply(t, r.Handle(at(0), command(alice, SetPlayMode{Mode: modes[i]}))); res.Err != nil {
			t.Fatalf("第 %d 次切换被拒：%v", i+1, res.Err)
		}
	}
	effects := r.Handle(at(0), command(alice, SetPlayMode{Mode: modes[modeBurst]}))
	if res := reply(t, effects); res.Err != ErrRateLimited || len(find[Persist](effects)) != 0 {
		t.Fatalf("连切 %d 次之后再切 = %#v，期望 ErrRateLimited，不落盘", modeBurst, effects)
	}
	if res := reply(t, r.Handle(at(0), command(bob, SetPlayMode{Mode: Random}))); res.Err != nil {
		t.Fatalf("别的成员切换 = %v，期望不受影响", res.Err)
	}
	if res := reply(t, r.Handle(at(modeRefill), command(alice, SetPlayMode{Mode: Sequential}))); res.Err != nil {
		t.Fatalf("过了 %v 再切 = %v，期望补回一次", modeRefill, res.Err)
	}
}

func TestIdleMemberLimitsAreForgotten(t *testing.T) {
	r := newRoom(t, Persistent)
	for _, m := range []Command{command(alice, Chat{Text: "你好"}), command(bob, Chat{Text: "你好"})} {
		r.Handle(at(0), m)
	}
	r.Handle(at(time.Hour), command(carol, Chat{Text: "你好"}))
	if len(r.chatLimits) != 1 {
		t.Errorf("一小时之后还记着 %d 个成员的聊天额度，期望只剩刚发过的那个", len(r.chatLimits))
	}
}

func TestRejectedPicksKeepThePickQuota(t *testing.T) {
	cases := map[string]struct {
		before   Command
		rejected Command
		kind     ErrorKind
	}{
		"删除不存在的条目": {rejected: command(alice, Remove{Entry: 99}), kind: NotFound},
		"临时房间点链接":  {rejected: enqueue(alice, track.Track{Source: track.Link, ID: "https://example.com/a.mp3"}), kind: Forbidden},
		"重复点歌":     {before: enqueue(alice, netease("a")), rejected: enqueue(alice, netease("a")), kind: Conflict},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRoom(t, Ephemeral)
			if c.before.Action != nil {
				r.Handle(at(0), c.before)
			}
			for range 2 * EphemeralBurst {
				wantError(t, r.Handle(at(0), c.rejected), c.kind)
			}
			if res := reply(t, r.Handle(at(0), enqueue(bob, netease("b")))); res.Err != nil {
				t.Fatalf("被拒的请求之后点歌 = %v，期望房间的点歌额度没有被用掉", res.Err)
			}
		})
	}
}

func TestLikingAMissingEntryKeepsTheLikeQuota(t *testing.T) {
	r := newRoom(t, Ephemeral)
	// 空闲的房间里第一首歌直接成为待播，第二首才留在等待队列里，只有等待队列里的歌能点赞。
	r.Handle(at(0), enqueue(alice, netease("a")))
	added := reply(t, r.Handle(at(0), enqueue(alice, netease("b")))).Result.(EnqueueResult).Entry
	for range 2 * EphemeralBurst {
		wantError(t, r.Handle(at(0), command(alice, Like{Entry: 99})), NotFound)
	}
	if res := reply(t, r.Handle(at(0), command(bob, Like{Entry: added.ID}))); res.Err != nil {
		t.Fatalf("给不存在的条目点赞之后再点赞 = %v，期望房间的点赞额度没有被用掉", res.Err)
	}
}
