package room

import (
	"fmt"
	"testing"
	"time"
)

func TestVoteSkipThreshold(t *testing.T) {
	cases := []struct{ sessions, required int }{{0, 1}, {3, 1}, {4, 2}, {6, 2}, {7, 3}}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%d 个会话", tc.sessions), func(t *testing.T) {
			r := newRoom(t, Persistent)
			for i := range tc.sessions {
				r.Handle(at(0), Join{Session: SessionID(fmt.Sprint("s", i)), Member: alice})
			}
			playing(t, r, 0, netease("a"), time.Minute)

			want := VoteResult{Votes: 1, Required: tc.required, Skipped: tc.required == 1}
			if res, ok := reply(t, r.Handle(at(time.Second), command(bob, VoteSkip{}))).Result.(VoteResult); !ok || res != want {
				t.Fatalf("%d 个会话的投票结果 = %#v，期望 %#v", tc.sessions, res, want)
			}
		})
	}
}

func TestVotesResetForNextSong(t *testing.T) {
	r := newRoom(t, Persistent)
	for _, s := range []SessionID{"s1", "s2", "s3", "s4"} {
		r.Handle(at(0), Join{Session: s, Member: alice})
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
	plays := find[RecordPlay](r.Handle(at(0), command(alice, VoteSkip{})))
	if len(plays) != 1 || !plays[0].EndedAt.Equal(plays[0].StartedAt) {
		t.Fatalf("开播前就被切掉时，结束时间等于开始时间：%#v", plays)
	}
}
