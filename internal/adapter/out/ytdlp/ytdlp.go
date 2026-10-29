// worker 会请求用户提交的任意地址，因此 yt-dlp 的网络请求必须经过只连接公网地址的进程内代理；
// ffmpeg 无论作为下载器还是后处理器都只能读取本地文件。
package ytdlp

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/track"
)

const (
	// yt-dlp 从 PATH 中查找，它再从 PATH 中查找 ffmpeg、ffprobe 和 Deno。
	executable  = "yt-dlp"
	waitDelay   = 5 * time.Second
	outputLimit = 64 << 10
	reasonLimit = 300
)

// 下载失败的原因，用作指标的标签。
const (
	failureUnsupported = "unsupported"
	failureBlocked     = "blocked"
	failureTimeout     = "timeout"
	failureError       = "error"
)

type Downloader struct {
	root     string
	resolver *net.Resolver
	cookies  port.LinkCookies
	observe  func(elapsed time.Duration, failure string)
	logger   *slog.Logger
}

var _ port.LinkDownloader = (*Downloader)(nil)

// observe 记下每次运行 yt-dlp 的耗时和失败的原因，成功时 failure 为空；任务被取消（worker 退出）的运行不记。
// cookies 必须非 nil：没有 cookies 来源的调用方传恒返回 ok=false 的实现。
func New(root string, resolver *net.Resolver, cookies port.LinkCookies, observe func(elapsed time.Duration, failure string), logger *slog.Logger) *Downloader {
	return &Downloader{root: root, resolver: resolver, cookies: cookies, observe: observe, logger: logger}
}

func (d *Downloader) Download(ctx context.Context, link string) (port.LinkDownload, func(), error) {
	dir, err := os.MkdirTemp(d.root, "job-")
	if err != nil {
		return port.LinkDownload{}, nil, fmt.Errorf("创建临时目录：%w", err)
	}
	release := func() { _ = os.RemoveAll(dir) }
	// 用 defer 是为了 panic 时也删除目录：River 会恢复任务中的 panic，worker 继续运行。
	done := false
	defer func() {
		if !done {
			release()
		}
	}()
	result, err := d.download(ctx, link, dir)
	if err != nil {
		return port.LinkDownload{}, nil, err
	}
	done = true
	return result, release, nil
}

func (d *Downloader) download(ctx context.Context, link, dir string) (port.LinkDownload, error) {
	cookies, releaseCookies, ok, err := d.cookies.CookieFile(ctx, link)
	if err != nil {
		return port.LinkDownload{}, err
	}
	if ok {
		// cookies 文件只在 yt-dlp 运行期间存在，下载结束（包括失败和取消）就删除。
		defer releaseCookies()
	}
	p, err := startProxy(dialPublic(d.resolver))
	if err != nil {
		return port.LinkDownload{}, fmt.Errorf("启动 yt-dlp 的代理：%w", err)
	}
	defer p.Close()
	start := time.Now()
	stdout, stderr, err := run(ctx, dir, args(link, dir, p.url, cookies))
	var result port.LinkDownload
	if err == nil {
		result, err = collect(dir, stdout)
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return port.LinkDownload{}, ctx.Err()
	}
	failure, err := classify(ctx, err, stderr, p.wasRefused())
	d.observe(time.Since(start), failure)
	switch failure {
	case "":
		return result, nil
	case failureTimeout, failureError:
		d.logger.WarnContext(ctx, "yt-dlp 下载失败", "track_id", link, "failure", failure, "stderr", string(stderr), "error", err)
	}
	return port.LinkDownload{}, err
}

func args(link, dir, proxy, cookies string) []string {
	a := []string{
		// 配置文件可能带入 cookies、--exec 之类的选项。
		"--ignore-config",
		"--proxy", proxy,
		"--downloader", "native",
		// ffmpeg 的请求不经过代理，所以只允许它读本地文件。原生下载器处理不了时（比如加密的 HLS、直播），yt-dlp 会改用 ffmpeg 下载；
		// 后处理器也用 ffmpeg 处理下载到的文件，而文件可能引用网络上的地址。给后处理器的参数要写成「后处理器名+ffmpeg_i」，
		// 不带后处理器名的 ffmpeg_i 只对 ffmpeg 下载器生效。这里列出的后处理器会处理音频文件：转 mp3，以及 HLS 和 DASH 的封装修复。
		"--downloader-args", "ffmpeg_i:-protocol_whitelist file",
		"--postprocessor-args", "ExtractAudio+ffmpeg_i:-protocol_whitelist file",
		"--postprocessor-args", "FixupM3u8+ffmpeg_i:-protocol_whitelist file",
		"--postprocessor-args", "FixupM4a+ffmpeg_i:-protocol_whitelist file",
		// 来源文件里的标签是用户提交的内容，转码时 ffmpeg 默认把它们复制成 mp3 的标签，所以不复制。
		// 来源已经是 mp3 时 yt-dlp 不转码，文件连同标签原样保留，存储的音频仍然不可信。
		"--postprocessor-args", "ExtractAudio+ffmpeg_o:-map_metadata -1",
		// --no-playlist 只对同时指向视频和播放列表的链接有效；对播放列表本身，--playlist-items 1 只下载第一项。
		"--no-playlist", "--playlist-items", "1",
		"--format", "bestaudio/best",
		"--extract-audio", "--audio-format", "mp3",
		"--write-thumbnail",
		"--paths", dir,
		"--output", "audio.%(ext)s",
		"--output", "thumbnail:cover.%(ext)s",
		"--no-progress",
		"--print", "after_move:%(.{title,artist,uploader,album,duration})j",
		// 链接以 - 开头时会被当作选项。
		"--", link,
	}
	if cookies != "" {
		a = slices.Insert(a, 1, "--cookies", cookies)
	}
	return a
}

