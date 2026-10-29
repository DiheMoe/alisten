package qq

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
)

const (
	testQRSig    = "a"
	testLoginSig = "login-sig"
	testMusicKey = "test"
)

var testQRPNG = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}

func serveQQQRLogin(t *testing.T, routes map[string]http.HandlerFunc, store func(context.Context, string) error) (func() *Client, *observed) {
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
	return func() *Client {
		return New(
			&http.Client{Transport: redirect{target}},
			func(context.Context) (string, error) { return testCookie, nil },
			store,
			obs.record,
		)
	}, obs
}

func checkQQLoginValues(t *testing.T, got url.Values, want url.Values) {
	t.Helper()
	for key, values := range want {
		if !slices.Equal(got[key], values) {
			t.Errorf("参数 %s = %q，期望 %q", key, got[key], values)
		}
	}
}

func addQQQRCreateRoutes(t *testing.T, routes map[string]http.HandlerFunc) {
	t.Helper()
	addQQQRCreateRoutesAtPath(t, routes, "/")
}

func addQQQRCreateRoutesAtPath(t *testing.T, routes map[string]http.HandlerFunc, cookiePath string) {
	t.Helper()
	routes["/cgi-bin/xlogin"] = func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("预热请求方法 = %s", r.Method)
		}
		checkQQLoginValues(t, r.URL.Query(), url.Values{
			"appid":            {"716027609"},
			"daid":             {"383"},
			"style":            {"33"},
			"login_text":       {"登录"},
			"hide_title_bar":   {"1"},
			"hide_border":      {"1"},
			"target":           {"self"},
			"s_url":            {"https://graph.qq.com/oauth2.0/login_jump"},
			"pt_3rd_aid":       {"100497308"},
			"pt_feedback_link": {"https://support.qq.com/products/77942?customInfo=.appid100497308"},
			"theme":            {"2"},
			"verify_theme":     {""},
		})
		http.SetCookie(w, &http.Cookie{Name: "pt_login_sig", Value: testLoginSig, Path: "/"})
		http.SetCookie(w, &http.Cookie{Name: "prewarm", Value: "temp", Path: "/"})
	}
	routes["/ssl/ptqrshow"] = func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("二维码请求方法 = %s", r.Method)
		}
		checkQQLoginValues(t, r.URL.Query(), url.Values{
			"appid":      {"716027609"},
			"e":          {"2"},
			"l":          {"M"},
			"s":          {"3"},
			"d":          {"72"},
			"v":          {"4"},
			"daid":       {"383"},
			"pt_3rd_aid": {"100497308"},
			"u1":         {"https://graph.qq.com/oauth2.0/login_jump"},
		})
		if r.URL.Query().Get("t") == "" {
			t.Error("二维码请求缺少时间戳")
		}
		for name, want := range map[string]string{"pt_login_sig": testLoginSig, "prewarm": "temp"} {
			cookie, err := r.Cookie(name)
			if err != nil || cookie.Value != want {
				t.Errorf("二维码请求的 Cookie %s = %#v, %v，期望 %q", name, cookie, err, want)
			}
		}
		http.SetCookie(w, &http.Cookie{Name: "qrsig", Value: testQRSig, Path: cookiePath})
		http.SetCookie(w, &http.Cookie{Name: "qr_temp", Value: "qr-temp", Path: cookiePath})
		_, _ = w.Write(testQRPNG)
	}
}

func addQQQRPollRoute(t *testing.T, routes map[string]http.HandlerFunc, response string) {
	t.Helper()
	routes["/ssl/ptqrlogin"] = func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("轮询请求方法 = %s", r.Method)
		}
		checkQQLoginValues(t, r.URL.Query(), url.Values{
			"u1":            {"https://graph.qq.com/oauth2.0/login_jump"},
			"ptqrtoken":     {"97"},
			"ptredirect":    {"0"},
			"h":             {"1"},
			"t":             {"1"},
			"g":             {"1"},
			"from_ui":       {"1"},
			"ptlang":        {"2052"},
			"js_ver":        {"26071711"},
			"js_type":       {"1"},
			"login_sig":     {testLoginSig},
			"pt_uistyle":    {"40"},
			"aid":           {"716027609"},
			"daid":          {"383"},
			"pt_3rd_aid":    {"100497308"},
			"pt_js_version": {"c1987b96"},
		})
		action := r.URL.Query().Get("action")
		milliseconds, err := strconv.ParseInt(strings.TrimPrefix(action, "0-0-"), 10, 64)
		if !strings.HasPrefix(action, "0-0-") || err != nil || milliseconds <= 0 {
			t.Errorf("轮询 action = %q", action)
		}
		for name, want := range map[string]string{
			"qrsig": testQRSig, "pt_login_sig": testLoginSig, "prewarm": "temp", "qr_temp": "qr-temp",
		} {
			cookie, err := r.Cookie(name)
			if err != nil || cookie.Value != want {
				t.Errorf("轮询请求的 Cookie %s = %#v, %v，期望 %q", name, cookie, err, want)
			}
		}
		body(response)(w, r)
	}
}

