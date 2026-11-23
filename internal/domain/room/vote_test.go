package room

import (
	"fmt"
	"testing"
	"time"

	"github.com/dihemoe/alisten/internal/domain/member"
)

func TestVoteSkipThreshold(t *testing.T) {
	cases := []struct{ people, required int }{{0, 1}, {3, 1}, {4, 2}, {6, 2}, {7, 3}}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%d 个人", tc.people), func(t *testing.T) {
			r := newRoom(t, Persistent)
			for i := range tc.people {
				r.Handle(at(0), Join{Session: SessionID(fmt.Sprint("s", i)), Member: member.Member{ID: fmt.Sprint("listener-", i)}})
			}
			playing(t, r, 0, netease("a"), time.Minute)

			want := VoteResult{Votes: 1, Required: tc.required, Skipped: tc.required == 1}
			if res, ok := reply(t, r.Handle(at(time.Second), command(bob, VoteSkip{}))).Result.(VoteResult); !ok || res != want {
				t.Fatalf("%d 个人的投票结果 = %#v，期望 %#v", tc.people, res, want)
			}
		})
	}
}

func TestVoteSkipCountsPeopleNotConnections(t *testing.T) {
	r := newRoom(t, Persistent)
	for i := range 4 {
		r.Handle(at(0), Join{Session: SessionID(fmt.Sprint("a", i)), Member: alice})
	}
	r.Handle(at(0), Join{Session: "c", Member: carol})
	playing(t, r, 0, netease("a"), time.Minute)

	want := VoteResult{Votes: 1, Required: 1, Skipped: true}
	if res, ok := reply(t, r.Handle(at(time.Second), command(alice, VoteSkip{}))).Result.(VoteResult); !ok || res != want {
		t.Fatalf("两个人、其中一人开了 4 个连接时的投票结果 = %#v，期望 %#v", res, want)
	}
}

func TestOnlineCountCountsEachMemberOnce(t *testing.T) {
	sessions := []Session{{ID: "a1", Member: alice}, {ID: "a2", Member: alice}, {ID: "b1", Member: bob}}
	if got := OnlineCount(sessions); got != 2 {
		t.Fatalf("OnlineCount = %d，期望 2", got)
	}
	if got := OnlineCount(nil); got != 0 {
		t.Fatalf("没有会话时 OnlineCount = %d，期望 0", got)
	}
}

func TestVotesResetForNextSong(t *testing.T) {
	r := newRoom(t, Persistent)
	for i, m := range []member.Member{alice, bob, carol, dave} {
		r.Handle(at(0), Join{Session: SessionID(fmt.Sprint("s", i)), Member: m})
	}
	playing(t, r, 0, netease("a"), time.Minute)
	r.Handle(at(0), enqueue(alice, netease("b")))
	r.Handle(at(0), Prepared{Track: netease("b"), Result: ready(time.Minute)})
	r.Handle(at(time.Second), command(alice, VoteSkip{}))

	r.Handle(at(2*time.Minute), Timer{Token: r.endTimer})
	if r.current == nil || r.current.Entry.Track != netease("b") {
		t.Fatalf("第一首播完之后应开播下一首：%#v", r.current)
	}
	effects := r.Handle(at(2*time.Minute), command(bob, VoteSkip{}))
	if res, ok := reply(t, effects).Result.(VoteResult); !ok || res != (VoteResult{Votes: 1, Required: 2}) {
		t.Fatalf("下一首开播后重新计票：%#v", effects)
	}
	if res := reply(t, r.Handle(at(2*time.Minute), command(alice, VoteSkip{}))); res.Err != nil {
		t.Fatalf("给上一首投过票的成员可以给下一首投票：%#v", res)
	}
}

func TestVoteWithoutPlayback(t *testing.T) {
	r := newRoom(t, Persistent)
	wantError(t, r.Handle(at(0), command(alice, VoteSkip{})), NothingPlaying)
	r.Handle(at(0), enqueue(alice, netease("a")))
	wantError(t, r.Handle(at(time.Second), command(alice, VoteSkip{})), NothingPlaying)
}

func TestStaleTimerIsIgnored(t *testing.T) {
	r := newRoom(t, Persistent)
	playing(t, r, 0, netease("a"), 3*time.Minute)
	stale := r.endTimer
	r.Handle(at(time.Second), command(alice, VoteSkip{}))
	if effects := r.Handle(at(3*time.Minute), Timer{Token: stale}); len(effects) != 0 {
		t.Fatalf("已被切掉的歌的定时器应忽略：%#v", effects)
	}
}

func TestSkipDuringStartLeadRecordsNoNegativeDuration(t *testing.T) {
	r := newRoom(t, Persistent)
	playing(t, r, 0, netease("a"), time.Minute)
	plays := recorded(r.Handle(at(0), command(alice, VoteSkip{})))
	if len(plays) != 1 || !plays[0].EndedAt.Equal(plays[0].StartedAt) {
		t.Fatalf("开播前就被切掉时，结束时间等于开始时间：%#v", plays)
	}
}

// 4 个人在线，alice 已经投了 1 票，门槛是 2。
func votedRoom(t *testing.T) *Room {
	t.Helper()
	r := newRoom(t, Persistent)
	for i, m := range []member.Member{alice, bob, carol, dave} {
		r.Handle(at(0), Join{Session: SessionID(fmt.Sprint("s", i)), Member: m})
	}
	playing(t, r, 0, netease("a"), time.Minute)
	pickReady(r, 0, bob, "b")
	if res, ok := reply(t, r.Handle(at(time.Second), command(alice, VoteSkip{}))).Result.(VoteResult); !ok || res.Skipped {
		t.Fatalf("4 个人时 1 票不够：%#v", res)
	}
	return r
}

func TestAVoteStaysAfterTheVoterLeaves(t *testing.T) {
	r := votedRoom(t)
	r.Handle(at(2*time.Second), Leave{Session: "s0"})
	if r.current == nil || r.current.Entry.Track != netease("b") {
		t.Fatalf("alice 离开后她的票仍然算数，3 个人的门槛是 1，应切歌：%#v", r.current)
	}
}
