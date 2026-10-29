package ytdlp

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/app/port/porttest"
)

// 地址用 media.test，只有测试的代理能把它解析到本机。
func startPaths(t *testing.T, routes map[string]http.Handler) (link func(path string) string, hits func(path string) int) {
	t.Helper()
	var mu sync.Mutex
	counts := map[string]int{}
	mux := http.NewServeMux()
	for pattern, handler := range routes {
		mux.Handle(pattern, handler)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		counts[r.URL.Path]++
		mu.Unlock()
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	_, port, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	base := "http://media.test:" + port
	return func(path string) string { return base + path }, func(path string) int {
		mu.Lock()
		defer mu.Unlock()
		return counts[path]
	}
}

func encodeAAC(t *testing.T, path string, extra ...string) {
	t.Helper()
	args := slices.Concat([]string{"-y", "-loglevel", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=1", "-c:a", "aac"}, extra, []string{path})
	if out, err := exec.CommandContext(t.Context(), "ffmpeg", args...).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg 生成 %s：%v\n%s", filepath.Base(path), err, out)
	}
}

func hlsSite(t *testing.T) (link func(path string) string, hits func(path string) int) {
	t.Helper()
	dir := t.TempDir()
	encodeAAC(t, filepath.Join(dir, "index.m3u8"), "-f", "hls", "-hls_time", "4", "-hls_playlist_type", "vod", "-hls_segment_filename", filepath.Join(dir, "seg%d.ts"))
	files := http.FileServer(http.Dir(dir))
	return startPaths(t, map[string]http.Handler{"/": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".m3u8") {
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		}
		files.ServeHTTP(w, r)
	})})
}

// 写出一份 yt-dlp 的 info.json，让它直接下载 format 描述的这一个格式，不经过 extractor。
func writeInfo(t *testing.T, format map[string]any) string {
	t.Helper()
	info := map[string]any{
		"_type": "video", "id": "x", "title": "x", "extractor": "generic", "extractor_key": "Generic",
		"vcodec": "none", "acodec": "aac", "format_id": "x", "http_headers": map[string]string{"User-Agent": "x"},
	}
	for k, v := range format {
		info[k] = v
	}
	b, err := json.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "info.json")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func verbose(t *testing.T, link string, extra ...string) (stderr []byte, err error) {
	t.Helper()
	p := siteProxy(t)
	dir := t.TempDir()
	_, stderr, err = run(t.Context(), dir, slices.Concat([]string{"--verbose"}, extra, args(link, dir, p.url, "")))
	return stderr, err
}

// 不包括 ffprobe：yt-dlp 不给它传后处理器的参数。
func ffmpegCommands(stderr []byte) [][]string {
	const marker = "[debug] ffmpeg command line: "
	var commands [][]string
	rest := string(stderr)
	for {
		_, after, ok := strings.Cut(rest, marker)
		if !ok {
			return commands
		}
		words, n := shellWords(after)
		rest = after[n:]
		if len(words) > 0 && words[0] == "ffmpeg" {
			commands = append(commands, words)
		}
	}
}

// 读到第一个不在引号内的换行为止，按空白拆成参数；单引号内的内容（可以跨行）属于同一个参数。返回参数和消耗的字节数。
func shellWords(s string) ([]string, int) {
	var words []string
	var word strings.Builder
	inWord, quoted := false, false
	flush := func() {
		if inWord {
			words = append(words, word.String())
			word.Reset()
			inWord = false
		}
	}
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '\'':
			quoted, inWord = !quoted, true
		case quoted:
			word.WriteByte(c)
		case c == '\n':
			flush()
			return words, i + 1
		case c == ' ':
			flush()
		default:
			word.WriteByte(c)
			inWord = true
		}
	}
	flush()
	return words, len(s)
}

func checkLocalOnly(t *testing.T, commands [][]string) {
	t.Helper()
	for _, argv := range commands {
		input, whitelist := slices.Index(argv, "-i"), slices.Index(argv, "-protocol_whitelist")
		if input < 0 || whitelist < 0 || whitelist > input || argv[whitelist+1] != "file" {
			t.Errorf("ffmpeg 的命令行没有在输入之前把协议限制为 file：%q", argv)
		}
	}
}

func commandWith(commands [][]string, words ...string) []string {
	for _, argv := range commands {
		if !slices.ContainsFunc(words, func(w string) bool { return !slices.Contains(argv, w) }) {
			return argv
		}
	}
	return nil
}