func createTestQQQRSession(t *testing.T, newClient func() *Client) port.QRLoginSession {
	t.Helper()
	session, err := newClient().CreateQRLogin(t.Context(), port.QQLoginQQ)
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func TestCreateQQQRLogin(t *testing.T) {
	saved := "未保存"
	routes := map[string]http.HandlerFunc{}
	addQQQRCreateRoutes(t, routes)
	newClient, obs := serveQQQRLogin(t, routes, func(_ context.Context, value string) error {
		saved = value
		return nil
	})

	before := time.Now().Add(2 * time.Minute)
	session, err := newClient().CreateQRLogin(t.Context(), port.QQLoginQQ)
	after := time.Now().Add(2 * time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if want := "data:image/png;base64," + base64.StdEncoding.EncodeToString(testQRPNG); session.QRImage != want {
		t.Errorf("二维码图片 = %q，期望 %q", session.QRImage, want)
	}
	if session.LoginURL != "" {
		t.Errorf("QQ 账号扫码的 LoginURL = %q，期望空字符串", session.LoginURL)
	}
	if session.ExpiresAt.Before(before) || session.ExpiresAt.After(after) {
		t.Errorf("登录到期时间 = %v，期望约为 2 分钟后", session.ExpiresAt)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(session.Key)
	if err != nil {
		t.Fatalf("解码登录 key：%v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(decoded, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 3 || fields["qrsig"] == nil || fields["login_sig"] == nil || fields["cookies"] == nil {
		t.Errorf("登录 key 的字段 = %v", fields)
	}
	var state struct {
		QRSig    string            `json:"qrsig"`
		LoginSig string            `json:"login_sig"`
		Cookies  map[string]string `json:"cookies"`
	}
	if err := json.Unmarshal(decoded, &state); err != nil {
		t.Fatal(err)
	}
	if state.QRSig != testQRSig || state.LoginSig != testLoginSig {
		t.Errorf("登录状态 = %#v", state)
	}
	wantCookies := map[string]string{"qrsig": testQRSig, "pt_login_sig": testLoginSig, "prewarm": "temp", "qr_temp": "qr-temp"}
	if len(state.Cookies) != len(wantCookies) {
		t.Errorf("临时 Cookie = %#v", state.Cookies)
	}
	for name, want := range wantCookies {
		if state.Cookies[name] != want {
			t.Errorf("临时 Cookie %s = %q，期望 %q", name, state.Cookies[name], want)
		}
	}
	if saved != "未保存" {
		t.Errorf("创建登录保存了 Cookie %q", saved)
	}
	obs.check(t, "qq login_create")
}

func TestQQQRLoginRetainsSSLPathCookiesAcrossClients(t *testing.T) {
	saved := "未保存"
	routes := map[string]http.HandlerFunc{}
	addQQQRCreateRoutesAtPath(t, routes, "/ssl")
	addQQQRPollRoute(t, routes, `ptuiCB('66','0','','0','等待扫码','')`)
	newClient, obs := serveQQQRLogin(t, routes, func(_ context.Context, value string) error {
		saved = value
		return nil
	})
	session := createTestQQQRSession(t, newClient)
	got, err := newClient().CheckQRLogin(t.Context(), port.QQLoginQQ, session.Key)
	want := port.QRLoginResult{Status: port.QRLoginWaiting, Message: "等待扫码"}
	if err != nil || got != want {
		t.Errorf("Path=/ssl 的扫码结果 = %#v, %v，期望 %#v", got, err, want)
	}
	if saved != "未保存" {
		t.Errorf("等待扫码时保存了 Cookie %q", saved)
	}
	obs.check(t, "qq login_create", "qq login_check")
}

func TestQQQRLoginStatusesAcrossClients(t *testing.T) {
	for _, tt := range []struct {
		name     string
		response string
		want     port.QRLoginResult
	}{
		{"过期", `ptuiCB('65','0','','0','二维码已过期','')`, port.QRLoginResult{Status: port.QRLoginExpired, Message: "二维码已过期"}},
		{"等待扫码", `ptuiCB('66','0','','0','等待扫码','')`, port.QRLoginResult{Status: port.QRLoginWaiting, Message: "等待扫码"}},
		{"已扫码", `ptuiCB('67','0','','0','已扫码','')`, port.QRLoginResult{Status: port.QRLoginScanned, Message: "已扫码"}},
		{"其他状态", `ptuiCB('68','0','','0','登录失败','')`, port.QRLoginResult{Status: port.QRLoginFailed, Message: "登录失败"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			saved := "未保存"
			routes := map[string]http.HandlerFunc{}
			addQQQRCreateRoutes(t, routes)
			addQQQRPollRoute(t, routes, tt.response)
			newClient, obs := serveQQQRLogin(t, routes, func(_ context.Context, value string) error {
				saved = value
				return nil
			})
			session := createTestQQQRSession(t, newClient)
			got, err := newClient().CheckQRLogin(t.Context(), port.QQLoginQQ, session.Key)
			if err != nil || got != tt.want {
				t.Errorf("跨 Client 登录结果 = %#v, %v，期望 %#v", got, err, tt.want)
			}
			if saved != "未保存" {
				t.Errorf("非成功状态保存了 Cookie %q", saved)
			}
			obs.check(t, "qq login_create", "qq login_check")
		})
	}
}

func addQQQOAuthRoutes(t *testing.T, routes map[string]http.HandlerFunc, musicResponse string) {
	t.Helper()
	addQQQRPollRoute(t, routes, `ptuiCB('0','0','https://graph.qq.com/oauth2.0/login_jump','0','登录成功','')`)
	routes["/oauth2.0/login_jump"] = func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://graph.qq.com/oauth2.0/login_jump_done", http.StatusFound)
	}
	routes["/oauth2.0/login_jump_done"] = func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "p_skey", Value: "a", Domain: ".qq.com", Path: "/"})
		body("ok")(w, r)
	}
	routes["/oauth2.0/authorize"] = func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("授权请求方法 = %s", r.Method)
		}
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		checkQQLoginValues(t, r.PostForm, url.Values{
			"response_type": {"code"},
			"client_id":     {"100497308"},
			"redirect_uri":  {"https://y.qq.com/portal/wx_redirect.html?login_type=1&surl=https%3A%2F%2Fy.qq.com%2F"},
			"scope":         {"get_user_info,get_app_friends"},
			"state":         {"state"},
			"switch":        {""},
			"from_ptlogin":  {"1"},
			"src":           {"1"},
			"update_auth":   {"1"},
			"openapi":       {"1010_1030"},
			"g_tk":          {"177670"},
		})
		if milliseconds, err := strconv.ParseInt(r.PostForm.Get("auth_time"), 10, 64); err != nil || milliseconds <= 0 {
			t.Errorf("授权时间 = %q", r.PostForm.Get("auth_time"))
		}
		if r.PostForm.Get("ui") == "" {
			t.Error("授权请求缺少 ui")
		}
		if cookie, err := r.Cookie("p_skey"); err != nil || cookie.Value != "a" {
			t.Errorf("授权请求的 p_skey = %#v, %v", cookie, err)
		}
		http.SetCookie(w, &http.Cookie{Name: "oauth_temp", Value: "keep", Domain: ".qq.com", Path: "/"})
		w.Header().Set("Location", "https://y.qq.com/portal/wx_redirect.html?login_type=1&surl=https%3A%2F%2Fy.qq.com%2F&code=fictitious-code")
		w.WriteHeader(http.StatusFound)
	}
	routes["/portal/wx_redirect.html"] = func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("code") != "fictitious-code" {
			t.Errorf("授权回调 code = %q", r.URL.Query().Get("code"))
		}
		http.SetCookie(w, &http.Cookie{Name: "redirect_extra", Value: "keep", Domain: ".qq.com", Path: "/"})
		body("ok")(w, r)
	}
	routes["/cgi-bin/musicu.fcg"] = func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("音乐登录请求 = %s, Content-Type = %q", r.Method, r.Header.Get("Content-Type"))
		}
		var payload struct {
			Comm struct {
				GTK      int    `json:"g_tk"`
				Platform string `json:"platform"`
				CT       int    `json:"ct"`
				CV       int    `json:"cv"`
			} `json:"comm"`
			Req struct {
				Module string `json:"module"`
				Method string `json:"method"`
				Param  struct {
					Code string `json:"code"`
				} `json:"param"`
			} `json:"req"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if payload.Comm.GTK != 5381 || payload.Comm.Platform != "yqq" || payload.Comm.CT != 24 || payload.Comm.CV != 0 ||
			payload.Req.Module != "QQConnectLogin.LoginServer" || payload.Req.Method != "QQLogin" || payload.Req.Param.Code != "fictitious-code" {
			t.Errorf("音乐登录请求体 = %#v", payload)
		}
		if cookie, err := r.Cookie("p_skey"); err != nil || cookie.Value != "a" {
			t.Errorf("音乐登录请求的 p_skey = %#v, %v", cookie, err)
		}
		if _, err := r.Cookie("qrsig"); err == nil {
			t.Error("音乐登录请求携带了 xui 的临时 qrsig")
		}
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "keep", Domain: ".qq.com", Path: "/"})
		body(musicResponse)(w, r)
	}
}

func parseSavedQQCookies(t *testing.T, saved string) map[string]string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "https://y.qq.com/", nil)
	req.Header.Set("Cookie", saved)
	cookies := req.Cookies()
	names := make([]string, len(cookies))
	values := make(map[string]string, len(cookies))
	for i, cookie := range cookies {
		names[i] = cookie.Name
		values[cookie.Name] = cookie.Value
	}
	if !sort.StringsAreSorted(names) {
		t.Errorf("保存的 Cookie 未按名称排序：%q", saved)
	}
	return values
}

func requireQQCookies(t *testing.T, saved string, want map[string]string) {
	t.Helper()
	got := parseSavedQQCookies(t, saved)
	for name, value := range want {
		if got[name] != value {
			t.Errorf("保存的 Cookie %s = %q，期望 %q；全部 Cookie = %#v", name, got[name], value, got)
		}
	}
}

func TestQQQRLoginCompletesOAuthAndSavesSortedCookies(t *testing.T) {
	var saved string
	routes := map[string]http.HandlerFunc{}
	addQQQRCreateRoutes(t, routes)
	addQQQOAuthRoutes(t, routes, `{"code":0,"req":{"code":0,"data":{"musickey":"test","musicid":"o1","json_extra":"keep","empty":""}}}`)
	newClient, obs := serveQQQRLogin(t, routes, func(_ context.Context, value string) error {
		saved = value
		return nil
	})
	session := createTestQQQRSession(t, newClient)
	client := newClient()
	result, err := client.CheckQRLogin(t.Context(), port.QQLoginQQ, session.Key)
	want := port.QRLoginResult{Status: port.QRLoginSuccess, Message: "登录成功"}
	if err != nil || result != want {
		t.Fatalf("QQ 登录 = %#v, %v", result, err)
	}
	requireQQCookies(t, saved, map[string]string{
		"qm_keyst": testMusicKey, "qqmusic_key": testMusicKey, "musickey": testMusicKey, "uin": "o1",
		"session": "keep", "redirect_extra": "keep", "oauth_temp": "keep",
	})
	if strings.Contains(saved, "json_extra=") || strings.Contains(saved, "empty=") {
		t.Errorf("保存了未知或空 Cookie：%q", saved)
	}
	if cached, err := client.cookie.get(t.Context()); err != nil || cached != saved {
		t.Errorf("本地缓存的 Cookie = %q, %v，期望 %q", cached, err, saved)
	}
	obs.check(t, "qq login_create", "qq login_check")
}

func TestQQQRLoginRejectsInvalidJSONMusicKeys(t *testing.T) {
	for _, name := range []string{"qm_keyst", "qqmusic_key", "musickey", "music_key", "strMusicKey"} {
		t.Run(name, func(t *testing.T) {
			saved := "未保存"
			routes := map[string]http.HandlerFunc{}
			addQQQRCreateRoutes(t, routes)
			response := `{"code":0,"req":{"code":0,"data":{"` + name + `":"bad;injected=value","musicid":"o1"}}}`
			addQQQOAuthRoutes(t, routes, response)
			newClient, obs := serveQQQRLogin(t, routes, func(_ context.Context, value string) error {
				saved = value
				return nil
			})
			session := createTestQQQRSession(t, newClient)
			result, err := newClient().CheckQRLogin(t.Context(), port.QQLoginQQ, session.Key)
			if err == nil || result.Status == port.QRLoginSuccess {
				t.Errorf("非法音乐 key 的登录结果 = %#v, %v，期望 Go 错误且不返回成功", result, err)
			}
			if saved != "未保存" {
				t.Errorf("非法音乐 key 时保存了 Cookie %q", saved)
			}
			obs.check(t, "qq login_create", "qq login_check 失败")
		})
	}
}

func TestQQQRLoginOmitsInvalidOptionalJSONCredentials(t *testing.T) {
	var saved string
	routes := map[string]http.HandlerFunc{}
	addQQQRCreateRoutes(t, routes)
	addQQQOAuthRoutes(t, routes, `{"code":0,"req":{"code":0,"data":{"musickey":"test","musicid":"o1","openid":"bad;injected=value"}}}`)
	newClient, obs := serveQQQRLogin(t, routes, func(_ context.Context, value string) error {
		saved = value
		return nil
	})
	session := createTestQQQRSession(t, newClient)
	result, err := newClient().CheckQRLogin(t.Context(), port.QQLoginQQ, session.Key)
	want := port.QRLoginResult{Status: port.QRLoginSuccess, Message: "登录成功"}
	if err != nil || result != want {
		t.Fatalf("带非法可选凭据的登录结果 = %#v, %v，期望成功并省略该字段", result, err)
	}
	requireQQCookies(t, saved, map[string]string{"musickey": testMusicKey, "uin": "o1"})
	if strings.Contains(saved, "openid=") || strings.Contains(saved, "injected") {
		t.Errorf("保存了非法可选凭据或畸形 Cookie 头：%q", saved)
	}
	obs.check(t, "qq login_create", "qq login_check")
}

func TestQQQRLoginMissingMusicKeyFailsWithoutSaving(t *testing.T) {
	saved := "未保存"
	routes := map[string]http.HandlerFunc{}
	addQQQRCreateRoutes(t, routes)
	addQQQOAuthRoutes(t, routes, `{"code":0,"req":{"code":0,"data":{"musicid":"o1","json_extra":"keep"}}}`)
	newClient, obs := serveQQQRLogin(t, routes, func(_ context.Context, value string) error {
		saved = value
		return nil
	})
	session := createTestQQQRSession(t, newClient)
	result, err := newClient().CheckQRLogin(t.Context(), port.QQLoginQQ, session.Key)
	if err != nil || result.Status != port.QRLoginFailed {
		t.Errorf("缺少音乐 key 的登录结果 = %#v, %v", result, err)
	}
	if saved != "未保存" {
		t.Errorf("缺少音乐 key 时保存了 %q", saved)
	}
	obs.check(t, "qq login_create", "qq login_check")
}

func TestQQQRLoginRejectsInvalidMusicResponseCodes(t *testing.T) {
	for _, tt := range []struct {
		name     string
		response string
	}{
		{"顶层 code 缺失", `{"req":{"code":0,"data":{"musickey":"test","uin":"o1"}}}`},
		{"顶层 code 为 null", `{"code":null,"req":{"code":0,"data":{"musickey":"test","uin":"o1"}}}`},
		{"req.code 缺失", `{"code":0,"req":{"data":{"musickey":"test","uin":"o1"}}}`},
		{"req.code 为 null", `{"code":0,"req":{"code":null,"data":{"musickey":"test","uin":"o1"}}}`},
		{"顶层 code 非零", `{"code":1,"req":{"code":0,"data":{"musickey":"test","uin":"o1"}}}`},
		{"req.code 非零", `{"code":0,"req":{"code":1,"data":{"musickey":"test","uin":"o1"}}}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			saved := "未保存"
			routes := map[string]http.HandlerFunc{}
			addQQQRCreateRoutes(t, routes)
			addQQQOAuthRoutes(t, routes, tt.response)
			newClient, obs := serveQQQRLogin(t, routes, func(_ context.Context, value string) error {
				saved = value
				return nil
			})
			session := createTestQQQRSession(t, newClient)
			result, err := newClient().CheckQRLogin(t.Context(), port.QQLoginQQ, session.Key)
			if err == nil || result.Status == port.QRLoginSuccess {
				t.Errorf("无效音乐响应码的登录结果 = %#v, %v，期望错误", result, err)
			}
			if saved != "未保存" {
				t.Errorf("无效音乐响应码时保存了 %q", saved)
			}
			obs.check(t, "qq login_create", "qq login_check 失败")
		})
	}
}

