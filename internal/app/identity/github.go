package identity

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/member"
)

var (
	ErrGitHubRequestExpired = errors.New("GitHub 授权已经过期，请重新发起")
	ErrGitHubFailed         = errors.New("没有从 GitHub 确认你的身份，请稍后再试")
	ErrGitHubLoginExpired   = errors.New("GitHub 登录已经失效，请重新发起")
	ErrGitHubMismatch       = errors.New("需要用这个账号绑定的 GitHub 确认，请在 GitHub 上换成绑定的账号再试")
	ErrGitHubUnverified     = errors.New("先用这个账号绑定的 GitHub 确认身份，再设置用户名和密码")
	ErrGitHubDisabled       = errors.New("站点没有开启 GitHub 登录")
)

const (
	githubRequestTTL = 10 * time.Minute
	githubGrantTTL   = time.Minute
	// 用 GitHub 确认身份之后，要在这段时间内补设用户名和密码。
	githubVerificationTTL = 10 * time.Minute
	// 每次发起授权都在 Redis 中保存一个 10 分钟的 state，额度与登录相同。
	githubAuthorizeBurst  = 10
	githubAuthorizeRefill = time.Minute
)

// 后台配置了 GitHub OAuth App 才能使用，没有配置时 Authorize、Callback、Exchange 和 Unbind 都返回 ErrGitHubDisabled。它们每次都重新读取
// 配置，不缓存：后台的修改在所有实例上立即生效，实例之间不必同步。流程：Authorize 记下意图、客户端、nonce 和 PKCE 的 verifier，返回
// GitHub 的授权地址；GitHub 回调时 Callback 换取用户 ID，生成一次性的兑换码，GitHub 没有授权时 Cancel 作废 state，两者都返回记下的
// 客户端，回调跳回它；发起授权的客户端用兑换码和它保存的 nonce 调用 Exchange，按意图登录、注册、绑定或确认身份。
type GitHub struct {
	accounts *Accounts
	apps     port.GitHubOAuthApps
	oauth    port.GitHubOAuth
	logins   port.GitHubLogins
}

func NewGitHub(accounts *Accounts, apps port.GitHubOAuthApps, oauth port.GitHubOAuth, logins port.GitHubLogins) *GitHub {
	return &GitHub{accounts: accounts, apps: apps, oauth: oauth, logins: logins}
}

// 没有配置时返回空字符串。不提供读取 Client Secret 的方法：它只用来向 GitHub 换取令牌。
func (g *GitHub) ClientID(ctx context.Context) (string, error) {
	app, err := g.apps.GitHubOAuthApp(ctx)
	if err != nil {
		return "", err
	}
	return app.ClientID, nil
}

// Client ID 和 Client Secret 必须已经校验过，都不为空。
func (g *GitHub) SetOAuthApp(ctx context.Context, app port.GitHubOAuthApp) error {
	return g.apps.SetGitHubOAuthApp(ctx, app)
}

// 关闭 GitHub 登录。已经绑定的账号保持绑定，重新配置之后照常登录。
func (g *GitHub) DeleteOAuthApp(ctx context.Context) error {
	return g.apps.DeleteGitHubOAuthApp(ctx)
}

func (g *GitHub) app(ctx context.Context) (port.GitHubOAuthApp, error) {
	app, err := g.apps.GitHubOAuthApp(ctx)
	if err != nil {
		return port.GitHubOAuthApp{}, err
	}
	if app.ClientID == "" {
		return port.GitHubOAuthApp{}, ErrGitHubDisabled
	}
	return app, nil
}