func TestPostprocessorsReadLocalFilesOnly(t *testing.T) {
	t.Run("extract_audio", func(t *testing.T) {
		_, _, link := startSite(t)
		stderr, err := verbose(t, link("/page"))
		if err != nil {
			t.Fatalf("yt-dlp 失败：%v\n%s", err, stderr)
		}
		commands := ffmpegCommands(stderr)
		if commandWith(commands, "libmp3lame") == nil {
			t.Fatalf("没有转 mp3 的 ffmpeg 命令行：\n%s", stderr)
		}
		checkLocalOnly(t, commands)
	})

	t.Run("fixup_m3u8", func(t *testing.T) {
		link, _ := hlsSite(t)
		stderr, err := verbose(t, link("/index.m3u8"))
		if err != nil {
			t.Fatalf("yt-dlp 失败：%v\n%s", err, stderr)
		}
		commands := ffmpegCommands(stderr)
		if commandWith(commands, "aac_adtstoasc") == nil || commandWith(commands, "libmp3lame") == nil {
			t.Fatalf("期望 FixupM3u8 和转 mp3 各有一条 ffmpeg 命令行：\n%s", stderr)
		}
		checkLocalOnly(t, commands)
	})

	t.Run("fixup_m4a", func(t *testing.T) {
		dir := t.TempDir()
		encodeAAC(t, filepath.Join(dir, "song.m4a"))
		link, _ := startPaths(t, map[string]http.Handler{"/": http.FileServer(http.Dir(dir))})
		info := writeInfo(t, map[string]any{"url": link("/song.m4a"), "webpage_url": link("/page"), "ext": "m4a", "protocol": "http", "container": "m4a_dash"})
		stderr, err := verbose(t, link("/page"), "--load-info-json", info)
		if err != nil {
			t.Fatalf("yt-dlp 失败：%v\n%s", err, stderr)
		}
		commands := ffmpegCommands(stderr)
		if commandWith(commands, "copy", "mp4") == nil || commandWith(commands, "libmp3lame") == nil {
			t.Fatalf("期望 FixupM4a 和转 mp3 各有一条 ffmpeg 命令行：\n%s", stderr)
		}
		checkLocalOnly(t, commands)
	})
}

// 原生下载器处理不了 SAMPLE-AES 加密的 HLS，yt-dlp 会交给 ffmpeg 下载。
func TestFFmpegDownloaderReadsLocalFilesOnly(t *testing.T) {
	playlist := "#EXTM3U\n#EXT-X-VERSION:5\n#EXT-X-TARGETDURATION:2\n#EXT-X-MEDIA-SEQUENCE:0\n" +
		"#EXT-X-KEY:METHOD=SAMPLE-AES,URI=\"key.bin\"\n#EXTINF:2.0,\nseg.ts\n#EXT-X-ENDLIST\n"
	link, hits := startPaths(t, map[string]http.Handler{
		"/enc.m3u8": http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			_, _ = io.WriteString(w, playlist)
		}),
		"/key.bin": http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(make([]byte, 16)) }),
		"/seg.ts":  http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(make([]byte, 188*4)) }),
	})
	stderr, err := verbose(t, link("/enc.m3u8"))
	if err == nil {
		t.Errorf("ffmpeg 读不了 http 的播放列表，yt-dlp 应当失败\n%s", stderr)
	}
	commands := ffmpegCommands(stderr)
	if len(commands) == 0 {
		t.Fatalf("yt-dlp 没有把这个流交给 ffmpeg 下载，需要换一个原生下载器处理不了的流：\n%s", stderr)
	}
	checkLocalOnly(t, commands)
	for _, path := range []string{"/seg.ts", "/key.bin"} {
		if n := hits(path); n != 0 {
			t.Errorf("%s 收到 %d 个请求，期望 ffmpeg 没有读它", path, n)
		}
	}
}

// 在 WAV 的 fmt 块和 data 块之间插入 LIST/INFO 块，INAM 是标题：ffmpeg 转码时会把它复制成 mp3 的 ID3 标签。
func taggedWAV(title string) []byte {
	wav := porttest.SilentWAV(time.Second)
	const dataChunk = 36
	info := append([]byte("INFOINAM"), binary.LittleEndian.AppendUint32(nil, uint32(len(title)+1))...)
	info = append(append(info, title...), 0)
	if len(info)%2 == 1 {
		info = append(info, 0)
	}
	list := append([]byte("LIST"), binary.LittleEndian.AppendUint32(nil, uint32(len(info)))...)
	out := slices.Concat(wav[:dataChunk], list, info, wav[dataChunk:])
	binary.LittleEndian.PutUint32(out[4:], uint32(len(out)-8))
	return out
}