func TestQQQRLoginSaveFailureIsObserved(t *testing.T) {
	saveErr := errors.New("保存测试 Cookie 失败")
	routes := map[string]http.HandlerFunc{}
	addQQQRCreateRoutes(t, routes)
	addQQQOAuthRoutes(t, routes, `{"code":0,"req":{"code":0,"data":{"qm_keyst":"test","uin":"o1"}}}`)
	newClient, obs := serveQQQRLogin(t, routes, func(context.Context, string) error { return saveErr })
	session := createTestQQQRSession(t, newClient)
	client := newClient()
	if _, err := client.cookie.get(t.Context()); err != nil {
		t.Fatal(err)
	}
	result, err := client.CheckQRLogin(t.Context(), port.QQLoginQQ, session.Key)
	if !errors.Is(err, saveErr) || result.Status == port.QRLoginSuccess {
		t.Errorf("保存失败的登录结果 = %#v, %v", result, err)
	}
	if cached, err := client.cookie.get(t.Context()); err != nil || cached != testCookie {
		t.Errorf("保存失败后的本地 Cookie = %q, %v", cached, err)
	}
	obs.check(t, "qq login_create", "qq login_check 失败")
}

func TestQQQRLoginRejectsUnsupportedTypeAndEmptyKey(t *testing.T) {
	newClient, obs := serveQQQRLogin(t, nil, func(context.Context, string) error { return nil })
	client := newClient()
	loginType := port.QQLoginType("unknown")
	if session, err := client.CreateQRLogin(t.Context(), loginType); err == nil {
		t.Errorf("类型 %q 创建的会话 = %#v，期望错误", loginType, session)
	}
	if result, err := client.CheckQRLogin(t.Context(), loginType, "key"); err == nil {
		t.Errorf("类型 %q 的轮询结果 = %#v，期望错误", loginType, result)
	}
	for _, loginType := range []port.QQLoginType{port.QQLoginQQ, port.QQLoginWeChat} {
		if result, err := client.CheckQRLogin(t.Context(), loginType, ""); err == nil {
			t.Errorf("类型 %q 空 key 的轮询结果 = %#v，期望错误", loginType, result)
		}
	}
	obs.check(t)
}

