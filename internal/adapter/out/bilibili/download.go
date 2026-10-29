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

func (c *Client) Download(ctx context.Context, id string) (port.BilibiliDownload, func(), error) {
	bvid, page, ok := track.ParseBilibiliID(id)
	if !ok {
		return port.BilibiliDownload{}, nil, fmt.Errorf("不是 Bilibili 曲目 ID：%q", id)
	}
	if c.root == "" {
		return port.BilibiliDownload{}, nil, errors.New("没有配置下载目录，这个进程不下载文件")
	}
	meta, info, err := c.trackMeta(ctx, "download_view", "download_pagelist", bvid, page)
	if err != nil {
		return port.BilibiliDownload{}, nil, err
	}
	cookie, err := c.cookie(ctx)
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
	// 用 defer 是为了 panic 时也删除目录。
	done := false
	defer func() {
		if !done {
			release()
		}
	}()

	audio := filepath.Join(dir, "audio")
	// 平台返回的节点未必都能下：baseUrl 常指向 EdgeOne 承载的镜像，可能直接拒绝（HTTP 567）；
	// 同一条流的 base_url、backup_url 仍是完整签名地址，逐个尝试，全部失败才报错。
	var downloadErr error
	for _, audioURL := range audioURLs {
		if downloadErr = c.downloadFile(ctx, "download_audio", audioURL, cookie, audio); downloadErr == nil {
			break
		}
	}
	if downloadErr != nil {
		return port.BilibiliDownload{}, nil, fmt.Errorf("下载音频失败（尝试了全部 %d 个节点）：%w", len(audioURLs), downloadErr)
	}
	d := port.BilibiliDownload{Meta: meta, Audio: file(audio)}
	if coverURL, ext, ok := coverInfo(info.pic); ok {
		cover := filepath.Join(dir, "cover."+ext)
		if err := c.downloadFile(ctx, "download_cover", coverURL, cookie, cover); err != nil {
			return port.BilibiliDownload{}, nil, err
		}
		d.Cover, d.CoverExt = file(cover), ext
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

// 优先以 fnval=80 在带 baseUrl 的 dash.audio 中选择 ID 最高的普通立体声 AAC（不选 FLAC 和杜比）；
// 没有可用候选地址时以 fnval=0、qn=16 回退到 durl 首项，避免为音频下载高清视频。
// 候选地址按 baseUrl、base_url、backup_url 排序。
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
	return nil, errors.New("平台没有返回可下载的音频")
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

// 平台返回的地址不可信，发送请求前必须经 downloadURL 校验。
func (c *Client) downloadFile(ctx context.Context, op, rawURL, cookie, dest string) (err error) {
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
	// EdgeOne 承载的节点拦截完整文件的 GET（返回 567），带 Range 的请求会回源到源站放行。
	req.Header.Set("Range", "bytes=0-")
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	resp, err := c.download.Do(req)
	if err != nil {
		return fmt.Errorf("下载文件失败：%w", requestError(err))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return fmt.Errorf("下载文件返回 HTTP %d", resp.StatusCode)
	}
	f, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("创建文件：%w", err)
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		_ = f.Close()
		return fmt.Errorf("保存文件：%w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("保存文件：%w", err)
	}
	return nil
}

// 封面地址无效、不在 B 站域名或格式不支持时跳过封面。
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
