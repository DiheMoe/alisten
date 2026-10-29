package netease

import (
	"context"
	"crypto/aes"
	"encoding/hex"
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
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/app/prepare"
	"github.com/dihemoe/alisten/internal/domain/track"
)

// 测试服务器按路径返回 testdata 中录制的网易云响应，Client 的所有请求都转到它。
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
	cookie := func(context.Context) (string, error) { return "MUSIC_U=secret", nil }
	return New(&http.Client{Transport: redirect{target}}, cookie, func(context.Context, string) error { return nil }, obs.record), obs
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
	return func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Cookie"); r.Method == http.MethodPost && got != "MUSIC_U=secret" {
			t.Errorf("%s 的 Cookie = %q", r.URL.Path, got)
		}
		b, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Error(err)
		}
		_, _ = w.Write(b)
	}
}

func status(code int) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) }
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
	if source != "netease" {
		op = source + " " + op
	}
	if err != nil {
		op += " 失败"
	}
	o.ops = append(o.ops, op)
}

func (o *observed) check(t *testing.T, want ...string) {
	t.Helper()
	o.mu.Lock()
	defer o.mu.Unlock()
	if !slices.Equal(o.ops, want) {
		t.Errorf("请求了网易云的接口 %q，期望 %q", o.ops, want)
	}
}

func decryptECB(t *testing.T, hexText, key string) string {
	t.Helper()
	data, err := hex.DecodeString(hexText)
	if err != nil {
		t.Fatal(err)
	}
	block, err := aes.NewCipher([]byte(key))
	if err != nil {
		t.Fatal(err)
	}
	out := make([]byte, len(data))
	for i := 0; i < len(data); i += aes.BlockSize {
		block.Decrypt(out[i:i+aes.BlockSize], data[i:i+aes.BlockSize])
	}
	return string(out[:len(out)-int(out[len(out)-1])])
}

// linuxapi 的请求中是转发给搜索接口的参数。
func searchParams(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	var forward struct {
		Params map[string]any `json:"params"`
	}
	if err := json.Unmarshal([]byte(decryptECB(t, r.FormValue("eparams"), linuxKey)), &forward); err != nil {
		t.Error(err)
	}
	return forward.Params
}

func wantSong(id string, title, artist, album, cover string, ms int) port.Song {
	return port.Song{
		Track: track.Track{Source: track.NetEase, ID: id},
		Meta: track.Meta{
			Title: title, Artist: artist, Album: album, CoverURL: cover,
			PageURL: "https://music.163.com/#/song?id=" + id, Duration: time.Duration(ms) * time.Millisecond,
		},
	}
}

var cover = wantSong("3387653989", "晴天 (Cover 蓝心羽)", "小汤圆", "", "https://p1.music.126.net/pvManIVaLdiDyKMDzuChuA==/109951173298787727.jpg", 176773)

func TestSearchTracks(t *testing.T) {
	c, obs := serve(t, map[string]http.HandlerFunc{
		"/api/linux/forward": func(w http.ResponseWriter, r *http.Request) {
			params := searchParams(t, r)
			if params["s"] != "晴天" || params["type"] != float64(1) || params["limit"] != float64(100) {
				t.Errorf("搜索参数 = %v，期望一次取 100 首歌", params)
			}
			fixture(t, "search-tracks.json")(w, r)
		},
	})
	want := []port.Song{
		wantSong("2652820720", "晴天(深情版)", "Lucky小爱", "晴天(深情版)", "http://p1.music.126.net/F0fTkmBTVykCa2o7Vgu1rQ==/109951173569626660.jpg", 278961),
		wantSong("3387653989", "晴天 (Cover 蓝心羽)", "小汤圆", "", "http://p1.music.126.net/pvManIVaLdiDyKMDzuChuA==/109951173298787727.jpg", 176773),
	}
	for range 2 {
		got, err := c.SearchTracks(t.Context(), "晴天")
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("搜索结果 = %#v\n期望 %#v", got, want)
		}
	}
	obs.check(t, "search_tracks")
}

