package bilibili

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
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
)

type Client struct {
	api            *http.Client
	download       *http.Client
	root           string
	loadCredential func(context.Context) (string, error)
	saveCredential func(context.Context, string) error
	observe        func(source, op string, elapsed time.Duration, err error)
}

var (
	_ port.BilibiliSource        = (*Client)(nil)
	_ port.BilibiliLinks         = (*Client)(nil)
	_ port.BilibiliQRLoginSource = (*Client)(nil)
)

// upstream 用于全部平台请求，不能带 Timeout：API 请求的期限由 context 控制（30 秒）。
// 下载复制一份 client，保留它的 Transport，期限放宽到 downloadTimeout；每次跳转都重新校验目标地址。
// root 是下载中转文件的目录，必须是进程崩溃后残留会被启动清理删掉的目录（worker 的工作目录）；
// 不下载文件的进程传空字符串，此时 Download 显式失败而不是写进系统临时目录。
func New(upstream *http.Client, root string, loadCredential func(context.Context) (string, error), saveCredential func(context.Context, string) error, observe func(source, op string, elapsed time.Duration, err error)) *Client {
	download := *upstream
	download.Timeout = downloadTimeout
	download.CheckRedirect = func(req *http.Request, _ []*http.Request) error {
		return checkDownloadURL(req.URL)
	}
	return &Client{
		api:            upstream,
		download:       &download,
		root:           root,
		loadCredential: loadCredential,
		saveCredential: saveCredential,
		observe:        observe,
	}
}

func (c *Client) report(op string, start time.Time, err error) {
	if c.observe != nil {
		c.observe("bilibili", op, time.Since(start), err)
	}
}

// 凭据对调用方是不透明字符串；Cookie 和 refresh token 的编码由本包决定。
type credential struct {
	Cookie       string `json:"cookie"`
	RefreshToken string `json:"refresh_token,omitempty"`
}

func encodeCredential(cookie, refreshToken string) string {
	b, _ := json.Marshal(credential{Cookie: cookie, RefreshToken: refreshToken})
	return string(b)
}

func decodeCredential(raw string) (credential, error) {
	var cred credential
	if err := json.Unmarshal([]byte(raw), &cred); err != nil {
		return credential{}, fmt.Errorf("bilibili 凭据格式无法解析：%w", err)
	}
	return cred, nil
}

// cookie 读取保存的凭据；没有凭据时返回空字符串。
func (c *Client) cookie(ctx context.Context) (string, error) {
	raw, err := c.loadCredential(ctx)
	if err != nil {
		return "", fmt.Errorf("读取 Bilibili 凭据：%w", err)
	}
	if raw == "" {
		return "", nil
	}
	cred, err := decodeCredential(raw)
	if err != nil {
		return "", err
	}
	return cred.Cookie, nil
}

// 请求地址可能含敏感查询参数，返回错误不得携带地址。
func (c *Client) get(ctx context.Context, rawURL, cookie string) ([]byte, []*http.Cookie, error) {
	ctx, cancel := context.WithTimeout(ctx, apiTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Referer", referer)
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	resp, err := c.api.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("请求 B 站接口失败：%w", requestError(err))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("bilibili 接口返回 HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, bodyLimit+1))
	if err != nil {
		return nil, nil, fmt.Errorf("读取 B 站接口的响应：%w", err)
	}
	if len(body) > bodyLimit {
		return nil, nil, errors.New("bilibili 接口的响应过大")
	}
	return body, resp.Cookies(), nil
}

type envelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// getData 解开 code/message/data 信封；code 非 0 时按平台拒绝或普通错误分类。
func (c *Client) getData(ctx context.Context, rawURL, cookie string, data any) error {
	body, _, err := c.get(ctx, rawURL, cookie)
	if err != nil {
		return err
	}
	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return fmt.Errorf("解析 B 站接口的响应：%w", err)
	}
	if env.Code != 0 {
		return classifyCode(env.Code, env.Message)
	}
	if err := json.Unmarshal(env.Data, data); err != nil {
		return fmt.Errorf("解析 B 站接口的数据：%w", err)
	}
	return nil
}

// 这些 code 表示平台明确拒绝了这首歌：准备不再重试。
var rejectionReasons = map[int]string{
	-400:  "视频不存在或请求有误",
	-404:  "视频不存在或已被删除",
	-403:  "没有权限观看这个视频",
	62002: "视频不可见",
	62004: "视频还在审核中",
}

func classifyCode(code int, message string) error {
	if reason, ok := rejectionReasons[code]; ok {
		if message != "" {
			reason += "（" + message + "）"
		}
		return &port.RejectedError{Err: errors.New(reason)}
	}
	if message == "" {
		return fmt.Errorf("bilibili 接口返回错误：code=%d", code)
	}
	return fmt.Errorf("bilibili 接口返回错误：code=%d message=%s", code, message)
}

// requestError 去掉 url.Error 携带的方法和地址：签名地址不能进错误和日志。
func requestError(err error) error {
	var uerr *url.Error
	if errors.As(err, &uerr) {
		return uerr.Err
	}
	return err
}

// isDownloadHost 只允许 B 站的域名：bilibili.com、bilivideo.com、hdslb.com 和它们的子域名。
func isDownloadHost(host string) bool {
	for _, d := range []string{"bilibili.com", "bilivideo.com", "hdslb.com"} {
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}

// downloadURL 校验并规范化平台返回的地址：协议相对地址补成 https，主机必须在 B 站域名内，http 升级成 https。
func downloadURL(rawURL string) (string, error) {
	if strings.HasPrefix(rawURL, "//") {
		rawURL = "https:" + rawURL
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return "", errors.New("平台返回的下载地址无法解析")
	}
	if err := checkDownloadURL(u); err != nil {
		return "", err
	}
	return u.String(), nil
}

func checkDownloadURL(u *url.URL) error {
	host := strings.ToLower(u.Hostname())
	if !isDownloadHost(host) {
		return fmt.Errorf("平台返回的下载地址不在 B 站域名内：%s", host)
	}
	if u.Scheme == "http" {
		u.Scheme = "https"
	}
	if u.Scheme != "https" {
		return fmt.Errorf("平台返回的下载地址不是 HTTPS：%s", u.Scheme)
	}
	return nil
}
