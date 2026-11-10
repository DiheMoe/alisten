package port

import (
	"context"
	"time"
)

type GitHubIntent int

const (
	// 游客用 GitHub 登录：GitHub 账号没有绑定过时，就是注册。
	GitHubLogin GitHubIntent = iota + 1
	// 已注册的成员绑定 GitHub。
	GitHubBind
	// 只绑定了 GitHub 的账号补设用户名和密码之前，用账号绑定的 GitHub 确认身份。
	GitHubVerify
)

// 发起授权的客户端，GitHub 授权结束之后跳回它。
type GitHubClient int

const (
	GitHubWeb GitHubClient = iota + 1
	// App 用系统浏览器授权，授权结束之后要跳回 App。
	GitHubApp
)

// 发起 GitHub 授权时记下的请求，以 state 为键。
type GitHubRequest struct {
	Intent GitHubIntent
	Client GitHubClient
	// 发起授权的成员。
	MemberID string
	// 发起授权的客户端保存的 nonce 的 SHA-256。
	NonceHash []byte
	// PKCE 的 code verifier：GitHub 签发的授权码只能配合它换取令牌，别人的授权码放进这次授权的回调里换不到令牌。
	CodeVerifier string
}

// GitHub 确认了用户的身份，等待发起授权的客户端兑换，以兑换码为键。
type GitHubGrant struct {
	Request  GitHubRequest
	GitHubID int64
}

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

// GitHub 的 OAuth 授权，使用调用方传入的 OAuth App。
type GitHubOAuth interface {
	// 请用户授权的地址：用户同意之后，GitHub 带着 state 和授权码回调，授权码与 verifier 绑定。
	AuthorizeURL(app GitHubOAuthApp, state, verifier string) string
	// 用回调带回的授权码和发起那次授权时的 verifier 换取 GitHub 用户的 ID。
	UserID(ctx context.Context, app GitHubOAuthApp, code, verifier string) (int64, error)
}

// state 和兑换码只以 SHA-256 哈希的形式出现在端口中，所有实例共用。
type GitHubLogins interface {
	SaveRequest(ctx context.Context, stateHash []byte, r GitHubRequest, ttl time.Duration) error
	// 取出并删除，同一个 state 只能取出一次。不存在或已过期时返回 ErrCodeNotFound。
	TakeRequest(ctx context.Context, stateHash []byte) (GitHubRequest, error)
	SaveGrant(ctx context.Context, codeHash []byte, g GitHubGrant, ttl time.Duration) error
	// 与 TakeRequest 相同。
	TakeGrant(ctx context.Context, codeHash []byte) (GitHubGrant, error)
}

// 只绑定了 GitHub 的账号补设用户名和密码之前，要用账号绑定的 GitHub 确认身份。确认记在发出请求的会话上，以会话的成员 token
// 的 SHA-256 哈希为键，所有实例共用。丢失时用户重新确认即可。
type GitHubVerifications interface {
	SaveVerification(ctx context.Context, tokenHash []byte, ttl time.Duration) error
	// 不存在或已过期时返回 false。
	Verified(ctx context.Context, tokenHash []byte) (bool, error)
	// 不存在时什么也不做。
	DeleteVerification(ctx context.Context, tokenHash []byte) error
}
