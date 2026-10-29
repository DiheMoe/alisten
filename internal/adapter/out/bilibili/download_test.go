package bilibili

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/track"
)

const dashResponse = `{"code":0,"message":"0","data":{"dash":{"audio":[
{"id":30216,"baseUrl":"https://upos-sz-mirrorcos.bilivideo.com/upgcxcode/audio30216.m4s?sig=SIG16"},
{"id":30280,"baseUrl":"https://upos-sz-mirrorcos.bilivideo.com/upgcxcode/audio30280.m4s?sig=SIG80"},
{"id":30232,"baseUrl":""}],
"flac":{"audio":{"id":30251,"baseUrl":"https://upos-sz-mirrorcos.bilivideo.com/upgcxcode/flac.flac?sig=SIGFLAC"}},
"dolby":{"audio":[{"id":30250,"baseUrl":"https://upos-sz-mirrorcos.bilivideo.com/upgcxcode/dolby.m4s?sig=SIGDOLBY"}]}}}}`

type playurlCalls struct {
	mu      sync.Mutex
	queries []url.Values
}

func (p *playurlCalls) handler(body func(q url.Values) string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		p.queries = append(p.queries, r.URL.Query())
		p.mu.Unlock()
		respondJSON(w, body(r.URL.Query()))
	}
}

func (p *playurlCalls) all() []url.Values {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]url.Values(nil), p.queries...)
}

func fileHandler(content string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, content)
	}
}

func readLinkFile(t *testing.T, f port.LinkFile) string {
	t.Helper()
	r, err := f.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func linkFilePath(t *testing.T, f port.LinkFile) string {
	t.Helper()
	path, ok := f.(file)
	if !ok {
		t.Fatalf("LinkFile 的实现是 %T，期望本包 file", f)
	}
	return string(path)
}

func countDirs(t *testing.T, root string) int {
	t.Helper()
	entries, err := filepath.Glob(filepath.Join(root, "bilibili-*"))
	if err != nil {
		t.Fatal(err)
	}
	return len(entries)
}

// 不提供 nav 路由：下载不应请求 nav，playurl 统一 fnval=80，不区分大会员；误调会因 404 失败。
func baseDownloadRoutes(calls *playurlCalls, playurl func(q url.Values) string) map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{
		"/x/web-interface/view": func(w http.ResponseWriter, r *http.Request) {
			respondJSON(w, viewResponse)
		},
		"/x/player/pagelist": func(w http.ResponseWriter, r *http.Request) {
			respondJSON(w, singlePage)
		},
		"/x/player/playurl":         calls.handler(playurl),
		"/upgcxcode/audio30280.m4s": fileHandler("AUDIO-DATA"),
		"/upgcxcode/low.mp4":        fileHandler("LOW-DATA"),
		"/bfs/archive/cover.jpg":    fileHandler("COVER-DATA"),
	}
}

func lastRequest(tr *redirectTransport, path string) *recordedRequest {
	reqs := tr.requests()
	for i := len(reqs) - 1; i >= 0; i-- {
		if reqs[i].path == path {
			return &reqs[i]
		}
	}
	return nil
}