const (
	testWeChatUUID        = "fictitious-wechat-uuid"
	testWeChatCode        = "fictitious-wechat-code"
	testWeChatRedirectURI = "https://y.qq.com/portal/wx_redirect.html?login_type=2&surl=https://y.qq.com/"
)

func addWeChatQRCreateRoute(t *testing.T, routes map[string]http.HandlerFunc, html string) {
	t.Helper()
	routes["/connect/qrconnect"] = func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("微信创建请求方法 = %s", r.Method)
		}
		checkQQLoginValues(t, r.URL.Query(), url.Values{
			"appid":         {"wx48db31d50e334801"},
			"redirect_uri":  {testWeChatRedirectURI},
			"response_type": {"code"},
			"scope":         {"snsapi_login"},
			"href":          {"https://y.qq.com/mediastyle/music_v17/src/css/popup_wechat.css#wechat_redirect"},
		})
		if r.URL.Query().Get("state") == "" {
			t.Error("微信创建请求缺少 state")
		}
		body(html)(w, r)
	}
}

func addWeChatQRPollRoute(t *testing.T, routes map[string]http.HandlerFunc, response string) {
	t.Helper()
	routes["/connect/l/qrconnect"] = func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("微信轮询请求方法 = %s", r.Method)
		}
		checkQQLoginValues(t, r.URL.Query(), url.Values{"uuid": {testWeChatUUID}})
		if len(r.URL.Query()) != 2 {
			t.Errorf("微信轮询参数 = %v，期望只有 uuid 和时间戳", r.URL.Query())
		}
		milliseconds, err := strconv.ParseInt(r.URL.Query().Get("_"), 10, 64)
		if err != nil || milliseconds <= 0 {
			t.Errorf("微信轮询时间戳 = %q", r.URL.Query().Get("_"))
		}
		body(response)(w, r)
	}
}

