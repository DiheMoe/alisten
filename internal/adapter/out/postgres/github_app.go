package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/dihemoe/alisten/internal/adapter/out/postgres/internal/query"
	"github.com/dihemoe/alisten/internal/app/port"
)

var _ port.GitHubOAuthApps = (*Store)(nil)

const githubCredential = "github"

// Client ID 和 Client Secret 存在凭据表的同一行里，一起写入、一起删除。
type githubOAuthApp struct {
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
}

func (s *Store) GitHubOAuthApp(ctx context.Context) (port.GitHubOAuthApp, error) {
	value, err := s.q.Credential(ctx, githubCredential)
	if errors.Is(err, pgx.ErrNoRows) {
		return port.GitHubOAuthApp{}, nil
	}
	if err != nil {
		return port.GitHubOAuthApp{}, fmt.Errorf("读取 GitHub OAuth App：%w", err)
	}
	var app githubOAuthApp
	// 不包装解码的错误：其中可能带有原值的片段，也就可能带有 Client Secret。
	if err := json.Unmarshal([]byte(value), &app); err != nil || app.ClientID == "" || app.ClientSecret == "" {
		return port.GitHubOAuthApp{}, errors.New("凭据表中 GitHub OAuth App 的格式不正确")
	}
	return port.GitHubOAuthApp(app), nil
}

func (s *Store) SetGitHubOAuthApp(ctx context.Context, app port.GitHubOAuthApp) error {
	value, err := json.Marshal(githubOAuthApp(app))
	if err != nil {
		return fmt.Errorf("编码 GitHub OAuth App：%w", err)
	}
	if err := s.q.SetCredential(ctx, query.SetCredentialParams{Name: githubCredential, Value: string(value)}); err != nil {
		return fmt.Errorf("保存 GitHub OAuth App：%w", err)
	}
	return nil
}

func (s *Store) DeleteGitHubOAuthApp(ctx context.Context) error {
	if err := s.q.DeleteCredential(ctx, githubCredential); err != nil {
		return fmt.Errorf("清除 GitHub OAuth App：%w", err)
	}
	return nil
}