func TestDownloadWithCredential(t *testing.T) {
	calls := &playurlCalls{}
	c, tr, obs, creds := serve(t, baseDownloadRoutes(calls, func(url.Values) string { return dashResponse }))
	creds.set(t, "SESSDATA=sess123; bili_jct=jct456", "refresh789")

	d, release, err := c.Download(t.Context(), "BV1xx411c7mD")
	if err != nil {
		t.Fatal(err)
	}
	if want := (track.Meta{Title: "视频标题", Artist: "UP主名字", Duration: 200 * time.Second}); d.Meta != want {
		t.Errorf("元数据 = %+v，期望 %+v", d.Meta, want)
	}
	if got := readLinkFile(t, d.Audio); got != "AUDIO-DATA" {
		t.Errorf("音频 = %q", got)
	}
	if got := readLinkFile(t, d.Cover); got != "COVER-DATA" {
		t.Errorf("封面 = %q", got)
	}
	if d.CoverExt != "jpg" {
		t.Errorf("CoverExt = %q", d.CoverExt)
	}

	queries := calls.all()
	if len(queries) != 1 {
		t.Fatalf("playurl 请求数 = %d", len(queries))
	}
	// playurl 统一 fnval=80，不区分大会员，也不为判定大会员请求 nav。
	if q := queries[0]; q.Get("fnval") != "80" || q.Get("qn") != "127" || q.Get("bvid") != "BV1xx411c7mD" || q.Get("cid") != "111" {
		t.Errorf("playurl 参数 = %v", q)
	}
	if tr.countRequests("/x/web-interface/nav") != 0 {
		t.Error("下载不应请求 nav")
	}
	// 不选 FLAC 和杜比，即使它们存在。
	if tr.countRequests("/upgcxcode/flac.flac") != 0 || tr.countRequests("/upgcxcode/dolby.m4s") != 0 {
		t.Errorf("下载了 FLAC 或杜比音轨：%+v", tr.requests())
	}
	audioReq := lastRequest(tr, "/upgcxcode/audio30280.m4s")
	if audioReq == nil {
		t.Fatalf("没有下载 30280 音轨：%+v", tr.requests())
	}
	if audioReq.scheme != "https" || audioReq.host != "upos-sz-mirrorcos.bilivideo.com" {
		t.Errorf("音频请求地址 = %s://%s", audioReq.scheme, audioReq.host)
	}
	if audioReq.cookie != "SESSDATA=sess123; bili_jct=jct456" || audioReq.referer != "https://www.bilibili.com/" {
		t.Errorf("音频请求头 = %+v", audioReq)
	}
	// 完整文件的 GET 会被部分 CDN 节点（EdgeOne）拦截，Range 请求会回源放行。
	if audioReq.range_ != "bytes=0-" {
		t.Errorf("音频请求没有带 Range 头：%+v", audioReq)
	}
	if coverReq := lastRequest(tr, "/bfs/archive/cover.jpg"); coverReq == nil || coverReq.referer != "https://www.bilibili.com/" {
		t.Errorf("封面请求 = %+v", coverReq)
	} else if coverReq.range_ != "bytes=0-" {
		t.Errorf("封面请求没有带 Range 头：%+v", coverReq)
	}

	audioPath := linkFilePath(t, d.Audio)
	coverPath := linkFilePath(t, d.Cover)
	if _, err := os.Stat(audioPath); err != nil {
		t.Fatalf("音频文件不在磁盘上：%v", err)
	}
	release()
	for _, path := range []string{audioPath, coverPath} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("release 之后 %s 仍存在（%v）", path, err)
		}
	}
	obs.check(t,
		"bilibili:download_pagelist:ok",
		"bilibili:download_view:ok",
		"bilibili:download_playurl:ok",
		"bilibili:download_audio:ok",
		"bilibili:download_cover:ok",
	)
}

func TestDownloadWithoutCredential(t *testing.T) {
	calls := &playurlCalls{}
	c, tr, _, _ := serve(t, baseDownloadRoutes(calls, func(url.Values) string { return dashResponse }))

	d, release, err := c.Download(t.Context(), "BV1xx411c7mD")
	if err != nil {
		t.Fatal(err)
	}
	release()
	if got := calls.all()[0].Get("fnval"); got != "80" {
		t.Errorf("没有凭据时的 fnval = %q，期望 80", got)
	}
	if tr.countRequests("/x/web-interface/nav") != 0 {
		t.Error("没有凭据时不应请求 nav")
	}
	for _, r := range tr.requests() {
		if r.cookie != "" {
			t.Errorf("没有凭据时不应带 Cookie：%+v", r)
		}
	}
	if d.Cover == nil {
		t.Error("封面缺失")
	}
}