// nonce 由发起授权的客户端生成并保存，兑换时要原样带上。
func (g *GitHub) Authorize(ctx context.Context, memberID, clientIP string, intent port.GitHubIntent, client port.GitHubClient, nonce, currentPassword string) (string, error) {
	app, err := g.app(ctx)
	if err != nil {
		return "", err
	}
	profile, err := g.accounts.members.AccountProfile(ctx, memberID)
	if err != nil {
		return "", err
	}
	switch {
	case intent == port.GitHubLogin && profile.Registered():
		return "", ErrAlreadyLoggedIn
	case intent == port.GitHubBind && !profile.Registered():
		return "", ErrNotRegistered
	case intent == port.GitHubBind && profile.GitHub:
		return "", port.ErrGitHubAlreadyBound
	case intent == port.GitHubVerify && !profile.Registered():
		return "", ErrNotRegistered
	case intent == port.GitHubVerify && profile.Username != "":
		return "", port.ErrAlreadyRegistered
	}
	// 能发起绑定的账号没有绑定 GitHub，一定设置过密码。
	if intent == port.GitHubBind {
		if _, err := g.accounts.checkCurrentPassword(ctx, memberID, profile.Username, currentPassword); err != nil {
			return "", err
		}
	}
	if err := g.accounts.take(ctx, "github:ip:"+quotaAddr(clientIP), githubAuthorizeBurst, githubAuthorizeRefill); err != nil {
		return "", err
	}
	state, stateHash := newSecret()
	verifier, _ := newSecret()
	request := port.GitHubRequest{Intent: intent, Client: client, MemberID: memberID, NonceHash: hashSecret(nonce), CodeVerifier: verifier}
	if err := g.logins.SaveRequest(ctx, stateHash, request, githubRequestTTL); err != nil {
		return "", err
	}
	return g.oauth.AuthorizeURL(app, state, verifier), nil
}

// 只换取 GitHub 用户 ID，不修改账号，返回发起授权的客户端和一次性的兑换码。登录、注册和绑定都留给 Exchange：回调请求不带成员 token
// 和 nonce，谁都可以把自己发起的授权地址发给别人，在回调时绑定，别人一授权，他的 GitHub 就绑到了发起者的账号上。
// 取出 state 之后出错时也返回发起授权的客户端，失败的结果同样要跳回它。授权进行中后台换了 OAuth App 时，GitHub 不认原来签发的
// 授权码，返回 ErrGitHubFailed。
func (g *GitHub) Callback(ctx context.Context, state, code string) (port.GitHubClient, string, error) {
	request, err := g.logins.TakeRequest(ctx, hashSecret(state))
	expired := errors.Is(err, port.ErrCodeNotFound)
	if err != nil && !expired {
		return 0, "", err
	}
	// 关闭了 GitHub 登录时重新发起也没有用，所以 state 无效时也不提示过期。
	app, err := g.app(ctx)
	if err != nil {
		return request.Client, "", err
	}
	if expired {
		return 0, "", ErrGitHubRequestExpired
	}
	githubID, err := g.oauth.UserID(ctx, app, code, request.CodeVerifier)
	if err != nil {
		return request.Client, "", fmt.Errorf("%w：%w", ErrGitHubFailed, err)
	}
	exchange, hash := newSecret()
	if err := g.logins.SaveGrant(ctx, hash, port.GitHubGrant{Request: request, GitHubID: githubID}, githubGrantTTL); err != nil {
		return request.Client, "", err
	}
	return request.Client, exchange, nil
}

// GitHub 没有授权（用户拒绝或者 GitHub 报错）时作废 state，返回发起授权的客户端。
func (g *GitHub) Cancel(ctx context.Context, state string) (port.GitHubClient, error) {
	request, err := g.logins.TakeRequest(ctx, hashSecret(state))
	if errors.Is(err, port.ErrCodeNotFound) {
		return 0, ErrGitHubRequestExpired
	}
	if err != nil {
		return 0, err
	}
	return request.Client, nil
}

type GitHubResult struct {
	// 登录了已经绑定这个 GitHub 账号的账号时才有：发出请求的游客会话已经换成这个账号的新会话。
	Session *Session
	Member  member.Member
	Account Account
}