func addWeChatQRMusicRoute(t *testing.T, routes map[string]http.HandlerFunc, response string) {
	t.Helper()
	routes["/cgi-bin/musicu.fcg"] = func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("微信音乐登录请求方法 = %s", r.Method)
		}
		if got := r.Header.Get("Content-Type"); got != "application/x-www-form-urlencoded" {
			t.Errorf("微信音乐登录 Content-Type = %q", got)
		}
		if got := r.Header.Get("Referer"); got != testWeChatRedirectURI {
			t.Errorf("微信音乐登录 Referer = %q", got)
		}
		if got := r.Header.Get("Origin"); got != "https://y.qq.com" {
			t.Errorf("微信音乐登录 Origin = %q", got)
		}
		if got := r.Header.Get("Cookie"); got != "login_type=2" {
			t.Errorf("微信音乐登录 Cookie = %q", got)
		}
		var payload struct {
			Comm struct {
				TMEAppID     string `json:"tmeAppID"`
				TMELoginType string `json:"tmeLoginType"`
				GTK          int    `json:"g_tk"`
				Platform     string `json:"platform"`
				CT           int    `json:"ct"`
				CV           int    `json:"cv"`
			} `json:"comm"`
			Req struct {
				Module string `json:"module"`
				Method string `json:"method"`
				Param  struct {
					AppID string `json:"strAppid"`
					Code  string `json:"code"`
				} `json:"param"`
			} `json:"req"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if payload.Comm.TMEAppID != "qqmusic" || payload.Comm.TMELoginType != "1" ||
			payload.Comm.GTK != 5381 || payload.Comm.Platform != "yqq" || payload.Comm.CT != 24 || payload.Comm.CV != 0 ||
			payload.Req.Module != "music.login.LoginServer" || payload.Req.Method != "Login" ||
			payload.Req.Param.AppID != "wx48db31d50e334801" || payload.Req.Param.Code != testWeChatCode {
			t.Errorf("微信音乐登录请求体 = %#v", payload)
		}
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "keep", Domain: ".qq.com", Path: "/"})
		http.SetCookie(w, &http.Cookie{Name: "empty", Value: "", Domain: ".qq.com", Path: "/"})
		body(response)(w, r)
	}
}

func createTestWeChatQRSession(t *testing.T, newClient func() *Client) port.QRLoginSession {
	t.Helper()
	session, err := newClient().CreateQRLogin(t.Context(), port.QQLoginWeChat)
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func TestCreateWeChatQRLogin(t *testing.T) {
	for _, tt := range []struct {
		name string
		html string
	}{
		{"轮询地址", `<script>window.location.href='/connect/l/qrconnect?uuid=` + testWeChatUUID + `&amp;foo=bar'</script>`},
		{"QRLogin 字段", `<script>window.QRLogin.uuid = "` + testWeChatUUID + `";</script>`},
		{"二维码地址", `<img src="/connect/qrcode/` + testWeChatUUID + `">`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			saved := "未保存"
			routes := map[string]http.HandlerFunc{}
			addWeChatQRCreateRoute(t, routes, tt.html)
			newClient, obs := serveQQQRLogin(t, routes, func(_ context.Context, value string) error {
				saved = value
				return nil
			})

			before := time.Now().Add(5 * time.Minute)
			session, err := newClient().CreateQRLogin(t.Context(), port.QQLoginWeChat)
			after := time.Now().Add(5 * time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			if want := "https://open.weixin.qq.com/connect/qrcode/" + testWeChatUUID; session.QRImage != want {
				t.Errorf("微信二维码图片 = %q，期望 %q", session.QRImage, want)
			}
			if session.ExpiresAt.Before(before) || session.ExpiresAt.After(after) {
				t.Errorf("微信登录到期时间 = %v，期望约为 5 分钟后", session.ExpiresAt)
			}
			loginURL, err := url.Parse(session.LoginURL)
			if err != nil {
				t.Fatal(err)
			}
			if loginURL.Scheme != "https" || loginURL.Host != "open.weixin.qq.com" || loginURL.Path != "/connect/qrconnect" {
				t.Errorf("微信扫码页面 URL = %q", session.LoginURL)
			}
			decoded, err := base64.RawURLEncoding.DecodeString(session.Key)
			if err != nil {
				t.Fatalf("解码微信登录 key：%v", err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(decoded, &fields); err != nil {
				t.Fatal(err)
			}
			if len(fields) != 2 || fields["uuid"] == nil || fields["state"] == nil {
				t.Errorf("微信登录 key 的字段 = %v", fields)
			}
			var state struct {
				UUID  string `json:"uuid"`
				State string `json:"state"`
			}
			if err := json.Unmarshal(decoded, &state); err != nil {
				t.Fatal(err)
			}
			if state.UUID != testWeChatUUID || state.State == "" || state.State != loginURL.Query().Get("state") {
				t.Errorf("微信登录状态 = %#v，页面 state = %q", state, loginURL.Query().Get("state"))
			}
			if saved != "未保存" {
				t.Errorf("创建微信登录保存了 Cookie %q", saved)
			}
			obs.check(t, "qq login_create")
		})
	}
}

func TestWeChatQRLoginStatusesAcrossClients(t *testing.T) {
	for _, tt := range []struct {
		name     string
		response string
		want     port.QRLoginResult
	}{
		{"过期", `window.wx_errcode=402;`, port.QRLoginResult{Status: port.QRLoginExpired}},
		{"已扫码", `window.wx_errcode=404;`, port.QRLoginResult{Status: port.QRLoginScanned}},
		{"等待扫码", `window.wx_errcode=408;`, port.QRLoginResult{Status: port.QRLoginWaiting}},
		{"其他状态", `window.wx_errcode=500;`, port.QRLoginResult{Status: port.QRLoginFailed}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			saved := "未保存"
			routes := map[string]http.HandlerFunc{}
			addWeChatQRCreateRoute(t, routes, `window.QRLogin.uuid = "`+testWeChatUUID+`";`)
			addWeChatQRPollRoute(t, routes, tt.response)
			newClient, obs := serveQQQRLogin(t, routes, func(_ context.Context, value string) error {
				saved = value
				return nil
			})
			session := createTestWeChatQRSession(t, newClient)
			got, err := newClient().CheckQRLogin(t.Context(), port.QQLoginWeChat, session.Key)
			if err != nil || got != tt.want {
				t.Errorf("跨 Client 微信登录结果 = %#v, %v，期望 %#v", got, err, tt.want)
			}
			if saved != "未保存" {
				t.Errorf("微信非成功状态保存了 Cookie %q", saved)
			}
			obs.check(t, "qq login_create", "qq login_check")
		})
	}
}

func TestWeChatQRLoginCompletesMusicLoginAndSavesSortedCookies(t *testing.T) {
	var saved string
	routes := map[string]http.HandlerFunc{}
	addWeChatQRCreateRoute(t, routes, `window.QRLogin.uuid = "`+testWeChatUUID+`";`)
	addWeChatQRPollRoute(t, routes, `window.wx_errcode=405;window.wx_code='`+testWeChatCode+`';`)
	addWeChatQRMusicRoute(t, routes, `{"code":0,"req":{"code":0,"data":{"musickey":"test","musicid":"wx-user","refresh_key":"refresh-key","refresh_token":"refresh-token","openid":"openid","unionid":"unionid","wxaccess_token":"access-token","json_extra":"keep","empty":""}}}`)
	newClient, obs := serveQQQRLogin(t, routes, func(_ context.Context, value string) error {
		saved = value
		return nil
	})
	session := createTestWeChatQRSession(t, newClient)
	client := newClient()
	result, err := client.CheckQRLogin(t.Context(), port.QQLoginWeChat, session.Key)
	want := port.QRLoginResult{Status: port.QRLoginSuccess}
	if err != nil || result != want {
		t.Fatalf("微信登录 = %#v, %v", result, err)
	}
	requireQQCookies(t, saved, map[string]string{
		"qm_keyst": testMusicKey, "qqmusic_key": testMusicKey, "musickey": testMusicKey, "uin": "wx-user",
		"refresh_key": "refresh-key", "refresh_token": "refresh-token", "openid": "openid", "unionid": "unionid",
		"wxaccess_token": "access-token", "session": "keep",
	})
	if strings.Contains(saved, "json_extra=") || strings.Contains(saved, "empty=") || strings.Contains(saved, "login_type=") {
		t.Errorf("保存了未知、空值或临时微信 Cookie：%q", saved)
	}
	if cached, err := client.cookie.get(t.Context()); err != nil || cached != saved {
		t.Errorf("本地缓存的 Cookie = %q, %v，期望 %q", cached, err, saved)
	}
	obs.check(t, "qq login_create", "qq login_check")
}

func TestWeChatQRLoginMissingAuthorizationCodeFailsWithoutSaving(t *testing.T) {
	saved := "未保存"
	routes := map[string]http.HandlerFunc{}
	addWeChatQRCreateRoute(t, routes, `window.QRLogin.uuid = "`+testWeChatUUID+`";`)
	addWeChatQRPollRoute(t, routes, `window.wx_errcode=405;`)
	newClient, obs := serveQQQRLogin(t, routes, func(_ context.Context, value string) error {
		saved = value
		return nil
	})
	session := createTestWeChatQRSession(t, newClient)
	result, err := newClient().CheckQRLogin(t.Context(), port.QQLoginWeChat, session.Key)
	if err == nil || result.Status == port.QRLoginSuccess {
		t.Errorf("缺少微信授权 code 的登录结果 = %#v, %v，期望 Go 错误且不返回成功", result, err)
	}
	if saved != "未保存" {
		t.Errorf("缺少微信授权 code 时保存了 %q", saved)
	}
	obs.check(t, "qq login_create", "qq login_check 失败")
}

func TestWeChatQRLoginMissingMusicKeyFailsWithoutSaving(t *testing.T) {
	saved := "未保存"
	routes := map[string]http.HandlerFunc{}
	addWeChatQRCreateRoute(t, routes, `window.QRLogin.uuid = "`+testWeChatUUID+`";`)
	addWeChatQRPollRoute(t, routes, `window.wx_errcode=405;window.wx_code='`+testWeChatCode+`';`)
	addWeChatQRMusicRoute(t, routes, `{"code":0,"req":{"code":0,"data":{"musicid":"wx-user","openid":"openid"}}}`)
	newClient, obs := serveQQQRLogin(t, routes, func(_ context.Context, value string) error {
		saved = value
		return nil
	})
	session := createTestWeChatQRSession(t, newClient)
	result, err := newClient().CheckQRLogin(t.Context(), port.QQLoginWeChat, session.Key)
	if err != nil || result.Status != port.QRLoginFailed {
		t.Errorf("微信缺少音乐 key 的登录结果 = %#v, %v", result, err)
	}
	if saved != "未保存" {
		t.Errorf("微信缺少音乐 key 时保存了 %q", saved)
	}
	obs.check(t, "qq login_create", "qq login_check")
}

func TestWeChatQRLoginRejectsInvalidMusicResponseCodes(t *testing.T) {
	for _, tt := range []struct {
		name     string
		response string
	}{
		{"顶层 code 缺失", `{"req":{"code":0,"data":{"musickey":"test","uin":"wx-user"}}}`},
		{"顶层 code 为 null", `{"code":null,"req":{"code":0,"data":{"musickey":"test","uin":"wx-user"}}}`},
		{"req.code 缺失", `{"code":0,"req":{"data":{"musickey":"test","uin":"wx-user"}}}`},
		{"req.code 为 null", `{"code":0,"req":{"code":null,"data":{"musickey":"test","uin":"wx-user"}}}`},
		{"顶层 code 非零", `{"code":1,"req":{"code":0,"data":{"musickey":"test","uin":"wx-user"}}}`},
		{"req.code 非零", `{"code":0,"req":{"code":1,"data":{"musickey":"test","uin":"wx-user"}}}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			saved := "未保存"
			routes := map[string]http.HandlerFunc{}
			addWeChatQRCreateRoute(t, routes, `window.QRLogin.uuid = "`+testWeChatUUID+`";`)
			addWeChatQRPollRoute(t, routes, `window.wx_errcode=405;window.wx_code='`+testWeChatCode+`';`)
			addWeChatQRMusicRoute(t, routes, tt.response)
			newClient, obs := serveQQQRLogin(t, routes, func(_ context.Context, value string) error {
				saved = value
				return nil
			})
			session := createTestWeChatQRSession(t, newClient)
			result, err := newClient().CheckQRLogin(t.Context(), port.QQLoginWeChat, session.Key)
			if err == nil || result.Status == port.QRLoginSuccess {
				t.Errorf("微信无效音乐响应码的登录结果 = %#v, %v，期望错误", result, err)
			}
			if saved != "未保存" {
				t.Errorf("微信无效音乐响应码时保存了 %q", saved)
			}
			obs.check(t, "qq login_create", "qq login_check 失败")
		})
	}
}

