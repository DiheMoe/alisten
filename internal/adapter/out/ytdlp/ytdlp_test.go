package ytdlp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/app/port/porttest"
)

// 网页中的地址都用 media.test，只有测试的代理能把它解析到本机：请求如果绕过代理，就无法连接。
func startSite(t *testing.T) (server *httptest.Server, requests *atomic.Int32, link func(path string) string) {
	t.Helper()
	requests = &atomic.Int32{}
	var base string
	mux := http.NewServeMux()
	mux.HandleFunc("/song.wav", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/wav")
		http.ServeContent(w, r, "song.wav", time.Time{}, bytes.NewReader(porttest.SilentWAV(2*time.Second)))
	})
	mux.HandleFunc("/cover.jpg", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = io.WriteString(w, "jpeg")
	})
	mux.HandleFunc("/page", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprintf(w, `<!doctype html><html><head><title>晴天</title><meta property="og:title" content="晴天">`+
			`<meta property="og:image" content="%[1]s/cover.jpg"></head><body><audio src="%[1]s/song.wav"></audio></body></html>`, base)
	})
	mux.HandleFunc("/empty", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, "<!doctype html><html><head><title>没有媒体</title></head><body>你好</body></html>")
	})
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	_, port, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	base = "http://media.test:" + port
	return server, requests, func(path string) string { return base + path }
}

func siteProxy(t *testing.T) *proxy {
	t.Helper()
	return startTestProxy(t, func(ctx context.Context, network, address string) (net.Conn, error) {
		if host, port, err := net.SplitHostPort(address); err == nil && host == "media.test" {
			address = net.JoinHostPort("127.0.0.1", port)
		}
		return direct(ctx, network, address)
	})
}