func TestDownloadDurlFallback(t *testing.T) {
	calls := &playurlCalls{}
	c, tr, _, _ := serve(t, baseDownloadRoutes(calls, func(q url.Values) string {
		if q.Get("fnval") == "0" {
			return `{"code":0,"message":"0","data":{"durl":[{"url":"https://upos-sz-mirrorcos.bilivideo.com/upgcxcode/low.mp4?sig=SIGLOW"}]}}`
		}
		return `{"code":0,"message":"0","data":{"dash":{"audio":[],
"flac":{"audio":{"id":30251,"baseUrl":"https://upos-sz-mirrorcos.bilivideo.com/upgcxcode/flac.flac?sig=SIGFLAC"}},
"dolby":{"audio":[{"id":30250,"baseUrl":"https://upos-sz-mirrorcos.bilivideo.com/upgcxcode/dolby.m4s?sig=SIGDOLBY"}]}}}}`
	}))

	d, release, err := c.Download(t.Context(), "BV1xx411c7mD")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if got := readLinkFile(t, d.Audio); got != "LOW-DATA" {
		t.Errorf("回退下载的内容 = %q，期望 LOW-DATA", got)
	}
	queries := calls.all()
	if len(queries) != 2 {
		t.Fatalf("playurl 请求数 = %d，期望 2", len(queries))
	}
	if queries[0].Get("fnval") == "0" || queries[1].Get("fnval") != "0" || queries[1].Get("qn") != "16" {
		t.Errorf("回退请求参数不对：%v", queries)
	}
	if tr.countRequests("/upgcxcode/flac.flac") != 0 || tr.countRequests("/upgcxcode/dolby.m4s") != 0 {
		t.Error("回退时下载了 FLAC 或杜比音轨")
	}
}

func TestDownloadNoStreams(t *testing.T) {
	calls := &playurlCalls{}
	c, _, _, _ := serve(t, baseDownloadRoutes(calls, func(url.Values) string {
		return `{"code":0,"message":"0","data":{}}`
	}))
	_, release, err := c.Download(t.Context(), "BV1xx411c7mD")
	if release != nil {
		release()
	}
	var rejected *port.RejectedError
	if err == nil || errors.As(err, &rejected) {
		t.Fatalf("没有任何流 = %v，期望普通错误", err)
	}
}

func TestDownloadPlayurlRejected(t *testing.T) {
	calls := &playurlCalls{}
	c, _, _, _ := serve(t, baseDownloadRoutes(calls, func(url.Values) string {
		return `{"code":-403,"message":"地区限制","data":{}}`
	}))
	_, release, err := c.Download(t.Context(), "BV1xx411c7mD")
	if release != nil {
		release()
	}
	var rejected *port.RejectedError
	if !errors.As(err, &rejected) || !strings.Contains(err.Error(), "地区限制") {
		t.Fatalf("地区限制 = %v，期望带平台提示的 RejectedError", err)
	}
}

// view 被风控返回 -404 时降级使用 pagelist 的元数据，下载继续。
func TestDownloadViewFailureFallsBack(t *testing.T) {
	calls := &playurlCalls{}
	routes := baseDownloadRoutes(calls, func(url.Values) string { return dashResponse })
	routes["/x/web-interface/view"] = func(w http.ResponseWriter, r *http.Request) {
		respondJSON(w, `{"code":-404,"message":"啥都木有","data":null}`)
	}
	c, tr, obs, _ := serve(t, routes)
	d, release, err := c.Download(t.Context(), "BV1xx411c7mD")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if d.Meta.Title != "BV1xx411c7mD" || d.Meta.Artist != "" {
		t.Errorf("降级后的元数据 = %+v，期望标题为 BV 号、歌手为空", d.Meta)
	}
	if d.Cover != nil {
		t.Errorf("降级后不应有封面")
	}
	if tr.countRequests("/x/player/playurl") != 1 {
		t.Error("view 失败后应继续下载")
	}
	obs.check(t,
		"bilibili:download_pagelist:ok",
		"bilibili:download_view:error",
		"bilibili:download_playurl:ok",
		"bilibili:download_audio:ok",
	)
}