func TestWeChatQRLoginSaveFailureIsObserved(t *testing.T) {
	saveErr := errors.New("保存测试 Cookie 失败")
	routes := map[string]http.HandlerFunc{}
	addWeChatQRCreateRoute(t, routes, `window.QRLogin.uuid = "`+testWeChatUUID+`";`)
	addWeChatQRPollRoute(t, routes, `window.wx_errcode=405;window.wx_code='`+testWeChatCode+`';`)
	addWeChatQRMusicRoute(t, routes, `{"code":0,"req":{"code":0,"data":{"qm_keyst":"test","uin":"wx-user"}}}`)
	newClient, obs := serveQQQRLogin(t, routes, func(context.Context, string) error { return saveErr })
	session := createTestWeChatQRSession(t, newClient)
	client := newClient()
	if _, err := client.cookie.get(t.Context()); err != nil {
		t.Fatal(err)
	}
	result, err := client.CheckQRLogin(t.Context(), port.QQLoginWeChat, session.Key)
	if !errors.Is(err, saveErr) || result.Status == port.QRLoginSuccess {
		t.Errorf("微信保存失败的登录结果 = %#v, %v", result, err)
	}
	if cached, err := client.cookie.get(t.Context()); err != nil || cached != testCookie {
		t.Errorf("微信保存失败后的本地 Cookie = %q, %v", cached, err)
	}
	obs.check(t, "qq login_create", "qq login_check 失败")
}

