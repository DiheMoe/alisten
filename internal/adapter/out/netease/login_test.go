package netease

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
)

func serveLogin(t *testing.T, routes map[string]http.HandlerFunc, saved *string) (*Client, *observed) {
	t.Helper()
	return serveLoginWithStore(t, routes, func(_ context.Context, value string) error {
		*saved = value
		return nil
	})
}

func serveLoginWithStore(t *testing.T, routes map[string]http.HandlerFunc, store func(context.Context, string) error) (*Client, *observed) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		checkClientIP(t, r)
		handle, ok := routes[r.URL.Path]
		if !ok {
			t.Errorf("意外的请求 %s %s", r.Method, r.URL)
			http.NotFound(w, r)
			return
		}
		handle(w, r)
	}))
	t.Cleanup(server.Close)
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	obs := &observed{}
	load := func(context.Context) (string, error) { return "MUSIC_U=loaded", nil }
	return New(&http.Client{Transport: redirect{target}}, load, store, obs.record), obs
}

func TestCreateQRLogin(t *testing.T) {
	c, obs := serveLogin(t, map[string]http.HandlerFunc{
		"/api/login/qrcode/unikey": func(w http.ResponseWriter, r *http.Request) {
			wantUA := "Mozilla/5.0 (Windows NT 10.0; WOW64) AppleWebKit/537.36 (KHTML, like Gecko) Safari/537.36 Chrome/91.0.4472.164 NeteaseMusicDesktop/3.0.18.203152"
			if got := r.Header.Get("User-Agent"); got != wantUA {
				t.Errorf("扫码登录的 User-Agent = %q，期望 %q", got, wantUA)
			}
			if r.Method != http.MethodPost || r.FormValue("type") != "3" {
				t.Errorf("创建登录的请求 = %s, type = %q", r.Method, r.FormValue("type"))
			}
			body(`{"code":200,"unikey":"test-key"}`)(w, r)
		},
	}, new(string))
	before := time.Now().Add(5 * time.Minute)
	got, err := c.CreateQRLogin(t.Context())
	after := time.Now().Add(5 * time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if got.Key != "test-key" || got.LoginURL != "https://music.163.com/login?codekey=test-key" || got.QRImage != "" {
		t.Errorf("登录会话 = %#v", got)
	}
	if got.ExpiresAt.Before(before) || got.ExpiresAt.After(after) {
		t.Errorf("登录到期时间 = %v，期望约为 5 分钟后", got.ExpiresAt)
	}
	obs.check(t, "login_create")
}

func TestCreateQRLoginRejectsInvalidResponse(t *testing.T) {
	for _, response := range []string{
		`{"code":500,"unikey":"test-key"}`,
		`{"code":200}`,
	} {
		c, obs := serveLogin(t, map[string]http.HandlerFunc{
			"/api/login/qrcode/unikey": body(response),
		}, new(string))
		if got, err := c.CreateQRLogin(t.Context()); err == nil {
			t.Errorf("响应 %s 的登录会话 = %#v，期望错误", response, got)
		}
		obs.check(t, "login_create 失败")
	}
}

func TestQRLoginStatuses(t *testing.T) {
	for _, tt := range []struct {
		name     string
		response string
		want     port.QRLoginResult
	}{
		{"过期", `{"code":800,"message":"二维码已过期","cookie":"MUSIC_U=do-not-save"}`, port.QRLoginResult{Status: port.QRLoginExpired, Message: "二维码已过期"}},
		{"等待扫码", `{"code":801,"message":"等待扫码","cookie":"MUSIC_U=do-not-save"}`, port.QRLoginResult{Status: port.QRLoginWaiting, Message: "等待扫码"}},
		{"已扫码", `{"code":802,"message":"已扫码","cookie":"MUSIC_U=do-not-save"}`, port.QRLoginResult{Status: port.QRLoginScanned, Message: "已扫码"}},
		{"其他状态", `{"code":500,"message":"登录失败","cookie":"MUSIC_U=do-not-save"}`, port.QRLoginResult{Status: port.QRLoginFailed, Message: "登录失败"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			saved := "未保存"
			c, obs := serveLogin(t, map[string]http.HandlerFunc{
				"/api/login/qrcode/client/login": func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodPost || r.FormValue("key") != "test-key" || r.FormValue("type") != "3" {
						t.Errorf("轮询登录的请求 = %s, key = %q, type = %q", r.Method, r.FormValue("key"), r.FormValue("type"))
					}
					w.Header().Add("Set-Cookie", "MUSIC_U=do-not-save; Path=/")
					body(tt.response)(w, r)
				},
			}, &saved)
			got, err := c.CheckQRLogin(t.Context(), "test-key")
			if err != nil || got != tt.want {
				t.Errorf("登录结果 = %#v, %v，期望 %#v", got, err, tt.want)
			}
			if saved != "未保存" {
				t.Errorf("非成功状态保存了 Cookie %q", saved)
			}
			obs.check(t, "login_check")
		})
	}
}

