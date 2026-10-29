package qq

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
)

func checkPlayURLRequest(t *testing.T, r *http.Request) {
	t.Helper()
	if got := r.Header.Get("Cookie"); got != testCookie {
		t.Errorf("QQ 播放地址接口的 Cookie = %q，期望虚构 QQ Cookie", got)
	}
	var payload struct {
		Req1 struct {
			Module string `json:"module"`
			Method string `json:"method"`
			Param  struct {
				Filenames []string `json:"filename"`
				SongMIDs  []string `json:"songmid"`
			} `json:"param"`
		} `json:"req_1"`
	}
	if err := json.Unmarshal([]byte(r.URL.Query().Get("data")), &payload); err != nil {
		t.Errorf("解析播放地址请求：%v", err)
		return
	}
	if payload.Req1.Module != "music.vkey.GetVkey" || payload.Req1.Method != "UrlGetVkey" {
		t.Errorf("播放地址请求的方法和模块 = %q %q", payload.Req1.Method, payload.Req1.Module)
	}
	wantFilenames := []string{"F000idid.flac", "M800idid.mp3", "M500idid.mp3"}
	if !slices.Equal(payload.Req1.Param.Filenames, wantFilenames) || !slices.Equal(payload.Req1.Param.SongMIDs, []string{"id"}) {
		t.Errorf("播放地址请求的参数 = %#v", payload.Req1.Param)
	}
}

func TestPlayURLSelectsRequestedQualityOrder(t *testing.T) {
	tests := []struct {
		name     string
		response string
		want     string
	}{
		{
			name:     "FLAC",
			response: `{"code":0,"req_1":{"code":0,"data":{"midurlinfo":[{"filename":"M500idid.mp3","purl":"128"},{"filename":"F000idid.flac","purl":"flac"},{"filename":"M800idid.mp3","purl":"320"}]}}}`,
			want:     "https://ws.stream.qqmusic.qq.com/flac",
		},
		{
			name:     "320k MP3",
			response: `{"code":0,"req_1":{"code":0,"data":{"midurlinfo":[{"filename":"M500idid.mp3","purl":"128"},{"filename":"F000idid.flac","purl":""},{"filename":"M800idid.mp3","purl":"320"}]}}}`,
			want:     "https://ws.stream.qqmusic.qq.com/320",
		},
		{
			name:     "128k MP3",
			response: `{"code":0,"req_1":{"code":0,"data":{"midurlinfo":[{"filename":"M500idid.mp3","purl":"128"},{"filename":"F000idid.flac","purl":""},{"filename":"M800idid.mp3","purl":""}]}}}`,
			want:     "https://ws.stream.qqmusic.qq.com/128",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, obs := serve(t, map[string]http.HandlerFunc{
				"/cgi-bin/musicu.fcg": func(w http.ResponseWriter, r *http.Request) {
					checkPlayURLRequest(t, r)
					body(tt.response)(w, r)
				},
			})
			got, err := c.PlayURL(t.Context(), "id")
			if err != nil || got != tt.want {
				t.Fatalf("播放地址 = %q, %v，期望 %q", got, err, tt.want)
			}
			obs.check(t, "qq play_url")
		})
	}
}

func TestPlayURLRejectsEmptyPURL(t *testing.T) {
	c, _ := serve(t, map[string]http.HandlerFunc{
		"/cgi-bin/musicu.fcg": body(`{"code":0,"req_1":{"code":0,"data":{"midurlinfo":[{"filename":"F000idid.flac","purl":""},{"filename":"M800idid.mp3","purl":""},{"filename":"M500idid.mp3","purl":""},{"filename":"F000otherother.flac","purl":"other"}]}}}`),
	})
	var rejected *port.RejectedError
	if got, err := c.PlayURL(t.Context(), "id"); got != "" || !errors.As(err, &rejected) || err.Error() != "QQ 音乐没有提供这首歌的播放地址" {
		t.Errorf("空播放地址的结果 = %q, %v，期望音源拒绝", got, err)
	}
}

func TestPlayURLUpstreamErrorsAreRetryable(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{name: "HTTP 502", handler: func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) }},
		{name: "响应 JSON 畸形", handler: body(`{"code":0,`)},
		{name: "顶层 code 缺失", handler: body(`{"req_1":{"code":0,"data":{"midurlinfo":[]}}}`)},
		{name: "顶层 code 为 null", handler: body(`{"code":null,"req_1":{"code":0,"data":{"midurlinfo":[]}}}`)},
		{name: "req_1.code 缺失", handler: body(`{"code":0,"req_1":{"data":{"midurlinfo":[]}}}`)},
		{name: "req_1.code 为 null", handler: body(`{"code":0,"req_1":{"code":null,"data":{"midurlinfo":[]}}}`)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := serve(t, map[string]http.HandlerFunc{"/cgi-bin/musicu.fcg": tt.handler})
			var rejected *port.RejectedError
			if _, err := c.PlayURL(t.Context(), "id"); err == nil || errors.As(err, &rejected) {
				t.Errorf("上游错误 = %v，期望可重试且不进入音源拒绝", err)
			}
		})
	}
}

