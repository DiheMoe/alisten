package qq

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/track"
)

const testCookie = "qq_test_cookie=fictitious"

var testLogger = slog.New(slog.DiscardHandler)

// 测试服务器按路径返回 testdata 中录制的响应，Client 的所有请求都转到它。
func serve(t *testing.T, routes map[string]http.HandlerFunc) (*Client, *observed) {
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
	return New(
		&http.Client{Transport: redirect{target}},
		func(context.Context) (string, error) { return testCookie, nil },
		func(context.Context, string) error { return nil },
		obs.record,
		testLogger,
	), obs
}

type redirect struct{ target *url.URL }

func (r redirect) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.URL.Scheme, req.URL.Host = r.target.Scheme, r.target.Host
	return http.DefaultTransport.RoundTrip(req)
}

// 国内运营商的地址段。
var chinaPrefixes = [][2]byte{{116, 255}, {116, 228}, {218, 192}, {124, 0}, {14, 132}, {183, 14}, {58, 14}, {113, 116}, {120, 230}}

// 请求接口时带上同一个随机的国内 IP，下载音频时不带。
func checkClientIP(t *testing.T, r *http.Request) {
	realIP, forwarded := r.Header.Get("X-Real-IP"), r.Header.Get("X-Forwarded-For")
	if r.URL.Path == audioPath {
		if realIP != "" || forwarded != "" {
			t.Errorf("下载音频时带上了 X-Real-IP %q、X-Forwarded-For %q", realIP, forwarded)
		}
		return
	}
	addr, err := netip.ParseAddr(realIP)
	if err != nil || !addr.Is4() || !slices.Contains(chinaPrefixes, [2]byte(addr.AsSlice())) || forwarded != realIP {
		t.Errorf("%s 的 X-Real-IP 为 %q、X-Forwarded-For 为 %q，期望同一个国内运营商的 IP", r.URL.Path, realIP, forwarded)
	}
}

func fixture(t *testing.T, name string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		b, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Error(err)
		}
		_, _ = w.Write(b)
	}
}

func body(text string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, text) }
}

type observed struct {
	mu  sync.Mutex
	ops []string
}

func (o *observed) record(source, op string, _ time.Duration, err error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if err != nil {
		op += " 失败"
	}
	o.ops = append(o.ops, source+" "+op)
}

func (o *observed) check(t *testing.T, want ...string) {
	t.Helper()
	o.mu.Lock()
	defer o.mu.Unlock()
	if !slices.Equal(o.ops, want) {
		t.Errorf("请求了接口 %q，期望 %q", o.ops, want)
	}
}

func wantSong(mid, title, album, albumMID string, seconds int) port.Song {
	return port.Song{
		Track: track.Track{Source: track.QQ, ID: mid},
		Meta: track.Meta{
			Title: title, Artist: "周杰伦", Album: album,
			CoverURL: "https://y.gtimg.cn/music/photo_new/T002R300x300M000" + albumMID + ".jpg",
			PageURL:  "https://y.qq.com/n/ryqq/songDetail/" + mid,
			Duration: time.Duration(seconds) * time.Second,
		},
	}
}

var live = wantSong("004Fs2FP1EvZYc", "晴天 (Live)", "周杰伦地表最强世界巡回演唱会", "004fXSyj3bWTMN", 249)

func TestSearchTracks(t *testing.T) {
	c, obs := serve(t, map[string]http.HandlerFunc{
		"/soso/fcgi-bin/search_for_qq_cp": func(w http.ResponseWriter, r *http.Request) {
			if q := r.URL.Query(); q.Get("w") != "晴天" || q.Get("n") != "20" {
				t.Errorf("搜索参数 = %v，期望一次取 20 首歌", q)
			}
			fixture(t, "search-tracks.json")(w, r)
		},
	})
	want := []port.Song{wantSong("0039MnYb0qxYhV", "晴天", "叶惠美", "000MkMni19ClKG", 269), live}
	for range 2 {
		got, err := c.SearchTracks(t.Context(), "晴天")
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("搜索结果 = %#v\n期望 %#v", got, want)
		}
	}
	obs.check(t, "qq search_tracks")
}

