package qq

import (
	"bytes"
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

	"github.com/hashicorp/golang-lru/v2/expirable"

	"github.com/dihemoe/alisten/internal/lib/chinaip"
	"github.com/dihemoe/alisten/internal/lib/cookiecache"
)

const (
	requestTimeout  = 30 * time.Second
	downloadTimeout = 10 * time.Minute
	cacheSize       = 512
	cacheTTL        = 30 * time.Minute
	bodyLimit       = 4 << 20
)

type Client struct {
	http    *http.Client
	api     *http.Client
	cookie  *cookiecache.Cache
	refresh refreshState
	cache   *expirable.LRU[string, any]
	observe func(source, op string, elapsed time.Duration, err error)
	logger  *slog.Logger
}

func New(
	httpClient *http.Client,
	loadCookie func(context.Context) (string, error),
	saveCookie func(context.Context, string) error,
	swapCookie func(context.Context, string, string) (bool, error),
	observe func(source, op string, elapsed time.Duration, err error),
	logger *slog.Logger,
) *Client {
	api := *httpClient
	api.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{
		http:    httpClient,
		api:     &api,
		cookie:  cookiecache.NewWithSwap(loadCookie, saveCookie, swapCookie),
		cache:   expirable.NewLRU[string, any](cacheSize, nil, cacheTTL),
		observe: observe,
		logger:  logger,
	}
}

// interfaceError 表示网络出错、HTTP 非 200 或上游返回错误码。这类失败重试可能恢复，而响应解析或解密失败重试也不会改变结果，所以不包成 interfaceError。
type interfaceError struct{ err error }

func (e *interfaceError) Error() string { return e.err.Error() }
func (e *interfaceError) Unwrap() error { return e.err }

func cached[T any](c *Client, key string, fetch func() (T, error)) (T, error) {
	if v, ok := c.cache.Get(key); ok {
		return v.(T), nil
	}
	v, err := fetch()
	if err != nil {
		return v, err
	}
	c.cache.Add(key, v)
	return v, nil
}

type response interface{ check() error }

type status struct {
	Code int `json:"code"`
}

func (s status) check() error {
	if s.Code != 0 {
		return fmt.Errorf("QQ 音乐返回错误码 %d", s.Code)
	}
	return nil
}

func (c *Client) get(ctx context.Context, op, endpoint string, query url.Values, header http.Header, out response) error {
	cookie, err := c.cookie.Get(ctx)
	if err != nil {
		return err
	}
	return c.getWithCookie(ctx, op, endpoint, query, header, cookie, out)
}

func (c *Client) getWithCookie(ctx context.Context, op, endpoint string, query url.Values, header http.Header, cookie string, out response) error {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	if cookie != "" {
		header = header.Clone()
		header.Set("Cookie", cookie)
	}
	start := time.Now()
	err := c.fetch(ctx, endpoint+"?"+query.Encode(), header, out)
	if err == nil {
		err = out.check()
	}
	c.observe("qq", op, time.Since(start), err)
	return err
}

func (c *Client) fetch(ctx context.Context, target string, header http.Header, out response) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return errors.New("QQ 音乐的请求地址无效")
	}
	req.Header = header.Clone()
	chinaip.Set(req.Header)
	resp, err := c.api.Do(req)
	if err != nil {
		return &interfaceError{requestError(req.URL.Host, err)}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return &interfaceError{fmt.Errorf("%s 返回 HTTP %d", req.URL.Host, resp.StatusCode)}
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, bodyLimit+1))
	if err != nil {
		return &interfaceError{fmt.Errorf("读取 %s 的响应：%w", req.URL.Host, err)}
	}
	if len(raw) > bodyLimit {
		return fmt.Errorf("%s 的响应超过大小上限", req.URL.Host)
	}
	if err := json.NewDecoder(bytes.NewReader(raw)).Decode(out); err != nil {
		return fmt.Errorf("解析 %s 的响应：%w", req.URL.Host, err)
	}
	return nil
}

func (c *Client) download(ctx context.Context, audioURL string) (io.ReadCloser, error) {
	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, audioURL, nil)
	if err != nil {
		cancel()
		return nil, errors.New("QQ 音乐的播放地址无效")
	}
	req.Header = audioHeader.Clone()
	resp, err := c.http.Do(req)
	if err != nil {
		cancel()
		return nil, requestError(req.URL.Host, err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		cancel()
		return nil, fmt.Errorf("下载音频：HTTP %d", resp.StatusCode)
	}
	return &audio{ReadCloser: resp.Body, cancel: cancel}, nil
}

// requestError 按文本识别并替换 Location 解析错误，因为 net/http 在调用 CheckRedirect 前就会解析 Location 头，而且这个错误没有独立类型，原样透传会把完整的登录跳转地址带进日志。
func requestError(host string, err error) error {
	var urlError *url.Error
	if errors.As(err, &urlError) {
		err = urlError.Err
	}
	if strings.Contains(err.Error(), "failed to parse Location header") {
		return fmt.Errorf("请求 %s：解析跳转地址失败", host)
	}
	return fmt.Errorf("请求 %s：%w", host, err)
}

type audio struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (a *audio) Close() error {
	err := a.ReadCloser.Close()
	a.cancel()
	return err
}

func text(s string) string {
	return strings.ReplaceAll(strings.ToValidUTF8(s, "\uFFFD"), "\x00", "")
}

func parseQQCookies(cookie string) map[string]string {
	creds := make(map[string]string)
	for part := range strings.SplitSeq(cookie, ";") {
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if key != "" && value != "" {
			creds[key] = value
		}
	}
	return creds
}
