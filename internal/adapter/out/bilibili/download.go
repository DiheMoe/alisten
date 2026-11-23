package bilibili

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/track"
)

var errFileTooLarge = errors.New("下载文件超过大小上限")

func (c *Client) Download(ctx context.Context, id string) (port.BilibiliDownload, func(), error) {
	bvid, page, ok := track.ParseBilibiliID(id)
	if !ok {
		return port.BilibiliDownload{}, nil, fmt.Errorf("不是 哔哩哔哩曲目 ID：%q", id)
	}
	if c.root == "" {
		return port.BilibiliDownload{}, nil, errors.New("没有配置下载目录，这个进程不下载文件")
	}
	cookie, err := c.cookie.Get(ctx)
	if err != nil {
		return port.BilibiliDownload{}, nil, err
	}
	meta, info, err := c.trackMeta(ctx, "download_view", "download_pagelist", bvid, page, cookie)
	if err != nil {
		return port.BilibiliDownload{}, nil, err
	}
	audioURLs, err := c.playURLs(ctx, bvid, info.cid, cookie)
	if err != nil {
		return port.BilibiliDownload{}, nil, err
	}

	dir, err := os.MkdirTemp(c.root, "bilibili-")
	if err != nil {
		return port.BilibiliDownload{}, nil, fmt.Errorf("创建临时目录：%w", err)
	}
	release := func() { _ = os.RemoveAll(dir) }
	// 用 defer 是为了任务 panic 时也删掉临时目录。
	done := false
	defer func() {
		if !done {
			release()
		}
	}()

	audio := filepath.Join(dir, "audio")
	// 按顺序逐个尝试候选节点，因为 baseUrl 常指向 EdgeOne 等海外镜像节点并返回 HTTP 567，而同一条流的备用节点仍可正常下载。
	// 流本身超过大小上限时立即拒绝，因为换节点也不会改变文件大小。
	var downloadErr error
	for _, audioURL := range audioURLs {
		downloadErr = c.downloadFile(ctx, "download_audio", audioURL, audio, port.MaxAudioSize)
		if downloadErr == nil || errors.Is(downloadErr, errFileTooLarge) {
			break
		}
	}
	if errors.Is(downloadErr, errFileTooLarge) {
		return port.BilibiliDownload{}, nil, &port.RejectedError{Err: port.ErrAudioTooLarge}
	}
	if downloadErr != nil {
		return port.BilibiliDownload{}, nil, fmt.Errorf("下载音频失败（尝试了全部 %d 个节点）：%w", len(audioURLs), downloadErr)
	}

	d := port.BilibiliDownload{Meta: meta, Audio: file(audio)}
	// 封面不影响播放，超过大小上限或节点出错时跳过封面，不让整首歌准备失败。
	if coverURL, ext, ok := coverInfo(info.pic); ok {
		cover := filepath.Join(dir, "cover."+ext)
		switch err := c.downloadFile(ctx, "download_cover", coverURL, cover, port.MaxCoverSize); {
		case err == nil:
			d.Cover, d.CoverExt = file(cover), ext
		case !errors.Is(err, errFileTooLarge) && ctx.Err() == nil:
			c.logger.WarnContext(ctx, "下载 哔哩哔哩封面失败，按无封面处理", "track_id", id, "error", err)
		}
	}
	done = true
	return d, release, nil
}

type playurlData struct {
	Durl []struct {
		URL       string   `json:"url"`
		BackupURL []string `json:"backup_url"`
	} `json:"durl"`
	Dash struct {
		Audio []struct {
			ID        int      `json:"id"`
			BaseURL   string   `json:"baseUrl"`
			BaseURL2  string   `json:"base_url"`
			BackupURL []string `json:"backup_url"`
		} `json:"audio"`
	} `json:"dash"`
}