func TestSearchPlaylists(t *testing.T) {
	c, _ := serve(t, map[string]http.HandlerFunc{
		"/soso/fcgi-bin/client_music_search_songlist": func(w http.ResponseWriter, r *http.Request) {
			if q := r.URL.Query(); q.Get("query") != "晴天" || q.Get("num_per_page") != "20" {
				t.Errorf("搜索参数 = %v，期望一次取 20 个歌单", q)
			}
			fixture(t, "search-playlists.json")(w, r)
		},
	})
	got, err := c.SearchPlaylists(t.Context(), "晴天")
	if err != nil {
		t.Fatal(err)
	}
	want := []port.Playlist{
		{ID: "7382629476", Name: "精选 | 好听到单曲循环的热歌", CoverURL: "http://p.qpic.cn/music_cover/Z89aLA93LOSOicz0QOnMboqgLaiaFohjweglHh6JSoL8hrjOfFOC6DXw/600", Creator: "故事未完待续", Description: "音乐，是生命中的一束光。", TrackCount: 130, PlayCount: 1646891961},
		{ID: "7709684645", Name: "抖音热播 | 瞬间心动的神仙歌曲", CoverURL: "http://qpic.y.qq.com/music_cover/aDicdhbLYu1KGBxNOsA6ff1v3vCMPkgFNhdQf18FKSOUiaVCmJ7TOIXQ/300", Creator: "行走的荷尔蒙", Description: "抖音热播的神仙歌曲，建议收藏。", TrackCount: 121, PlayCount: 979011192},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("歌单 = %#v\n期望 %#v", got, want)
	}
}

func TestTrackMeta(t *testing.T) {
	c, _ := serve(t, map[string]http.HandlerFunc{"/v8/fcg-bin/fcg_play_single_song.fcg": fixture(t, "song-detail.json")})
	if got, err := c.TrackMeta(t.Context(), "004Fs2FP1EvZYc"); err != nil || got != live.Meta {
		t.Errorf("元数据 = %#v, %v，期望 %#v：标题要带上版本", got, err, live.Meta)
	}

	c, _ = serve(t, map[string]http.HandlerFunc{"/v8/fcg-bin/fcg_play_single_song.fcg": fixture(t, "song-detail-missing.json")})
	var rejected *port.RejectedError
	if _, err := c.TrackMeta(t.Context(), "000000000000xx"); !errors.As(err, &rejected) {
		t.Errorf("不存在的歌的错误 = %v，期望音源拒绝", err)
	}
}

// 歌曲的元数据和歌词会写入 PostgreSQL，text 类型不能存储 U+0000，要在解密之后清除。
func TestTextDropsNUL(t *testing.T) {
	c, _ := serve(t, map[string]http.HandlerFunc{
		"/v8/fcg-bin/fcg_play_single_song.fcg": body(`{"code":0,"data":[{"mid":"004Fs2FP1EvZYc","title":"晴\u0000天","interval":249,"album":{"mid":"004fXS\u0000yj3bWTMN","name":"叶\u0000惠美"},"singer":[{"name":"周\u0000杰伦"}]}]}`),
		"/cgi-bin/musicu.fcg":                  fixture(t, "lyric-nul.json"),
	})
	want := wantSong("004Fs2FP1EvZYc", "晴天", "叶惠美", "004fXSyj3bWTMN", 249).Meta
	if got, err := c.TrackMeta(t.Context(), "004Fs2FP1EvZYc"); err != nil || got != want {
		t.Errorf("元数据 = %#v, %v，期望 %#v", got, err, want)
	}
	if got, err := c.lyric(t.Context(), "004Fs2FP1EvZYc"); err != nil || got.LRC != "[00:01.00]故事\n" {
		t.Errorf("歌词 = %q, %v", got.LRC, err)
	}
}