func TestSearchPlaylists(t *testing.T) {
	c, obs := serve(t, map[string]http.HandlerFunc{
		"/api/linux/forward": func(w http.ResponseWriter, r *http.Request) {
			if params := searchParams(t, r); params["type"] != float64(1000) || params["limit"] != float64(100) {
				t.Errorf("搜索参数 = %v，期望一次取 100 个歌单", params)
			}
			fixture(t, "search-playlists.json")(w, r)
		},
	})
	got, err := c.SearchPlaylists(t.Context(), "晴天")
	if err != nil {
		t.Fatal(err)
	}
	want := []port.Playlist{
		{ID: "6792103822", Name: "周杰伦-Jay 『网易云精选』", CoverURL: "http://p1.music.126.net/WFQ4EKF5QabD33U3NUOPWQ==/109951169535051638.jpg", Creator: "Buradarrr", Description: "【持续更新】欢迎投稿", TrackCount: 143, PlayCount: 34028960},
		{ID: "17745261207", Name: "周杰伦歌单", CoverURL: "http://p1.music.126.net/-79-XFhWolhMzGESC8ifkg==/109951170218252280.jpg", Creator: "双鱼不困", TrackCount: 29, PlayCount: 514834},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("歌单 = %#v\n期望 %#v", got, want)
	}
	obs.check(t, "search_playlists")
}

func TestPlaylistTracks(t *testing.T) {
	c, obs := serve(t, map[string]http.HandlerFunc{
		"/weapi/v3/playlist/detail": fixture(t, "playlist-detail.json"),
		"/weapi/v3/song/detail":     fixture(t, "playlist-songs.json"),
	})
	got, err := c.PlaylistTracks(t.Context(), "3778678")
	if err != nil {
		t.Fatal(err)
	}
	want := []port.Song{
		wantSong("287398", "我不难过", "孙燕姿", "未完成", "https://p1.music.126.net/Pzxy6py26NgNmhJjYMf2RQ==/109951169724216323.jpg", 320400),
		wantSong("1973665667", "海屿你", "马也_Crabbit", "海屿你", "https://p1.music.126.net/RfguLkQJ8aMVkiIaxdy70Q==/109951170483263672.jpg", 295940),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("歌单内的歌曲 = %#v\n期望 %#v", got, want)
	}
	obs.check(t, "playlist_tracks", "track_detail")
}

func TestTrackMeta(t *testing.T) {
	c, _ := serve(t, map[string]http.HandlerFunc{"/weapi/v3/song/detail": fixture(t, "song-detail.json")})
	got, err := c.TrackMeta(t.Context(), "3387653989")
	if err != nil || got != cover.Meta {
		t.Errorf("元数据 = %#v, %v，期望 %#v", got, err, cover.Meta)
	}

	c, _ = serve(t, map[string]http.HandlerFunc{"/weapi/v3/song/detail": fixture(t, "song-detail-missing.json")})
	var rejected *port.RejectedError
	if _, err := c.TrackMeta(t.Context(), "1"); !errors.As(err, &rejected) {
		t.Errorf("不存在的歌的错误 = %v，期望音源拒绝", err)
	}
}

// 歌曲的元数据和歌词会写入 PostgreSQL，text 类型不能存储 U+0000。
func TestTextDropsNUL(t *testing.T) {
	c, _ := serve(t, map[string]http.HandlerFunc{
		"/weapi/v3/song/detail": body(`{"songs":[{"name":"晴\u0000天","id":1,"ar":[{"name":"周\u0000杰伦"}],"al":{"name":"叶\u0000惠美","picUrl":"https://p1.music.126.net/a\u0000.jpg"},"dt":1000}],"code":200}`),
		"/weapi/song/lyric":     body(`{"lrc":{"lyric":"[00:01.00]故\u0000事"},"code":200}`),
	})
	want := track.Meta{Title: "晴天", Artist: "周杰伦", Album: "叶惠美", CoverURL: "https://p1.music.126.net/a.jpg", PageURL: "https://music.163.com/#/song?id=1", Duration: time.Second}
	if got, err := c.TrackMeta(t.Context(), "1"); err != nil || got != want {
		t.Errorf("元数据 = %#v, %v，期望 %#v", got, err, want)
	}
	if got, err := c.lyric(t.Context(), "1"); err != nil || got != "[00:01.00]故事" {
		t.Errorf("歌词 = %q, %v", got, err)
	}
}

func TestSimilarTracks(t *testing.T) {
	c, obs := serve(t, map[string]http.HandlerFunc{"/weapi/discovery/simiSong": fixture(t, "similar.json")})
	for range 2 {
		got, err := c.SimilarTracks(t.Context(), "108914")
		if err != nil {
			t.Fatal(err)
		}
		want := []port.Song{
			wantSong("1842728629", "如果呢", "郑润泽", "如果呢", "https://p2.music.126.net/-xMsNLpquZTmMZlIztTgHg==/109951165953469081.jpg", 257532),
			wantSong("1391891631", "嗜好", "颜人中", "嗜好", "https://p2.music.126.net/J6HaJjtgv-yVVjyUm-h-AA==/109951164373633387.jpg", 284113),
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("相似歌曲 = %#v\n期望 %#v", got, want)
		}
	}
	obs.check(t, "similar")
}