func run(ctx context.Context, dir string, args []string) (stdout, stderr []byte, err error) {
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Dir = dir
	cmd.Env = environ()
	// yt-dlp 会启动 ffmpeg，超时或取消时杀掉整个进程组。
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = waitDelay
	out, errOut := &tail{limit: outputLimit}, &tail{limit: outputLimit}
	cmd.Stdout, cmd.Stderr = out, errOut
	err = cmd.Run()
	return out.buf, redactCookies(errOut.lines()), err
}

// yt-dlp 读 cookies 文件出错时，会在这些说明后面打印出错的行或值，日志和任务错误里都不能出现。
var cookieEcho = regexp.MustCompile(`(cookie file entry|Netscape format cookies file|could not convert string to float)[^\S\n]*\S[^\n]*`)

func redactCookies(stderr []byte) []byte {
	return cookieEcho.ReplaceAll(stderr, []byte("${1} [REDACTED]"))
}

// yt-dlp 模拟浏览器时用 curl 发请求，而 curl 即使指定了代理，仍会按环境变量 no_proxy 绕过代理，所以去掉环境中的代理设置。
func environ() []string {
	return slices.DeleteFunc(os.Environ(), func(kv string) bool {
		name, _, _ := strings.Cut(kv, "=")
		switch strings.ToLower(name) {
		case "http_proxy", "https_proxy", "all_proxy", "no_proxy":
			return true
		}
		return false
	})
}

type tail struct {
	limit int
	buf   []byte
	cut   bool
}

func (t *tail) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if extra := len(t.buf) - t.limit; extra > 0 {
		t.buf = t.buf[extra:]
		t.cut = true
	}
	return len(p), nil
}

// 截断后开头的半行认不出是不是 cookies 的回显，丢掉。
func (t *tail) lines() []byte {
	if !t.cut {
		return t.buf
	}
	_, rest, _ := bytes.Cut(t.buf, []byte("\n"))
	return rest
}

type info struct {
	Title    string   `json:"title"`
	Artist   string   `json:"artist"`
	Uploader string   `json:"uploader"`
	Album    string   `json:"album"`
	Duration *float64 `json:"duration"`
}

var errNoAudio = errors.New("yt-dlp 没有下载到音频")

func collect(dir string, stdout []byte) (port.LinkDownload, error) {
	audio := filepath.Join(dir, "audio.mp3")
	if _, err := os.Stat(audio); err != nil {
		return port.LinkDownload{}, errNoAudio
	}
	var i info
	if err := json.Unmarshal(bytes.TrimSpace(stdout), &i); err != nil {
		return port.LinkDownload{}, fmt.Errorf("解析 yt-dlp 输出的元数据：%w", err)
	}
	d := port.LinkDownload{
		Meta:  track.Meta{Title: i.Title, Artist: cmp.Or(i.Artist, i.Uploader), Album: i.Album},
		Audio: file(audio),
	}
	if i.Duration != nil {
		d.Meta.Duration = time.Duration(*i.Duration * float64(time.Second))
	}
	if covers, _ := filepath.Glob(filepath.Join(dir, "cover.*")); len(covers) == 1 {
		d.Cover, d.CoverExt = file(covers[0]), strings.ToLower(strings.TrimPrefix(filepath.Ext(covers[0]), "."))
	}
	return d, nil
}

type file string

func (f file) Open() (io.ReadSeekCloser, error) {
	r, err := os.Open(string(f))
	if err != nil {
		return nil, err
	}
	return r, nil
}

func classify(ctx context.Context, err error, stderr []byte, refused bool) (string, error) {
	switch {
	case err == nil:
		return "", nil
	case ctx.Err() != nil:
		return failureTimeout, fmt.Errorf("yt-dlp 下载超时：%w", ctx.Err())
	case refused:
		return failureBlocked, &port.RejectedError{Err: errors.New("链接指向的地址不是公网地址，不能下载")}
	case errors.Is(err, errNoAudio) || bytes.Contains(stderr, []byte("ERROR: Unsupported URL: ")):
		return failureUnsupported, &port.RejectedError{Err: errors.New("yt-dlp 不支持这个链接")}
	}
	if message := lastError(stderr); message != "" {
		return failureError, fmt.Errorf("yt-dlp 下载失败：%s", message)
	}
	return failureError, fmt.Errorf("yt-dlp 下载失败：%w", err)
}

func lastError(stderr []byte) string {
	lines := strings.Split(strings.TrimSpace(string(stderr)), "\n")
	for _, line := range slices.Backward(lines) {
		if message, ok := strings.CutPrefix(line, "ERROR: "); ok {
			if r := []rune(message); len(r) > reasonLimit {
				message = string(r[:reasonLimit]) + "…"
			}
			return message
		}
	}
	return ""
}
