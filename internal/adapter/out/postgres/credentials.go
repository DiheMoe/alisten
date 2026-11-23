package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/dihemoe/alisten/internal/adapter/out/postgres/internal/query"
	"github.com/dihemoe/alisten/internal/app/port"
)

var _ port.YouTubeCookies = (*Store)(nil)

const (
	neteaseCookie  = "netease"
	qqCookie       = "qq"
	bilibiliCookie = "bilibili"
	youtubeCookies = "youtube"
)

func (s *Store) NetEaseCookie(ctx context.Context) (string, error) {
	return s.credential(ctx, neteaseCookie, "读取网易云 Cookie")
}

func (s *Store) SetNetEaseCookie(ctx context.Context, cookie string) error {
	if err := s.setCredential(ctx, neteaseCookie, cookie); err != nil {
		return fmt.Errorf("保存网易云 Cookie：%w", err)
	}
	return nil
}

func (s *Store) QQCookie(ctx context.Context) (string, error) {
	return s.credential(ctx, qqCookie, "读取 QQ Cookie")
}

func (s *Store) SetQQCookie(ctx context.Context, cookie string) error {
	if err := s.setCredential(ctx, qqCookie, cookie); err != nil {
		return fmt.Errorf("保存 QQ Cookie：%w", err)
	}
	return nil
}

func (s *Store) SwapQQCookie(ctx context.Context, expected, cookie string) (bool, error) {
	rows, err := s.q.SwapCredential(ctx, query.SwapCredentialParams{
		Name:     qqCookie,
		Expected: expected,
		Value:    cookie,
	})
	if err != nil {
		return false, fmt.Errorf("更新 QQ Cookie：%w", err)
	}
	return rows > 0, nil
}

func (s *Store) BilibiliCookie(ctx context.Context) (string, error) {
	return s.credential(ctx, bilibiliCookie, "读取 B 站 Cookie")
}

func (s *Store) SetBilibiliCookie(ctx context.Context, cookie string) error {
	if err := s.setCredential(ctx, bilibiliCookie, cookie); err != nil {
		return fmt.Errorf("保存 B 站 Cookie：%w", err)
	}
	return nil
}

func (s *Store) SetYouTubeCookies(ctx context.Context, cookies string) error {
	if err := s.setCredential(ctx, youtubeCookies, cookies); err != nil {
		return fmt.Errorf("保存 YouTube cookies：%w", err)
	}
	return nil
}

func (s *Store) YouTubeCookies(ctx context.Context) (string, error) {
	return s.credential(ctx, youtubeCookies, "读取 YouTube cookies")
}

func (s *Store) YouTubeCredentialStatus(ctx context.Context) (port.YouTubeCredentialStatus, error) {
	updatedAt, err := s.q.CredentialUpdatedAt(ctx, youtubeCookies)
	if errors.Is(err, pgx.ErrNoRows) {
		return port.YouTubeCredentialStatus{}, nil
	}
	if err != nil {
		return port.YouTubeCredentialStatus{}, fmt.Errorf("读取 YouTube cookies 状态：%w", err)
	}
	return port.YouTubeCredentialStatus{Set: true, UpdatedAt: updatedAt}, nil
}

func (s *Store) credential(ctx context.Context, name, what string) (string, error) {
	value, err := s.q.Credential(ctx, name)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("%s：%w", what, err)
	}
	return value, nil
}

func (s *Store) setCredential(ctx context.Context, name, value string) error {
	if value == "" {
		return s.q.DeleteCredential(ctx, name)
	}
	return s.q.SetCredential(ctx, query.SetCredentialParams{Name: name, Value: value})
}
