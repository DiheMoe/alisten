package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dihemoe/alisten/internal/app/admin"
	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/app/port/porttest"
	"github.com/dihemoe/alisten/protocol"
)

var adminHeader = []string{"Authorization", "Bearer admin-secret"}

func startQRLogin(t *testing.T, netease port.NetEaseQRLoginSource, qq port.QQQRLoginSource) (*httptest.Server, *bytes.Buffer) {
	t.Helper()
	store := porttest.New()
	s := services(store, store, "admin-secret")
	s.Admin = admin.New("admin-secret", s.Lobby, store, store, store, netease, qq, porttest.NewBilibiliQRLogin(), porttest.NewYouTubeCookies(), s.Gateway)
	return start(t, s)
}

func qrLoginOf(t *testing.T, got response) protocol.QRLogin {
	t.Helper()
	var login protocol.QRLogin
	if err := json.Unmarshal([]byte(got.body), &login); err != nil || got.status != http.StatusCreated {
		t.Fatalf("生成二维码 = %#v", got)
	}
	return login
}

type noQRCode struct{ *porttest.NetEaseQRLogin }

func (noQRCode) CreateQRLogin(context.Context) (port.QRLoginSession, error) {
	return port.QRLoginSession{Key: "netease-1", ExpiresAt: time.Now().Add(time.Minute)}, nil
}

func TestQRLoginImage(t *testing.T) {
	server, _ := startQRLogin(t, porttest.NewNetEaseQRLogin(), porttest.NewQQQRLogin())
	before := time.Now()

	netease := qrLoginOf(t, send(t, server, http.MethodPost, "/api/admin/credentials/netease/qr-logins", "{}", adminHeader...))
	data, ok := strings.CutPrefix(netease.QrImage, "data:image/png;base64,")
	if !ok {
		t.Fatalf("网易云的二维码 = %q，期望由登录链接生成的 PNG", netease.QrImage)
	}
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		t.Fatal(err)
	}
	if img, err := png.Decode(bytes.NewReader(raw)); err != nil || img.Bounds().Dx() == 0 {
		t.Errorf("生成的二维码不是能解码的 PNG：%v", err)
	}
	if netease.Key != "netease-1" || netease.ExpiresAtMs <= before.UnixMilli() {
		t.Errorf("网易云的登录会话 = key %q、过期于 %d", netease.Key, netease.ExpiresAtMs)
	}

	empty, _ := startQRLogin(t, noQRCode{porttest.NewNetEaseQRLogin()}, porttest.NewQQQRLogin())
	if got, want := send(t, empty, http.MethodPost, "/api/admin/credentials/netease/qr-logins", "{}", adminHeader...), failure(protocol.ErrorCodeProviderError, "音乐平台没有正常响应，请稍后再试。"); got != want {
		t.Errorf("音源没有给出二维码图片和登录链接时 = %#v，期望 %#v", got, want)
	}
}

