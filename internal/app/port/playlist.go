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
	ErrShareNotFound    = errors.New("分享已失效")
)

type PlaylistRecord struct {
	ID         string
	Name       string
	Favorites  bool
	TrackCount int
	CreatedAt  time.Time
	UpdatedAt  time.Time
	Cover      *Cover
	// 没有分享时为空。
	ShareID string
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

// 分享页上显示的内容：不带所有者的成员 ID 和用户名。
type SharedPlaylist struct {
	Name            string
	Favorites       bool
	OwnerName       string
	OwnerAvatarHash string
	// 新加入的在前。
	Items []PlaylistItem
}

// 所有方法都按 ownerID 过滤：别人的歌单和不存在的歌单一样，返回 ErrPlaylistNotFound。“我喜欢”由 Members.Register 创建。
type Playlists interface {
	// “我喜欢”在最前，其余按创建的先后。Cover 是歌单里排在最前、有封面的那首歌的封面，没有时为 nil。
	MemberPlaylists(ctx context.Context, ownerID string) ([]PlaylistRecord, error)
	// 已有 limit 个歌单（含“我喜欢”）时返回 ErrPlaylistLimit。
	CreatePlaylist(ctx context.Context, ownerID, name string, limit int) (PlaylistRecord, error)
	// 新建歌单并写入 items，items[0] 排在最前；返回的记录带曲目数和封面。已有 limit 个歌单（含“我喜欢”）时返回 ErrPlaylistLimit。
	// 调用方保证 items 中没有重复的歌、不超过歌单的曲目上限。
	ImportPlaylist(ctx context.Context, ownerID, name string, items []PlaylistItem, limit int) (PlaylistRecord, error)
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
	// 只看这个成员自己的歌单；没有命中时返回空切片。
	TrackPlaylists(ctx context.Context, ownerID string, t track.Track) ([]string, error)
	// 把 fromID 歌单里的这些歌连同元数据复制到 toID 歌单，返回复制的数量。fromID 不属于 ownerID 时返回 ErrPlaylistNotFound。
	// 不在 fromID 里的和已在 toID 里的跳过；新复制的排在最前，tracks[0] 最靠前。
	// 复制之后超过 limit 首时整批拒绝，返回 ErrPlaylistLimit。
	CopyPlaylistItems(ctx context.Context, ownerID, fromID, toID string, tracks []track.Track, limit int) (int, error)
	// 按 playlist.Move 的规则调整顺序，错误原样返回。
	MovePlaylistItems(ctx context.Context, ownerID, id string, tracks []track.Track, before *track.Track) error
	// shareID 为空字符串时停止分享。shareID 已被其他歌单使用时返回错误。
	SetPlaylistShare(ctx context.Context, ownerID, id, shareID string) error
	// 没有歌单使用这个 shareID 时返回 ErrShareNotFound。
	SharedPlaylist(ctx context.Context, shareID string) (SharedPlaylist, error)
	// 新建 ownerID 的歌单，复制 shareID 对应歌单的全部曲目，顺序不变；返回的记录带曲目数和封面。已有 limit 个歌单（含“我喜欢”）时
	// 返回 ErrPlaylistLimit，没有歌单使用这个 shareID 时返回 ErrShareNotFound，两种情况都不新建歌单。
	CopySharedPlaylist(ctx context.Context, ownerID, shareID, name string, limit int) (PlaylistRecord, error)
}
