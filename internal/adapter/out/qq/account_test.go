package qq

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"

	"github.com/dihemoe/alisten/internal/app/port"
)

func accountTestClient(t *testing.T, cookie string, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/cgi-bin/musicu.fcg" {
			t.Errorf("意外的请求 %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Cookie"); got != cookie {
			t.Errorf("账号接口的 Cookie = %q，期望已保存的凭据", got)
		}
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	return New(
		&http.Client{Transport: redirect{target}},
		func(context.Context) (string, error) { return cookie, nil },
		nil,
		(&observed{}).record,
		testLogger,
	)
}

func TestCurrentAccount(t *testing.T) {
	tests := []struct {
		name   string
		cookie string
		wantID string
	}{
		{"QQ uin 去前缀和前导零", "uin=o000123", "123"},
		{"微信 uin", "wxuin=000101", "101"},
		{"ptui_loginuin", "ptui_loginuin=o000102", "102"},
		{"luin", "luin=o000103", "103"},
		{"pt2gguin", "pt2gguin=o000104", "104"},
		{"superuin", "superuin=o000105", "105"},
		{"p_uin", "p_uin=o000106", "106"},
		{"musicid", "musicid=107", "107"},
		{"userid", "userid=108", "108"},
		{"user_id", "user_id=109", "109"},
		{"微信 uin 优先", "uin=000123; wxuin=000456", "456"},
		{"没有账号标识", "qqmusic_key=fictitious", ""},
		{"账号标识为零", "uin=o0000", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := accountTestClient(t, tc.cookie, fixture(t, "account.json")).CurrentAccount(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if tc.wantID == "" {
				if got != nil {
					t.Fatalf("没有账号标识时 = %+v，期望 nil", got)
				}
				return
			}
			want := &port.SourceAccount{ID: tc.wantID, VIP: new(bool), Verified: true}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("账号资料 = %+v，期望 %+v", got, want)
			}
		})
	}
}

// 绿钻和豪华绿钻任一有效都算会员，identity 缺失时 VIP 留 nil 显示未知。
func TestCurrentAccountVIP(t *testing.T) {
	for _, tc := range []struct {
		file    string
		wantVIP *bool
	}{
		{"account-vip.json", boolPtr(true)},
		{"account-svip.json", boolPtr(true)},
		{"account.json", boolPtr(false)},
		{"account-no-identity.json", nil},
	} {
		got, err := accountTestClient(t, "uin=o000123", fixture(t, tc.file)).CurrentAccount(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if got == nil || got.Verified != true || !reflect.DeepEqual(got.VIP, tc.wantVIP) {
			t.Errorf("%s 的 VIP = %v，期望 %v", tc.file, got.VIP, tc.wantVIP)
		}
	}
}

// QQ 音乐认为登录无效时返回 nil 账号而不是错误，与网易云个人资料为空一致。
func TestCurrentAccountExpired(t *testing.T) {
	got, err := accountTestClient(t, "uin=o000123", fixture(t, "account-expired.json")).CurrentAccount(t.Context())
	if err != nil || got != nil {
		t.Fatalf("登录失效时 = %+v, %v，期望 nil 账号且无错误", got, err)
	}
}

// 上游异常是查询失败，不能冒充没有账号。
func TestCurrentAccountUpstreamError(t *testing.T) {
	for name, handler := range map[string]http.HandlerFunc{
		"HTTP 502":   func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) },
		"响应 JSON 畸形": body(`{"code":0,`),
		"业务码缺失":      body(`{"code":0,"req":{"data":{}}}`),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := accountTestClient(t, "uin=o000123", handler).CurrentAccount(t.Context()); err == nil {
				t.Error("上游异常时没有返回错误")
			}
		})
	}
}

func TestCurrentAccountCookieError(t *testing.T) {
	want := errors.New("读取失败")
	load := func(context.Context) (string, error) { return "", want }
	c := New(&http.Client{}, load, nil, (&observed{}).record, testLogger)
	if _, err := c.CurrentAccount(t.Context()); !errors.Is(err, want) {
		t.Fatalf("读取凭据的错误 = %v，期望 %v", err, want)
	}
}

func boolPtr(b bool) *bool { return &b }