const audioPath = "/song.mp3"

func resolveRoutes(t *testing.T) map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{
		"/weapi/v3/song/detail": fixture(t, "song-detail.json"),
		"/weapi/song/lyric":     fixture(t, "lyric.json"),
		audioPath: func(w http.ResponseWriter, r *http.Request) {
			// 音频来自 CDN，网易云的 Cookie 不能发给它。
			if got := r.Header.Get("Cookie"); got != "" {
				t.Errorf("下载音频时带上了 Cookie %q", got)
			}
			_, _ = io.WriteString(w, "ID3 音频")
		},
	}
}

func readAudio(t *testing.T, r port.Resolved) string {
	t.Helper()
	defer func() { _ = r.Audio.Close() }()
	b, err := io.ReadAll(r.Audio)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestPlayURL(t *testing.T) {
	routes := resolveRoutes(t)
	routes["/eapi/song/enhance/player/url/v1"] = fixture(t, "play-url.json")
	c, obs := serve(t, routes)
	var source port.PlayURLs = c
	got, err := source.PlayURL(t.Context(), "3387653989")
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(got)
	if err != nil || u.Path != audioPath {
		t.Fatalf("播放地址 = %q, %v，期望音频路径 %q", got, err, audioPath)
	}
	obs.check(t, "play_url")
}

func TestResolve(t *testing.T) {
	routes := resolveRoutes(t)
	routes["/eapi/song/enhance/player/url/v1"] = func(w http.ResponseWriter, r *http.Request) {
		plain := decryptECB(t, r.FormValue("params"), eapiKey)
		if !strings.HasPrefix(plain, "/api/song/enhance/player/url/v1-36cd479b6b5-") || !strings.Contains(plain, `"ids":[3387653989]`) || !strings.Contains(plain, `"level":"exhigh"`) {
			t.Errorf("eapi 的参数 = %s", plain)
		}
		fixture(t, "play-url.json")(w, r)
	}
	c, obs := serve(t, routes)
	got, err := c.Resolve(t.Context(), "3387653989")
	if err != nil {
		t.Fatal(err)
	}
	if got.Meta != cover.Meta || got.Lyric != "[00:16.35]故事的小黄花\n[00:20.16]从出生那年就飘着\n" {
		t.Errorf("解析结果 = %#v", got)
	}
	if audio := readAudio(t, got); audio != "ID3 音频" {
		t.Errorf("音频 = %q", audio)
	}
	obs.check(t, "track_detail", "lyric", "play_url")
}

func TestResolveFallsBackToStandardQuality(t *testing.T) {
	routes := resolveRoutes(t)
	routes["/eapi/song/enhance/player/url/v1"] = fixture(t, "play-url-missing.json")
	routes["/weapi/song/enhance/player/url"] = fixture(t, "play-url.json")
	c, obs := serve(t, routes)
	got, err := c.Resolve(t.Context(), "3387653989")
	if err != nil {
		t.Fatal(err)
	}
	if audio := readAudio(t, got); audio != "ID3 音频" {
		t.Errorf("音频 = %q", audio)
	}
	obs.check(t, "track_detail", "lyric", "play_url", "play_url")
}

// 播放地址有时效性，两次准备之间不能复用；元数据和歌词命中缓存。
func TestResolveDoesNotCachePlayURL(t *testing.T) {
	routes := resolveRoutes(t)
	routes["/eapi/song/enhance/player/url/v1"] = fixture(t, "play-url.json")
	c, obs := serve(t, routes)
	for range 2 {
		got, err := c.Resolve(t.Context(), "3387653989")
		if err != nil {
			t.Fatal(err)
		}
		readAudio(t, got)
	}
	obs.check(t, "track_detail", "lyric", "play_url", "play_url")
}

// 没有会员权限的账号只能取到试听片段：播放地址有值，freeTrialInfo 给出片段的范围。
// 录制不到这样的响应，按解析时读取的字段构造。
func trialClip() http.HandlerFunc {
	return body(`{"data":[{"id":3387653989,"url":"http://m801.music.126.net` + audioPath + `?trial=1","br":320000,"code":200,"freeTrialInfo":{"start":0,"end":30},"fee":1}],"code":200}`)
}

// 记下写入曲目表的歌。
type savedTracks struct{ saved []track.Track }

func (*savedTracks) PreparedTrack(context.Context, track.Track) (port.PreparedTrack, bool, error) {
	return port.PreparedTrack{}, false, nil
}

func (s *savedTracks) SavePreparedTrack(_ context.Context, t track.Track, _ port.PreparedTrack) error {
	s.saved = append(s.saved, t)
	return nil
}

// 试听片段不是完整的歌，存下来这首歌就只有 30 秒：按音源拒绝处理，不下载，也不写入曲目表。
func TestTrialClipIsRejectedAndNotStored(t *testing.T) {
	missing := fixture(t, "play-url-missing.json")
	for _, tt := range []struct {
		name        string
		eapi, weapi http.HandlerFunc
	}{
		{"两个接口都只给试听片段", trialClip(), trialClip()},
		{"回落的接口没有播放地址", trialClip(), missing},
		{"eapi 没有播放地址，回落的接口只给试听片段", missing, trialClip()},
	} {
		t.Run(tt.name, func(t *testing.T) {
			routes := resolveRoutes(t)
			routes["/eapi/song/enhance/player/url/v1"] = tt.eapi
			routes["/weapi/song/enhance/player/url"] = tt.weapi
			routes[audioPath] = func(http.ResponseWriter, *http.Request) { t.Error("试听片段不应下载") }
			c, _ := serve(t, routes)
			tracks := &savedTracks{}
			err := prepare.NewRunner(map[track.Source]port.Source{track.NetEase: c}, tracks, slog.New(slog.DiscardHandler)).Run(t.Context(), track.Track{Source: track.NetEase, ID: "3387653989"})
			var rejected *port.RejectedError
			if !errors.As(err, &rejected) || err.Error() != "这首歌需要网易云会员账号才能完整播放，请管理员在后台设置会员账号的 Cookie" {
				t.Errorf("试听片段的错误 = %v，期望音源拒绝，原因说明需要会员账号", err)
			}
			if len(tracks.saved) != 0 {
				t.Errorf("曲目表中写入了 %v", tracks.saved)
			}
		})
	}
}

// 第一个接口只给试听片段时，回落的接口给出完整的歌就用它。
func TestResolveFallsBackFromTrialClip(t *testing.T) {
	routes := resolveRoutes(t)
	routes["/eapi/song/enhance/player/url/v1"] = trialClip()
	routes["/weapi/song/enhance/player/url"] = fixture(t, "play-url.json")
	fullSong := routes[audioPath]
	routes[audioPath] = func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Has("trial") {
			_, _ = io.WriteString(w, "ID3 试听片段")
			return
		}
		fullSong(w, r)
	}
	c, _ := serve(t, routes)
	got, err := c.Resolve(t.Context(), "3387653989")
	if err != nil {
		t.Fatal(err)
	}
	if audio := readAudio(t, got); audio != "ID3 音频" {
		t.Errorf("音频 = %q，期望回落的接口给出的完整的歌", audio)
	}
}

