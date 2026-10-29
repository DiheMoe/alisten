package bilibili

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
)

func TestCreateQRLogin(t *testing.T) {
	c, tr, obs, _ := serve(t, map[string]http.HandlerFunc{
		"/x/passport-login/web/qrcode/generate": func(w http.ResponseWriter, r *http.Request) {
			respondJSON(w, `{"code":0,"message":"0","data":{"url":"https://passport.bilibili.com/h5-app/passport/login/scan?qrcode_key=KEY123","qrcode_key":"KEY123"}}`)
		},
	})
	before := time.Now()
	session, err := c.CreateQRLogin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if session.Key != "KEY123" {
		t.Errorf("Key = %q", session.Key)
	}
	if want := "https://passport.bilibili.com/h5-app/passport/login/scan?qrcode_key=KEY123"; session.LoginURL != want {
		t.Errorf("LoginURL = %q，期望 %q", session.LoginURL, want)
	}
	if session.QRImage != "" {
		t.Errorf("QRImage = %q，期望留空", session.QRImage)
	}
	if session.ExpiresAt.Before(before.Add(3*time.Minute-time.Second)) || session.ExpiresAt.After(time.Now().Add(3*time.Minute+time.Second)) {
		t.Errorf("ExpiresAt = %v，期望约为 3 分钟后", session.ExpiresAt)
	}
	reqs := tr.requests()
	if len(reqs) != 1 || reqs[0].host != "passport.bilibili.com" {
		t.Fatalf("请求 = %+v", reqs)
	}
	if reqs[0].referer != "https://www.bilibili.com/" {
		t.Errorf("Referer = %q", reqs[0].referer)
	}
	obs.check(t, "bilibili:login_create:ok")
}

func TestCreateQRLoginFailure(t *testing.T) {
	for name, body := range map[string]string{
		"平台错误":  `{"code":-1,"message":"操作频繁","data":{}}`,
		"缺少key": `{"code":0,"message":"0","data":{"url":"https://example.com","qrcode_key":""}}`,
		"格式错误":  `不是 JSON`,
	} {
		t.Run(name, func(t *testing.T) {
			c, _, obs, _ := serve(t, map[string]http.HandlerFunc{
				"/x/passport-login/web/qrcode/generate": func(w http.ResponseWriter, r *http.Request) {
					respondJSON(w, body)
				},
			})
			if _, err := c.CreateQRLogin(t.Context()); err == nil {
				t.Fatal("没有返回错误")
			} else if strings.Contains(err.Error(), "example.com") {
				t.Fatalf("错误包含平台地址：%v", err)
			}
			obs.check(t, "bilibili:login_create:error")
		})
	}

	c, _, _, _ := serve(t, map[string]http.HandlerFunc{
		"/x/passport-login/web/qrcode/generate": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		},
	})
	if _, err := c.CreateQRLogin(t.Context()); err == nil {
		t.Fatal("HTTP 500 没有返回错误")
	}
}

func TestCheckQRLoginStatusMapping(t *testing.T) {
	for code, want := range map[int]port.QRLoginStatus{
		86101: port.QRLoginWaiting,
		86090: port.QRLoginScanned,
		86038: port.QRLoginExpired,
		86200: port.QRLoginFailed,
	} {
		t.Run(string(want), func(t *testing.T) {
			c, tr, obs, _ := serve(t, map[string]http.HandlerFunc{
				"/x/passport-login/web/qrcode/poll": func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Query().Get("qrcode_key") != "KEY123" {
						t.Errorf("qrcode_key = %q", r.URL.Query().Get("qrcode_key"))
					}
					respondJSON(w, `{"code":0,"message":"0","data":{"code":`+strconv.Itoa(code)+`,"message":"状态提示"}}`)
				},
			})
			result, err := c.CheckQRLogin(t.Context(), "KEY123")
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != want || result.Message != "状态提示" {
				t.Fatalf("结果 = %+v，期望状态 %q", result, want)
			}
			if tr.countRequests("/x/passport-login/web/qrcode/poll") != 1 {
				t.Fatalf("poll 请求数 = %d", tr.countRequests("/x/passport-login/web/qrcode/poll"))
			}
			obs.check(t, "bilibili:login_check:ok")
		})
	}
}

