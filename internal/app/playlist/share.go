package playlist

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"

	"github.com/dihemoe/alisten/internal/app/port"
)

// 分享 ID 是 128 位随机值，拿到链接的人才能打开，无法猜出别人的分享。
const shareIDBytes = 16

var shareIDLength = base64.RawURLEncoding.EncodedLen(shareIDBytes)

// 开启分享和重新生成是同一个操作：每次都换一个新的分享 ID，旧链接随即失效。
func (s *Service) Share(ctx context.Context, memberID, id string) (string, error) {
	if err := s.registered(ctx, memberID); err != nil {
		return "", err
	}
	b := make([]byte, shareIDBytes)
	// crypto/rand.Read 不会返回错误。
	rand.Read(b)
	shareID := base64.RawURLEncoding.EncodeToString(b)
	if err := s.playlists.SetPlaylistShare(ctx, memberID, id, shareID); err != nil {
		return "", err
	}
	return shareID, nil
}

func (s *Service) Unshare(ctx context.Context, memberID, id string) error {
	if err := s.registered(ctx, memberID); err != nil {
		return err
	}
	return s.playlists.SetPlaylistShare(ctx, memberID, id, "")
}

// 不需要身份。
func (s *Service) Shared(ctx context.Context, shareID string) (port.SharedPlaylist, error) {
	if !validShareID(shareID) {
		return port.SharedPlaylist{}, port.ErrShareNotFound
	}
	return s.playlists.SharedPlaylist(ctx, shareID)
}

func (s *Service) CopyShared(ctx context.Context, memberID, shareID, name string) (port.PlaylistRecord, error) {
	if err := s.registered(ctx, memberID); err != nil {
		return port.PlaylistRecord{}, err
	}
	name, err := validName(name)
	if err != nil {
		return port.PlaylistRecord{}, err
	}
	if !validShareID(shareID) {
		return port.PlaylistRecord{}, port.ErrShareNotFound
	}
	// 来源歌单本身不超过曲目上限，复制出的也不会超过。
	p, err := s.playlists.CopySharedPlaylist(ctx, memberID, shareID, name, maxPlaylists)
	if errors.Is(err, port.ErrPlaylistLimit) {
		return port.PlaylistRecord{}, ErrTooManyPlaylists
	}
	return p, err
}

// 分享 ID 取自地址，可能是任意文本：不像生成出来的就不必查存储，NUL 之类的字符也就不会到达数据库。
func validShareID(id string) bool {
	if len(id) != shareIDLength {
		return false
	}
	_, err := base64.RawURLEncoding.DecodeString(id)
	return err == nil
}