func TestAudioDropsSourceTags(t *testing.T) {
	const markup = "<script>alert(2)</script>"
	source := taggedWAV(markup)
	path := filepath.Join(t.TempDir(), "tagged.wav")
	if err := os.WriteFile(path, source, 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.CommandContext(t.Context(), "ffprobe", "-v", "error", "-show_entries", "format_tags=title", "-of", "default=noprint_wrappers=1:nokey=1", path).Output(); err != nil || !strings.Contains(string(out), markup) {
		t.Fatalf("测试用的 WAV 没有被 ffprobe 读出标题：%q, %v", out, err)
	}

	link, _ := startPaths(t, map[string]http.Handler{
		"/page": http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, `<!doctype html><html><head><title>标签</title></head><body><audio src="/tagged.wav"></audio></body></html>`)
		}),
		"/tagged.wav": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "audio/wav")
			http.ServeContent(w, r, "tagged.wav", time.Time{}, bytes.NewReader(source))
		}),
	})
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
	audio := readFile(t, d.Audio)
	if !isMP3(audio) {
		t.Fatalf("audio.mp3 的开头 %x 不是 MP3", audio[:min(len(audio), 4)])
	}
	if bytes.Contains(audio, []byte("alert(2)")) {
		t.Errorf("mp3 中还带着来源文件的标题标签")
	}
}

func TestConfigFilesAreIgnored(t *testing.T) {
	executed := filepath.Join(t.TempDir(), "executed")
	config := t.TempDir()
	conf := "--proxy \"\"\n--exec \"touch " + executed + "\"\n"
	if err := os.WriteFile(filepath.Join(config, "yt-dlp.conf"), []byte(conf), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", config)

	_, _, link := startSite(t)
	p := siteProxy(t)
	dir := t.TempDir()
	// media.test 只有测试的代理能解析：下载成功说明请求经过了命令行指定的代理。
	if _, stderr, err := run(t.Context(), dir, args(link("/page"), dir, p.url, "")); err != nil {
		t.Fatalf("yt-dlp 失败：%v\n%s", err, stderr)
	}
	if _, err := os.Stat(executed); err == nil {
		t.Error("配置文件里的 --exec 被执行了")
	}

	server, requests, _ := startSite(t)
	o := &observed{}
	_, _, err := New(t.TempDir(), net.DefaultResolver, noCookies{}, o.observe, slog.New(slog.DiscardHandler)).Download(t.Context(), server.URL+"/page")
	var rejected *port.RejectedError
	if !errors.As(err, &rejected) || requests.Load() != 0 {
		t.Errorf("配置文件里有 --proxy \"\" 时下载本机的网站 = %v，网站收到 %d 个请求，期望仍被拒绝", err, requests.Load())
	}
}

// 页面里有多个媒体时，generic extractor 把它们当作播放列表。
func TestOnlyFirstPlaylistItemIsDownloaded(t *testing.T) {
	wav := func(d time.Duration) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "audio/wav")
			http.ServeContent(w, r, "x.wav", time.Time{}, bytes.NewReader(porttest.SilentWAV(d)))
		})
	}
	link, hits := startPaths(t, map[string]http.Handler{
		"/list": http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, `<!doctype html><html><head><title>列表</title></head><body><audio src="/one.wav"></audio><audio src="/two.wav"></audio></body></html>`)
		}),
		"/one.wav": wav(time.Second),
		"/two.wav": wav(2 * time.Second),
	})
	p := siteProxy(t)
	dir := t.TempDir()
	stdout, stderr, err := run(t.Context(), dir, args(link("/list"), dir, p.url, ""))
	if err != nil {
		t.Fatalf("yt-dlp 失败：%v\n%s", err, stderr)
	}
	if _, err := collect(dir, stdout); err != nil {
		t.Errorf("播放列表的输出 %q 应当只有第一项的元数据：%v", stdout, err)
	}
	if n := hits("/two.wav"); n != 0 {
		t.Errorf("播放列表的第二项收到 %d 个请求，期望没有下载", n)
	}
}

func TestHLSFormatsAreDownloadedNatively(t *testing.T) {
	link, hits := hlsSite(t)
	info := writeInfo(t, map[string]any{"url": link("/index.m3u8"), "webpage_url": link("/page"), "ext": "mp4", "protocol": "m3u8"})
	stderr, err := verbose(t, link("/page"), "--load-info-json", info)
	if err != nil {
		t.Fatalf("yt-dlp 失败：%v\n%s", err, stderr)
	}
	if !bytes.Contains(stderr, []byte("Invoking hlsnative downloader")) {
		t.Errorf("期望原生下载器下载 m3u8：\n%s", stderr)
	}
	if n := hits("/seg0.ts"); n == 0 {
		t.Error("分片没有被下载")
	}
}