func TestQQLoginRedirectRejectsUnsafeTargetsBeforeRequest(t *testing.T) {
	for _, target := range []string{
		"http://xui.ptlogin2.qq.com/start",
		"https://example.com/start",
		"https://notqq.com/start",
		"https://qq.com.example.com/start",
	} {
		t.Run(target, func(t *testing.T) {
			var hits atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				checkClientIP(t, r)
			}))
			t.Cleanup(server.Close)
			serverURL, err := url.Parse(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			client := &http.Client{Transport: redirect{serverURL}}
			finalURL, err := followQQLogin(t.Context(), client, target)
			if err == nil {
				t.Errorf("不安全跳转 %q 的最终 URL = %q，期望错误", target, finalURL)
			} else if strings.Contains(err.Error(), target) {
				t.Errorf("跳转错误泄露了完整登录地址：%v", err)
			}
			if hits.Load() != 0 {
				t.Errorf("不安全目标收到 %d 个请求", hits.Load())
			}
		})
	}
}

type failingQQLoginTransport struct{}

func (failingQQLoginTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("测试网络失败")
}

func TestQQLoginNetworkErrorHidesLoginQuery(t *testing.T) {
	client := &http.Client{Transport: failingQQLoginTransport{}}
	target := "https://xui.ptlogin2.qq.com/ssl/ptqrlogin?login_sig=fictitious-secret"
	if _, err := followQQLogin(t.Context(), client, target); err == nil {
		t.Fatal("网络失败的跳转返回了 nil 错误")
	} else if strings.Contains(err.Error(), "fictitious-secret") {
		t.Errorf("网络错误泄露了登录参数：%v", err)
	}
}

