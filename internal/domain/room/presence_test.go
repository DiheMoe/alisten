package room

import (
	"slices"
	"testing"
	"time"

	"github.com/dihemoe/alisten/internal/domain/member"
)

func TestJoinAndLeaveBroadcastSessions(t *testing.T) {
	r := newRoom(t, Persistent)

	effects := r.Handle(at(time.Second), Join{Session: "s1", Member: alice})
	if got := events[MembersChanged](effects); len(got) != 1 || !slices.Equal(got[0].Sessions, []Session{{"s1", alice}}) {
		t.Fatalf("加入后的在线列表不对：%#v", effects)
	}
	if len(find[Persist](effects)) != 0 {
		t.Fatal("在线会话不落盘")
	}
	if effects := r.Handle(at(2*time.Second), Join{Session: "s1", Member: alice}); len(effects) != 0 {
		t.Fatalf("重复的 Join 不应产生输出：%#v", effects)
	}

	effects = r.Handle(at(3*time.Second), Leave{Session: "s1"})
	if got := events[MembersChanged](effects); len(got) != 1 || len(got[0].Sessions) != 0 {
		t.Fatalf("离开后的在线列表不对：%#v", effects)
	}
	if len(find[Schedule](effects)) != 0 {
		t.Fatal("常驻房间无人时不关闭")
	}
	if effects := r.Handle(at(4*time.Second), Leave{Session: "s1"}); len(effects) != 0 {
		t.Fatalf("不在线的会话 Leave 不应产生输出：%#v", effects)
	}
}

func TestEphemeralRoomCloseEndsThePlayingSong(t *testing.T) {
	r := newRoom(t, Ephemeral)
	r.Handle(at(0), Join{Session: "s1", Member: alice})
	playing(t, r, 0, netease("a"), 10*time.Minute)

	closing := find[Schedule](r.Handle(at(time.Minute), Leave{Session: "s1"}))
	if len(closing) != 1 {
		t.Fatalf("最后一个会话离开后应开始无人计时：%#v", closing)
	}
	effects := r.Handle(at(6*time.Minute), Timer{Token: closing[0].Token})
	plays := find[RecordPlay](effects)
	if len(plays) != 1 || plays[0].Entry.Track != netease("a") || !plays[0].EndedAt.Equal(t0.Add(6*time.Minute)) {
		t.Fatalf("关闭房间时应记下正在播放的歌：%#v", effects)
	}
	recordAt := slices.IndexFunc(effects, func(e Effect) bool { _, ok := e.(RecordPlay); return ok })
	closeAt := slices.IndexFunc(effects, func(e Effect) bool { _, ok := e.(Close); return ok })
	if closeAt < recordAt {
		t.Fatalf("播放记录应在关闭房间之前写入：%#v", effects)
	}
}

func TestEphemeralRoomCloseRecordsAnOverdueSongAtItsEnd(t *testing.T) {
	r := newRoom(t, Ephemeral)
	r.Handle(at(0), Join{Session: "s1", Member: alice})
	playing(t, r, 0, netease("a"), 2*time.Minute)
	ends := r.current.EndsAt()

	closing := find[Schedule](r.Handle(at(time.Minute), Leave{Session: "s1"}))
	if len(closing) != 1 {
		t.Fatalf("最后一个会话离开后应开始无人计时：%#v", closing)
	}
	plays := find[RecordPlay](r.Handle(at(6*time.Minute), Timer{Token: closing[0].Token}))
	if len(plays) != 1 || !plays[0].EndedAt.Equal(ends) {
		t.Fatalf("已超过原定结束时间的歌应按原定结束时间记录：%#v", plays)
	}
}

func TestMemberUpdatedRefreshesAllSessionsOfMember(t *testing.T) {
	r := newRoom(t, Persistent)
	r.Handle(at(0), Join{Session: "s1", Member: alice})
	r.Handle(at(0), Join{Session: "s2", Member: alice})
	r.Handle(at(0), Join{Session: "s3", Member: bob})

	renamed := member.Member{ID: alice.ID, Name: "甲二"}
	got := events[MembersChanged](r.Handle(at(time.Second), MemberUpdated{Member: renamed}))
	want := []Session{{"s1", renamed}, {"s2", renamed}, {"s3", bob}}
	if len(got) != 1 || !slices.Equal(got[0].Sessions, want) {
		t.Fatalf("在线列表 = %#v，期望 %#v", got, want)
	}
	if effects := r.Handle(at(2*time.Second), MemberUpdated{Member: member.Member{ID: "m9"}}); len(effects) != 0 {
		t.Fatalf("不在线的成员更新资料不应产生输出：%#v", effects)
	}
}
