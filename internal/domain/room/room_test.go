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
	// Close 会删除房间，播放记录必须先写入。
	if want := []string{"room.Persist", "room.RecordPlay", "room.Emit", "room.Close", "room.Reply"}; !slices.Equal(kinds, want) {
		t.Fatalf("删除时应依次落盘、记录播放、广播、关闭、回复：%v", kinds)
	}
	plays := find[RecordPlay](effects)
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

	plays := find[RecordPlay](r.Handle(at(10*time.Minute), Delete{RequestID: "q"}))
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
