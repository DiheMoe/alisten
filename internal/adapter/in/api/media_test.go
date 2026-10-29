package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/dihemoe/alisten/internal/app/media"
	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/app/port/porttest"
	"github.com/dihemoe/alisten/internal/domain/track"
	"github.com/dihemoe/alisten/internal/lib/logctx"
	"github.com/dihemoe/alisten/protocol"
)

type storedMedia struct {
	items []port.StoredMedia
	used  bool
}

func (m *storedMedia) ListStoredMedia(context.Context) ([]port.StoredMedia, error) {
	return m.items, nil
}

func (m *storedMedia) StoredMedia(_ context.Context, key string) (port.StoredMedia, bool, error) {
	for _, item := range m.items {
		if item.Key == key {
			return item, true, nil
		}
	}
	return port.StoredMedia{}, false, nil
}

func (m *storedMedia) DeleteStoredMedia(context.Context, string) error {
	m.items = nil
	return nil
}

func (m *storedMedia) TrackInUse(context.Context, track.Track) (bool, error) {
	return m.used, nil
}

func TestAdminMedia(t *testing.T) {
	key := media.Key("https://example.com/a.mp3")
	video := media.Key("BV1xx411c7mD/2")
	played := time.UnixMilli(1760000000456)
	stored := &storedMedia{items: []port.StoredMedia{
		{Key: key, Source: track.Link, Link: "https://example.com/a.mp3", Title: "晴天", Size: 12290, StoredAt: time.UnixMilli(1760000000123), LastPlayedAt: &played},
		{Key: media.Key("https://example.com/b.mp3"), Source: track.Link, Link: "https://example.com/b.mp3", Title: "七里香", Size: 1, StoredAt: time.UnixMilli(1760000000789)},
		{Key: video, Source: track.Bilibili, Link: "BV1xx411c7mD/2", Title: "晴天（钢琴版）", Size: 2, StoredAt: time.UnixMilli(1760000000999)},
	}}
	store := porttest.New()
	s := services(store, store, "admin-secret")
	s.Media = media.NewLibrary(stored, stored, porttest.NewMediaStore())
	server, _ := start(t, s)
	admin := []string{"Authorization", "Bearer admin-secret"}

	want := response{status: http.StatusOK, body: `{"items":[` +
		`{"key":"` + key + `","lastPlayedAtMs":1760000000456,"link":"https://example.com/a.mp3","size":12290,"source":"link","storedAtMs":1760000000123,"title":"晴天"},` +
		`{"key":"` + media.Key("https://example.com/b.mp3") + `","lastPlayedAtMs":null,"link":"https://example.com/b.mp3","size":1,"source":"link","storedAtMs":1760000000789,"title":"七里香"},` +
		`{"key":"` + video + `","lastPlayedAtMs":null,"link":"https://www.bilibili.com/video/BV1xx411c7mD?p=2","size":2,"source":"bilibili","storedAtMs":1760000000999,"title":"晴天（钢琴版）"}]}`}
	if got := send(t, server, http.MethodGet, "/api/admin/media", "", admin...); got != want {
		t.Errorf("列出已存储的媒体 = %#v\n期望 %#v", got, want)
	}

	stored.used = true
	if got, want := send(t, server, http.MethodDelete, "/api/admin/media/"+key, "", admin...), failure(protocol.ErrorCodeConflict, "这首歌还在房间的播放列表中，不能删除"); got != want {
		t.Errorf("删除仍被房间引用的媒体 = %#v，期望 %#v", got, want)
	}
	stored.used = false
	resp, header := exchange(t, server, http.MethodDelete, "/api/admin/media/"+key, "", admin...)
	checkNoContent(t, "删除媒体", resp, header)
}

type playURL map[string]string

type inRooms []track.Track

func (r inRooms) TrackInUse(_ context.Context, t track.Track) (bool, error) {
	return slices.Contains(r, t), nil
}

func (p playURL) PlayURL(_ context.Context, id string) (string, error) {
	if url, ok := p[id]; ok {
		return url, nil
	}
	return "", &port.RejectedError{Err: errors.New("网易云没有提供这首歌的播放地址")}
}