func TestResolveDoesNotFallbackForInvalidPlayURLResponses(t *testing.T) {
	tests := []struct {
		name     string
		response string
	}{
		{name: "响应 JSON 畸形", response: `{"code":0,`},
		{name: "顶层 code 缺失", response: `{"req_1":{"code":0,"data":{"midurlinfo":[]}}}`},
		{name: "顶层 code 为 null", response: `{"code":null,"req_1":{"code":0,"data":{"midurlinfo":[]}}}`},
		{name: "req_1.code 缺失", response: `{"code":0,"req_1":{"data":{"midurlinfo":[]}}}`},
		{name: "req_1.code 为 null", response: `{"code":0,"req_1":{"code":null,"data":{"midurlinfo":[]}}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			routes := resolveRoutes(t)
			routes["/cgi-bin/musicu.fcg"] = body(tt.response)
			c, obs := serve(t, routes)
			got, err := c.Resolve(t.Context(), "004Fs2FP1EvZYc")
			if got.Audio != nil {
				_ = got.Audio.Close()
			}
			var rejected *port.RejectedError
			if err == nil || errors.As(err, &rejected) {
				t.Errorf("无效原生播放响应的 Resolve 错误 = %v，期望普通可重试错误", err)
			}
			obs.check(t, "qq track_detail", "qq lyric", "qq play_url 失败")
		})
	}
}

func TestResolveRejectsEmptyPlayURL(t *testing.T) {
	c, obs := serve(t, resolveRoutes(t))
	_, err := c.Resolve(t.Context(), "004Fs2FP1EvZYc")
	var rejected *port.RejectedError
	if !errors.As(err, &rejected) || err.Error() != "QQ 音乐没有提供这首歌的播放地址" {
		t.Fatalf("QQ 没有播放地址时的错误 = %v，期望 QQ 音源拒绝", err)
	}
	obs.check(t, "qq track_detail", "qq lyric", "qq play_url")
}

func TestPlayURLIsNotCached(t *testing.T) {
	var calls atomic.Int32
	c, obs := serve(t, map[string]http.HandlerFunc{
		"/cgi-bin/musicu.fcg": func(w http.ResponseWriter, r *http.Request) {
			purl := "first"
			if calls.Add(1) == 2 {
				purl = "second"
			}
			body(`{"code":0,"req_1":{"code":0,"data":{"midurlinfo":[{"filename":"M500idid.mp3","purl":"`+purl+`"}]}}}`)(w, r)
		},
	})
	for _, want := range []string{"https://ws.stream.qqmusic.qq.com/first", "https://ws.stream.qqmusic.qq.com/second"} {
		got, err := c.PlayURL(t.Context(), "id")
		if err != nil || got != want {
			t.Fatalf("播放地址 = %q, %v，期望 %q", got, err, want)
		}
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("播放地址接口调用了 %d 次，期望不缓存地址", got)
	}
	obs.check(t, "qq play_url", "qq play_url")
}

func TestPlayURLDoesNotFollowRedirects(t *testing.T) {
	c, obs := serve(t, map[string]http.HandlerFunc{
		"/cgi-bin/musicu.fcg": func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/redirected", http.StatusTemporaryRedirect)
		},
		"/redirected": func(w http.ResponseWriter, r *http.Request) {
			t.Error("带 Cookie 的 QQ 接口自动跟随了重定向")
			body(`{"code":0,"req_1":{"code":0,"data":{"midurlinfo":[]}}}`)(w, r)
		},
	})
	var rejected *port.RejectedError
	if _, err := c.PlayURL(t.Context(), "id"); err == nil || errors.As(err, &rejected) {
		t.Errorf("重定向结果 = %v，期望 HTTP 错误", err)
	}
	obs.check(t, "qq play_url 失败")
}

type failingTransport struct{}

func (failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("网络中断")
}

func TestDownloadErrorDoesNotExposeSignedURL(t *testing.T) {
	signedURL := "https://ws.stream.qqmusic.qq.com/audio.flac?sign=signed-secret&uin=123"
	c := New(
		&http.Client{Transport: failingTransport{}},
		func(context.Context) (string, error) { return "", nil },
		func(context.Context, string) error { return nil },
		func(string, string, time.Duration, error) {},
	)
	_, err := c.download(t.Context(), signedURL)
	if err == nil || !strings.Contains(err.Error(), "网络中断") {
		t.Fatalf("下载错误 = %v，期望保留网络错误", err)
	}
	if strings.Contains(err.Error(), "signed-secret") || strings.Contains(err.Error(), signedURL) {
		t.Errorf("下载错误携带了签名地址：%v", err)
	}
}
