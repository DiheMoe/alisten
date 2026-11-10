package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/dihemoe/alisten/internal/app/identity"
	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/protocol"
)

// 用户在 GitHub 上授权之后，浏览器跳到这个路径。网页的 origin 加上 GitHubCallbackPath，就是 OAuth App 里登记的回调地址。
const GitHubCallbackPath = "/api/auth/github/callback"

// 回调处理完之后的落地页，结果放在片段里。App 用系统浏览器授权，要跳到 App 的自定义 scheme 才能回到 App。
// 自定义 scheme 可能被别的 App 抢注，但截获的兑换码换不到身份。兑换码 1 分钟内有效，只能用一次。兑换时要带上发起授权的客户端在本机保存的
// nonce，而且必须由发起授权的同一个成员调用。
const (
	githubWebLanding = "/auth/github"
	githubAppLanding = "moe.dihe.alisten:/auth/github"
)

func (s *server) startGithubLogin(ctx context.Context, c call) (any, error) {
	req, err := protocol.DecodeStartGithubLoginRequest(c.body)
	if err != nil {
		return nil, err
	}
	intent := port.GitHubLogin
	switch req.Intent {
	case protocol.GitHubIntentBind:
		intent = port.GitHubBind
	case protocol.GitHubIntentVerify:
		intent = port.GitHubVerify
	}
	client := port.GitHubWeb
	if req.Client != nil && *req.Client == protocol.GitHubClientApp {
		client = port.GitHubApp
	}
	url, err := s.GitHub.Authorize(ctx, c.member.ID, c.clientIP, intent, client, req.Nonce, deref(req.CurrentPassword))
	if err != nil {
		return nil, err
	}
	return protocol.StartGithubLoginResponse{URL: url}, nil
}

func (s *server) finishGithubLogin(ctx context.Context, c call) (any, error) {
	req, err := protocol.DecodeFinishGithubLoginRequest(c.body)
	if err != nil {
		return nil, err
	}
	result, err := s.GitHub.Exchange(ctx, c.member, bearer(c.r), req.Code, req.Nonce)
	if err != nil {
		return nil, err
	}
	resp := protocol.FinishGithubLoginResponse{Member: memberDTO(result.Member), Account: accountDTO(result.Account)}
	if result.Session != nil {
		resp.MemberToken = &result.Session.MemberToken
	}
	return resp, nil
}

func (s *server) unbindGithub(ctx context.Context, c call) (any, error) {
	req, err := protocol.DecodeUnbindGithubRequest(c.body)
	if err != nil {
		return nil, err
	}
	return nil, s.GitHub.Unbind(ctx, c.member.ID, req.CurrentPassword)
}

func (s *server) adminGetGithubCredential(ctx context.Context, _ call) (any, error) {
	clientID, err := s.GitHub.ClientID(ctx)
	if err != nil {
		return nil, err
	}
	return protocol.AdminGetGithubCredentialResponse{ClientID: optional(clientID), ClientSecretSet: clientID != "", CallbackURL: s.PublicURL + GitHubCallbackPath}, nil
}

func (s *server) adminSetGithubCredential(ctx context.Context, c call) (any, error) {
	req, err := protocol.DecodeAdminSetGithubCredentialRequest(c.body)
	if err != nil {
		return nil, err
	}
	return nil, s.GitHub.SetOAuthApp(ctx, port.GitHubOAuthApp{ClientID: req.ClientID, ClientSecret: req.ClientSecret})
}

func (s *server) adminDeleteGithubCredential(ctx context.Context, _ call) (any, error) {
	return nil, s.GitHub.DeleteOAuthApp(ctx)
}

// 浏览器从 GitHub 跳回来，换到兑换码之后跳到发起授权的客户端的落地页。结果放在片段里，不会出现在之后的请求和 Referer 里。
// 回调地址的查询串里有授权码，所以响应也不让浏览器把回调地址当作 Referer 发出去。跳转目标只看 state 对应的记录，不看回调地址里的参数，
// 否则就成了开放重定向。
func (s *server) githubCallback(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), requestDeadline)
	defer cancel()
	q := r.URL.Query()
	var (
		client   port.GitHubClient
		fragment string
		err      error
	)
	if reason := q.Get("error"); reason != "" {
		fragment = "error=denied"
		if reason != "access_denied" {
			s.logger.WarnContext(ctx, "GitHub 拒绝了授权请求", "reason", reason, "description", q.Get("error_description"))
			fragment = "error=failed"
		}
		client, err = s.GitHub.Cancel(ctx, q.Get("state"))
		if err != nil && !errors.Is(err, identity.ErrGitHubRequestExpired) {
			s.logger.ErrorContext(ctx, "作废 GitHub 授权的 state 失败", "error", err)
		}
	} else {
		var code string
		client, code, err = s.GitHub.Callback(ctx, q.Get("state"), q.Get("code"))
		switch {
		case err == nil:
			fragment = "code=" + code
		case errors.Is(err, identity.ErrGitHubRequestExpired):
			fragment = "error=expired"
		case errors.Is(err, identity.ErrGitHubDisabled):
			fragment = "error=failed"
		case errors.Is(err, identity.ErrGitHubFailed):
			s.logger.WarnContext(ctx, "没有从 GitHub 换到用户", "error", err)
			fragment = "error=failed"
		default:
			s.logger.ErrorContext(ctx, "处理 GitHub 的回调失败", "error", err)
			fragment = "error=failed"
		}
	}
	landing := githubWebLanding
	if client == port.GitHubApp {
		landing = githubAppLanding
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	// 不用 http.Redirect，因为 http.Redirect 会把片段当作路径的一部分清理掉。
	w.Header().Set("Location", landing+"#"+fragment)
	w.WriteHeader(http.StatusFound)
}