func TestCheckQRLoginSavesCookie(t *testing.T) {
	var saved string
	c, obs := serveLogin(t, map[string]http.HandlerFunc{
		"/api/login/qrcode/client/login": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Add("Set-Cookie", "MUSIC_U=header; Path=/")
			body(`{"code":803,"message":"授权成功","cookie":"MUSIC_U=test"}`)(w, r)
		},
	}, &saved)
	got, err := c.CheckQRLogin(t.Context(), "test-key")
	want := port.QRLoginResult{Status: port.QRLoginSuccess, Message: "授权成功"}
	if err != nil || got != want || saved != "MUSIC_U=test" {
		t.Fatalf("登录结果 = %#v, %v，保存的 Cookie = %q", got, err, saved)
	}
	if cached, err := c.cookie.get(t.Context()); err != nil || cached != "MUSIC_U=test" {
		t.Errorf("本地缓存的 Cookie = %q, %v", cached, err)
	}
	obs.check(t, "login_check")
}

func TestCheckQRLoginUsesSortedResponseCookies(t *testing.T) {
	var saved string
	c, _ := serveLogin(t, map[string]http.HandlerFunc{
		"/api/login/qrcode/client/login": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Add("Set-Cookie", "z_cookie=last; Path=/")
			w.Header().Add("Set-Cookie", "MUSIC_U=test; Path=/")
			w.Header().Add("Set-Cookie", "a_cookie=first; Path=/")
			body(`{"code":803,"message":"授权成功"}`)(w, r)
		},
	}, &saved)
	if _, err := c.CheckQRLogin(t.Context(), "test-key"); err != nil {
		t.Fatal(err)
	}
	if want := "MUSIC_U=test; a_cookie=first; z_cookie=last"; saved != want {
		t.Errorf("保存的 Cookie = %q，期望 %q", saved, want)
	}
}

func TestCheckQRLoginRequiresCookie(t *testing.T) {
	saved := "未保存"
	c, obs := serveLogin(t, map[string]http.HandlerFunc{
		"/api/login/qrcode/client/login": body(`{"code":803,"message":"授权成功"}`),
	}, &saved)
	if got, err := c.CheckQRLogin(t.Context(), "test-key"); err == nil {
		t.Errorf("没有 Cookie 的登录结果 = %#v，期望错误", got)
	}
	if saved != "未保存" {
		t.Errorf("没有 Cookie 时保存了 %q", saved)
	}
	obs.check(t, "login_check 失败")
}

func TestCheckQRLoginDoesNotCacheFailedSave(t *testing.T) {
	saveErr := errors.New("保存测试 Cookie 失败")
	c, obs := serveLoginWithStore(t, map[string]http.HandlerFunc{
		"/api/login/qrcode/client/login": body(`{"code":803,"message":"授权成功","cookie":"MUSIC_U=test"}`),
	}, func(context.Context, string) error { return saveErr })
	if _, err := c.cookie.get(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got, err := c.CheckQRLogin(t.Context(), "test-key"); !errors.Is(err, saveErr) || got.Status == port.QRLoginSuccess {
		t.Errorf("保存失败的登录结果 = %#v, %v", got, err)
	}
	if cached, err := c.cookie.get(t.Context()); err != nil || cached != "MUSIC_U=loaded" {
		t.Errorf("保存失败后的本地 Cookie = %q, %v", cached, err)
	}
	obs.check(t, "login_check 失败")
}

func TestCheckQRLoginRejectsEmptyKey(t *testing.T) {
	saved := "未保存"
	c, obs := serveLogin(t, nil, &saved)
	if got, err := c.CheckQRLogin(t.Context(), ""); err == nil {
		t.Errorf("空 key 的登录结果 = %#v，期望错误", got)
	}
	if saved != "未保存" {
		t.Errorf("空 key 保存了 Cookie %q", saved)
	}
	obs.check(t)
}
