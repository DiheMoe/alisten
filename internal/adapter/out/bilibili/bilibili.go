package bilibili

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/lib/cookiecache"
)

const (
	qrGenerateURL = "https://passport.bilibili.com/x/passport-login/web/qrcode/generate"
	qrPollURL     = "https://passport.bilibili.com/x/passport-login/web/qrcode/poll"
	navURL        = "https://api.bilibili.com/x/web-interface/nav"
	viewURL       = "https://api.bilibili.com/x/web-interface/view"
	pagelistURL   = "https://api.bilibili.com/x/player/pagelist"
	playurlURL    = "https://api.bilibili.com/x/player/playurl"

	userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/121.0.0.0 Safari/537.36"
	referer   = "https://www.bilibili.com/"

	apiTimeout      = 30 * time.Second
	downloadTimeout = 10 * time.Minute
	qrLoginTTL      = 3 * time.Minute
	bodyLimit       = 4 << 20
	textLimit       = 500
	maxDuration     = 24 * time.Hour
)

type Client struct {
	api      *http.Client
	download *http.Client
	root     string
	cookie   *cookiecache.Cache
	observe  func(source, op string, elapsed time.Duration, err error)
	logger   *slog.Logger
}

var (
	_ port.BilibiliSource = (*Client)(nil)
	_ port.BilibiliLinks  = (*Client)(nil)
	_ port.QRLoginSource  = (*Client)(nil)
)

// upstream 不能自带 Timeout，因为接口请求的 30 秒期限由 context 控制。
// 接口客户端禁止跟随重定向，防止手动设置的 Cookie 头被带往明文跳转目标。
// 下载客户端放宽期限到 downloadTimeout，并在每次跳转时校验目标域名和 HTTPS 协议。
// root 是 worker 存放下载中转文件的目录，不下载文件的进程传空字符串，误调 Download 时会直接报错而不会写进系统临时目录。
func New(
	upstream *http.Client,
	root string,
	loadCookie func(context.Context) (string, error),
	saveCookie func(context.Context, string) error,
	observe func(source, op string, elapsed time.Duration, err error),
	logger *slog.Logger,
) *Client {
	api := *upstream
	api.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	download := *upstream
	download.Timeout = downloadTimeout
	download.CheckRedirect = func(req *http.Request, _ []*http.Request) error {
		return checkDownloadRedirect(req.URL)
	}
	return &Client{
		api:      &api,
		download: &download,
		root:     root,
		cookie:   cookiecache.New(loadCookie, saveCookie),
		observe:  observe,
		logger:   logger,
	}
}

func (c *Client) report(op string, start time.Time, err error) {
	c.observe("bilibili", op, time.Since(start), err)
}

// 返回的错误不带完整请求地址，因为查询参数里可能有签名等敏感信息。
func (c *Client) get(ctx context.Context, rawURL, cookie string) ([]byte, []*http.Cookie, error) {
	ctx, cancel := context.WithTimeout(ctx, apiTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, nil, errors.New("哔哩哔哩的请求地址无效")
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Referer", referer)
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	resp, err := c.api.Do(req)
	if err != nil {
		return nil, nil, requestError(req.URL.Host, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("哔哩哔哩接口返回 HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, bodyLimit+1))
	if err != nil {
		return nil, nil, fmt.Errorf("读取 哔哩哔哩接口的响应：%w", err)
	}
	if len(body) > bodyLimit {
		return nil, nil, errors.New("哔哩哔哩接口的响应过大")
	}
	return body, resp.Cookies(), nil
}

type envelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func (c *Client) getData(ctx context.Context, rawURL, cookie string, data any) error {
	body, _, err := c.get(ctx, rawURL, cookie)
	if err != nil {
		return err
	}
	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return fmt.Errorf("解析 哔哩哔哩接口的响应：%w", err)
	}
	if env.Code != 0 {
		return classifyCode(env.Code, env.Message)
	}
	if err := json.Unmarshal(env.Data, data); err != nil {
		return fmt.Errorf("解析 哔哩哔哩接口的数据：%w", err)
	}
	return nil
}

// 命中 rejectionReasons 说明平台明确拒绝了这个视频，准备任务不再重试。
var rejectionReasons = map[int]string{
	-400:  "视频不存在或请求有误",
	-404:  "视频不存在或已被删除",
	-403:  "没有权限观看这个视频",
	62002: "视频不可见",
	62004: "视频还在审核中",
}

func classifyCode(code int, message string) error {
	if reason, ok := rejectionReasons[code]; ok {
		if msg := cleanText(message); msg != "" {
			reason += "（" + msg + "）"
		}
		return &port.RejectedError{Err: errors.New(reason)}
	}
	if message == "" {
		return fmt.Errorf("哔哩哔哩接口返回错误码 %d", code)
	}
	return fmt.Errorf("哔哩哔哩接口返回错误码 %d（%s）", code, cleanText(message))
}

// requestError 剥掉 url.Error 携带的方法和完整地址，防止签名参数进入错误和日志。
func requestError(host string, err error) error {
	var uerr *url.Error
	if errors.As(err, &uerr) {
		err = uerr.Err
	}
	if strings.Contains(err.Error(), "failed to parse Location header") {
		return fmt.Errorf("请求 %s：解析跳转地址失败", host)
	}
	return fmt.Errorf("请求 %s：%w", host, err)
}

func isDownloadHost(host string) bool {
	for _, d := range []string{"bilibili.com", "bilivideo.com", "hdslb.com"} {
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}

// downloadURL 校验并规范化平台返回的下载地址，把协议相对地址和 http 地址统一升级成 https。
func downloadURL(rawURL string) (string, error) {
	if strings.HasPrefix(rawURL, "//") {
		rawURL = "https:" + rawURL
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return "", errors.New("哔哩哔哩返回的下载地址无法解析")
	}
	host := strings.ToLower(u.Hostname())
	if !isDownloadHost(host) {
		return "", fmt.Errorf("哔哩哔哩返回的下载地址不在允许的域名内：%s", host)
	}
	if u.Scheme == "http" {
		u.Scheme = "https"
	}
	if u.Scheme != "https" {
		return "", fmt.Errorf("哔哩哔哩返回的下载地址不是 HTTPS：%s", u.Scheme)
	}
	return u.String(), nil
}

func checkDownloadRedirect(u *url.URL) error {
	host := strings.ToLower(u.Hostname())
	if !isDownloadHost(host) {
		return fmt.Errorf("哔哩哔哩下载跳转地址不在允许的域名内：%s", host)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("哔哩哔哩下载跳转地址不是 HTTPS：%s", u.Scheme)
	}
	return nil
}

// cleanText 先去掉控制字符和非法 UTF-8 再按长度截断，因为标题和作者来自用户上传的内容，会写入 PostgreSQL 的 text 列。
func cleanText(s string) string {
	s = strings.ToValidUTF8(s, "\uFFFD")
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	if r := []rune(s); len(r) > textLimit {
		s = string(r[:textLimit])
	}
	return s
}
