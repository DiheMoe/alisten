package room

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestChatTruncatesTo500Characters(t *testing.T) {
	r := newRoom(t, Persistent)
	effects := r.Handle(at(time.Second), command(alice, Chat{Text: strings.Repeat("好", 600)}))

	got := events[ChatPosted](effects)
	if len(got) != 1 || utf8.RuneCountInString(got[0].Text) != 500 || got[0].Member != alice || !got[0].SentAt.Equal(t0.Add(time.Second)) {
		t.Fatalf("聊天内容应截断为 500 个字符：%#v", effects)
	}
	if len(find[Persist](effects)) != 0 {
		t.Fatal("聊天不落盘")
	}
	if got := events[ChatPosted](r.Handle(at(time.Second), command(alice, Chat{Text: "  "}))); len(got) != 1 || got[0].Text != "  " {
		t.Fatal("空白内容照常广播")
	}
}

func TestDeleteEndsThePlayingSong(t *testing.T) {
	r := newRoom(t, Persistent)
	playing(t, r, 0, netease("a"), 3*time.Minute)
	started := r.current.StartedAt

	effects := r.Handle(at(time.Minute), Delete{RequestID: "q"})
	var kinds []string
	for _, e := range effects {
		kinds = append(kinds, fmt.Sprintf("%T", e))
	}
	// Close 会删除房间，房间的播放记录必须先写入。
	if want := []string{"room.Persist", "room.Emit", "room.Close", "room.Reply"}; !slices.Equal(kinds, want) {
		t.Fatalf("删除时应依次落盘并记录播放、广播、关闭、回复：%v", kinds)
	}
	plays := recorded(effects)
	if plays[0].Entry.Track != netease("a") || !plays[0].StartedAt.Equal(started) || !plays[0].EndedAt.Equal(t0.Add(time.Minute)) {
		t.Fatalf("删除房间时应记下正在播放的歌：%#v", plays)
	}
	if pb := events[PlaybackChanged](effects); len(pb) != 1 || pb[0].Current != nil {
		t.Fatalf("删除房间时播放应停止：%#v", effects)
	}
}

func TestDeleteRecordsAnOverdueSongAtItsEnd(t *testing.T) {
	r := newRoom(t, Persistent)
	playing(t, r, 0, netease("a"), 3*time.Minute)
	ends := r.current.EndsAt()

	plays := recorded(r.Handle(at(10*time.Minute), Delete{RequestID: "q"}))
	if len(plays) != 1 || !plays[0].EndedAt.Equal(ends) {
		t.Fatalf("已超过原定结束时间的歌应按原定结束时间记录：%#v", plays)
	}
}

func TestInputsWithTokenZeroAreDropped(t *testing.T) {
	if effects := newRoom(t, Persistent).Handle(at(0), Timer{Token: 0}); len(effects) != 0 {
		t.Fatalf("空闲的房间不应响应 token 为 0 的定时器：%#v", effects)
	}
	if effects := listenedRoom(t).Handle(at(time.Minute), Recommended{Token: 0, Track: netease("b")}); len(effects) != 0 {
		t.Fatalf("没有待回的推荐时不应接受 token 为 0 的推荐：%#v", effects)
	}
}

func TestUpdateInfoKeepsFieldsNotGiven(t *testing.T) {
	r, _ := Restore(at(0), 1, Snapshot{Info: Info{ID: "r1", Name: "房间", Description: "介绍", Kind: Persistent, Capacity: 64}, Mode: Sequential})
	name, description, capacity := "新名字", "新介绍", 2
	tests := []struct {
		in   UpdateInfo
		want []InfoChanged
	}{
		{UpdateInfo{RequestID: "q1", Name: &name}, []InfoChanged{{Name: "新名字", Description: "介绍", Capacity: 64}}},
		{UpdateInfo{RequestID: "q2", Description: &description}, []InfoChanged{{Name: "新名字", Description: "新介绍", Capacity: 64}}},
		{UpdateInfo{RequestID: "q3", Capacity: &capacity}, []InfoChanged{{Name: "新名字", Description: "新介绍", Capacity: 2}}},
		{UpdateInfo{RequestID: "q4"}, nil},
	}
	for _, tt := range tests {
		effects := r.Handle(at(0), tt.in)
		if got := events[InfoChanged](effects); !slices.Equal(got, tt.want) {
			t.Errorf("%s 之后的 InfoChanged = %+v，期望 %+v", tt.in.RequestID, got, tt.want)
		}
		if replies := find[Reply](effects); len(replies) != 1 || replies[0].RequestID != tt.in.RequestID || replies[0].Err != nil {
			t.Errorf("%s 的回复 = %+v，期望成功", tt.in.RequestID, replies)
		}
	}
}
