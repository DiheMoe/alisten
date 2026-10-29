package bilibili

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/track"
)

var plainAV = regexp.MustCompile(`^[aA][vV]([0-9]+)$`)

func (c *Client) Recognize(ctx context.Context, link string) (string, bool, error) {
	if id, ok := plainID(link); ok {
		return id, true, nil
	}
	u, err := url.Parse(link)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", false, nil
	}
	host := strings.ToLower(u.Hostname())
	if host == "b23.tv" {
		return c.resolveShortLink(ctx, u)
	}
	if !isBilibiliHost(host) {
		return "", false, nil
	}
	return classifyVideoURL(u)
}

func isBilibiliHost(host string) bool {
	return host == "bilibili.com" || strings.HasSuffix(host, ".bilibili.com")
}

// plainID 处理不带 scheme 的写法：BV 号（可带分 P）和 av 号。
func plainID(s string) (string, bool) {
	if bvid, page, ok := track.ParseBilibiliID(s); ok {
		return track.BilibiliID(bvid, page), true
	}
	if m := plainAV.FindStringSubmatch(s); m != nil {
		if aid, err := strconv.ParseInt(m[1], 10, 64); err == nil {
			if bvid, ok := avToBV(aid); ok {
				return bvid, true
			}
		}
	}
	return "", false
}

// classifyVideoURL 处理 bilibili.com 的链接：视频返回曲目 ID，其他（番剧、直播、个人空间等）都拒绝。
func classifyVideoURL(u *url.URL) (string, bool, error) {
	if !strings.HasPrefix(u.Path, "/video/") {
		return "", false, &port.RejectedError{Err: errors.New("这个 B 站链接不是视频，番剧、直播和个人空间等都不支持")}
	}
	idPart := strings.Trim(strings.TrimPrefix(u.Path, "/video/"), "/")
	var bvid string
	switch parsed, page, ok := track.ParseBilibiliID(idPart); {
	case ok && page == 1:
		bvid = parsed
	default:
		m := plainAV.FindStringSubmatch(idPart)
		if m == nil {
			return "", false, &port.RejectedError{Err: fmt.Errorf("无法识别的 B 站视频 ID：%s", idPart)}
		}
		aid, err := strconv.ParseInt(m[1], 10, 64)
		if err != nil {
			return "", false, &port.RejectedError{Err: fmt.Errorf("无法识别的 B 站视频 ID：%s", idPart)}
		}
		converted, ok := avToBV(aid)
		if !ok {
			return "", false, &port.RejectedError{Err: fmt.Errorf("无法识别的 B 站视频 ID：%s", idPart)}
		}
		bvid = converted
	}
	page := 1
	if p := u.Query().Get("p"); p != "" {
		if n, err := strconv.Atoi(p); err == nil && n >= 1 {
			page = n
		}
	}
	// 曲目 ID 的分 P 最多 4 位，更大序号的视频不存在。
	if page > 9999 {
		return "", false, &port.RejectedError{Err: fmt.Errorf("这个视频没有第 %d P", page)}
	}
	return track.BilibiliID(bvid, page), true, nil
}

func (c *Client) resolveShortLink(ctx context.Context, u *url.URL) (id string, ok bool, err error) {
	start := time.Now()
	defer func() { c.report("recognize", start, err) }()
	ctx, cancel := context.WithTimeout(ctx, apiTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", false, err
	}
	req.Header.Set("User-Agent", userAgent)
	// 保留注入 client 的 Transport，只禁止跟随重定向：跳转地址由下面的识别逻辑校验。
	client := *c.api
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return "", false, fmt.Errorf("请求 b23.tv 短链失败：%w", requestError(err))
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 300 || resp.StatusCode >= 400 {
		return "", false, fmt.Errorf("b23.tv 短链返回 HTTP %d", resp.StatusCode)
	}
	target, err := url.Parse(resp.Header.Get("Location"))
	if err != nil || (target.Scheme != "http" && target.Scheme != "https") || target.Host == "" {
		return "", false, errors.New("b23.tv 短链没有给出跳转地址")
	}
	if !isBilibiliHost(strings.ToLower(target.Hostname())) {
		return "", false, &port.RejectedError{Err: errors.New("b23.tv 短链指向的不是 B 站视频")}
	}
	id, _, err = classifyVideoURL(target)
	return id, err == nil, err
}

// av 号和 BV 号的换算，与平台前端使用的算法相同。
const (
	bvTable = "fZodR9XQDSUm21yCkr6zBqiveYah8bt4xsWpHnJE7jL5VG3guMTKNPAwcF"
	bvXOR   = 177451812
	bvAdd   = 8728348608
)

var (
	bvPositions = [6]int{11, 10, 3, 8, 4, 6}
	bvBase      = [6]int64{1, 58, 58 * 58, 58 * 58 * 58, 58 * 58 * 58 * 58, 58 * 58 * 58 * 58 * 58}
)

func avToBV(aid int64) (string, bool) {
	if aid <= 0 {
		return "", false
	}
	x := (aid ^ bvXOR) + bvAdd
	r := []byte("BV1  4 1 7  ")
	for i, pos := range bvPositions {
		r[pos] = bvTable[x/bvBase[i]%58]
	}
	bvid := string(r)
	// 超出 6 位 base-58 能表示的范围时换算不成立。
	if back, ok := bvToAV(bvid); !ok || back != aid {
		return "", false
	}
	return bvid, true
}

func bvToAV(bvid string) (int64, bool) {
	if len(bvid) != len("BV1  4 1 7  ") {
		return 0, false
	}
	var x int64
	for i, pos := range bvPositions {
		idx := strings.IndexByte(bvTable, bvid[pos])
		if idx < 0 {
			return 0, false
		}
		x += int64(idx) * bvBase[i]
	}
	return (x - bvAdd) ^ bvXOR, true
}
