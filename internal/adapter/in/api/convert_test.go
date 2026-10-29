package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/dihemoe/alisten/internal/app/catalog"
	"github.com/dihemoe/alisten/internal/app/media"
	"github.com/dihemoe/alisten/internal/domain/member"
	"github.com/dihemoe/alisten/internal/domain/room"
	"github.com/dihemoe/alisten/internal/domain/track"
	"github.com/dihemoe/alisten/protocol"
)

var (
	alice      = member.Member{ID: "0199f1c2-8a4b-7c3d-9e5f-6a7b8c9d0e1f", Name: "alice", AvatarHash: "c160f8cc69a4f0bf2b0362752353d060"}
	sunnyTrack = track.Track{Source: track.NetEase, ID: "186016"}
	sunnyEntry = room.Entry{
		ID:    12,
		Track: sunnyTrack,
		Meta:  track.Meta{Title: "晴天", Artist: "周杰伦", Album: "叶惠美", CoverURL: "https://p1.music.126.net/example.jpg", PageURL: "https://music.163.com/#/song?id=186016", Duration: 269 * time.Second},
		By:    alice,
		Likes: 2,
		Ready: true,
		Prep:  track.Prepared{Duration: 269500 * time.Millisecond, Gain: 0.75, Lyric: "[00:01.00]故事的小黄花"},
	}
	linkTrack = track.Track{Source: track.Link, ID: "https://www.youtube.com/watch?v=x"}
)

