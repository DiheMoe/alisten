package api

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/dihemoe/alisten/internal/app/media"
	"github.com/dihemoe/alisten/internal/app/realtime"
	"github.com/dihemoe/alisten/internal/domain/member"
	"github.com/dihemoe/alisten/internal/domain/room"
	"github.com/dihemoe/alisten/internal/domain/track"
	"github.com/dihemoe/alisten/protocol"
)

var roomErrorCodes = map[room.ErrorKind]protocol.ErrorCode{
	room.NotFound:       protocol.ErrorCodeNotFound,
	room.Forbidden:      protocol.ErrorCodeForbidden,
	room.Conflict:       protocol.ErrorCodeConflict,
	room.QueueFull:      protocol.ErrorCodeQueueFull,
	room.RateLimited:    protocol.ErrorCodeRateLimited,
	room.NothingPlaying: protocol.ErrorCodeNothingPlaying,
}

// bot 表示 by 是常驻房间的 bot 成员。返回的成员是房间命令的发起人，bot 代填了点歌人时换成那个名字。
func (s *server) action(ctx context.Context, by member.Member, bot bool, data any) (room.Action, member.Member, error) {
	switch d := data.(type) {
	case protocol.RoomSyncRequest:
		return room.Sync{}, by, nil
	case protocol.QueueAddRequest:
		return s.Gateway.Resolve(ctx, by, bot, pick(d))
	case protocol.QueueRemoveRequest:
		return room.Remove{Entry: entryID(d.EntryID)}, by, nil
	case protocol.QueueLikeRequest:
		return room.Like{Entry: entryID(d.EntryID)}, by, nil
	case protocol.PlaybackVoteSkipRequest:
		return room.VoteSkip{}, by, nil
	case protocol.SettingsSetPlayModeRequest:
		return room.SetPlayMode{Mode: playModeOf(d.PlayMode)}, by, nil
	case protocol.ChatSendRequest:
		return room.Chat{Text: d.Content}, by, nil
	}
	panic(fmt.Sprintf("未知的房间请求 %T", data))
}

// 协议解码保证要么只给出 track，要么只给出 source 和 keyword。
func pick(d protocol.QueueAddRequest) realtime.Pick {
	if d.Track != nil {
		t := track.Track{Source: track.Source(d.Track.Source), ID: d.Track.ID}
		if t.Source == track.Link {
			t.ID = track.LinkID(t.ID)
		}
		return realtime.Pick{Track: t, Requester: deref(d.Requester)}
	}
	return realtime.Pick{Source: track.Source(*d.Source), Keyword: *d.Keyword, Requester: deref(d.Requester)}
}

// 条目 ID 从 1 开始，无法解析的 ID 当作不存在的 0。
func entryID(id string) room.EntryID {
	n, err := strconv.ParseUint(id, 10, 64)
	if err != nil {
		return 0
	}
	return room.EntryID(n)
}

func (s *server) result(res room.Result) any {
	switch res := res.(type) {
	case room.Done:
		return protocol.Empty{}
	case room.EnqueueResult:
		return protocol.QueueAddResponse{Entry: s.entry(res.Entry)}
	case room.LikeResult:
		return protocol.QueueLikeResponse{Likes: int64(res.Likes)}
	case room.VoteResult:
		return protocol.PlaybackVoteSkipResponse{Votes: int64(res.Votes), Required: int64(res.Required), Skipped: res.Skipped}
	case room.SyncResult:
		return protocol.RoomSyncResponse{Room: s.snapshot(res.View)}
	}
	panic(fmt.Sprintf("未知的房间回复 %T", res))
}

func (s *server) event(e room.Event) (string, any) {
	switch e := e.(type) {
	case room.PlaybackChanged:
		return protocol.EventPlaybackUpdated, protocol.PlaybackUpdatedEvent{NowPlaying: s.nowPlaying(e.Current)}
	case room.QueueChanged:
		return protocol.EventQueueUpdated, protocol.QueueUpdatedEvent{Pending: s.entryPtr(e.Pending), Items: s.entries(e.Waiting)}
	case room.MembersChanged:
		return protocol.EventMembersUpdated, members(e.Sessions)
	case room.ModeChanged:
		return protocol.EventSettingsUpdated, protocol.SettingsUpdatedEvent{PlayMode: playMode(e.Mode)}
	case room.InfoChanged:
		return protocol.EventRoomUpdated, protocol.RoomUpdatedEvent{Name: e.Name, Description: e.Description}
	case room.ChatPosted:
		return protocol.EventChatMessage, protocol.ChatMessageEvent{Member: memberDTO(e.Member), Content: e.Text, SentAtMs: e.SentAt.UnixMilli()}
	case room.Activity:
		return protocol.EventRoomActivity, s.activity(e)
	}
	panic(fmt.Sprintf("未知的房间事件 %T", e))
}

func (s *server) snapshot(v room.View) protocol.RoomSnapshot {
	return protocol.RoomSnapshot{
		Term:       int64(v.Term),
		Seq:        int64(v.Seq),
		Room:       protocol.RoomInfo{ID: v.Snapshot.Info.ID, Name: v.Snapshot.Info.Name, Description: v.Snapshot.Info.Description, Kind: roomKind(v.Snapshot.Info.Kind)},
		PlayMode:   playMode(v.Snapshot.Mode),
		NowPlaying: s.nowPlaying(v.Snapshot.Current),
		Pending:    s.entryPtr(v.Snapshot.Pending),
		Items:      s.entries(v.Snapshot.Waiting),
		Members:    members(v.Sessions),
	}
}