func TestResolveRejectsSongWithoutURL(t *testing.T) {
	routes := resolveRoutes(t)
	routes["/eapi/song/enhance/player/url/v1"] = fixture(t, "play-url-missing.json")
	routes["/weapi/song/enhance/player/url"] = fixture(t, "play-url-missing.json")
	c, _ := serve(t, routes)
	var rejected *port.RejectedError
	if _, err := c.Resolve(t.Context(), "3387653989"); !errors.As(err, &rejected) || err.Error() != "网易云没有提供这首歌的播放地址" {
		t.Errorf("没有播放地址时的错误 = %v，期望音源拒绝", err)
	}
}

func TestResolveRetriesUpstreamFailures(t *testing.T) {
	routes := resolveRoutes(t)
	routes["/eapi/song/enhance/player/url/v1"] = status(http.StatusBadGateway)
	routes["/weapi/song/enhance/player/url"] = status(http.StatusBadGateway)
	c, obs := serve(t, routes)
	var rejected *port.RejectedError
	if _, err := c.Resolve(t.Context(), "3387653989"); err == nil || errors.As(err, &rejected) {
		t.Errorf("网易云出错时的错误 = %v，期望可以重试的错误", err)
	}
	obs.check(t, "track_detail", "lyric", "play_url 失败", "play_url 失败")

	routes = resolveRoutes(t)
	routes["/eapi/song/enhance/player/url/v1"] = fixture(t, "play-url.json")
	routes[audioPath] = status(http.StatusForbidden)
	c, _ = serve(t, routes)
	if _, err := c.Resolve(t.Context(), "3387653989"); err == nil || errors.As(err, &rejected) {
		t.Errorf("下载音频失败时的错误 = %v，期望可以重试的错误", err)
	}
}