func TestDownloadRedirectToAllowedHost(t *testing.T) {
	calls := &playurlCalls{}
	routes := baseDownloadRoutes(calls, func(url.Values) string {
		return `{"code":0,"message":"0","data":{"dash":{"audio":[{"id":30280,"baseUrl":"https://upos-sz-mirrorcos.bilivideo.com/upgcxcode/redirect.m4s?sig=SIG80"}]}}}`
	})
	routes["/upgcxcode/redirect.m4s"] = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "https://upos-sz-mirrorali.bilivideo.com/upgcxcode/audio30280.m4s?sig=SIG80B")
		w.WriteHeader(http.StatusFound)
	}
	c, tr, _, _ := serve(t, routes)
	d, release, err := c.Download(t.Context(), "BV1xx411c7mD")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if got := readLinkFile(t, d.Audio); got != "AUDIO-DATA" {
		t.Errorf("音频 = %q", got)
	}
	if tr.countRequests("/upgcxcode/redirect.m4s") != 1 || tr.countRequests("/upgcxcode/audio30280.m4s") != 1 {
		t.Errorf("跳转没有被跟随：%+v", tr.requests())
	}
}

func TestDownloadRedirectOutsideRejected(t *testing.T) {
	calls := &playurlCalls{}
	routes := baseDownloadRoutes(calls, func(url.Values) string {
		return `{"code":0,"message":"0","data":{"dash":{"audio":[{"id":30280,"baseUrl":"https://upos-sz-mirrorcos.bilivideo.com/upgcxcode/redirect.m4s?sig=SIG80"}]}}}`
	})
	routes["/upgcxcode/redirect.m4s"] = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "https://evil.example/upgcxcode/evil.m4s?sig=SIGREDIRECT")
		w.WriteHeader(http.StatusFound)
	}
	c, tr, _, _ := serve(t, routes)
	_, release, err := c.Download(t.Context(), "BV1xx411c7mD")
	if release != nil {
		release()
	}
	if err == nil {
		t.Fatal("跳转到站外地址没有返回错误")
	}
	var rejected *port.RejectedError
	if errors.As(err, &rejected) {
		t.Fatalf("跳转校验失败不应是 RejectedError：%v", err)
	}
	for _, secret := range []string{"SIGREDIRECT", "SIG80", "evil.m4s"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("错误泄露了 %q：%v", secret, err)
		}
	}
	if tr.countRequests("/upgcxcode/evil.m4s") != 0 {
		t.Errorf("跟随了到站外的跳转：%+v", tr.requests())
	}
}

func TestDownloadProtocolRelativeCover(t *testing.T) {
	calls := &playurlCalls{}
	routes := baseDownloadRoutes(calls, func(url.Values) string { return dashResponse })
	routes["/x/web-interface/view"] = func(w http.ResponseWriter, r *http.Request) {
		respondJSON(w, `{"code":0,"message":"0","data":{"bvid":"BV1xx411c7mD","title":"视频标题","pic":"//i0.hdslb.com/bfs/archive/cover.jpg","owner":{"name":"UP主名字","mid":99}}}`)
	}
	c, tr, _, _ := serve(t, routes)
	d, release, err := c.Download(t.Context(), "BV1xx411c7mD")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if d.Cover == nil {
		t.Fatal("协议相对封面地址没有升级成 HTTPS 后下载")
	}
	if got := readLinkFile(t, d.Cover); got != "COVER-DATA" {
		t.Errorf("封面 = %q", got)
	}
	if d.CoverExt != "jpg" {
		t.Errorf("CoverExt = %q", d.CoverExt)
	}
	if r := lastRequest(tr, "/bfs/archive/cover.jpg"); r == nil || r.scheme != "https" {
		t.Errorf("封面请求 = %+v，期望 https", r)
	}
}