func TestQRLoginKeyIsNotLogged(t *testing.T) {
	netease, qq := porttest.NewNetEaseQRLogin(), porttest.NewQQQRLogin()
	server, logs := startQRLogin(t, netease, qq)
	key := qrLoginOf(t, send(t, server, http.MethodPost, "/api/admin/credentials/netease/qr-logins", "{}", adminHeader...)).Key
	wx := qrLoginOf(t, send(t, server, http.MethodPost, "/api/admin/credentials/qq/qr-logins", `{"type":"wx"}`, adminHeader...)).Key

	netease.SetResult(key, port.QRLoginResult{Status: port.QRLoginFailed, Message: "二维码已失效"})
	if got := send(t, server, http.MethodPost, "/api/admin/credentials/netease/qr-logins/status", `{"key":"`+key+`"}`, adminHeader...); got != (response{status: http.StatusOK, body: `{"message":"二维码已失效","status":"failed"}`}) {
		t.Errorf("网易云的扫码结果 = %#v", got)
	}
	qq.SetResult(wx, port.QRLoginResult{Status: port.QRLoginSuccess})
	if got := send(t, server, http.MethodPost, "/api/admin/credentials/qq/qr-logins/status", `{"type":"wx","key":"`+wx+`"}`, adminHeader...); got != (response{status: http.StatusOK, body: `{"message":"","status":"success"}`}) {
		t.Errorf("微信扫码的结果 = %#v", got)
	}

	netease.Fail(errors.New("登录会话 " + key + " 已失效"))
	qq.Fail(errors.New("登录会话 " + wx + " 已失效"))
	for path, body := range map[string]string{
		"/api/admin/credentials/netease/qr-logins/status": `{"key":"` + key + `"}`,
		"/api/admin/credentials/qq/qr-logins/status":      `{"type":"wx","key":"` + wx + `"}`,
	} {
		if got, want := send(t, server, http.MethodPost, path, body, adminHeader...), failure(protocol.ErrorCodeProviderError, "音乐平台没有正常响应，请稍后再试。"); got != want {
			t.Errorf("音源出错时 %s = %#v，期望 %#v", path, got, want)
		}
	}
	if strings.Contains(logs.String(), key) || strings.Contains(logs.String(), wx) {
		t.Errorf("日志中出现了扫码登录的 key：\n%s", logs)
	}
}

