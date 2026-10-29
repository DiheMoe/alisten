package prepare

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"sync"
	"time"

	"github.com/dihemoe/alisten/internal/app/media"
	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/track"
)

const cleanupTimeout = 10 * time.Second

// 限定封面格式并不能阻止脚本：链接的封面来自用户提交的网页，而对象存储会按匿名请求中的 response-content-type 改写 Content-Type。
// 对象存储与页面同源时，要靠入口为 /storage/ 的响应添加 Content-Security-Policy: sandbox 和 X-Content-Type-Options: nosniff 来防范脚本。
var coverTypes = map[string]string{"jpg": "image/jpeg", "jpeg": "image/jpeg", "png": "image/png", "webp": "image/webp"}

// MediaRunner 准备音频和封面要存进对象存储的歌：链接由 yt-dlp 下载并转成 mp3，B 站的歌由音源下载原始文件，再转成 mp3。
type MediaRunner struct {
	links      port.LinkDownloader
	bilibili   port.BilibiliSource
	transcoder port.Transcoder
	store      port.MediaStore
	tracks     port.Tracks
	logger     *slog.Logger
}

func NewMediaRunner(links port.LinkDownloader, bilibili port.BilibiliSource, transcoder port.Transcoder, store port.MediaStore, tracks port.Tracks, logger *slog.Logger) *MediaRunner {
	return &MediaRunner{links: links, bilibili: bilibili, transcoder: transcoder, store: store, tracks: tracks, logger: logger}
}

func (r *MediaRunner) Run(ctx context.Context, t track.Track) (err error) {
	d, release, err := r.download(ctx, t)
	if err != nil {
		return err
	}
	defer release()

	key := media.Key(t.ID)
	if err := r.clear(ctx, key); err != nil {
		return err
	}
	// 上传开始之后任何一步失败，都要删除已经上传的对象：它们匿名可读，却没有准备结果，后台也看不到，不会再有人清理。
	defer func() {
		if err != nil {
			r.discard(ctx, t, key)
		}
	}()
	var (
		wg         sync.WaitGroup
		result     track.Prepared
		analyzeErr error
		size       int64
		coverKey   string
		uploadErr  error
	)
	wg.Go(func() { result, analyzeErr = analyzeFile(ctx, r.logger, t, d.Audio, d.Meta.Duration) })
	size, coverKey, uploadErr = r.upload(ctx, key, d)
	wg.Wait()
	// 音频无法分析时重试也没有用，先于上传的错误返回。
	if analyzeErr != nil {
		return analyzeErr
	}
	if uploadErr != nil {
		return uploadErr
	}

	meta := track.Meta{Title: d.Meta.Title, Artist: d.Meta.Artist, Album: d.Meta.Album, CoverKey: track.ObjectKey(coverKey), PageURL: pageURL(t), Duration: d.Meta.Duration}
	ctx, cancel := context.WithTimeout(ctx, storeTimeout)
	defer cancel()
	return r.tracks.SavePreparedTrack(ctx, t, port.PreparedTrack{Meta: meta, Result: result, MediaKey: key, MediaSize: size})
}

// 任务参数来自 River 的任务表，不一定经过网关的校验。
func (r *MediaRunner) download(ctx context.Context, t track.Track) (port.LinkDownload, func(), error) {
	switch {
	case t.Source == track.Link && httpLink(t.ID):
		return r.links.Download(ctx, t.ID)
	case t.Source == track.Bilibili && bilibiliID(t.ID):
		return r.downloadBilibili(ctx, t.ID)
	}
	return port.LinkDownload{}, nil, &port.RejectedError{Err: fmt.Errorf("不能准备来源为 %q、ID 为 %q 的歌", t.Source, t.ID)}
}

// 平台给的原始文件转成 mp3 之后，与链接下载到的文件一样分析和上传。两份文件都保留到上传结束。
func (r *MediaRunner) downloadBilibili(ctx context.Context, id string) (port.LinkDownload, func(), error) {
	d, release, err := r.bilibili.Download(ctx, id)
	if err != nil {
		return port.LinkDownload{}, nil, err
	}
	// 用 defer 是为了 panic 时也删除文件：River 会恢复任务中的 panic，worker 继续运行。
	done := false
	defer func() {
		if !done {
			release()
		}
	}()
	mp3, releaseMP3, err := r.transcoder.ToMP3(ctx, d.Audio)
	if err != nil {
		return port.LinkDownload{}, nil, err
	}
	done = true
	return port.LinkDownload{Meta: d.Meta, Audio: mp3, Cover: d.Cover, CoverExt: d.CoverExt}, func() {
		releaseMP3()
		release()
	}, nil
}

func pageURL(t track.Track) string {
	if t.Source == track.Bilibili {
		return track.BilibiliPageURL(t.ID)
	}
	return t.ID
}

// 对象键由曲目 ID 决定，重新准备时要先清除上一次留下的对象：这一次封面的格式可能不同，也可能没有封面。
func (r *MediaRunner) clear(ctx context.Context, key string) error {
	errs := []error{r.store.Delete(ctx, media.AudioObject(key))}
	for ext := range coverTypes {
		errs = append(errs, r.store.Delete(ctx, media.CoverObject(key, ext)))
	}
	return errors.Join(errs...)
}

// 尽力删除已经上传的对象，失败只记 WARN：下一次准备同一首歌时还会再清理一遍。ctx 可能已经结束（任务被取消或超时），
// 所以使用脱离 ctx 的短期限。
func (r *MediaRunner) discard(ctx context.Context, t track.Track, key string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer cancel()
	if err := r.clear(ctx, key); err != nil {
		r.logger.WarnContext(ctx, "删除没有准备成功的歌已上传的对象失败", "source", t.Source, "track_id", t.ID, "error", err)
	}
}

func httpLink(link string) bool {
	u, err := url.Parse(link)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != ""
}

func bilibiliID(id string) bool {
	_, _, ok := track.ParseBilibiliID(id)
	return ok
}

func analyzeFile(ctx context.Context, logger *slog.Logger, t track.Track, audio port.LinkFile, fallback time.Duration) (track.Prepared, error) {
	f, err := audio.Open()
	if err != nil {
		return track.Prepared{}, err
	}
	defer func() { _ = f.Close() }()
	return analyze(ctx, logger, t, ctxReader{ctx: ctx, r: f}, fallback, "")
}

// 解码不接收 ctx，只能靠这层包装在 ctx 结束后让读取失败；否则任务取消或超时之后，长文件的分析仍会继续。
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

func (r *MediaRunner) upload(ctx context.Context, key string, d port.LinkDownload) (int64, string, error) {
	size, err := r.put(ctx, media.AudioObject(key), d.Audio, "audio/mpeg")
	if err != nil {
		return 0, "", err
	}
	contentType, ok := coverTypes[d.CoverExt]
	if d.Cover == nil || !ok {
		return size, "", nil
	}
	coverKey := media.CoverObject(key, d.CoverExt)
	if _, err := r.put(ctx, coverKey, d.Cover, contentType); err != nil {
		return 0, "", err
	}
	return size, coverKey, nil
}

func (r *MediaRunner) put(ctx context.Context, key string, file port.LinkFile, contentType string) (int64, error) {
	f, err := file.Open()
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()
	size, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return 0, err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return 0, err
	}
	if err := r.store.Put(ctx, key, f, contentType); err != nil {
		return 0, err
	}
	return size, nil
}
