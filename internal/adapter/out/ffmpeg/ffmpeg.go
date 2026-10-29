package ffmpeg

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
)

const (
	executable  = "ffmpeg"
	waitDelay   = 5 * time.Second
	outputLimit = 64 << 10
)

type Transcoder struct {
	root   string
	logger *slog.Logger
}

var _ port.Transcoder = (*Transcoder)(nil)

func New(root string, logger *slog.Logger) *Transcoder {
	return &Transcoder{root: root, logger: logger}
}

func (t *Transcoder) ToMP3(ctx context.Context, audio port.LinkFile) (port.LinkFile, func(), error) {
	dir, err := os.MkdirTemp(t.root, "ffmpeg-*")
	if err != nil {
		return nil, nil, fmt.Errorf("创建临时目录：%w", err)
	}
	release := func() { _ = os.RemoveAll(dir) }
	// 用 defer 是为了 panic 时也删除目录：River 会恢复任务中的 panic，worker 继续运行。
	done := false
	defer func() {
		if !done {
			release()
		}
	}()
	mp3, err := t.transcode(ctx, audio, dir)
	if err != nil {
		return nil, nil, err
	}
	done = true
	return mp3, release, nil
}

func (t *Transcoder) transcode(ctx context.Context, audio port.LinkFile, dir string) (port.LinkFile, error) {
	// 先复制成文件再交给 ffmpeg：MP4 的 moov 可能在文件末尾，从管道读不了。文件名不带扩展名，ffmpeg 只能按内容识别格式。
	input := filepath.Join(dir, "input")
	if err := copyFile(input, audio); err != nil {
		return nil, fmt.Errorf("复制输入的音频：%w", err)
	}
	output := filepath.Join(dir, "audio.mp3")
	stderr, err := run(ctx, args(input, output))
	if err == nil {
		// 转码成功后立即删除输入副本，不等 release：源音频和 MP3 在上传期间仍占用临时空间，
		// 同一容器还可能并行准备其他链接。
		_ = os.Remove(input)
		return file(output), nil
	}
	ctxErr := ctx.Err()
	// 任务被取消（worker 退出）不是转码的问题，不记日志。
	if !errors.Is(ctxErr, context.Canceled) {
		t.logger.WarnContext(ctx, "ffmpeg 转码失败", "stderr", string(stderr), "error", err)
	}
	if ctxErr != nil {
		return nil, fmt.Errorf("ffmpeg 转码：%w", ctxErr)
	}
	return nil, fmt.Errorf("ffmpeg 转码失败：%w", err)
}

func copyFile(dst string, src port.LinkFile) error {
	in, err := src.Open()
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func args(input, output string) []string {
	return []string{
		"-nostdin", "-hide_banner", "-loglevel", "error",
		// 输入可能引用网络地址，例如 HLS 播放列表，所以只允许读本地文件。输入选项要放在 -i 之前。
		"-protocol_whitelist", "file",
		"-i", input,
		"-map", "0:a:0", "-vn", "-sn", "-dn",
		// 来源的标签和章节是用户上传的内容，ffmpeg 默认会把它们复制进 mp3，所以不复制。
		"-map_metadata", "-1", "-map_chapters", "-1",
		// 与 yt-dlp 的 --audio-format mp3 默认质量一致。
		"-c:a", "libmp3lame", "-q:a", "5",
		output,
	}
}

func run(ctx context.Context, argv []string) (stderr []byte, err error) {
	cmd := exec.CommandContext(ctx, executable, argv...)
	cmd.WaitDelay = waitDelay
	errOut := &tail{limit: outputLimit}
	cmd.Stdout, cmd.Stderr = io.Discard, errOut
	err = cmd.Run()
	return errOut.buf, err
}

type tail struct {
	limit int
	buf   []byte
}

func (t *tail) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if extra := len(t.buf) - t.limit; extra > 0 {
		t.buf = t.buf[extra:]
	}
	return len(p), nil
}

type file string

func (f file) Open() (io.ReadSeekCloser, error) {
	r, err := os.Open(string(f))
	if err != nil {
		return nil, err
	}
	return r, nil
}
