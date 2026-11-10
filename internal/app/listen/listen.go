package listen

import (
	"context"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/track"
)

// 游客和已注册成员都有播放历史，不像歌单要求注册。
type Service struct {
	listens port.Listens
}

func New(listens port.Listens) *Service {
	return &Service{listens: listens}
}

func (s *Service) List(ctx context.Context, memberID string) ([]port.Listen, error) {
	return s.listens.MemberListens(ctx, memberID)
}

// 链接曲目的 t.ID 必须已经按 track.LinkID 规范化。
func (s *Service) Remove(ctx context.Context, memberID string, t track.Track) error {
	return s.listens.DeleteListen(ctx, memberID, t)
}

func (s *Service) Clear(ctx context.Context, memberID string) error {
	return s.listens.ClearListens(ctx, memberID)
}

func (s *Service) Recording(ctx context.Context, memberID string) (bool, error) {
	return s.listens.ListenRecording(ctx, memberID)
}

func (s *Service) SetRecording(ctx context.Context, memberID string, on bool) error {
	return s.listens.SetListenRecording(ctx, memberID, on)
}