// current 和 currentToken 是发出请求的成员和它的成员 token，必须是发起授权的那个成员。
func (g *GitHub) Exchange(ctx context.Context, current member.Member, currentToken, code, nonce string) (GitHubResult, error) {
	if _, err := g.app(ctx); err != nil {
		return GitHubResult{}, err
	}
	grant, err := g.logins.TakeGrant(ctx, hashSecret(code))
	if errors.Is(err, port.ErrCodeNotFound) {
		return GitHubResult{}, ErrGitHubLoginExpired
	}
	if err != nil {
		return GitHubResult{}, err
	}
	if grant.Request.MemberID != current.ID || subtle.ConstantTimeCompare(grant.Request.NonceHash, hashSecret(nonce)) != 1 {
		return GitHubResult{}, ErrGitHubLoginExpired
	}
	switch grant.Request.Intent {
	case port.GitHubBind:
		return g.bind(ctx, current, grant.GitHubID)
	case port.GitHubVerify:
		return g.verify(ctx, current, currentToken, grant.GitHubID)
	}
	return g.login(ctx, current, currentToken, grant.GitHubID)
}

func (g *GitHub) login(ctx context.Context, current member.Member, currentToken string, githubID int64) (GitHubResult, error) {
	a := g.accounts
	if err := a.requireGuest(ctx, current.ID); err != nil {
		return GitHubResult{}, err
	}
	account, err := a.members.AccountByGitHub(ctx, githubID)
	if err == nil {
		session, err := a.signIn(ctx, currentToken, account)
		// 账号的密码恰好在这时被修改或重置：修改删除了其他会话，这次登录也作废，重新登录即可。
		if errors.Is(err, port.ErrAccountPasswordChanged) {
			return GitHubResult{}, ErrGitHubLoginExpired
		}
		if err != nil {
			return GitHubResult{}, err
		}
		return GitHubResult{Session: &session, Member: session.Member, Account: accountOf(account)}, nil
	}
	if !errors.Is(err, port.ErrMemberNotFound) {
		return GitHubResult{}, err
	}
	// 没有绑定过的 GitHub 账号登录就是注册：绑到发出请求的游客上，成员和它的会话都不变。
	settings, err := a.site.Settings(ctx)
	if err != nil {
		return GitHubResult{}, err
	}
	if !settings.RegistrationOpen {
		return GitHubResult{}, ErrRegistrationClosed
	}
	if err := a.members.BindGitHub(ctx, current.ID, githubID); err != nil {
		return GitHubResult{}, err
	}
	return GitHubResult{Member: current, Account: Account{GitHub: true}}, nil
}

func (g *GitHub) bind(ctx context.Context, current member.Member, githubID int64) (GitHubResult, error) {
	profile, err := g.accounts.members.AccountProfile(ctx, current.ID)
	if err != nil {
		return GitHubResult{}, err
	}
	if !profile.Registered() {
		return GitHubResult{}, ErrNotRegistered
	}
	if err := g.accounts.members.BindGitHub(ctx, current.ID, githubID); err != nil {
		return GitHubResult{}, err
	}
	return GitHubResult{Member: current, Account: Account{Username: profile.Username, Email: profile.Email, GitHub: true}}, nil
}

// 只确认身份，不修改账号：GitHub 账号要是这个账号绑定的。确认记在发出请求的会话上，Accounts.Register 补设用户名和密码时要用。
func (g *GitHub) verify(ctx context.Context, current member.Member, currentToken string, githubID int64) (GitHubResult, error) {
	account, err := g.accounts.members.AccountByID(ctx, current.ID)
	if err != nil {
		return GitHubResult{}, err
	}
	if account.GitHubID != githubID {
		return GitHubResult{}, ErrGitHubMismatch
	}
	if err := g.accounts.verifications.SaveVerification(ctx, hashSecret(currentToken), githubVerificationTTL); err != nil {
		return GitHubResult{}, err
	}
	return GitHubResult{Member: current, Account: accountOf(account)}, nil
}

// 解绑之后要能用密码登录，所以账号必须设置过密码。
func (g *GitHub) Unbind(ctx context.Context, memberID, currentPassword string) error {
	if _, err := g.app(ctx); err != nil {
		return err
	}
	profile, err := g.accounts.members.AccountProfile(ctx, memberID)
	if err != nil {
		return err
	}
	switch {
	case !profile.Registered():
		return ErrNotRegistered
	case profile.Username == "":
		return port.ErrNoPassword
	}
	if _, err := g.accounts.checkCurrentPassword(ctx, memberID, profile.Username, currentPassword); err != nil {
		return err
	}
	return g.accounts.members.UnbindGitHub(ctx, memberID)
}
