package ffmpeg

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"math"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/app/port/porttest"
)

const markup = "<script>alert(2)</script>"

var discard = slog.New(slog.DiscardHandler)

// 输入用 lavfi 现场生成，只用 ffmpeg 内置的编码器。带视频的输入把视频放在前面，让音轨不是第 0 条流。
var (
	sine  = []string{"-f", "lavfi", "-i", "sine=frequency=440:duration=2"}
	video = []string{"-f", "lavfi", "-i", "testsrc=size=160x120:rate=10:duration=2"}
)

func generate(t *testing.T, name string, args ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	argv := slices.Concat([]string{"-nostdin", "-y", "-loglevel", "error"}, args, []string{path})
	if out, err := exec.CommandContext(t.Context(), "ffmpeg", argv...).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg 生成 %s：%v\n%s", name, err, out)
	}
	return path
}

func source(t *testing.T, path string) port.LinkFile {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return porttest.LinkFile(b)
}

func readAll(t *testing.T, f port.LinkFile) []byte {
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

type probed struct {
	Streams []struct {
		CodecType string `json:"codec_type"`
		CodecName string `json:"codec_name"`
		Channels  int    `json:"channels"`
	} `json:"streams"`
	Chapters []json.RawMessage `json:"chapters"`
	Format   struct {
		Name     string            `json:"format_name"`
		Duration string            `json:"duration"`
		Tags     map[string]string `json:"tags"`
	} `json:"format"`
}

func ffprobe(t *testing.T, path string) probed {
	t.Helper()
	out, err := exec.CommandContext(t.Context(), "ffprobe", "-v", "error", "-print_format", "json", "-show_format", "-show_streams", "-show_chapters", path).Output()
	if err != nil {
		t.Fatalf("ffprobe %s：%v", filepath.Base(path), err)
	}
	var p probed
	if err := json.Unmarshal(out, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func (p probed) seconds(t *testing.T) float64 {
	t.Helper()
	d, err := strconv.ParseFloat(p.Format.Duration, 64)
	if err != nil {
		t.Fatalf("ffprobe 给出的时长 %q：%v", p.Format.Duration, err)
	}
	return d
}

func (p probed) title() (string, bool) {
	for k, v := range p.Format.Tags {
		if strings.EqualFold(k, "title") {
			return v, true
		}
	}
	return "", false
}

// 转码成功，返回输出的 ffprobe 结果。
func transcode(t *testing.T, input string) probed {
	t.Helper()
	mp3, release, err := New(t.TempDir(), discard).ToMP3(t.Context(), source(t, input))
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	output := filepath.Join(t.TempDir(), "audio.mp3")
	if err := os.WriteFile(output, readAll(t, mp3), 0o600); err != nil {
		t.Fatal(err)
	}
	return ffprobe(t, output)
}

func requireEmpty(t *testing.T, dir string) {
	t.Helper()
	left, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Errorf("%s 中还有 %v", dir, left)
	}
}

type record struct {
	Level  string `json:"level"`
	Stderr string `json:"stderr"`
}

func recorder() (*slog.Logger, func(t *testing.T) []record) {
	var buf bytes.Buffer
	return slog.New(slog.NewJSONHandler(&buf, nil)), func(t *testing.T) []record {
		t.Helper()
		var records []record
		dec := json.NewDecoder(bytes.NewReader(buf.Bytes()))
		for dec.More() {
			var r record
			if err := dec.Decode(&r); err != nil {
				t.Fatal(err)
			}
			records = append(records, r)
		}
		return records
	}
}

func TestToMP3(t *testing.T) {
	title := "title=" + markup
	tests := []struct {
		name string
		file string
		args []string
	}{
		{"分片 MP4 的 AAC", "audio.m4s", slices.Concat(sine, []string{"-c:a", "aac", "-movflags", "+frag_keyframe+empty_moov+default_base_moof", "-metadata", title, "-f", "mp4"})},
		{"带视频的 FLV", "video.flv", slices.Concat(video, sine, []string{"-c:v", "flv", "-c:a", "aac", "-metadata", title})},
		{"moov 在末尾的 MP4", "video.mp4", slices.Concat(video, sine, []string{"-c:v", "mpeg4", "-c:a", "aac", "-metadata", title})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := generate(t, tt.file, tt.args...)
			in := ffprobe(t, input)
			if got, ok := in.title(); !ok || got != markup {
				t.Fatalf("测试用的输入没有被 ffprobe 读出标题：%q, %v", got, ok)
			}

			out := transcode(t, input)
			if len(out.Streams) != 1 || out.Streams[0].CodecType != "audio" || out.Streams[0].CodecName != "mp3" || out.Format.Name != "mp3" {
				t.Errorf("输出 = %+v，期望只有一条 mp3 音轨", out)
			}
			if diff := math.Abs(out.seconds(t) - in.seconds(t)); diff >= 0.2 {
				t.Errorf("输出时长 %v 秒，输入时长 %v 秒", out.Format.Duration, in.Format.Duration)
			}
			if got, ok := out.title(); ok {
				t.Errorf("输出还带着来源的标题 %q", got)
			}
		})
	}
}

// 第二条音轨是声道更多的默认音轨：不指定映射时 ffmpeg 会选它。
func TestToMP3UsesFirstAudioTrack(t *testing.T) {
	input := generate(t, "two.mp4", slices.Concat(sine, []string{
		"-f", "lavfi", "-i", "sine=frequency=880:duration=2", "-map", "0:a", "-map", "1:a", "-c:a", "aac",
		"-ac:a:1", "2", "-disposition:a:0", "0", "-disposition:a:1", "default",
	})...)
	if out := transcode(t, input); len(out.Streams) != 1 || out.Streams[0].Channels != 1 {
		t.Errorf("输出 = %+v，期望只有第一条（单声道）音轨", out.Streams)
	}
}

func TestToMP3DropsSourceChapters(t *testing.T) {
	chapters := filepath.Join(t.TempDir(), "chapters.txt")
	if err := os.WriteFile(chapters, []byte(";FFMETADATA1\n[CHAPTER]\nTIMEBASE=1/1000\nSTART=0\nEND=2000\ntitle="+markup+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	input := generate(t, "chapters.mp4", slices.Concat(sine, []string{"-f", "ffmetadata", "-i", chapters, "-map", "0:a", "-map_chapters", "1", "-c:a", "aac"})...)
	if n := len(ffprobe(t, input).Chapters); n != 1 {
		t.Fatalf("测试用的输入有 %d 个章节，期望 1 个", n)
	}
	if out := transcode(t, input); len(out.Chapters) != 0 {
		t.Errorf("输出还带着来源的章节 %s", out.Chapters)
	}
}

// 与 yt-dlp 的默认质量一样用 VBR：ffmpeg 给码率变化的 mp3 写 Xing 头，固定码率时写 Info 头。
func TestToMP3EncodesVBR(t *testing.T) {
	input := generate(t, "audio.m4a", slices.Concat(sine, []string{"-c:a", "aac"})...)
	mp3, release, err := New(t.TempDir(), discard).ToMP3(t.Context(), source(t, input))
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	head := readAll(t, mp3)
	if !bytes.Contains(head[:min(len(head), 4096)], []byte("Xing")) {
		t.Error("输出的第一帧没有 Xing 头，不是 VBR 编码")
	}
}

func TestToMP3FailsWithoutAudio(t *testing.T) {
	input := generate(t, "silent.mp4", slices.Concat(video, []string{"-c:v", "mpeg4"})...)
	root := t.TempDir()
	logger, records := recorder()
	_, release, err := New(root, logger).ToMP3(t.Context(), source(t, input))
	if err == nil {
		release()
		t.Fatal("没有音轨的输入转码成功了")
	}
	var rejected *port.RejectedError
	if errors.As(err, &rejected) {
		t.Errorf("转码失败 = %v，期望是普通错误，让准备流程重试", err)
	}
	requireEmpty(t, root)
	if got := records(t); len(got) != 1 || got[0].Level != "WARN" || got[0].Stderr == "" {
		t.Errorf("日志 = %+v，期望一条带 stderr 的 WARN", got)
	}
}

// 握手完成的连接留在 accept 队列里，不必先 Accept 也不会漏掉。
func listenLocal(t *testing.T) (address string, connected func() bool) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	tcp := l.(*net.TCPListener)
	return l.Addr().String(), func() bool {
		_ = tcp.SetDeadline(time.Now().Add(100 * time.Millisecond))
		conn, err := tcp.Accept()
		if err != nil {
			return false
		}
		_ = conn.Close()
		return true
	}
}

// 输入复制成了不带扩展名的文件，ffmpeg 不会把它当 HLS 播放列表，所以 HLS 一行走不到打开分片的一步；DASH 清单按内容识别，会走到那一步，被白名单拒绝。
// ffmpeg 读本地文件时默认的白名单是 file,crypto,data：stderr 里写着 'file'，说明命令行上的 -protocol_whitelist file 起了作用。
func TestToMP3DoesNotUseNetwork(t *testing.T) {
	tests := []struct {
		name       string
		manifest   string
		wantStderr string
	}{
		{"HLS 播放列表", "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:2\n#EXT-X-MEDIA-SEQUENCE:0\n#EXTINF:2.0,\nhttp://%s/seg0.ts\n#EXT-X-ENDLIST\n", ""},
		{"DASH 清单", `<?xml version="1.0" encoding="UTF-8"?>
<MPD xmlns="urn:mpeg:dash:schema:mpd:2011" profiles="urn:mpeg:dash:profile:isoff-on-demand:2011" type="static" mediaPresentationDuration="PT2S" minBufferTime="PT1S">
<Period><AdaptationSet mimeType="audio/mp4"><Representation id="1" bandwidth="128000" codecs="mp4a.40.2">
<BaseURL>http://%s/audio.m4s</BaseURL>
</Representation></AdaptationSet></Period>
</MPD>
`, "not on whitelist 'file'!"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			address, connected := listenLocal(t)
			root := t.TempDir()
			logger, records := recorder()
			// 地址如果被访问，ffmpeg 会一直等响应：限定期限，免得挂住测试。
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			_, release, err := New(root, logger).ToMP3(ctx, porttest.LinkFile(fmt.Sprintf(tt.manifest, address)))
			if err == nil {
				release()
				t.Fatal("转码成功了，期望输入不是可以转码的音频")
			}
			if connected() {
				t.Error("ffmpeg 连接了输入中引用的地址")
			}
			requireEmpty(t, root)
			if got := records(t); len(got) != 1 || !strings.Contains(got[0].Stderr, tt.wantStderr) {
				t.Errorf("日志 = %+v，期望 stderr 中有 %q", got, tt.wantStderr)
			}
		})
	}
}

func TestToMP3StopsFFmpegWhenContextEnds(t *testing.T) {
	// 10 分钟的静音 AAC 生成得很快，转成 mp3 花的时间却远远超过期限。
	input := source(t, generate(t, "long.mp4", "-f", "lavfi", "-i", "anullsrc", "-t", "600", "-c:a", "aac", "-b:a", "32k"))
	tests := []struct {
		name   string
		newCtx func(parent context.Context) (context.Context, context.CancelFunc)
		want   error
		logged int
	}{
		{"超时", func(parent context.Context) (context.Context, context.CancelFunc) {
			return context.WithTimeout(parent, 200*time.Millisecond)
		}, context.DeadlineExceeded, 1},
		{"取消", func(parent context.Context) (context.Context, context.CancelFunc) {
			ctx, cancel := context.WithCancel(parent)
			time.AfterFunc(200*time.Millisecond, cancel)
			return ctx, cancel
		}, context.Canceled, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			logger, records := recorder()
			ctx, cancel := tt.newCtx(t.Context())
			defer cancel()
			_, release, err := New(root, logger).ToMP3(ctx, input)
			if !errors.Is(err, tt.want) {
				if err == nil {
					release()
				}
				t.Errorf("转码 = %v，期望 %v", err, tt.want)
			}
			requireEmpty(t, root)
			if got := records(t); len(got) != tt.logged {
				t.Errorf("日志 = %+v，期望 %d 条", got, tt.logged)
			}
		})
	}
}