func TestYtdlpDownloadsAudioAndCover(t *testing.T) {
	_, requests, link := startSite(t)
	p := siteProxy(t)
	dir := t.TempDir()
	stdout, stderr, err := run(t.Context(), dir, args(link("/page"), dir, p.url, ""))
	if err != nil {
		t.Fatalf("yt-dlp 失败：%v\n%s", err, stderr)
	}
	d, err := collect(dir, stdout)
	if err != nil {
		t.Fatal(err)
	}
	if d.Audio != file(filepath.Join(dir, "audio.mp3")) || d.Cover != file(filepath.Join(dir, "cover.jpg")) || d.CoverExt != "jpg" {
		t.Errorf("下载结果 = %+v", d)
	}
	if !strings.Contains(d.Meta.Title, "晴天") {
		t.Errorf("标题 = %q，期望取自网页", d.Meta.Title)
	}
	audio := readFile(t, d.Audio)
	if !isMP3(audio) {
		t.Errorf("audio.mp3 的开头 %x 不是 MP3", audio[:min(len(audio), 4)])
	}
	if requests.Load() < 3 {
		t.Errorf("网站收到 %d 个请求，期望网页、音频和封面都经代理请求", requests.Load())
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 2 {
		t.Errorf("目录中有 %d 个文件，期望只有音频和封面", len(entries))
	}
}

// MP3 文件以 ID3 标签或 MPEG 帧同步字开头。
func isMP3(b []byte) bool {
	return bytes.HasPrefix(b, []byte("ID3")) || len(b) > 1 && b[0] == 0xFF && b[1]&0xE0 == 0xE0
}

func readFile(t *testing.T, f port.LinkFile) []byte {
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
	return b
}

func TestYtdlpRejectsUnsupportedLink(t *testing.T) {
	_, _, link := startSite(t)
	p := siteProxy(t)
	dir := t.TempDir()
	_, stderr, err := run(t.Context(), dir, args(link("/empty"), dir, p.url, ""))
	failure, err := classify(t.Context(), err, stderr, p.wasRefused())
	var rejected *port.RejectedError
	if failure != failureUnsupported || !errors.As(err, &rejected) {
		t.Errorf("没有媒体的网页 = %q, %v，期望不支持\n%s", failure, err, stderr)
	}
}

type observed struct {
	mu       sync.Mutex
	failures []string
}

func (o *observed) observe(elapsed time.Duration, failure string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if elapsed <= 0 {
		failure += "（没有耗时）"
	}
	o.failures = append(o.failures, failure)
}

func TestDownloadRefusesPrivateAddresses(t *testing.T) {
	server, requests, _ := startSite(t)
	o := &observed{}
	root := t.TempDir()
	_, _, err := New(root, net.DefaultResolver, noCookies{}, o.observe, slog.New(slog.DiscardHandler)).Download(t.Context(), server.URL+"/page")
	var rejected *port.RejectedError
	if !errors.As(err, &rejected) || !strings.Contains(err.Error(), "不是公网地址") {
		t.Errorf("下载本机的地址 = %v，期望拒绝", err)
	}
	if n := requests.Load(); n != 0 {
		t.Errorf("网站收到了 %d 个请求", n)
	}
	if !slices.Equal(o.failures, []string{failureBlocked}) {
		t.Errorf("记下的失败 = %q", o.failures)
	}
	if left, _ := os.ReadDir(root); len(left) != 0 {
		t.Errorf("下载失败之后 %s 中还有 %v", root, left)
	}
}

func fakeYtdlp(t *testing.T, script string) {
	t.Helper()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, executable), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestDownloadKeepsFilesUntilReleased(t *testing.T) {
	fakeYtdlp(t, "#!/bin/sh\nprintf mp3 > audio.mp3\necho '{}'\n")
	root := t.TempDir()
	d, release, err := New(root, net.DefaultResolver, noCookies{}, func(time.Duration, string) {}, slog.New(slog.DiscardHandler)).Download(t.Context(), "https://example.com/page")
	if err != nil {
		t.Fatal(err)
	}
	if jobs, _ := os.ReadDir(root); len(jobs) != 1 || !jobs[0].IsDir() {
		t.Errorf("%s 中有 %v，期望只有这次任务的目录", root, jobs)
	}
	if audio := readFile(t, d.Audio); string(audio) != "mp3" {
		t.Errorf("音频 = %q", audio)
	}
	release()
	if left, _ := os.ReadDir(root); len(left) != 0 {
		t.Errorf("release 之后 %s 中还有 %v", root, left)
	}
}

func TestCancelledDownloadIsNotObserved(t *testing.T) {
	var once sync.Once
	entered := make(chan struct{})
	resolver := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
		once.Do(func() { close(entered) })
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	o := &observed{}
	var logs bytes.Buffer
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, _, err := New(t.TempDir(), resolver, noCookies{}, o.observe, slog.New(slog.NewJSONHandler(&logs, nil))).Download(ctx, "http://hang.test/page")
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(30 * time.Second):
		t.Fatal("yt-dlp 的请求没有到达代理")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Errorf("取消的下载返回 %v，期望 context.Canceled", err)
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.failures) != 0 || logs.Len() != 0 {
		t.Errorf("取消的下载记下了 %q，日志：%s", o.failures, logs.String())
	}
}

func TestImpersonatedRequestsUseProxy(t *testing.T) {
	t.Setenv("no_proxy", "*")
	t.Setenv("NO_PROXY", "*")
	server, requests, _ := startSite(t)
	p := startTestProxy(t, dialPublic(net.DefaultResolver))
	dir := t.TempDir()
	_, stderr, err := run(t.Context(), dir, append([]string{"--impersonate", "chrome"}, args(server.URL+"/page", dir, p.url, "")...))
	if err == nil || requests.Load() != 0 || !p.wasRefused() {
		t.Errorf("模拟浏览器请求本机的网站 = %v，网站收到 %d 个请求，期望被代理拒绝\n%s", err, requests.Load(), stderr)
	}
}

func processesIn(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir("/proc")
	if err != nil {
		t.Fatal(err)
	}
	var pids []string
	for _, e := range entries {
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		if cwd, err := os.Readlink(filepath.Join("/proc", e.Name(), "cwd")); err == nil && cwd == dir {
			pids = append(pids, e.Name())
		}
	}
	return pids
}

