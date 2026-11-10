package github

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
)

const (
	verifier  = "dBjftJeZ4CVP-mJ92K1D5gV9dxWXJyqi5aZoxwYkrUM"
	challenge = "kXQYIXxOaWujAPbjqX_yBdiwMfNi4RzDNMDwDNpE7QQ"
)

var app = port.GitHubOAuthApp{ClientID: "client-1", ClientSecret: "secret-1"}

// 假的 GitHub：授权码 good 签发时绑定了 challenge，带上 verifier 才能换到令牌 token-1，令牌 token-1 对应用户 42。
type fakeGitHub struct {
	token func(w http.ResponseWriter, r *http.Request)
	user  func(w http.ResponseWriter, r *http.Request)
}

func newFake(t *testing.T) (*fakeGitHub, Config) {
	t.Helper()
	fake := &fakeGitHub{}
	fake.token = func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if r.Method != http.MethodPost || r.Header.Get("Accept") != "application/json" {
			t.Errorf("换取令牌的请求 = %s，Accept: %q", r.Method, r.Header.Get("Accept"))
		}
		want := url.Values{"client_id": {"client-1"}, "client_secret": {"secret-1"}, "code": {r.PostForm.Get("code")}, "code_verifier": {verifier}, "redirect_uri": {"https://alisten.example/api/auth/github/callback"}}
		if r.PostForm.Encode() != want.Encode() {
			t.Errorf("换取令牌的参数 = %s，期望 %s", r.PostForm.Encode(), want.Encode())
		}
		w.Header().Set("Content-Type", "application/json")
		if r.PostForm.Get("code") == "good" {
			_, _ = w.Write([]byte(`{"access_token":"token-1","token_type":"bearer","scope":""}`))
			return
		}
		_, _ = w.Write([]byte(`{"error":"bad_verification_code","error_description":"The code passed is incorrect or expired.","error_uri":"/apps/managing-oauth-apps/troubleshooting-oauth-app-access-token-request-errors/#bad-verification-code"}`))
	}
	fake.user = func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token-1" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"message":"Bad credentials"}`))
			return
		}
		if r.Header.Get("User-Agent") == "" || r.Header.Get("Accept") != "application/vnd.github+json" {
			t.Errorf("用户接口的请求头 User-Agent: %q，Accept: %q", r.Header.Get("User-Agent"), r.Header.Get("Accept"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"login":"octocat","id":42,"name":"The Octocat"}`))
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/login/oauth/access_token", func(w http.ResponseWriter, r *http.Request) { fake.token(w, r) })
	mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) { fake.user(w, r) })
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return fake, Config{
		RedirectURL: "https://alisten.example/api/auth/github/callback",
		Endpoint: Endpoint{
			AuthorizeURL: server.URL + "/login/oauth/authorize",
			TokenURL:     server.URL + "/login/oauth/access_token",
			UserURL:      server.URL + "/user",
		},
	}
}

func TestAuthorizeURL(t *testing.T) {
	o := New(http.DefaultClient, Config{RedirectURL: "https://alisten.example/api/auth/github/callback", Endpoint: GitHubCom})
	u, err := url.Parse(o.AuthorizeURL(app, "state 1&2", verifier))
	if err != nil {
		t.Fatal(err)
	}
	if got := u.Scheme + "://" + u.Host + u.Path; got != "https://github.com/login/oauth/authorize" {
		t.Errorf("授权地址 = %s", got)
	}
	want := url.Values{
		"client_id":             {"client-1"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"redirect_uri":          {"https://alisten.example/api/auth/github/callback"},
		"state":                 {"state 1&2"},
	}
	if got := u.Query(); got.Encode() != want.Encode() {
		t.Errorf("授权地址的参数 = %s，期望 %s：不申请任何 scope，只读公开资料", got.Encode(), want.Encode())
	}
}

func TestUserID(t *testing.T) {
	_, cfg := newFake(t)
	o := New(http.DefaultClient, cfg)
	if id, err := o.UserID(t.Context(), app, "good", verifier); err != nil || id != 42 {
		t.Errorf("UserID = %d, %v，期望 42", id, err)
	}
}

func TestUserIDFails(t *testing.T) {
	for _, tc := range []struct {
		name  string
		code  string
		token func(w http.ResponseWriter, r *http.Request)
		user  func(w http.ResponseWriter, r *http.Request)
		want  string
	}{
		{name: "授权码错误或过期", code: "expired", want: "bad_verification_code"},
		{name: "令牌接口返回非 200", code: "good", token: func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}, want: "503"},
		{name: "令牌接口返回的不是 JSON", code: "good", token: func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`access_token=token-1&token_type=bearer`))
		}, want: "解码"},
		{name: "令牌为空", code: "good", token: func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"access_token":""}`))
		}, want: "没有给出令牌"},
		{name: "用户接口拒绝令牌", code: "good", user: func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}, want: "401"},
		{name: "用户 ID 不是正数", code: "good", user: func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"login":"ghost"}`))
		}, want: "用户 ID"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake, cfg := newFake(t)
			if tc.token != nil {
				fake.token = tc.token
			}
			if tc.user != nil {
				fake.user = tc.user
			}
			_, err := New(http.DefaultClient, cfg).UserID(t.Context(), app, tc.code, verifier)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("UserID 的错误 = %v，期望包含 %q", err, tc.want)
			}
			if err != nil && strings.Contains(err.Error(), "secret-1") {
				t.Errorf("错误信息中不能有 client secret：%v", err)
			}
		})
	}
}

// 不响应的 GitHub：请求一直挂着，直到请求的 ctx 结束。
type hangingTransport struct{}

func (hangingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	<-r.Context().Done()
	return nil, r.Context().Err()
}

func TestUserIDTimesOut(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o := New(&http.Client{Transport: hangingTransport{}}, Config{Endpoint: GitHubCom})
		start := time.Now()
		if _, err := o.UserID(t.Context(), app, "good", verifier); !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("GitHub 不响应时的错误 = %v，期望超过期限", err)
		}
		if elapsed := time.Since(start); elapsed != 10*time.Second {
			t.Errorf("等了 %v 才中止，期望 10 秒", elapsed)
		}
	})
}