// 解密后的歌词不保证是 UTF-8，比如 GBK 编码的歌词；PostgreSQL 的 text 类型拒绝不合法的 UTF-8，
// 这样的歌词存不进去，准备任务每次都失败。
func TestLyricReplacesInvalidUTF8(t *testing.T) {
	c, _ := serve(t, map[string]http.HandlerFunc{
		"/cgi-bin/musicu.fcg": fixture(t, "lyric-gbk.json"),
	})
	if got, err := c.lyric(t.Context(), "004Fs2FP1EvZYc"); err != nil || got.LRC != "[00:01.00]\uFFFD\n[00:02.00]晴天" {
		t.Errorf("歌词 = %q, %v，期望不合法的字节替换为 U+FFFD", got.LRC, err)
	}
}

const audioPath = "/qq/80456317.flac"

// musicu 网关同时承载歌词和播放地址：按请求里的 req_1（播放地址）有无分发打桩。
func musicu(t *testing.T, playResponse string) http.HandlerFunc {
	return musicuLyric(t, playResponse, fixture(t, "lyric.json"))
}

func musicuLyric(t *testing.T, playResponse string, lyric http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Cookie"); got != testCookie {
			t.Errorf("musicu 接口的 Cookie = %q，期望虚构 QQ Cookie", got)
		}
		var payload struct {
			Req1 *struct {
				Module string `json:"module"`
			} `json:"req_1"`
		}
		if err := json.Unmarshal([]byte(r.URL.Query().Get("data")), &payload); err != nil {
			t.Errorf("解析 musicu 请求：%v", err)
		}
		if payload.Req1 != nil {
			body(playResponse)(w, r)
			return
		}
		lyric(w, r)
	}
}

// playRequest 只把播放地址请求转给打桩，歌词仍走固定件。
func playRequest(t *testing.T, handle http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Query().Get("data"), "req_1") {
			handle(w, r)
			return
		}
		fixture(t, "lyric.json")(w, r)
	}
}

func resolveRoutes(t *testing.T) map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{
		"/v8/fcg-bin/fcg_play_single_song.fcg": fixture(t, "song-detail.json"),
		"/cgi-bin/musicu.fcg":                  musicu(t, `{"code":0,"req_1":{"code":0,"data":{"midurlinfo":[]}}}`),
		audioPath: func(w http.ResponseWriter, r *http.Request) {
			if got := r.Header.Get("Cookie"); got != "" {
				t.Errorf("音频 CDN 收到了 Cookie %q", got)
			}
			body("ID3 音频")(w, r)
		},
	}
}

func TestResolve(t *testing.T) {
	routes := resolveRoutes(t)
	routes["/cgi-bin/musicu.fcg"] = musicu(t, `{"code":0,"req_1":{"code":0,"data":{"midurlinfo":[{"filename":"F000004Fs2FP1EvZYc004Fs2FP1EvZYc.flac","purl":"`+audioPath[1:]+`"}]}}}`)
	c, obs := serve(t, routes)
	got, err := c.Resolve(t.Context(), "004Fs2FP1EvZYc")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = got.Audio.Close() }()
	wantLyric := "[ti:逐字测试]\n[ar:歌手]\n[00:01.00]你好世\n[00:03.00]再见\n"
	wantWords := "[1000,1500](1000,500,0)你(1500,500,0)好(2000,500,0)世\n[3000,1000](3000,500,0)再(3500,500,0)见\n"
	wantTrans := "[00:01.00]你好世界\n[00:03.00]再见\n"
	if got.Meta != live.Meta || got.Lyric != wantLyric || got.LyricWords != wantWords || got.LyricTrans != wantTrans {
		t.Errorf("解析结果 = %#v", got)
	}
	if b, err := io.ReadAll(got.Audio); err != nil || string(b) != "ID3 音频" {
		t.Errorf("音频 = %q, %v", b, err)
	}
	obs.check(t, "qq track_detail", "qq lyric", "qq play_url")
}

func TestResolveRejects(t *testing.T) {
	c, _ := serve(t, resolveRoutes(t))
	var rejected *port.RejectedError
	if _, err := c.Resolve(t.Context(), "004Fs2FP1EvZYc"); !errors.As(err, &rejected) || err.Error() != "QQ 音乐没有提供这首歌的播放地址" {
		t.Errorf("QQ 没有播放地址时的错误 = %v，期望音源拒绝", err)
	}
}

