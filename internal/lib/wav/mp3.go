package wav

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/DiheMoe/gl3d"
)

// gl3d 的 ReadPCM 按采样帧计数，而 Decoder.Read 按采样个数计数。
type mp3Decoder struct{ *gl3d.Decoder }

// ctx 传 Background：取消靠 r 在 ctx 结束后读取失败（下载流，以及 prepare 中包了 ctx 的文件），gl3d 会原样返回这个错误；
// WAV 和 FLAC 也是这样取消的，所以 Decoder 不接收 ctx。gl3d 自己的 ctx 只在每帧之前和每次读取之前检查，
// 即使传入真实的 ctx，也至多少解码已缓冲的 64 KiB。
func newMP3Decoder(r io.Reader) (Decoder, error) {
	dec, err := gl3d.NewDecoder(context.Background(), r)
	if err != nil {
		return nil, mp3Error(err)
	}
	return mp3Decoder{dec}, nil
}

func (d mp3Decoder) Read(p []float64) (int, error) {
	n, err := d.ReadPCM(context.Background(), p)
	return n * d.Channels(), mp3Error(err)
}

// io.EOF 原样返回，调用方靠它判断读完。
// 每次 Read 都经过这里：errors.As 的目标变量要在提前返回之后声明，否则它们会逃逸到堆上，使每次 Read 多分配四个对象。
func mp3Error(err error) error {
	if err == nil || err == io.EOF {
		return err
	}
	var (
		unsupported *gl3d.UnsupportedError
		truncated   *gl3d.TruncatedError
		corrupt     *gl3d.CorruptError
		limit       *gl3d.LimitError
	)
	switch {
	case errors.Is(err, gl3d.ErrNoAudio):
		return fmt.Errorf("wav: MP3 中没有音频帧: %w", err)
	case errors.As(err, &unsupported):
		return fmt.Errorf("wav: 不支持的 MP3 格式: %w", err)
	case errors.As(err, &truncated):
		return fmt.Errorf("wav: MP3 数据被截断: %w", err)
	case errors.As(err, &corrupt):
		return fmt.Errorf("wav: MP3 数据损坏: %w", err)
	case errors.As(err, &limit):
		return fmt.Errorf("wav: MP3 超出解码的资源上限: %w", err)
	default:
		return fmt.Errorf("wav: 读取或解码 MP3 失败: %w", err)
	}
}
