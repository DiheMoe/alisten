package port

import (
	"context"
	"errors"
	"time"

	"github.com/dihemoe/alisten/internal/domain/track"
)

var (
	ErrPlaylistNotFound = errors.New("歌单不存在")
	ErrFavoritesFixed   = errors.New("“我喜欢”不能重命名或删除")
	ErrPlaylistLimit    = errors.New("歌单或歌单里的歌已达上限")
)

type PlaylistRecord struct {
	ID         string
	Name       string
	Favorites  bool
	TrackCount int
	CreatedAt  time.Time
	UpdatedAt  time.Time
	Cover      *Cover
}

// 链接曲目的封面在对象存储中，只有对象键。
type Cover struct {
	URL string
	Key track.ObjectKey
}

type PlaylistItem struct {
	Track track.Track
	Meta  track.Meta
}

// 所有方法都按 ownerID 过滤：别人的歌单和不存在的歌单一样，返回 ErrPlaylistNotFound。“我喜欢”由 Members.Register 创建。
type Playlists interface {
	// “我喜欢”在最前，其余按创建的先后。Cover 是歌单里排在最前、有封面的那首歌的封面，没有时为 nil。
	MemberPlaylists(ctx context.Context, ownerID string) ([]PlaylistRecord, error)
	// 已有 limit 个歌单（含“我喜欢”）时返回 ErrPlaylistLimit。
	CreatePlaylist(ctx context.Context, ownerID, name string, limit int) (PlaylistRecord, error)
	// “我喜欢”返回 ErrFavoritesFixed。
	RenamePlaylist(ctx context.Context, ownerID, id, name string) error
	// “我喜欢”返回 ErrFavoritesFixed。
	DeletePlaylist(ctx context.Context, ownerID, id string) error
	// 新加入的在前。
	PlaylistItems(ctx context.Context, ownerID, id string) ([]PlaylistItem, error)
	// 已经在歌单里时什么也不做；已有 limit 首时返回 ErrPlaylistLimit。
	AddPlaylistItem(ctx context.Context, ownerID, id string, item PlaylistItem, limit int) error
	// 不在歌单里的歌忽略。
	RemovePlaylistItems(ctx context.Context, ownerID, id string, tracks []track.Track) error
	// 把 fromID 歌单里的这些歌连同元数据复制到 toID 歌单，返回复制的数量。fromID 不属于 ownerID 时返回 ErrPlaylistNotFound。
	// 不在 fromID 里的和已在 toID 里的跳过；新复制的排在最前，tracks[0] 最靠前。
	// 复制之后超过 limit 首时整批拒绝，返回 ErrPlaylistLimit。
	CopyPlaylistItems(ctx context.Context, ownerID, fromID, toID string, tracks []track.Track, limit int) (int, error)
	// 按 playlist.Move 的规则调整顺序，错误原样返回。
	MovePlaylistItems(ctx context.Context, ownerID, id string, tracks []track.Track, before *track.Track) error
}