func TestDownloadAudioHostNotAllowed(t *testing.T) {
	calls := &playurlCalls{}
	c, tr, _, _ := serve(t, baseDownloadRoutes(calls, func(url.Values) string {
		return `{"code":0,"message":"0","data":{"dash":{"audio":[{"id":30280,"baseUrl":"https://evil.example/upgcxcode/x.m4s?sig=SIGX"}]}}}`
	}))
	_, release, err := c.Download(t.Context(), "BV1xx411c7mD")
	if release != nil {
		release()
	}
	var rejected *port.RejectedError
	if err == nil || errors.As(err, &rejected) {
		t.Fatalf("站外音频地址 = %v，期望普通错误", err)
	}
	if strings.Contains(err.Error(), "SIGX") {
		t.Fatalf("错误包含签名地址：%v", err)
	}
	if tr.countRequests("/upgcxcode/x.m4s") != 0 {
		t.Error("不应请求站外地址")
	}
}

func TestDownloadAudioFailureSanitized(t *testing.T) {
	calls := &playurlCalls{}
	routes := baseDownloadRoutes(calls, func(url.Values) string {
		return `{"code":0,"message":"0","data":{"dash":{"audio":[{"id":30280,"baseUrl":"https://upos-sz-mirrorcos.bilivideo.com/upgcxcode/audio30280.m4s?sig=SECRETSIG"}]}}}`
	})
	routes["/upgcxcode/audio30280.m4s"] = func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}
	c, _, _, creds := serve(t, routes)
	creds.set(t, "SESSDATA=sess123", "refresh789")

	_, release, err := c.Download(t.Context(), "BV1xx411c7mD")
	if release != nil {
		release()
	}
	if err == nil {
		t.Fatal("音频 403 没有返回错误")
	}
	for _, secret := range []string{"SECRETSIG", "sess123", "refresh789", "audio30280.m4s"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("错误泄露了 %q：%v", secret, err)
		}
	}
	var rejected *port.RejectedError
	if errors.As(err, &rejected) {
		t.Fatalf("下载失败不应是 RejectedError：%v", err)
	}
}

func TestDownloadSchemeUpgrade(t *testing.T) {
	calls := &playurlCalls{}
	c, tr, _, _ := serve(t, baseDownloadRoutes(calls, func(url.Values) string {
		return `{"code":0,"message":"0","data":{"dash":{"audio":[{"id":30280,"baseUrl":"http://upos-sz-mirrorcos.bilivideo.com/upgcxcode/audio30280.m4s?sig=SIG80"}]}}}`
	}))
	d, release, err := c.Download(t.Context(), "BV1xx411c7mD")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if got := readLinkFile(t, d.Audio); got != "AUDIO-DATA" {
		t.Errorf("音频 = %q", got)
	}
	if r := lastRequest(tr, "/upgcxcode/audio30280.m4s"); r == nil || r.scheme != "https" {
		t.Errorf("http 地址没有升级成 https：%+v", r)
	}
}

func TestDownloadCoverSkipped(t *testing.T) {
	for name, pic := range map[string]string{
		"没有封面":   ``,
		"不支持的格式": `https://i0.hdslb.com/bfs/archive/cover.gif`,
		"站外封面":   `https://evil.example/cover.jpg`,
	} {
		t.Run(name, func(t *testing.T) {
			calls := &playurlCalls{}
			routes := baseDownloadRoutes(calls, func(url.Values) string { return dashResponse })
			routes["/x/web-interface/view"] = func(w http.ResponseWriter, r *http.Request) {
				if pic == "" {
					respondJSON(w, `{"code":0,"message":"0","data":{"bvid":"BV1xx411c7mD","title":"视频标题","pic":"","owner":{"name":"UP主名字","mid":99}}}`)
				} else {
					respondJSON(w, `{"code":0,"message":"0","data":{"bvid":"BV1xx411c7mD","title":"视频标题","pic":`+strconv.Quote(pic)+`,"owner":{"name":"UP主名字","mid":99}}}`)
				}
			}
			c, tr, _, _ := serve(t, routes)
			d, release, err := c.Download(t.Context(), "BV1xx411c7mD")
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			if d.Cover != nil || d.CoverExt != "" {
				t.Errorf("封面 = %v, %q，期望都为空", d.Cover != nil, d.CoverExt)
			}
			if tr.countRequests("/bfs/archive/cover.gif") != 0 || tr.countRequests("/cover.jpg") != 0 {
				t.Errorf("不应下载封面：%+v", tr.requests())
			}
		})
	}
}

