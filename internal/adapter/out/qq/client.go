package qq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/golang-lru/v2/expirable"

	"github.com/dihemoe/alisten/internal/lib/chinaip"
)

const (
	requestTimeout    = 30 * time.Second
	downloadTimeout   = 10 * time.Minute
	cacheSize         = 512
	cacheTTL          = 30 * time.Minute
	cookieTTL         = 30 * time.Second
	cookieLoadTimeout = 10 * time.Second
)

type Client struct {
	http    *http.Client
	api     *http.Client
	cookie  cookieCache
	cache   *expirable.LRU[string, any]
	observe func(source, op string, elapsed time.Duration, err error)
}

func New(httpClient *http.Client, loadCookie func(context.Context) (string, error), saveCookie func(context.Context, string) error, observe func(source, op string, elapsed time.Duration, err error)) *Client {
	api := *httpClient
	api.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{
		http:    httpClient,
		api:     &api,
		cookie:  cookieCache{load: loadCookie, store: saveCookie},
		cache:   expirable.NewLRU[string, any](cacheSize, nil, cacheTTL),
		observe: observe,
	}
}

type cookieCache struct {
	load    func(context.Context) (string, error)
	store   func(context.Context, string) error
	mu      sync.Mutex
	value   string
	expires time.Time
}

func (c *cookieCache) get(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Now().Before(c.expires) {
		return c.value, nil
	}
	ctx, cancel := context.WithTimeout(ctx, cookieLoadTimeout)
	defer cancel()
	value, err := c.load(ctx)
	if err != nil {
		return "", err
	}
	c.value, c.expires = value, time.Now().Add(cookieTTL)
	return value, nil
}

func (c *cookieCache) put(ctx context.Context, value string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, cookieLoadTimeout)
	defer cancel()
	if err := c.store(ctx, value); err != nil {
		return err
	}
	c.value, c.expires = value, time.Now().Add(cookieTTL)
	return nil
}

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

func (c *Client) get(ctx context.Context, source, op, endpoint string, query url.Values, header http.Header, out response) error {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	if source == "qq" {
		cookie, err := c.cookie.get(ctx)
		if err != nil {
			return err
		}
		if cookie != "" {
			header = header.Clone()
			header.Set("Cookie", cookie)
		}
	}
	start := time.Now()
	err := c.fetch(ctx, endpoint+"?"+query.Encode(), header, out)
	if err == nil {
		err = out.check()
	}
	c.observe(source, op, time.Since(start), err)
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
		return requestError(req.URL.Host, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s 返回 HTTP %d", req.URL.Host, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
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

func requestError(host string, err error) error {
	var urlError *url.Error
	if errors.As(err, &urlError) {
		err = urlError.Err
	}
	// 播放地址的查询参数可能携带签名，错误包装不得泄漏完整 URL。
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