func TestResolveRetriesUpstreamFailures(t *testing.T) {
	var rejected *port.RejectedError
	routes := resolveRoutes(t)
	routes["/cgi-bin/musicu.fcg"] = playRequest(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) })
	c, obs := serve(t, routes)
	if _, err := c.Resolve(t.Context(), "004Fs2FP1EvZYc"); err == nil || errors.As(err, &rejected) {
		t.Errorf("QQ 原生播放接口出错时的错误 = %v，期望可以重试的错误", err)
	}
	obs.check(t, "qq track_detail", "qq lyric", "qq play_url 失败")
}

func TestLyricMissing(t *testing.T) {
	c, _ := serve(t, map[string]http.HandlerFunc{"/cgi-bin/musicu.fcg": fixture(t, "lyric-missing.json")})
	if got, err := c.lyric(t.Context(), "000000000000xx"); err != nil || got != (lyricResult{}) {
		t.Errorf("没有歌词的歌 = %#v, %v，期望空结果", got, err)
	}
}

func TestCookieIsReloadedEvery30Seconds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		loads := 0
		c := cookieCache{load: func(context.Context) (string, error) {
			loads++
			return "qq_cookie=" + string(rune('0'+loads)), nil
		}}
		for range 2 {
			if got, err := c.get(t.Context()); err != nil || got != "qq_cookie=1" {
				t.Fatalf("Cookie = %q, %v", got, err)
			}
		}
		time.Sleep(30*time.Second - time.Nanosecond)
		if got, err := c.get(t.Context()); err != nil || got != "qq_cookie=1" {
			t.Fatalf("不到 30 秒的 Cookie = %q, %v，期望仍是缓存的", got, err)
		}
		time.Sleep(time.Nanosecond)
		if got, err := c.get(t.Context()); err != nil || got != "qq_cookie=2" {
			t.Errorf("30 秒之后的 Cookie = %q, %v，期望重新读取", got, err)
		}
	})
}

func TestCookieLoadHasOwnDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := cookieCache{load: func(ctx context.Context) (string, error) {
			<-ctx.Done()
			return "", ctx.Err()
		}}
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		defer cancel()
		start := time.Now()
		if _, err := c.get(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("读取卡住时的错误 = %v，期望超过期限", err)
		}
		if waited := time.Since(start); waited != 10*time.Second {
			t.Errorf("等了 %v，期望 10 秒", waited)
		}
	})
}

func TestCookieSaveHasOwnDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := cookieCache{store: func(ctx context.Context, _ string) error {
			<-ctx.Done()
			return ctx.Err()
		}}
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		defer cancel()
		start := time.Now()
		if err := c.put(ctx, testCookie); !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("保存卡住时的错误 = %v，期望超过期限", err)
		}
		if waited := time.Since(start); waited != 10*time.Second {
			t.Errorf("等了 %v，期望 10 秒", waited)
		}
	})
}

func TestCookiePutUpdatesCache(t *testing.T) {
	loads := 0
	saved := ""
	c := cookieCache{
		load: func(context.Context) (string, error) {
			loads++
			return "loaded", nil
		},
		store: func(_ context.Context, value string) error {
			saved = value
			return nil
		},
	}
	if err := c.put(t.Context(), testCookie); err != nil {
		t.Fatal(err)
	}
	if got, err := c.get(t.Context()); err != nil || got != testCookie {
		t.Errorf("保存后的 Cookie = %q, %v，期望 %q", got, err, testCookie)
	}
	if saved != testCookie || loads != 0 {
		t.Errorf("保存的 Cookie = %q，重新加载了 %d 次", saved, loads)
	}
}

func TestCleanTransDropsTagsAndPlaceholders(t *testing.T) {
	in := "[ti:标题]\n[00:00.00]//\n[00:01.00]第一句\n[00:02.00] // \n[00:03.00]第二句\n"
	want := "[00:01.00]第一句\n[00:03.00]第二句\n"
	if got := cleanTrans(in); got != want {
		t.Errorf("清理后的翻译 = %q，期望 %q", got, want)
	}
}