func TestReleaseDeletesTempDir(t *testing.T) {
	input := generate(t, "audio.m4s", slices.Concat(sine, []string{"-c:a", "aac", "-f", "mp4"})...)
	root := t.TempDir()
	mp3, release, err := New(root, discard).ToMP3(t.Context(), source(t, input))
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	dirs, err := os.ReadDir(root)
	if err != nil || len(dirs) != 1 || !dirs[0].IsDir() {
		t.Fatalf("%s 中有 %v（%v），期望只有这次转码的目录", root, dirs, err)
	}
	dir := filepath.Join(root, dirs[0].Name())
	if files, err := os.ReadDir(dir); err != nil || len(files) != 1 || files[0].Name() != "audio.mp3" {
		t.Errorf("转码之后 %s 中有 %v（%v），期望只剩 audio.mp3", dir, files, err)
	}

	// 分析和上传同时打开同一个文件。
	var readers [2]io.ReadSeekCloser
	for i := range readers {
		if readers[i], err = mp3.Open(); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = readers[i].Close() }()
	}
	var contents [2][]byte
	var errs [2]error
	var wg sync.WaitGroup
	for i := range readers {
		wg.Go(func() { contents[i], errs[i] = io.ReadAll(readers[i]) })
	}
	wg.Wait()
	if err := errors.Join(errs[:]...); err != nil {
		t.Fatal(err)
	}
	if len(contents[0]) == 0 || !bytes.Equal(contents[0], contents[1]) {
		t.Errorf("同时打开两次读到 %d 和 %d 字节，期望相同且不为空", len(contents[0]), len(contents[1]))
	}
	if again := readAll(t, mp3); !bytes.Equal(again, contents[0]) {
		t.Errorf("再次打开读到 %d 字节，期望与第一次相同的 %d 字节", len(again), len(contents[0]))
	}

	release()
	if _, err := os.Stat(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("release 之后 %s 的状态 = %v，期望不存在", dir, err)
	}
	requireEmpty(t, root)
}

func TestTailKeepsLastBytes(t *testing.T) {
	tests := []struct {
		writes []string
		want   string
	}{
		{[]string{"ab", "cd"}, "abcd"},
		{[]string{"ab", "cdefg", "h"}, "defgh"},
		{[]string{"abcdefgh"}, "defgh"},
	}
	for _, tt := range tests {
		tl := &tail{limit: 5}
		for _, w := range tt.writes {
			if n, err := tl.Write([]byte(w)); n != len(w) || err != nil {
				t.Fatalf("写入 %q = %d, %v", w, n, err)
			}
		}
		if string(tl.buf) != tt.want {
			t.Errorf("依次写入 %q 之后保留 %q，期望 %q", tt.writes, tl.buf, tt.want)
		}
	}
}