func TestMediaRedirect(t *testing.T) {
	store := porttest.New()
	s := services(store, store, "")
	s.Playback = media.NewPlayback(map[track.Source]port.PlayURLs{track.NetEase: playURL{"186016": "http://m701.music.126.net/a.mp3"}}, inRooms{{Source: track.NetEase, ID: "186016"}, {Source: track.NetEase, ID: "1"}})
	metrics := prometheus.NewRegistry()
	server := httptest.NewServer(SameOrigin([]string{allowedOrigin, "http://localhost:9876"}, New(s, metrics, noop.NewTracerProvider(), slog.New(slog.DiscardHandler))))
	defer server.Close()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	do := func(method, path string) *http.Response {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), method, server.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp
	}

	resp := do(http.MethodGet, "/media/netease/186016")
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "https://m701.music.126.net/a.mp3" || resp.Header.Get("Cache-Control") != "no-store" {
		t.Errorf("跳转 = %d %q %q，期望 302 到 https 的地址，不缓存", resp.StatusCode, resp.Header.Get("Location"), resp.Header.Get("Cache-Control"))
	}
	for path, want := range map[string]int{
		"/media/netease/1": http.StatusBadGateway,
		"/media/qq/1":      http.StatusBadGateway,
		"/media/link/x":    http.StatusNotFound,
	} {
		if got := do(http.MethodGet, path).StatusCode; got != want {
			t.Errorf("%s = %d，期望 %d", path, got, want)
		}
	}
	if got := do(http.MethodPost, "/media/netease/186016").StatusCode; got != http.StatusNotFound {
		t.Errorf("POST /media = %d，期望 404", got)
	}
	if got, want := send(t, server, http.MethodGet, "/media/netease/185811", ""), failure(protocol.ErrorCodeNotFound, "这首歌不在任何房间中"); got != want {
		t.Errorf("不在房间里的歌 = %#v，期望 %#v", got, want)
	}

	want := map[string]float64{"netease ok": 1, "netease PROVIDER_ERROR": 1, "netease NOT_FOUND": 1, "qq PROVIDER_ERROR": 1}
	if got := redirectCounts(t, metrics); !maps.Equal(got, want) {
		t.Errorf("跳转的计数 = %v，期望 %v", got, want)
	}
}

func redirectCounts(t *testing.T, metrics prometheus.Gatherer) map[string]float64 {
	t.Helper()
	families, err := metrics.Gather()
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]float64{}
	for _, f := range families {
		if f.GetName() != "alisten_media_redirects_total" {
			continue
		}
		for _, m := range f.GetMetric() {
			labels := map[string]string{}
			for _, l := range m.GetLabel() {
				labels[l.GetName()] = l.GetValue()
			}
			counts[labels["source"]+" "+labels["result"]] = m.GetCounter().GetValue()
		}
	}
	return counts
}

type blockingPlayURL struct {
	entered chan struct{}
	release chan struct{}
}

func (b blockingPlayURL) PlayURL(ctx context.Context, _ string) (string, error) {
	close(b.entered)
	select {
	case <-b.release:
		return "https://m701.music.126.net/a.mp3", nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func TestMediaRedirectIgnoresDepartedClients(t *testing.T) {
	source := blockingPlayURL{entered: make(chan struct{}), release: make(chan struct{})}
	defer close(source.release)
	store := porttest.New()
	s := services(store, store, "")
	s.Playback = media.NewPlayback(map[track.Source]port.PlayURLs{track.NetEase: source}, inRooms{{Source: track.NetEase, ID: "186016"}})
	metrics := prometheus.NewRegistry()
	handler := SameOrigin([]string{allowedOrigin, "http://localhost:9876"}, New(s, metrics, noop.NewTracerProvider(), slog.New(slog.DiscardHandler)))
	returned := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.ServeHTTP(w, r)
		close(returned)
	}))
	defer server.Close()

	ctx, leave := context.WithCancel(t.Context())
	defer leave()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/media/netease/186016", nil)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		if resp, err := server.Client().Do(req); err == nil {
			_ = resp.Body.Close()
		}
	}()
	<-source.entered
	leave()
	select {
	case <-returned:
	case <-time.After(10 * time.Second):
		t.Fatal("客户端离开之后请求没有返回")
	}

	families, err := metrics.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range families {
		if f.GetName() == "alisten_media_redirects_total" && len(f.GetMetric()) != 0 {
			t.Errorf("客户端离开的请求被计入了跳转的结果：%v", f.GetMetric())
		}
	}
}

type panickingUsage struct{}

func (panickingUsage) TrackInUse(context.Context, track.Track) (bool, error) {
	panic("存储坏了")
}

func TestPanickingMediaRedirectIsInternal(t *testing.T) {
	store := porttest.New()
	s := services(store, store, "")
	s.Playback = media.NewPlayback(map[track.Source]port.PlayURLs{track.NetEase: playURL{}}, panickingUsage{})
	var logs bytes.Buffer
	metrics := prometheus.NewRegistry()
	server := httptest.NewServer(SameOrigin([]string{allowedOrigin, "http://localhost:9876"}, New(s, metrics, noop.NewTracerProvider(), slog.New(logctx.NewHandler(slog.NewJSONHandler(&logs, nil))))))
	defer server.Close()

	if got, want := send(t, server, http.MethodGet, "/media/netease/1", ""), failure(protocol.ErrorCodeInternal, "服务器出错了，请稍后再试。"); got != want {
		t.Errorf("panic 的跳转 = %#v，期望 %#v", got, want)
	}
	var entry map[string]any
	if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
		t.Fatalf("应记录一行 JSON 日志：%v\n%s", err, logs.String())
	}
	if entry["msg"] != "处理 HTTP 请求时发生 panic" || entry["path"] != "/media/netease/1" {
		t.Errorf("日志 = %v，期望记录跳转请求中的 panic", entry)
	}
	if got, want := redirectCounts(t, metrics), map[string]float64{"netease INTERNAL": 1}; !maps.Equal(got, want) {
		t.Errorf("跳转的计数 = %v，期望 %v", got, want)
	}
}