func TestDownloadCoverFailure(t *testing.T) {
	calls := &playurlCalls{}
	routes := baseDownloadRoutes(calls, func(url.Values) string { return dashResponse })
	routes["/bfs/archive/cover.jpg"] = func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}
	c, _, _, _ := serve(t, routes)
	before := countDirs(t, c.root)
	_, release, err := c.Download(t.Context(), "BV1xx411c7mD")
	if release != nil {
		release()
	}
	if err == nil {
		t.Fatal("封面下载失败时没有返回错误")
	}
	if after := countDirs(t, c.root); after != before {
		t.Fatalf("失败后留下了临时目录：之前 %d 个，之后 %d 个", before, after)
	}
}

// 下载的中转文件必须建在调用方给出的目录里，进程崩溃留下的残留才能被 worker 启动时的清理删除。
func TestDownloadUnderGivenRoot(t *testing.T) {
	calls := &playurlCalls{}
	c, _, _, _ := serve(t, baseDownloadRoutes(calls, func(url.Values) string { return dashResponse }))
	d, release, err := c.Download(t.Context(), "BV1xx411c7mD")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if path := linkFilePath(t, d.Audio); !strings.HasPrefix(path, c.root) {
		t.Errorf("音频文件 %s 不在工作目录 %s 内", path, c.root)
	}
}

// 不下载的进程（serve）以空目录构造；误调用 Download 应当显式失败，而不是把文件写进系统临时目录。
func TestDownloadWithoutRoot(t *testing.T) {
	c, tr, _, _ := serve(t, baseDownloadRoutes(&playurlCalls{}, func(url.Values) string { return dashResponse }))
	noRoot := *c
	noRoot.root = ""
	_, release, err := noRoot.Download(t.Context(), "BV1xx411c7mD")
	if release != nil {
		release()
	}
	if err == nil || !strings.Contains(err.Error(), "下载目录") {
		t.Fatalf("没有下载目录 = %v，期望显式错误", err)
	}
	if len(tr.requests()) != 0 {
		t.Errorf("不应请求平台：%+v", tr.requests())
	}
}

func TestDownloadInvalidID(t *testing.T) {
	c, tr, _, _ := serve(t, nil)
	if _, release, err := c.Download(t.Context(), "nonsense"); err == nil || release != nil {
		t.Fatalf("非法 ID = %v, release 非空 %v", err, release != nil)
	}
	if len(tr.requests()) != 0 {
		t.Fatalf("非法 ID 不应请求平台，请求了 %+v", tr.requests())
	}
}

// baseUrl 指向的节点（比如 EdgeOne 承载的镜像）会拦截下载，同一条流的 backup_url 仍可用。
func TestDownloadFallsBackToBackupNode(t *testing.T) {
	calls := &playurlCalls{}
	routes := baseDownloadRoutes(calls, func(url.Values) string {
		return `{"code":0,"message":"0","data":{"dash":{"audio":[{"id":30280,` +
			`"baseUrl":"https://upos-sz-mirrorcosov.bilivideo.com/upgcxcode/blocked.m4s?sig=SIGBLOCKED",` +
			`"base_url":"https://upos-sz-mirrorcosov.bilivideo.com/upgcxcode/blocked.m4s?sig=SIGBLOCKED",` +
			`"backup_url":["https://upos-sz-mirrorcos.bilivideo.com/upgcxcode/audio30280.m4s?sig=SIG80"]}]}}}`
	})
	routes["/upgcxcode/blocked.m4s"] = func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusForbidden) }
	c, tr, _, _ := serve(t, routes)
	d, release, err := c.Download(t.Context(), "BV1xx411c7mD")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if got := readLinkFile(t, d.Audio); got != "AUDIO-DATA" {
		t.Errorf("音频 = %q", got)
	}
	if tr.countRequests("/upgcxcode/blocked.m4s") != 1 || tr.countRequests("/upgcxcode/audio30280.m4s") != 1 {
		t.Errorf("应先试 baseUrl 再换备用节点：%+v", tr.requests())
	}
}

