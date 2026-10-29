package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/dihemoe/alisten/internal/adapter/out/postgres/internal/query"
	"github.com/dihemoe/alisten/internal/app/port"
)

var _ port.Credentials = (*Store)(nil)
var _ port.YouTubeCookies = (*Store)(nil)

const (
	neteaseCookie      = "netease"
	qqCookie           = "qq"
	bilibiliCredential = "bilibili"
	youtubeCredential  = "youtube"
)

func (s *Store) NeteaseCookie(ctx context.Context) (string, error) {
	cookie, err := s.q.Credential(ctx, neteaseCookie)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("读取网易云 Cookie：%w", err)
	}
	return cookie, nil
}

func (s *Store) SetNeteaseCookie(ctx context.Context, cookie string) error {
	if err := s.setCredential(ctx, neteaseCookie, cookie); err != nil {
		return fmt.Errorf("保存网易云 Cookie：%w", err)
	}
	return nil
}

func (s *Store) QQCookie(ctx context.Context) (string, error) {
	cookie, err := s.q.Credential(ctx, qqCookie)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("读取 QQ Cookie：%w", err)
	}
	return cookie, nil
}

func (s *Store) SetQQCookie(ctx context.Context, cookie string) error {
	if err := s.setCredential(ctx, qqCookie, cookie); err != nil {
		return fmt.Errorf("保存 QQ Cookie：%w", err)
	}
	return nil
}

func (s *Store) BilibiliCredential(ctx context.Context) (string, error) {
	credential, err := s.q.Credential(ctx, bilibiliCredential)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("读取 Bilibili 凭据：%w", err)
	}
	return credential, nil
}

func (s *Store) SetBilibiliCredential(ctx context.Context, credential string) error {
	if err := s.setCredential(ctx, bilibiliCredential, credential); err != nil {
		return fmt.Errorf("保存 Bilibili 凭据：%w", err)
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
	cookies, err := s.q.Credential(ctx, youtubeCredential)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("读取 YouTube cookies：%w", err)
	}
	return cookies, nil
}

func (s *Store) SetCookies(ctx context.Context, cookies string) error {
	return s.SetYouTubeCookies(ctx, cookies)
}

func (s *Store) Status(ctx context.Context) (port.YouTubeCookiesStatus, error) {
	credential, err := s.q.CredentialWithUpdatedAt(ctx, youtubeCredential)
	if errors.Is(err, pgx.ErrNoRows) {
		return port.YouTubeCookiesStatus{}, nil
	}
	if err != nil {
		return port.YouTubeCookiesStatus{}, fmt.Errorf("读取 YouTube cookies 状态：%w", err)
	}
	return port.YouTubeCookiesStatus{Set: true, UpdatedAt: credential.UpdatedAt}, nil
}

func (s *Store) setCredential(ctx context.Context, name, value string) error {
	if value == "" {
		return s.q.DeleteCredential(ctx, name)
	}
	return s.q.SetCredential(ctx, query.SetCredentialParams{Name: name, Value: value})
}
