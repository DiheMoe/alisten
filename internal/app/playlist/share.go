package playlist

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"

	"github.com/dihemoe/alisten/internal/app/identity"
	"github.com/dihemoe/alisten/internal/app/port"
)

// 分享 ID 是 128 位随机值，拿到链接的人才能打开，无法猜出别人的分享。
const shareIDBytes = 16

// 开启分享和重新生成是同一个操作。每次都换一个新的分享 ID，旧链接随即失效。
func (s *Service) Share(ctx context.Context, owner identity.AccountOwner, id string) (string, error) {
	b := make([]byte, shareIDBytes)
	// crypto/rand.Read 不会返回错误。
	rand.Read(b)
	shareID := base64.RawURLEncoding.EncodeToString(b)
	if err := s.playlists.SetPlaylistShare(ctx, owner.MemberID(), id, shareID); err != nil {
		return "", err
	}
	return shareID, nil
}

func (s *Service) Unshare(ctx context.Context, owner identity.AccountOwner, id string) error {
	return s.playlists.SetPlaylistShare(ctx, owner.MemberID(), id, "")
}

// 不需要身份。
func (s *Service) Shared(ctx context.Context, shareID string) (port.SharedPlaylist, error) {
	return s.playlists.SharedPlaylist(ctx, shareID)
}

func (s *Service) CopyShared(ctx context.Context, owner identity.AccountOwner, shareID, name string) (port.PlaylistRecord, error) {
	name, err := validName(name)
	if err != nil {
		return port.PlaylistRecord{}, err
	}
	// 来源歌单本身不超过曲目上限，复制出的也不会超过。
	p, err := s.playlists.CopySharedPlaylist(ctx, owner.MemberID(), shareID, name, maxPlaylists)
	if errors.Is(err, port.ErrPlaylistLimit) {
		return port.PlaylistRecord{}, ErrTooManyPlaylists
	}
	return p, err
}
