package qq

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/dihemoe/alisten/internal/app/port"
)

// 播放地址拿不到时用严格登录态接口复核：登录失效返回 ErrSourceSignedOut，登录有效才按音源拒绝处理。
func TestPlayURLSignedOut(t *testing.T) {
	for _, tc := range []struct {
		name      string
		account   string
		signedOut bool
	}{
		{"登录失效", `{"code":0,"req":{"code":10004,"data":{}}}`, true},
		{"登录有效但需要会员或没有版权", accountValidResponse, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := serve(t, map[string]http.HandlerFunc{
				"/cgi-bin/musicu.fcg": func(w http.ResponseWriter, r *http.Request) {
					if strings.Contains(r.URL.Query().Get("data"), "DissInfo") {
						body(tc.account)(w, r)
						return
					}
					body(`{"code":0,"req_1":{"code":0,"data":{"midurlinfo":[]}}}`)(w, r)
				},
			})
			_, err := c.PlayURL(t.Context(), "id")
			if tc.signedOut {
				if !errors.Is(err, port.ErrSourceSignedOut) {
					t.Errorf("错误 = %v，期望 ErrSourceSignedOut", err)
				}
				return
			}
			var rejected *port.RejectedError
			if !errors.As(err, &rejected) {
				t.Errorf("错误 = %v，期望音源拒绝", err)
			}
		})
	}
}

// 没有保存凭据时不复核登录：不是登录失效，按音源拒绝处理。
func TestPlayURLRejectsWithoutCookie(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Query().Get("data"), "DissInfo") {
			t.Error("没有凭据时不应查询登录状态")
		}
		body(`{"code":0,"req_1":{"code":0,"data":{"midurlinfo":[]}}}`)(w, r)
	}))
	t.Cleanup(server.Close)
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	c := New(&http.Client{Transport: redirect{target}}, func(context.Context) (string, error) { return "", nil }, nil, (&observed{}).record, testLogger)
	var rejected *port.RejectedError
	if _, err := c.PlayURL(t.Context(), "id"); !errors.As(err, &rejected) {
		t.Errorf("没有凭据时的错误 = %v，期望音源拒绝", err)
	}
}