// 所有候选节点都失败时才返回错误。
func TestDownloadAllNodesFail(t *testing.T) {
	calls := &playurlCalls{}
	routes := baseDownloadRoutes(calls, func(url.Values) string {
		return `{"code":0,"message":"0","data":{"dash":{"audio":[{"id":30280,` +
			`"baseUrl":"https://upos-sz-mirrorcosov.bilivideo.com/upgcxcode/blocked.m4s?sig=SIGA",` +
			`"backup_url":["https://upos-sz-mirrorcos.bilivideo.com/upgcxcode/blocked2.m4s?sig=SIGB"]}]}}}`
	})
	routes["/upgcxcode/blocked.m4s"] = func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusForbidden) }
	routes["/upgcxcode/blocked2.m4s"] = func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusForbidden) }
	c, tr, _, _ := serve(t, routes)
	_, release, err := c.Download(t.Context(), "BV1xx411c7mD")
	if release != nil {
		release()
	}
	if err == nil {
		t.Fatal("所有节点失败应返回错误")
	}
	if tr.countRequests("/upgcxcode/blocked.m4s") != 1 || tr.countRequests("/upgcxcode/blocked2.m4s") != 1 {
		t.Errorf("应尝试全部候选节点：%+v", tr.requests())
	}
	for _, secret := range []string{"SIGA", "SIGB", "blocked.m4s", "blocked2.m4s"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("错误泄露了 %q：%v", secret, err)
		}
	}
}

// durl 兜底同样有备用地址。
func TestDownloadDurlFallsBackToBackupNode(t *testing.T) {
	calls := &playurlCalls{}
	routes := baseDownloadRoutes(calls, func(q url.Values) string {
		if q.Get("fnval") == "0" {
			return `{"code":0,"message":"0","data":{"durl":[{` +
				`"url":"https://upos-sz-mirrorcosov.bilivideo.com/upgcxcode/blocked.mp4?sig=SIGLOW",` +
				`"backup_url":["https://upos-sz-mirrorcos.bilivideo.com/upgcxcode/low.mp4?sig=SIGLOW2"]}]}}`
		}
		return `{"code":0,"message":"0","data":{"dash":{"audio":[]}}}`
	})
	routes["/upgcxcode/blocked.mp4"] = func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusForbidden) }
	c, tr, _, _ := serve(t, routes)
	d, release, err := c.Download(t.Context(), "BV1xx411c7mD")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if got := readLinkFile(t, d.Audio); got != "LOW-DATA" {
		t.Errorf("音频 = %q", got)
	}
	if tr.countRequests("/upgcxcode/blocked.mp4") != 1 || tr.countRequests("/upgcxcode/low.mp4") != 1 {
		t.Errorf("durl 应先试 url 再换备用地址：%+v", tr.requests())
	}
}

// 节点对 Range 请求返回 206 Partial Content。
func TestDownloadAcceptsPartialContent(t *testing.T) {
	calls := &playurlCalls{}
	routes := baseDownloadRoutes(calls, func(url.Values) string { return dashResponse })
	routes["/upgcxcode/audio30280.m4s"] = func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "bytes=0-" {
			t.Error("下载没有带 Range 头")
		}
		w.Header().Set("Content-Range", "bytes 0-9/10")
		w.Header().Set("Content-Length", "10")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = io.WriteString(w, "AUDIO-DATA")
	}
	c, _, _, _ := serve(t, routes)
	d, release, err := c.Download(t.Context(), "BV1xx411c7mD")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if got := readLinkFile(t, d.Audio); got != "AUDIO-DATA" {
		t.Errorf("音频 = %q", got)
	}
}