func (s *server) nowPlaying(p *room.NowPlaying) *protocol.NowPlaying {
	if p == nil {
		return nil
	}
	return &protocol.NowPlaying{
		Entry:       s.entry(p.Entry),
		MediaURL:    s.mediaURL(p.Entry.Track),
		StartedAtMs: p.StartedAt.UnixMilli(),
		DurationMs:  p.Entry.Prep.Duration.Milliseconds(),
		Gain:        p.Entry.Prep.Gain,
		Lyric:       p.Entry.Prep.Lyric,
	}
}

func (s *server) mediaURL(t track.Track) string {
	if media.Stored(t.Source) {
		return s.StorageURL + "/" + media.AudioObject(media.Key(t.ID))
	}
	return "/media/" + string(t.Source) + "/" + url.PathEscape(t.ID)
}

func (s *server) entry(e room.Entry) protocol.Entry {
	d := protocol.Entry{
		ID:     strconv.FormatUint(uint64(e.ID), 10),
		Track:  protocol.Track{Source: protocol.Source(e.Track.Source), ID: e.Track.ID},
		Meta:   meta(e.Meta),
		Auto:   e.Auto,
		Likes:  int64(e.Likes),
		Status: protocol.EntryStatusPreparing,
	}
	if e.Meta.CoverKey != "" {
		d.Meta.CoverURL = s.StorageURL + "/" + string(e.Meta.CoverKey)
	}
	if !e.Auto {
		by := memberDTO(e.By)
		d.By = &by
	}
	if e.Ready {
		d.Status = protocol.EntryStatusReady
	}
	return d
}

func (s *server) entryPtr(e *room.Entry) *protocol.Entry {
	if e == nil {
		return nil
	}
	d := s.entry(*e)
	return &d
}

func (s *server) entries(es []room.Entry) []protocol.Entry {
	items := make([]protocol.Entry, len(es))
	for i, e := range es {
		items[i] = s.entry(e)
	}
	return items
}

func meta(m track.Meta) protocol.TrackMeta {
	return protocol.TrackMeta{Title: m.Title, Artist: m.Artist, Album: m.Album, CoverURL: m.CoverURL, PageURL: m.PageURL, DurationMs: m.Duration.Milliseconds()}
}

func members(sessions []room.Session) protocol.Members {
	list := make([]protocol.Member, len(sessions))
	for i, s := range sessions {
		list[i] = memberDTO(s.Member)
	}
	return protocol.Members{Sessions: list, OnlineCount: int64(len(sessions))}
}

var activityKinds = map[room.ActivityKind]protocol.ActivityKind{
	room.ActivityAdded:   protocol.ActivityKindAdded,
	room.ActivityRemoved: protocol.ActivityKindRemoved,
	room.ActivityLiked:   protocol.ActivityKindLiked,
	room.ActivityVoted:   protocol.ActivityKindVoted,
	room.ActivitySkipped: protocol.ActivityKindSkipped,
	room.ActivityFailed:  protocol.ActivityKindFailed,
}

func (s *server) activity(a room.Activity) protocol.RoomActivityEvent {
	e := s.entry(a.Entry)
	d := protocol.RoomActivityEvent{Kind: activityKinds[a.Kind], Entry: &e, Message: activityMessage(a)}
	if a.Member != (member.Member{}) {
		m := memberDTO(a.Member)
		d.Member = &m
	}
	if a.Kind == room.ActivityVoted || a.Kind == room.ActivitySkipped {
		votes, required := int64(a.Votes), int64(a.Required)
		d.Votes, d.Required = &votes, &required
	}
	return d
}

// 歌名里已经有书名号时不再加一层：B 站的标题常带《》。
func bookTitle(title string) string {
	if strings.ContainsAny(title, "《》") {
		return title
	}
	return "《" + title + "》"
}

func activityMessage(a room.Activity) string {
	title := a.Entry.Meta.Title
	switch a.Kind {
	case room.ActivityAdded:
		return "点了" + bookTitle(title)
	case room.ActivityRemoved:
		return "移除了" + bookTitle(title)
	case room.ActivityLiked:
		return fmt.Sprintf("给%s点了赞，共 %d 个赞", bookTitle(title), a.Entry.Likes)
	case room.ActivityVoted:
		return fmt.Sprintf("投票切歌（%d/%d）", a.Votes, a.Required)
	case room.ActivitySkipped:
		return "投票切歌成功，换下一首"
	}
	return fmt.Sprintf("%s没能准备好：%s", bookTitle(title), a.Reason)
}

func playMode(m room.PlayMode) protocol.PlayMode {
	switch m {
	case room.Random:
		return protocol.PlayModeRandom
	case room.Rotation:
		return protocol.PlayModeRotation
	}
	return protocol.PlayModeSequential
}

func playModeOf(m protocol.PlayMode) room.PlayMode {
	switch m {
	case protocol.PlayModeRandom:
		return room.Random
	case protocol.PlayModeRotation:
		return room.Rotation
	}
	return room.Sequential
}