func TestCredentialStatus(t *testing.T) {
	store := porttest.New()
	netease, qq := porttest.NewNetEaseQRLogin(), porttest.NewQQQRLogin()
	s := services(store, store, "admin-secret")
	s.Admin = admin.New("admin-secret", s.Lobby, store, store, store, netease, qq, porttest.NewBilibiliQRLogin(), porttest.NewYouTubeCookies(), s.Gateway)
	server, _ := start(t, s)
	for _, path := range []string{"/api/admin/credentials/netease", "/api/admin/credentials/qq"} {
		want := response{status: http.StatusOK, body: `{"account":null,"set":false}`}
		if got := send(t, server, http.MethodGet, path, "", adminHeader...); got != want {
			t.Errorf("没有凭据时 %s = %#v，期望 %#v", path, got, want)
		}
	}

	if err := store.SetNeteaseCookie(t.Context(), "netease-cookie-secret"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetQQCookie(t.Context(), "qq-cookie-secret"); err != nil {
		t.Fatal(err)
	}
	name, vip := "测试账号", true
	netease.SetAccount(&port.SourceAccount{ID: "123", Name: &name, VIP: &vip, Verified: true})
	qq.SetAccount(&port.SourceAccount{ID: "456"})
	for path, want := range map[string]string{
		"/api/admin/credentials/netease": `{"account":{"id":"123","name":"测试账号","verified":true,"vip":true},"set":true}`,
		"/api/admin/credentials/qq":      `{"account":{"id":"456","name":null,"verified":false,"vip":null},"set":true}`,
	} {
		got := send(t, server, http.MethodGet, path, "", adminHeader...)
		if got.status != http.StatusOK || got.body != want {
			t.Errorf("有账号资料时 %s = %#v，期望 %s", path, got, want)
		}
		if strings.Contains(got.body, "cookie-secret") {
			t.Errorf("%s 的响应泄漏了 Cookie：%s", path, got.body)
		}
	}

	netease.FailAccount(errors.New("上游失败"))
	if got, want := send(t, server, http.MethodGet, "/api/admin/credentials/netease", "", adminHeader...), failure(protocol.ErrorCodeProviderError, "音乐平台没有正常响应，请稍后再试。"); got != want {
		t.Errorf("账号查询失败 = %#v，期望 %#v", got, want)
	}
}

func startAdmin(t *testing.T, bilibili port.BilibiliQRLoginSource, youtube port.YouTubeCookies) (*httptest.Server, *bytes.Buffer) {
	t.Helper()
	store := porttest.New()
	s := services(store, store, "admin-secret")
	s.Admin = admin.New("admin-secret", s.Lobby, store, store, store, porttest.NewNetEaseQRLogin(), porttest.NewQQQRLogin(), bilibili, youtube, s.Gateway)
	return start(t, s)
}

func TestBilibiliQRLogin(t *testing.T) {
	bilibili := porttest.NewBilibiliQRLogin()
	server, logs := startAdmin(t, bilibili, porttest.NewYouTubeCookies())
	login := qrLoginOf(t, send(t, server, http.MethodPost, "/api/admin/credentials/bilibili/qr-logins", "{}", adminHeader...))
	if !strings.HasPrefix(login.QrImage, "data:image/png;base64,") || login.Key != "bilibili-1" {
		t.Errorf("B 站的二维码 = %+v，期望由登录链接生成的 PNG", login)
	}
	bilibili.SetResult(login.Key, port.QRLoginResult{Status: port.QRLoginSuccess})
	check := `{"key":"` + login.Key + `"}`
	if got := send(t, server, http.MethodPost, "/api/admin/credentials/bilibili/qr-logins/status", check, adminHeader...); got != (response{status: http.StatusOK, body: `{"message":"","status":"success"}`}) {
		t.Errorf("B 站的扫码结果 = %#v", got)
	}
	bilibili.Fail(errors.New("登录会话 " + login.Key + " 已失效"))
	if got, want := send(t, server, http.MethodPost, "/api/admin/credentials/bilibili/qr-logins/status", check, adminHeader...), failure(protocol.ErrorCodeProviderError, "音乐平台没有正常响应，请稍后再试。"); got != want {
		t.Errorf("音源出错时 = %#v，期望 %#v", got, want)
	}
	if strings.Contains(logs.String(), login.Key) {
		t.Errorf("日志中出现了扫码登录的 key：\n%s", logs)
	}
}

func TestBilibiliAndYouTubeCredentialStatus(t *testing.T) {
	bilibili, youtube := porttest.NewBilibiliQRLogin(), porttest.NewYouTubeCookies()
	server, _ := startAdmin(t, bilibili, youtube)
	for path, want := range map[string]string{
		"/api/admin/credentials/bilibili": `{"account":null,"set":false}`,
		"/api/admin/credentials/youtube":  `{"set":false,"updatedAtMs":0}`,
	} {
		if got := send(t, server, http.MethodGet, path, "", adminHeader...); got != (response{status: http.StatusOK, body: want}) {
			t.Errorf("没有凭据时 %s = %#v，期望 %s", path, got, want)
		}
	}
	name := "测试账号"
	bilibili.SetCredential(&port.SourceAccount{ID: "789", Name: &name, Verified: true})
	youtube.Set(time.UnixMilli(1760000000123))
	for path, want := range map[string]string{
		"/api/admin/credentials/bilibili": `{"account":{"id":"789","name":"测试账号","verified":true,"vip":null},"set":true}`,
		"/api/admin/credentials/youtube":  `{"set":true,"updatedAtMs":1760000000123}`,
	} {
		if got := send(t, server, http.MethodGet, path, "", adminHeader...); got != (response{status: http.StatusOK, body: want}) {
			t.Errorf("有凭据时 %s = %#v，期望 %s", path, got, want)
		}
	}
	bilibili.FailStatus(errors.New("上游失败"))
	if got, want := send(t, server, http.MethodGet, "/api/admin/credentials/bilibili", "", adminHeader...), failure(protocol.ErrorCodeProviderError, "音乐平台没有正常响应，请稍后再试。"); got != want {
		t.Errorf("查询 B 站的登录状态失败 = %#v，期望 %#v", got, want)
	}
	youtube.Fail(errors.New("数据库不可用"))
	if got, want := send(t, server, http.MethodGet, "/api/admin/credentials/youtube", "", adminHeader...), failure(protocol.ErrorCodeInternal, "服务器出错了，请稍后再试。"); got != want {
		t.Errorf("读取 YouTube cookies 的状态失败 = %#v，期望 %#v", got, want)
	}
}
