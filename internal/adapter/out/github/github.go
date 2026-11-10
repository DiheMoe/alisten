package github

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
)

var _ port.GitHubOAuth = (*OAuth)(nil)

const (
	timeout = 10 * time.Second
	// GitHub 的这两个响应都只有几百字节。
	maxResponseBytes = 64 << 10
)

type Endpoint struct {
	AuthorizeURL string
	TokenURL     string
	UserURL      string
}

var GitHubCom = Endpoint{
	AuthorizeURL: "https://github.com/login/oauth/authorize",
	TokenURL:     "https://github.com/login/oauth/access_token",
	UserURL:      "https://api.github.com/user",
}

type Config struct {
	RedirectURL string
	Endpoint    Endpoint
}

type OAuth struct {
	client *http.Client
	cfg    Config
}

func New(client *http.Client, cfg Config) *OAuth {
	return &OAuth{client: client, cfg: cfg}
}

func (o *OAuth) AuthorizeURL(app port.GitHubOAuthApp, state, verifier string) string {
	challenge := sha256.Sum256([]byte(verifier))
	q := url.Values{
		"client_id":             {app.ClientID},
		"redirect_uri":          {o.cfg.RedirectURL},
		"state":                 {state},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(challenge[:])},
		"code_challenge_method": {"S256"},
	}
	return o.cfg.Endpoint.AuthorizeURL + "?" + q.Encode()
}

func (o *OAuth) UserID(ctx context.Context, app port.GitHubOAuthApp, code, verifier string) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	token, err := o.token(ctx, app, code, verifier)
	if err != nil {
		return 0, fmt.Errorf("向 GitHub 换取令牌：%w", err)
	}
	id, err := o.user(ctx, token)
	if err != nil {
		return 0, fmt.Errorf("读取 GitHub 用户：%w", err)
	}
	return id, nil
}

func (o *OAuth) token(ctx context.Context, app port.GitHubOAuthApp, code, verifier string) (string, error) {
	form := url.Values{"client_id": {app.ClientID}, "client_secret": {app.ClientSecret}, "code": {code}, "code_verifier": {verifier}, "redirect_uri": {o.cfg.RedirectURL}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.cfg.Endpoint.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	// 授权码错误或过期时，GitHub 也以 200 返回，错误放在响应体的 error 中。
	var body struct {
		AccessToken      string `json:"access_token"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := o.do(req, &body); err != nil {
		return "", err
	}
	if body.Error != "" {
		return "", fmt.Errorf("GitHub 拒绝了授权码：%s（%s）", body.Error, body.ErrorDescription)
	}
	if body.AccessToken == "" {
		return "", fmt.Errorf("GitHub 没有给出令牌")
	}
	return body.AccessToken, nil
}

func (o *OAuth) user(ctx context.Context, token string) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.cfg.Endpoint.UserURL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	// GitHub 的 REST API 拒绝不带 User-Agent 的请求。
	req.Header.Set("User-Agent", "alisten")
	var body struct {
		ID int64 `json:"id"`
	}
	if err := o.do(req, &body); err != nil {
		return 0, err
	}
	if body.ID <= 0 {
		return 0, fmt.Errorf("GitHub 返回的用户 ID 不是正数：%d", body.ID)
	}
	return body.ID, nil
}

func (o *OAuth) do(req *http.Request, v any) error {
	resp, err := o.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub 返回 HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(v); err != nil {
		return fmt.Errorf("解码 GitHub 的响应：%w", err)
	}
	return nil
}