func TestRunKillsProcessGroup(t *testing.T) {
	fakeYtdlp(t, "#!/bin/sh\nsleep 300 &\nsleep 300\n")
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	start := time.Now()
	_, _, err := run(ctx, dir, nil)
	if err == nil || time.Since(start) > waitDelay {
		t.Errorf("run = %v，用了 %v，期望超时后立即结束", err, time.Since(start))
	}
	if failure, _ := classify(ctx, err, nil, false); failure != failureTimeout {
		t.Errorf("超时的失败原因 = %q", failure)
	}
	for deadline := time.Now().Add(5 * time.Second); len(processesIn(t, dir)) > 0; time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("超时之后仍有进程 %v", processesIn(t, dir))
		}
	}
}

func TestArgsEndWithLink(t *testing.T) {
	got := args("-x", "/tmp/d", "http://127.0.0.1:1", "")
	if !slices.Equal(got[len(got)-2:], []string{"--", "-x"}) {
		t.Errorf("参数的末尾 = %q，期望链接在 -- 之后", got[len(got)-2:])
	}
}

func TestClassify(t *testing.T) {
	exited := errors.New("exit status 1")
	expired, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	long := strings.Repeat("长", reasonLimit+10)
	tests := []struct {
		ctx      context.Context
		err      error
		stderr   string
		refused  bool
		failure  string
		rejected bool
		message  string
	}{
		{t.Context(), nil, "WARNING: x", false, "", false, ""},
		{expired, exited, "", false, failureTimeout, false, "yt-dlp 下载超时：context deadline exceeded"},
		{t.Context(), exited, "ERROR: [generic] a: HTTP Error 403: Forbidden\n", true, failureBlocked, true, "链接指向的地址不是公网地址，不能下载"},
		{t.Context(), exited, "WARNING: x\nERROR: Unsupported URL: http://example.com/\n", false, failureUnsupported, true, "yt-dlp 不支持这个链接"},
		{t.Context(), errNoAudio, "", false, failureUnsupported, true, "yt-dlp 不支持这个链接"},
		{t.Context(), exited, "ERROR: 第一条\nWARNING: x\nERROR: [youtube] x: Video unavailable\n", false, failureError, false, "yt-dlp 下载失败：[youtube] x: Video unavailable"},
		{t.Context(), exited, "ERROR: " + long, false, failureError, false, "yt-dlp 下载失败：" + strings.Repeat("长", reasonLimit) + "…"},
		{t.Context(), exited, "", false, failureError, false, "yt-dlp 下载失败：exit status 1"},
	}
	for _, tt := range tests {
		failure, err := classify(tt.ctx, tt.err, []byte(tt.stderr), tt.refused)
		var rejected *port.RejectedError
		if failure != tt.failure || errors.As(err, &rejected) != tt.rejected || (err == nil) != (tt.message == "") || (err != nil && err.Error() != tt.message) {
			t.Errorf("classify(%v, %q) = %q, %v，期望 %q, %q", tt.err, tt.stderr, failure, err, tt.failure, tt.message)
		}
	}
}

