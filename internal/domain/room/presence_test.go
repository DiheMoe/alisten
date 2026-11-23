package room

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/dihemoe/alisten/internal/domain/member"
)

func TestJoinAndLeaveBroadcastSessions(t *testing.T) {
	r := newRoom(t, Persistent)

	effects := r.Handle(at(time.Second), Join{RequestID: "q1", Session: "s1", Member: alice})
	if got := events[MembersChanged](effects); len(got) != 1 || !slices.Equal(got[0].Sessions, []Session{{"s1", alice}}) {
		t.Fatalf("加入后的在线列表不对：%#v", effects)
	}
	if got := reply(t, effects); got != (Reply{RequestID: "q1", Result: Done{}}) {
		t.Fatalf("新会话加入后的回复 = %#v，期望 Done", got)
	}
	if len(find[Persist](effects)) != 0 {
		t.Fatal("在线会话不落盘")
	}
	effects = r.Handle(at(2*time.Second), Join{RequestID: "q2", Session: "s1", Member: alice})
	if len(effects) != 1 || reply(t, effects) != (Reply{RequestID: "q2", Result: Done{}}) {
		t.Fatalf("会话 ID 已经在列表里时，新会话的 Join 只回复 Done：%#v", effects)
	}
	if effects := r.Handle(at(2*time.Second), Join{Session: "s1", Member: alice, Rejoin: true}); len(effects) != 0 {
		t.Fatalf("会话 ID 已经在列表里时，重新加入不应产生输出：%#v", effects)
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
	plays := recorded(effects)
	if len(plays) != 1 || plays[0].Entry.Track != netease("a") || !plays[0].EndedAt.Equal(t0.Add(6*time.Minute)) {
		t.Fatalf("关闭房间时应记下正在播放的歌：%#v", effects)
	}
	recordAt := slices.IndexFunc(effects, func(e Effect) bool { p, ok := e.(Persist); return ok && len(p.Plays) > 0 })
	closeAt := slices.IndexFunc(effects, func(e Effect) bool { _, ok := e.(Close); return ok })
	if closeAt < recordAt {
		t.Fatalf("房间的播放记录应在关闭房间之前写入：%#v", effects)
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
	plays := recorded(r.Handle(at(6*time.Minute), Timer{Token: closing[0].Token}))
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

func sessionIDs(r *Room) []SessionID {
	out := make([]SessionID, len(r.sessions))
	for i, s := range r.sessions {
		out[i] = s.ID
	}
	return out
}

func joinAs(t *testing.T, r *Room, session SessionID, m member.Member) {
	t.Helper()
	if got := reply(t, r.Handle(at(0), Join{RequestID: "q", Session: session, Member: m})); got.Err != nil {
		t.Fatalf("%s 用 %s 加入被拒：%v", m.Name, session, got.Err)
	}
}

func wantRefused(t *testing.T, effects []Effect, kind ErrorKind, message string) {
	t.Helper()
	wantError(t, effects, kind)
	if got := reply(t, effects).Err.Message; got != message {
		t.Fatalf("拒绝的文案 = %q，期望 %q", got, message)
	}
}

// 用重新加入让人数超过上限，和上限调到比当前人数还低是同一种状态。
func TestPeopleOverCapacityStayButNewMembersAreRefused(t *testing.T) {
	r := newRoomWithCapacity(t, Persistent, 2)
	for i, m := range []member.Member{alice, bob, carol} {
		r.Handle(at(0), Join{Session: SessionID(fmt.Sprint("s", i)), Member: m, Rejoin: true})
	}

	wantRefused(t, r.Handle(at(time.Second), Join{RequestID: "q", Session: "d1", Member: dave}), RoomFull, "房间已满，最多 2 人。")
	joinAs(t, r, "c2", carol)
	r.Handle(at(2*time.Second), Leave{Session: "s0"})
	wantRefused(t, r.Handle(at(2*time.Second), Join{RequestID: "q", Session: "d1", Member: dave}), RoomFull, "房间已满，最多 2 人。")
	r.Handle(at(3*time.Second), Leave{Session: "s1"})
	joinAs(t, r, "d1", dave)
	if got := sessionIDs(r); !slices.Equal(got, []SessionID{"s2", "c2", "d1"}) {
		t.Fatalf("在线列表 = %v，期望 carol 的两个会话和 dave", got)
	}
}

func TestLoweringCapacityKeepsPeopleAndRefusesNewMembers(t *testing.T) {
	r := newRoom(t, Persistent)
	joinAs(t, r, "a1", alice)
	joinAs(t, r, "b1", bob)
	joinAs(t, r, "c1", carol)

	capacity := 2
	effects := r.Handle(at(time.Second), UpdateInfo{RequestID: "q", Capacity: &capacity})
	if !persistsFirst(effects) || find[Persist](effects)[0].Snapshot.Info.Capacity != 2 {
		t.Fatalf("修改上限之后应先落盘新的上限：%#v", effects)
	}
	if got := events[InfoChanged](effects); !slices.Equal(got, []InfoChanged{{Name: "房间", Capacity: 2}}) {
		t.Fatalf("修改上限之后广播的房间信息 = %+v，期望带上新的上限", got)
	}
	if len(events[MembersChanged](effects)) != 0 || !slices.Equal(sessionIDs(r), []SessionID{"a1", "b1", "c1"}) {
		t.Fatalf("上限调到比当前人数还低时不踢人，在线列表 = %v", sessionIDs(r))
	}

	wantRefused(t, r.Handle(at(2*time.Second), Join{RequestID: "q", Session: "d1", Member: dave}), RoomFull, "房间已满，最多 2 人。")
	joinAs(t, r, "a2", alice)
	r.Handle(at(3*time.Second), Leave{Session: "c1"})
	wantRefused(t, r.Handle(at(3*time.Second), Join{RequestID: "q", Session: "d1", Member: dave}), RoomFull, "房间已满，最多 2 人。")
	r.Handle(at(4*time.Second), Leave{Session: "b1"})
	joinAs(t, r, "d1", dave)
}

func TestRejoinIsNotCheckedOrAnswered(t *testing.T) {
	r := newRoomWithCapacity(t, Ephemeral, 2)
	joinAs(t, r, "a1", alice)
	joinAs(t, r, "b1", bob)

	effects := r.Handle(at(time.Second), Join{Session: "c1", Member: carol, Rejoin: true})
	if len(find[Reply](effects)) != 0 || len(events[MembersChanged](effects)) != 1 {
		t.Fatalf("重新加入满员的房间应加入在线列表，不回复：%#v", effects)
	}
	for i := 2; i <= maxSessionsPerMember+1; i++ {
		effects := r.Handle(at(time.Second), Join{Session: SessionID(fmt.Sprint("a", i)), Member: alice, Rejoin: true})
		if len(find[Reply](effects)) != 0 || len(events[MembersChanged](effects)) != 1 {
			t.Fatalf("重新加入 alice 的第 %d 个连接应加入在线列表，不回复：%#v", i, effects)
		}
	}
	if people, n := OnlineCount(r.sessions), len(r.sessions); people != 3 || n != 7 {
		t.Fatalf("重新加入之后有 %d 人、%d 个会话，期望 3 人、7 个会话，其中 alice 有 5 个", people, n)
	}
}
