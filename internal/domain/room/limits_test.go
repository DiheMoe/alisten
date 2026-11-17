package room

import (
	"testing"

	"github.com/dihemoe/alisten/internal/domain/track"
)

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