func TestCollect(t *testing.T) {
	dir := t.TempDir()
	if _, err := collect(dir, []byte(`{"title": "晴天"}`)); !errors.Is(err, errNoAudio) {
		t.Errorf("没有音频时 = %v", err)
	}
	for _, name := range []string{"audio.mp3", "cover.WEBP"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	d, err := collect(dir, []byte("{\"title\": \"晴天\", \"uploader\": \"周杰伦\", \"album\": \"叶惠美\", \"duration\": 269.5}\n"))
	want := port.LinkDownload{Audio: file(filepath.Join(dir, "audio.mp3")), Cover: file(filepath.Join(dir, "cover.WEBP")), CoverExt: "webp"}
	want.Meta.Title, want.Meta.Artist, want.Meta.Album, want.Meta.Duration = "晴天", "周杰伦", "叶惠美", 269500*time.Millisecond
	if err != nil || d != want {
		t.Errorf("collect = %+v, %v\n期望 %+v", d, err, want)
	}
	if d, _ := collect(dir, []byte(`{"title": "晴天", "artist": "周杰伦、方文山", "uploader": "JVR Music"}`)); d.Meta.Artist != "周杰伦、方文山" {
		t.Errorf("有歌手时 Artist = %q，期望取歌手而不是上传者", d.Meta.Artist)
	}
}

func TestTail(t *testing.T) {
	out := &tail{limit: 5}
	for _, s := range []string{"ab\nc", "def\n", "g"} {
		if _, err := io.WriteString(out, s); err != nil {
			t.Fatal(err)
		}
	}
	if string(out.buf) != "def\ng" {
		t.Errorf("保留的输出 = %q，期望末尾的 5 个字节", out.buf)
	}
	if string(out.lines()) != "g" {
		t.Errorf("lines() = %q，期望去掉开头的半行", out.lines())
	}
	whole := &tail{limit: 6}
	if _, err := io.WriteString(whole, "ab\ncd"); err != nil {
		t.Fatal(err)
	}
	if string(whole.lines()) != "ab\ncd" {
		t.Errorf("没有截断时 lines() = %q", whole.lines())
	}
}

func TestRedactCookies(t *testing.T) {
	stderr := "WARNING: skipping cookie file entry due to invalid length 1: 'SID=fictional\\n'\n" +
		"ERROR: '/tmp/a/cookies.txt' does not look like a Netscape format cookies file\n" +
		"WARNING: [youtube] The provided YouTube account cookies are no longer valid.\n" +
		"ValueError: could not convert string to float: 'fictional'\n" +
		"ERROR: invalid Netscape format cookies file '/tmp/a/cookies.txt': 'fictional'"
	want := "WARNING: skipping cookie file entry [REDACTED]\n" +
		"ERROR: '/tmp/a/cookies.txt' does not look like a Netscape format cookies file\n" +
		"WARNING: [youtube] The provided YouTube account cookies are no longer valid.\n" +
		"ValueError: could not convert string to float [REDACTED]\n" +
		"ERROR: invalid Netscape format cookies file [REDACTED]"
	if got := string(redactCookies([]byte(stderr))); got != want {
		t.Errorf("redactCookies =\n%s\n期望\n%s", got, want)
	}
}

type noCookies struct{}

func (noCookies) CookieFile(context.Context, string) (string, func(), bool, error) {
	return "", nil, false, nil
}

type fakeCookies struct {
	path     string
	ok       bool
	err      error
	released int
	asked    int
}

func (f *fakeCookies) CookieFile(_ context.Context, _ string) (string, func(), bool, error) {
	f.asked++
	if f.err != nil {
		return "", nil, false, f.err
	}
	if !f.ok {
		return "", nil, false, nil
	}
	return f.path, func() { f.released++ }, true, nil
}

// fakeYtdlp 把收到的参数写进 args.txt，供测试核对 --cookies。
func recordedArgs(t *testing.T, root string) []string {
	t.Helper()
	jobs, err := os.ReadDir(root)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("读取任务目录 %s：%v %v", root, jobs, err)
	}
	raw, err := os.ReadFile(filepath.Join(root, jobs[0].Name(), "args.txt"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
}

func TestDownloadPassesCookiesFile(t *testing.T) {
	fakeYtdlp(t, "#!/bin/sh\nprintf '%s\\n' \"$@\" > args.txt\nprintf mp3 > audio.mp3\necho '{}'\n")
	root := t.TempDir()
	cookies := &fakeCookies{path: filepath.Join(t.TempDir(), "cookies.txt"), ok: true}
	_, release, err := New(root, net.DefaultResolver, cookies, func(time.Duration, string) {}, slog.New(slog.DiscardHandler)).Download(t.Context(), "https://www.youtube.com/watch?v=dQw4w9WgXcQ")
	if err != nil {
		t.Fatal(err)
	}
	got := recordedArgs(t, root)
	if i := slices.Index(got, "--cookies"); i < 0 || i+1 >= len(got) || got[i+1] != cookies.path {
		t.Errorf("参数中没有 --cookies %s：%q", cookies.path, got)
	}
	if cookies.released != 1 {
		t.Errorf("下载结束调用 release %d 次，期望 1 次", cookies.released)
	}
	release()
	if cookies.released != 1 {
		t.Errorf("任务目录的 release 又调用了 cookies 的 release")
	}
}

func TestDownloadOmitsCookiesWhenNotOffered(t *testing.T) {
	fakeYtdlp(t, "#!/bin/sh\nprintf '%s\\n' \"$@\" > args.txt\nprintf mp3 > audio.mp3\necho '{}'\n")
	root := t.TempDir()
	cookies := &fakeCookies{}
	_, release, err := New(root, net.DefaultResolver, cookies, func(time.Duration, string) {}, slog.New(slog.DiscardHandler)).Download(t.Context(), "https://example.com/page")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if cookies.asked != 1 {
		t.Errorf("CookieFile 被调用 %d 次", cookies.asked)
	}
	if got := recordedArgs(t, root); slices.Contains(got, "--cookies") {
		t.Errorf("不需要 cookies 时传了 --cookies：%q", got)
	}
	if cookies.released != 0 {
		t.Errorf("没有 cookies 时调用了 release %d 次", cookies.released)
	}
}

func TestDownloadReleasesCookiesOnFailure(t *testing.T) {
	fakeYtdlp(t, "#!/bin/sh\necho 'ERROR: unavailable' >&2\nexit 1\n")
	cookies := &fakeCookies{path: filepath.Join(t.TempDir(), "cookies.txt"), ok: true}
	_, _, err := New(t.TempDir(), net.DefaultResolver, cookies, func(time.Duration, string) {}, slog.New(slog.DiscardHandler)).Download(t.Context(), "https://www.youtube.com/watch?v=dQw4w9WgXcQ")
	if err == nil {
		t.Fatal("下载失败应返回错误")
	}
	if cookies.released != 1 {
		t.Errorf("失败后调用 release %d 次，期望 1 次", cookies.released)
	}
}

func TestDownloadFailsWhenCookiesUnavailable(t *testing.T) {
	fakeYtdlp(t, "#!/bin/sh\nprintf '%s\\n' \"$@\" > args.txt\nprintf mp3 > audio.mp3\necho '{}'\n")
	root := t.TempDir()
	cookies := &fakeCookies{err: errors.New("数据库不可用")}
	_, _, err := New(root, net.DefaultResolver, cookies, func(time.Duration, string) {}, slog.New(slog.DiscardHandler)).Download(t.Context(), "https://www.youtube.com/watch?v=dQw4w9WgXcQ")
	if err == nil || !strings.Contains(err.Error(), "数据库不可用") {
		t.Fatalf("读取 cookies 失败应返回错误，得到 %v", err)
	}
	if jobs, _ := os.ReadDir(root); len(jobs) != 0 {
		t.Errorf("失败后 %s 中还有 %v", root, jobs)
	}
}

func TestDownloadRedactsCookiesEchoedByYtdlp(t *testing.T) {
	// 第 2 到 4 行格式不对，yt-dlp 跳过并打印出来，第 3 行长到截断之后的输出从它的半行开始；
	// 最后一行有两层 #HttpOnly_ 前缀，读取失败，报错和 traceback 里都带着它的内容。
	path := filepath.Join(t.TempDir(), "cookies.txt")
	content := "# Netscape HTTP Cookie File\n" +
		"SID=fictional-sid; PREF=f4=4000000\n" +
		"SID=" + strings.Repeat("x", 70000) + "fictional-tail\n" +
		".youtube.com\tTRUE\t/\tTRUE\tfictional-expiry\tHSID\tfictional-hsid\n" +
		"#HttpOnly_#HttpOnly_.youtube.com\tTRUE\t/\tTRUE\tfictional-float\tSSID\tfictional-ssid\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	offline := &net.Resolver{PreferGo: true, Dial: func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("测试不连网")
	}}
	var logs bytes.Buffer
	_, _, err := New(t.TempDir(), offline, &fakeCookies{path: path, ok: true}, func(time.Duration, string) {}, slog.New(slog.NewJSONHandler(&logs, nil))).Download(t.Context(), "http://cookies.test/page")
	if want := "yt-dlp 下载失败：invalid Netscape format cookies file [REDACTED]"; err == nil || err.Error() != want {
		t.Errorf("Download 返回 %v，期望 %q", err, want)
	}
	if strings.Contains(logs.String(), "fictional") || !strings.Contains(logs.String(), "skipping cookie file entry [REDACTED]") {
		t.Errorf("日志没有去掉 cookies 文件的内容：%s", logs.String())
	}
}
