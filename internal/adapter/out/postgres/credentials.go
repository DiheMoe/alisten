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
	neteaseCookie      = "netease"
	qqCookie           = "qq"
	bilibiliCredential = "bilibili"
	youtubeCredential  = "youtube"
)

func (s *Store) NeteaseCookie(ctx context.Context) (string, error) {
	return s.credential(ctx, neteaseCookie, "读取网易云 Cookie")
}

func (s *Store) SetNeteaseCookie(ctx context.Context, cookie string) error {
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

func (s *Store) BilibiliCredential(ctx context.Context) (string, error) {
	return s.credential(ctx, bilibiliCredential, "读取 B 站凭据")
}

func (s *Store) SetBilibiliCredential(ctx context.Context, credential string) error {
	if err := s.setCredential(ctx, bilibiliCredential, credential); err != nil {
		return fmt.Errorf("保存 B 站凭据：%w", err)
	}
	return nil
}

func (s *Store) SetYouTubeCookies(ctx context.Context, cookies string) error {
	if err := s.setCredential(ctx, youtubeCredential, cookies); err != nil {
		return fmt.Errorf("保存 YouTube cookies：%w", err)
	}
	return nil
}

func (s *Store) YouTubeCookies(ctx context.Context) (string, error) {
	return s.credential(ctx, youtubeCredential, "读取 YouTube cookies")
}

func (s *Store) YouTubeCookiesStatus(ctx context.Context) (port.YouTubeCookiesStatus, error) {
	updatedAt, err := s.q.CredentialUpdatedAt(ctx, youtubeCredential)
	if errors.Is(err, pgx.ErrNoRows) {
		return port.YouTubeCookiesStatus{}, nil
	}
	if err != nil {
		return port.YouTubeCookiesStatus{}, fmt.Errorf("读取 YouTube cookies 状态：%w", err)
	}
	return port.YouTubeCookiesStatus{Set: true, UpdatedAt: updatedAt}, nil
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