func TestFailedResponseIsNotCached(t *testing.T) {
	var calls atomic.Int32
	c, obs := serve(t, map[string]http.HandlerFunc{
		"/api/linux/forward": func(w http.ResponseWriter, r *http.Request) {
			if calls.Add(1) == 1 {
				_, _ = io.WriteString(w, `{"code":-460,"message":"Cheating"}`)
				return
			}
			fixture(t, "search-tracks.json")(w, r)
		},
	})
	if _, err := c.SearchTracks(t.Context(), "晴天"); err == nil {
		t.Fatal("错误码不是 200 时应失败")
	}
	if songs, err := c.SearchTracks(t.Context(), "晴天"); err != nil || len(songs) != 2 {
		t.Errorf("再次搜索 = %d 首, %v，失败的响应不应缓存", len(songs), err)
	}
	obs.check(t, "search_tracks 失败", "search_tracks")
}

func TestCookieIsReloadedEvery30Seconds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		loads := 0
		c := cookieCache{load: func(context.Context) (string, error) {
			loads++
			return "MUSIC_U=" + string(rune('0'+loads)), nil
		}}
		for range 2 {
			if got, err := c.get(t.Context()); err != nil || got != "MUSIC_U=1" {
				t.Fatalf("Cookie = %q, %v", got, err)
			}
		}
		time.Sleep(30*time.Second - time.Nanosecond)
		if got, err := c.get(t.Context()); err != nil || got != "MUSIC_U=1" {
			t.Fatalf("不到 30 秒的 Cookie = %q, %v，期望仍是缓存的", got, err)
		}
		time.Sleep(time.Nanosecond)
		if got, err := c.get(t.Context()); err != nil || got != "MUSIC_U=2" {
			t.Errorf("30 秒之后的 Cookie = %q, %v，期望重新读取", got, err)
		}
	})
}

// 读取 Cookie 是读 PostgreSQL，期限与其他读写 PostgreSQL 的操作相同，不占用接口调用的 30 秒。
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

// 把 HTTPS 的请求转给 TLS 服务器，HTTP 的请求转给明文服务器。
type split struct {
	secure, plain *url.URL
	transport     http.RoundTripper
}

func (s split) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	if req.URL.Scheme == "https" {
		req.URL.Host = s.secure.Host
		return s.transport.RoundTrip(req)
	}
	req.URL.Host = s.plain.Host
	return http.DefaultTransport.RoundTrip(req)
}

// Go 会把手动设置的 Cookie 头带到同主机的重定向目标，不管目标是不是 HTTPS。
func TestCookieIsNotSentOverPlainHTTPAfterRedirect(t *testing.T) {
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("明文的 HTTP 服务器收到了 %s，Cookie = %q", r.URL, r.Header.Get("Cookie"))
		fixture(t, "song-detail.json")(w, r)
	}))
	t.Cleanup(plain.Close)
	secure := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://music.163.com"+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(secure.Close)
	plainURL, err := url.Parse(plain.URL)
	if err != nil {
		t.Fatal(err)
	}
	secureURL, err := url.Parse(secure.URL)
	if err != nil {
		t.Fatal(err)
	}
	obs := &observed{}
	cookie := func(context.Context) (string, error) { return "MUSIC_U=secret", nil }
	c := New(&http.Client{Transport: split{secure: secureURL, plain: plainURL, transport: secure.Client().Transport}}, cookie, func(context.Context, string) error { return nil }, obs.record)

	if _, err := c.TrackMeta(t.Context(), "3387653989"); err == nil || !strings.Contains(err.Error(), "HTTP 307") {
		t.Errorf("接口重定向时的错误 = %v，期望 HTTP 307", err)
	}
	obs.check(t, "track_detail 失败")
}

// 音频下载不带 Cookie，沿用默认的重定向策略：网易云给出的 CDN 地址可能再跳转一次。
func TestResolveFollowsAudioRedirects(t *testing.T) {
	routes := resolveRoutes(t)
	routes["/eapi/song/enhance/player/url/v1"] = fixture(t, "play-url.json")
	routes[audioPath] = func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("cdn") == "" {
			http.Redirect(w, r, audioPath+"?cdn=1", http.StatusFound)
			return
		}
		_, _ = io.WriteString(w, "ID3 音频")
	}
	c, _ := serve(t, routes)
	got, err := c.Resolve(t.Context(), "3387653989")
	if err != nil {
		t.Fatal(err)
	}
	if audio := readAudio(t, got); audio != "ID3 音频" {
		t.Errorf("跳转之后的音频 = %q", audio)
	}
}

type failingTransport struct{}

func (failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("网络中断")
}

func TestDownloadErrorDoesNotExposeSignedURL(t *testing.T) {
	signedURL := "https://m701.music.126.net/audio.mp3?sign=signed-secret&uid=123"
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