func TestCheckQRLoginBlankKey(t *testing.T) {
	c, tr, _, _ := serve(t, map[string]http.HandlerFunc{
		"/x/passport-login/web/qrcode/poll": func(w http.ResponseWriter, r *http.Request) {
			t.Error("空白 key 不应请求平台")
		},
	})
	for _, key := range []string{"", "   "} {
		if _, err := c.CheckQRLogin(t.Context(), key); err == nil {
			t.Errorf("key = %q 没有返回错误", key)
		}
	}
	if len(tr.requests()) != 0 {
		t.Fatalf("空白 key 不应请求平台，请求了 %+v", tr.requests())
	}
}

func TestCheckQRLoginMessageFallback(t *testing.T) {
	c, _, _, _ := serve(t, map[string]http.HandlerFunc{
		"/x/passport-login/web/qrcode/poll": func(w http.ResponseWriter, r *http.Request) {
			respondJSON(w, `{"code":0,"message":"顶层提示","data":{"code":86101,"message":""}}`)
		},
	})
	result, err := c.CheckQRLogin(t.Context(), "KEY123")
	if err != nil {
		t.Fatal(err)
	}
	if result.Message != "顶层提示" {
		t.Fatalf("Message = %q，期望回落到顶层 message", result.Message)
	}
}

func TestCheckQRLoginSuccess(t *testing.T) {
	c, _, obs, creds := serve(t, map[string]http.HandlerFunc{
		"/x/passport-login/web/qrcode/poll": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Add("Set-Cookie", "bili_jct=jct456; Path=/")
			w.Header().Add("Set-Cookie", "SESSDATA=sess123; Path=/; HttpOnly")
			respondJSON(w, `{"code":0,"message":"0","data":{"code":0,"message":"登录成功","refresh_token":"refresh789"}}`)
		},
	})
	result, err := c.CheckQRLogin(t.Context(), "KEY123")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != port.QRLoginSuccess || result.Message != "登录成功" {
		t.Fatalf("结果 = %+v", result)
	}
	if len(creds.saves) != 1 {
		t.Fatalf("保存次数 = %d", len(creds.saves))
	}
	saved := creds.saves[0]
	for _, want := range []string{"SESSDATA=sess123; bili_jct=jct456", "refresh789"} {
		if !strings.Contains(saved, want) {
			t.Errorf("保存的凭据不含 %q：%q", want, saved)
		}
	}
	// 结果不能把凭据带出去。
	if strings.Contains(result.Message, "sess123") || strings.Contains(result.Message, "refresh789") {
		t.Fatalf("结果泄露了凭据：%+v", result)
	}
	obs.check(t, "bilibili:login_check:ok")
}

func TestCheckQRLoginSuccessWithoutCookies(t *testing.T) {
	c, _, _, creds := serve(t, map[string]http.HandlerFunc{
		"/x/passport-login/web/qrcode/poll": func(w http.ResponseWriter, r *http.Request) {
			respondJSON(w, `{"code":0,"message":"0","data":{"code":0,"message":"登录成功","refresh_token":"refresh789"}}`)
		},
	})
	if _, err := c.CheckQRLogin(t.Context(), "KEY123"); err == nil {
		t.Fatal("扫码成功但没有 Cookie 时没有返回错误")
	}
	if len(creds.saves) != 0 {
		t.Fatalf("不应保存凭据，保存了 %d 次", len(creds.saves))
	}
}

