package port

import "context"

// 后台填写的 GitHub OAuth App。
type GitHubOAuthApp struct {
	ClientID     string
	ClientSecret string
}

// Client ID 和 Client Secret 一起保存、一起清除，读到的两者总是同一次保存的。
type GitHubOAuthApps interface {
	// 没有配置时返回零值。
	GitHubOAuthApp(ctx context.Context) (GitHubOAuthApp, error)
	SetGitHubOAuthApp(ctx context.Context, app GitHubOAuthApp) error
	// 没有配置时什么也不做。
	DeleteGitHubOAuthApp(ctx context.Context) error
}
