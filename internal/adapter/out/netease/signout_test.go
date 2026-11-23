package netease

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/dihemoe/alisten/internal/app/port"
)

// 播放接口对失效凭据不直接报登录失效，拒绝时用账号接口复核。
func TestPlayURLSignedOut(t *testing.T) {
	routes := resolveRoutes(t)
	routes["/eapi/song/enhance/player/url/v1"] = fixture(t, "play-url-missing.json")
	routes["/weapi/song/enhance/player/url"] = fixture(t, "play-url-missing.json")
	routes["/weapi/nuser/account/get"] = body(`{"code":200,"profile":{}}`)
	c, _ := serve(t, routes)
	if _, err := c.PlayURL(t.Context(), "3387653989"); !errors.Is(err, port.ErrSourceSignedOut) {
		t.Errorf("登录失效时的错误 = %v，期望 ErrSourceSignedOut", err)
	}
}

// 没有保存凭据时不复核登录，按音源拒绝处理而不是登录失效。
func TestPlayURLRejectsWithoutCookie(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/weapi/nuser/account/get" {
			t.Error("没有凭据时不应查询账号")
		}
		if r.URL.Path == "/eapi/song/enhance/player/url/v1" || r.URL.Path == "/weapi/song/enhance/player/url" {
			body(`{"data":[{"id":186016,"url":null,"br":0,"size":0,"md5":null,"code":404,"expi":1200,"type":null,"level":null,"encodeType":null,"freeTrialInfo":null,"fee":0}],"code":200}`)(w, r)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	c := New(&http.Client{Transport: redirect{target}}, func(context.Context) (string, error) { return "", nil }, nil, (&observed{}).record, testLogger)
	var rejected *port.RejectedError
	if _, err := c.PlayURL(t.Context(), "3387653989"); !errors.As(err, &rejected) {
		t.Errorf("没有凭据时的错误 = %v，期望音源拒绝", err)
	}
}