func TestCheckQRLoginFailure(t *testing.T) {
	c, _, _, _ := serve(t, map[string]http.HandlerFunc{
		"/x/passport-login/web/qrcode/poll": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		},
	})
	if _, err := c.CheckQRLogin(t.Context(), "KEY123"); err == nil {
		t.Fatal("HTTP 500 没有返回错误")
	}

	c, _, _, creds := serve(t, map[string]http.HandlerFunc{
		"/x/passport-login/web/qrcode/poll": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Add("Set-Cookie", "SESSDATA=sess123; Path=/")
			respondJSON(w, `{"code":0,"message":"0","data":{"code":0,"message":"登录成功","refresh_token":"refresh789"}}`)
		},
	})
	creds.saveErr = errors.New("数据库不可用")
	if _, err := c.CheckQRLogin(t.Context(), "KEY123"); err == nil {
		t.Fatal("保存失败时没有返回错误")
	}
	if len(creds.saves) != 0 {
		t.Fatal("保存失败不应留下凭据")
	}
}

func TestCredentialStatusNotSet(t *testing.T) {
	c, tr, obs, _ := serve(t, nil)
	status, err := c.CredentialStatus(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if status.Set || status.Account != nil {
		t.Fatalf("状态 = %+v，期望未设置", status)
	}
	if len(tr.requests()) != 0 {
		t.Fatalf("没有凭据时不应请求平台，请求了 %+v", tr.requests())
	}
	obs.check(t)
}

func TestCredentialStatusLoggedIn(t *testing.T) {
	for name, tc := range map[string]struct {
		vipStatus int
		vipType   int
		vip       bool
	}{
		"大会员":   {1, 2, true},
		"非大会员":  {0, 0, false},
		"过期大会员": {1, 0, false},
	} {
		t.Run(name, func(t *testing.T) {
			c, tr, obs, creds := serve(t, map[string]http.HandlerFunc{
				"/x/web-interface/nav": func(w http.ResponseWriter, r *http.Request) {
					respondJSON(w, `{"code":0,"message":"0","data":{"isLogin":true,"uname":"测试用户","mid":12345,"vipStatus":`+strconv.Itoa(tc.vipStatus)+`,"vipType":`+strconv.Itoa(tc.vipType)+`}}`)
				},
			})
			creds.set(t, "SESSDATA=sess123; bili_jct=jct456", "refresh789")
			status, err := c.CredentialStatus(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if !status.Set || status.Account == nil {
				t.Fatalf("状态 = %+v，期望已登录", status)
			}
			a := status.Account
			if a.ID != "12345" || a.Name == nil || *a.Name != "测试用户" || a.VIP == nil || *a.VIP != tc.vip || !a.Verified {
				t.Errorf("账号 = %+v", a)
			}
			reqs := tr.requests()
			if len(reqs) != 1 || reqs[0].cookie != "SESSDATA=sess123; bili_jct=jct456" {
				t.Fatalf("nav 请求 = %+v", reqs)
			}
			obs.check(t, "bilibili:credential_status:ok")
		})
	}
}

func TestCredentialStatusNotLoggedIn(t *testing.T) {
	c, _, _, creds := serve(t, map[string]http.HandlerFunc{
		"/x/web-interface/nav": func(w http.ResponseWriter, r *http.Request) {
			respondJSON(w, `{"code":-101,"message":"账号未登录","data":{"isLogin":false}}`)
		},
	})
	creds.set(t, "SESSDATA=expired", "refresh789")
	status, err := c.CredentialStatus(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !status.Set || status.Account != nil {
		t.Fatalf("状态 = %+v，期望已保存但未识别到账号", status)
	}
}

func TestCredentialStatusFailure(t *testing.T) {
	c, _, _, creds := serve(t, map[string]http.HandlerFunc{
		"/x/web-interface/nav": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		},
	})
	creds.set(t, "SESSDATA=sess123", "")
	if _, err := c.CredentialStatus(t.Context()); err == nil {
		t.Fatal("nav 请求失败时没有返回错误")
	} else if strings.Contains(err.Error(), "sess123") {
		t.Fatalf("错误泄露了凭据：%v", err)
	}

	creds.loadErr = errors.New("数据库不可用")
	if _, err := c.CredentialStatus(t.Context()); err == nil {
		t.Fatal("读取凭据失败时没有返回错误")
	}
}