func jsonOf(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSnapshotDTO(t *testing.T) {
	s := &server{Services: Services{StorageURL: "https://alisten.example/storage/alisten"}}
	auto := room.Entry{ID: 14, Track: track.Track{Source: track.NetEase, ID: "185811"}, Meta: track.Meta{Title: "七里香"}, Auto: true, Ready: true}
	pending := room.Entry{ID: 13, Track: linkTrack, Meta: track.Meta{Title: linkTrack.ID, PageURL: linkTrack.ID}, By: alice}
	view := room.View{
		Term: 3,
		Seq:  128,
		Snapshot: room.Snapshot{
			Info:    room.Info{ID: "0199f1c2-0000-7000-8000-000000000001", Name: "大厅", Description: "常驻", Kind: room.Persistent},
			Mode:    room.Random,
			Waiting: []room.Entry{auto},
			Pending: &pending,
			Current: &room.NowPlaying{Entry: sunnyEntry, StartedAt: time.UnixMilli(1760000000123)},
		},
		Sessions: []room.Session{{ID: "s1", Member: alice}, {ID: "s2", Member: alice}},
	}
	want := `{"items":[{"auto":true,"by":null,"id":"14","likes":0,"meta":{"album":"","artist":"","coverUrl":"","durationMs":0,"pageUrl":"","title":"七里香"},"status":"ready","track":{"id":"185811","source":"netease"}}],` +
		`"members":{"onlineCount":2,"sessions":[{"avatarHash":"c160f8cc69a4f0bf2b0362752353d060","id":"0199f1c2-8a4b-7c3d-9e5f-6a7b8c9d0e1f","name":"alice"},{"avatarHash":"c160f8cc69a4f0bf2b0362752353d060","id":"0199f1c2-8a4b-7c3d-9e5f-6a7b8c9d0e1f","name":"alice"}]},` +
		`"nowPlaying":{"durationMs":269500,"entry":{"auto":false,"by":{"avatarHash":"c160f8cc69a4f0bf2b0362752353d060","id":"0199f1c2-8a4b-7c3d-9e5f-6a7b8c9d0e1f","name":"alice"},"id":"12","likes":2,"meta":{"album":"叶惠美","artist":"周杰伦","coverUrl":"https://p1.music.126.net/example.jpg","durationMs":269000,"pageUrl":"https://music.163.com/#/song?id=186016","title":"晴天"},"status":"ready","track":{"id":"186016","source":"netease"}},"gain":0.75,"lyric":"[00:01.00]故事的小黄花","mediaUrl":"/media/netease/186016","startedAtMs":1760000000123},` +
		`"pending":{"auto":false,"by":{"avatarHash":"c160f8cc69a4f0bf2b0362752353d060","id":"0199f1c2-8a4b-7c3d-9e5f-6a7b8c9d0e1f","name":"alice"},"id":"13","likes":0,"meta":{"album":"","artist":"","coverUrl":"","durationMs":0,"pageUrl":"https://www.youtube.com/watch?v=x","title":"https://www.youtube.com/watch?v=x"},"status":"preparing","track":{"id":"https://www.youtube.com/watch?v=x","source":"link"}},` +
		`"playMode":"random","room":{"description":"常驻","id":"0199f1c2-0000-7000-8000-000000000001","kind":"persistent","name":"大厅"},"seq":128,"term":3}`
	if got := jsonOf(t, s.snapshot(view)); got != want {
		t.Errorf("快照编码为\n%s\n期望\n%s", got, want)
	}

	empty := room.View{Term: 1, Snapshot: room.Snapshot{Info: room.Info{ID: "r", Kind: room.Ephemeral}, Mode: room.Sequential}}
	wantEmpty := `{"items":[],"members":{"onlineCount":0,"sessions":[]},"nowPlaying":null,"pending":null,"playMode":"sequential","room":{"description":"","id":"r","kind":"ephemeral","name":""},"seq":0,"term":1}`
	if got := jsonOf(t, s.snapshot(empty)); got != wantEmpty {
		t.Errorf("空房间的快照编码为\n%s\n期望\n%s", got, wantEmpty)
	}
}

func TestEventDTO(t *testing.T) {
	s := &server{}
	sentAt := time.UnixMilli(1760000000456)
	tests := []struct {
		event room.Event
		typ   string
		data  string
	}{
		{room.ChatPosted{Member: alice, Text: "你好", SentAt: sentAt}, "chat.message", `{"content":"你好","member":{"avatarHash":"c160f8cc69a4f0bf2b0362752353d060","id":"0199f1c2-8a4b-7c3d-9e5f-6a7b8c9d0e1f","name":"alice"},"sentAtMs":1760000000456}`},
	}
	for _, tt := range tests {
		typ, data := s.event(tt.event)
		if got := jsonOf(t, data); typ != tt.typ || got != tt.data {
			t.Errorf("%T 编码为 %s %s，期望 %s %s", tt.event, typ, got, tt.typ, tt.data)
		}
	}
}

func TestActivityDTO(t *testing.T) {
	s := &server{}
	plain := room.Entry{ID: 7, Track: sunnyTrack, Meta: track.Meta{Title: "晴天"}, By: alice, Likes: 3}
	tests := []struct {
		activity room.Activity
		member   bool
		votes    bool
		message  string
	}{
		{room.Activity{Kind: room.ActivityRemoved, Member: alice, Entry: plain}, true, false, "移除了《晴天》"},
		{room.Activity{Kind: room.ActivityVoted, Member: alice, Entry: plain, Votes: 1, Required: 2}, true, true, "投票切歌（1/2）"},
	}
	for _, tt := range tests {
		got := s.activity(tt.activity)
		if got.Message != tt.message || (got.Member != nil) != tt.member || (got.Votes != nil) != tt.votes || (got.Required != nil) != tt.votes || got.Entry == nil {
			t.Errorf("%v 编码为 %s，期望消息 %q", tt.activity.Kind, jsonOf(t, got), tt.message)
		}
	}
	voted := s.activity(room.Activity{Kind: room.ActivityVoted, Member: alice, Entry: plain, Votes: 1, Required: 2})
	if *voted.Votes != 1 || *voted.Required != 2 || voted.Kind != protocol.ActivityKindVoted {
		t.Errorf("投票的动态 = %s", jsonOf(t, voted))
	}
}

func TestRoomErrorCodes(t *testing.T) {
	kinds := map[room.ErrorKind]protocol.ErrorCode{
		room.Conflict:       protocol.ErrorCodeConflict,
		room.QueueFull:      protocol.ErrorCodeQueueFull,
		room.RateLimited:    protocol.ErrorCodeRateLimited,
		room.NothingPlaying: protocol.ErrorCodeNothingPlaying,
	}
	for kind, code := range kinds {
		info, expected := errorInfo(&room.Error{Kind: kind, Message: "说明"})
		if info != (protocol.ErrorInfo{Code: code, Message: "说明"}) || !expected {
			t.Errorf("领域错误 %d 映射为 %#v, %v，期望 %s", kind, info, expected, code)
		}
	}
	if _, expected := errorInfo(errors.New("连接中断")); expected {
		t.Error("未知的错误应按内部错误处理")
	}
}

func TestPlayModes(t *testing.T) {
	modes := map[room.PlayMode]protocol.PlayMode{
		room.Sequential: protocol.PlayModeSequential,
		room.Random:     protocol.PlayModeRandom,
		room.Rotation:   protocol.PlayModeRotation,
	}
	for mode, dto := range modes {
		if got := playMode(mode); got != dto {
			t.Errorf("播放模式 %d 编码为 %q，期望 %q", mode, got, dto)
		}
		if got := playModeOf(dto); got != mode {
			t.Errorf("%q 解码为播放模式 %d，期望 %d", dto, got, mode)
		}
	}
}

func TestRejectedLinkIsInvalidArgument(t *testing.T) {
	info, expected := errorInfo(fmt.Errorf("点歌：%w", &catalog.RejectedLinkError{Reason: "哔哩哔哩的番剧不能点播"}))
	if info != (protocol.ErrorInfo{Code: protocol.ErrorCodeInvalidArgument, Message: "哔哩哔哩的番剧不能点播"}) || !expected {
		t.Errorf("不能点的链接 = %#v, %v，期望 INVALID_ARGUMENT，message 是音源给出的原因", info, expected)
	}
}

func TestMediaURL(t *testing.T) {
	s := &server{Services: Services{StorageURL: "https://alisten.example/storage/alisten"}}
	video := track.Track{Source: track.Bilibili, ID: "BV1xx411c7mD/2"}
	tests := map[track.Track]string{
		sunnyTrack:                               "/media/netease/186016",
		{Source: track.QQ, ID: "0039MnYb0qxYhV"}: "/media/qq/0039MnYb0qxYhV",
		linkTrack:                                "https://alisten.example/storage/alisten/link/" + media.Key(linkTrack.ID) + "/audio.mp3",
		video:                                    "https://alisten.example/storage/alisten/link/" + media.Key(video.ID) + "/audio.mp3",
	}
	for tr, want := range tests {
		if got := s.mediaURL(tr); got != want {
			t.Errorf("mediaURL(%v) = %q，期望 %q", tr, got, want)
		}
	}
}