func checkMalformedQQLoginError(t *testing.T, err error, location, credential string) {
	t.Helper()
	if err == nil {
		t.Fatal("malformed Location 返回了 nil 错误")
	}
	if strings.Contains(err.Error(), credential) || strings.Contains(err.Error(), location) {
		t.Errorf("malformed Location 错误泄露了敏感地址：%v", err)
	}
}

func TestQQLoginMalformedLocationErrorIsSanitized(t *testing.T) {
	const credential = "fictitious-code-token"
	location := "https://y.qq.com/callback/" + credential + "%zz"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		checkClientIP(t, r)
		w.Header().Set("Location", location)
		w.WriteHeader(http.StatusFound)
	}))
	t.Cleanup(server.Close)
	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: redirect{serverURL}}
	_, err = followQQLogin(t.Context(), client, "https://xui.ptlogin2.qq.com/start")
	checkMalformedQQLoginError(t, err, location, credential)
}

func TestQQQOAuthMalformedLocationErrorIsSanitized(t *testing.T) {
	const credential = "fictitious-oauth-token"
	location := "https://y.qq.com/callback/" + credential + "%zz"
	saved := "未保存"
	routes := map[string]http.HandlerFunc{}
	addQQQRCreateRoutes(t, routes)
	addQQQOAuthRoutes(t, routes, `{"code":0,"req":{"code":0,"data":{"musickey":"test","uin":"o1"}}}`)
	routes["/oauth2.0/authorize"] = func(w http.ResponseWriter, r *http.Request) {
		checkClientIP(t, r)
		w.Header().Set("Location", location)
		w.WriteHeader(http.StatusFound)
	}
	newClient, obs := serveQQQRLogin(t, routes, func(_ context.Context, value string) error {
		saved = value
		return nil
	})
	session := createTestQQQRSession(t, newClient)
	result, err := newClient().CheckQRLogin(t.Context(), port.QQLoginQQ, session.Key)
	checkMalformedQQLoginError(t, err, location, credential)
	if result.Status == port.QRLoginSuccess || saved != "未保存" {
		t.Errorf("malformed OAuth Location 的结果 = %#v，保存的 Cookie = %q", result, saved)
	}
	obs.check(t, "qq login_create", "qq login_check 失败")
}

func TestQQLoginRedirectRejectsUnsafeLocation(t *testing.T) {
	var targetHits atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		checkClientIP(t, r)
		switch r.URL.Path {
		case "/start":
			w.Header().Set("Location", "http://xui.ptlogin2.qq.com/target")
			w.WriteHeader(http.StatusFound)
		case "/target":
			targetHits.Add(1)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: redirect{serverURL}}
	if finalURL, err := followQQLogin(t.Context(), client, "https://xui.ptlogin2.qq.com/start"); err == nil {
		t.Errorf("不安全 Location 的最终 URL = %q，期望错误", finalURL)
	}
	if targetHits.Load() != 0 {
		t.Errorf("不安全 Location 目标收到 %d 个请求", targetHits.Load())
	}
}

func TestQQLoginRedirectLimit(t *testing.T) {
	var hits atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		checkClientIP(t, r)
		hits.Add(1)
		w.Header().Set("Location", "https://xui.ptlogin2.qq.com/loop")
		w.WriteHeader(http.StatusFound)
	}))
	t.Cleanup(server.Close)
	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: redirect{serverURL}}
	if finalURL, err := followQQLogin(t.Context(), client, "https://xui.ptlogin2.qq.com/loop"); err == nil {
		t.Errorf("循环跳转的最终 URL = %q，期望错误", finalURL)
	}
	if want := int64(11); hits.Load() != want {
		t.Errorf("10 次跳转限制的请求数 = %d，期望 %d", hits.Load(), want)
	}
}

type weChatEndpointTransport struct {
	calls []string
}

func (t *weChatEndpointTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.calls = append(t.calls, req.URL.Host)
	resp := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: http.NoBody, Request: req}
	switch req.URL.Host {
	case "u.y.qq.com":
		resp.StatusCode = http.StatusBadGateway
	case "szu.y.qq.com":
		resp.Body = io.NopCloser(strings.NewReader(`{"code":0,"req":{"code":0,"data":{"musickey":"test","musicid":"wx-user"}}}`))
	default:
		resp.StatusCode = http.StatusBadGateway
	}
	return resp, nil
}

func TestWeChatMusicLoginFallsBackAcrossEndpoints(t *testing.T) {
	transport := &weChatEndpointTransport{}
	c := New(
		&http.Client{Transport: transport},
		func(context.Context) (string, error) { return "", nil },
		func(context.Context, string) error { return nil },
		func(string, string, time.Duration, error) {},
	)
	cookie, err := c.completeWeChatLogin(t.Context(), c.api, testWeChatCode)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cookie, "musickey=test") || !strings.Contains(cookie, "uin=wx-user") {
		t.Fatalf("备用域名保存的 Cookie = %q", cookie)
	}
	if want := []string{"u.y.qq.com", "szu.y.qq.com"}; !slices.Equal(transport.calls, want) {
		t.Fatalf("音乐登录调用的域名 = %v，期望 %v", transport.calls, want)
	}
}