// 优先用 fnval=80 在带 baseUrl 的 dash.audio 中选择码率 ID 最高的普通立体声 AAC，不选 FLAC 和杜比音轨。
// 没有可用的 DASH 音轨时再用 fnval=0 和 qn=16 回退到最低画质的 durl 流，避免为提取音频去下高清视频。
func (c *Client) playURLs(ctx context.Context, bvid string, cid int64, cookie string) ([]string, error) {
	data, err := c.fetchPlayurl(ctx, 80, 127, bvid, cid, cookie)
	if err != nil {
		return nil, err
	}
	bestID := -1
	var candidates []string
	for _, a := range data.Dash.Audio {
		if a.ID > bestID && a.BaseURL != "" {
			bestID = a.ID
			candidates = dedupe([]string{a.BaseURL, a.BaseURL2}, a.BackupURL)
		}
	}
	if len(candidates) > 0 {
		return candidates, nil
	}
	data, err = c.fetchPlayurl(ctx, 0, 16, bvid, cid, cookie)
	if err != nil {
		return nil, err
	}
	if len(data.Durl) > 0 && data.Durl[0].URL != "" {
		return dedupe([]string{data.Durl[0].URL}, data.Durl[0].BackupURL), nil
	}
	return nil, errors.New("哔哩哔哩没有返回可下载的音频")
}

func dedupe(first []string, rest []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, u := range append(first, rest...) {
		if u != "" && !seen[u] {
			seen[u] = true
			out = append(out, u)
		}
	}
	return out
}

func (c *Client) fetchPlayurl(ctx context.Context, fnval, qn int, bvid string, cid int64, cookie string) (d playurlData, err error) {
	start := time.Now()
	defer func() { c.report("download_playurl", start, err) }()
	u := fmt.Sprintf("%s?fnval=%d&qn=%d&bvid=%s&cid=%d", playurlURL, fnval, qn, url.QueryEscape(bvid), cid)
	err = c.getData(ctx, u, cookie, &d)
	return d, err
}

// 下载请求不带账号 Cookie，因为音视频和封面节点靠 URL 签名鉴权，而且可能由第三方 CDN 承载。
func (c *Client) downloadFile(ctx context.Context, op, rawURL, dest string, maxBytes int64) (err error) {
	start := time.Now()
	defer func() { c.report(op, start, err) }()
	safe, err := downloadURL(rawURL)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, safe, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Referer", referer)
	// 请求固定带上 Range 头，因为 EdgeOne 承载的节点会拦截普通 GET 并返回 567，带上 Range 头才会回源放行。
	req.Header.Set("Range", "bytes=0-")
	resp, err := c.download.Do(req)
	if err != nil {
		return fmt.Errorf("下载文件失败：%w", requestError(req.URL.Host, err))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return fmt.Errorf("下载文件返回 HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > maxBytes {
		return errFileTooLarge
	}
	f, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("创建文件：%w", err)
	}
	n, copyErr := io.Copy(f, io.LimitReader(resp.Body, maxBytes+1))
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(dest)
		return fmt.Errorf("保存文件：%w", copyErr)
	}
	if n > maxBytes {
		_ = os.Remove(dest)
		return errFileTooLarge
	}
	if closeErr != nil {
		_ = os.Remove(dest)
		return fmt.Errorf("保存文件：%w", closeErr)
	}
	return nil
}

func coverInfo(pic string) (string, string, bool) {
	if pic == "" {
		return "", "", false
	}
	safe, err := downloadURL(pic)
	if err != nil {
		return "", "", false
	}
	u, err := url.Parse(safe)
	if err != nil {
		return "", "", false
	}
	ext := strings.ToLower(strings.TrimPrefix(path.Ext(u.Path), "."))
	switch ext {
	case "jpg", "jpeg":
		ext = "jpg"
	case "png", "webp":
	default:
		return "", "", false
	}
	return safe, ext, true
}

type file string

func (f file) Open() (io.ReadSeekCloser, error) {
	return os.Open(string(f))
}
