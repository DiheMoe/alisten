package netease

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

const userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36"

type Client struct {
	http    *http.Client
	api     *http.Client
	cookie  cookieCache
	cache   *expirable.LRU[string, any]
	observe func(source, op string, elapsed time.Duration, err error)
}

func New(httpClient *http.Client, cookie func(context.Context) (string, error), saveCookie func(context.Context, string) error, observe func(source, op string, elapsed time.Duration, err error)) *Client {
	api := *httpClient
	api.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{
		http:    httpClient,
		api:     &api,
		cookie:  cookieCache{load: cookie, store: saveCookie},
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

type response interface{ status() int }

type base struct {
	Code int `json:"code"`
}

func (b base) status() int { return b.Code }

func (c *Client) call(ctx context.Context, op, endpoint string, form url.Values, out response) error {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	cookie, err := c.cookie.get(ctx)
	if err != nil {
		return err
	}
	start := time.Now()
	err = c.post(ctx, endpoint, form, cookie, out)
	if err == nil && out.status() != http.StatusOK {
		err = fmt.Errorf("网易云的接口 %s 返回错误码 %d", op, out.status())
	}
	c.observe("netease", op, time.Since(start), err)
	return err
}

func (c *Client) post(ctx context.Context, endpoint string, form url.Values, cookie string, out response) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return errors.New("网易云的请求地址无效")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", "https://music.163.com/")
	req.Header.Set("User-Agent", userAgent)
	chinaip.Set(req.Header)
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	resp, err := c.api.Do(req)
	if err != nil {
		return requestError(req.URL.Host, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("网易云返回 HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("解析网易云的响应：%w", err)
	}
	return nil
}

func linux(data any) (url.Values, error) {
	b, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	return url.Values{"eparams": {linuxParams(b)}}, nil
}

func weapi(data any) (url.Values, error) {
	b, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	params, encSecKey := weapiParams(b, newSecretKey())
	return url.Values{"params": {params}, "encSecKey": {encSecKey}}, nil
}

func eapi(endpoint string, data any) (url.Values, error) {
	b, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	return url.Values{"params": {eapiParams(u.Path, b)}}, nil
}

func (c *Client) download(ctx context.Context, audioURL string) (io.ReadCloser, error) {
	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, audioURL, nil)
	if err != nil {
		cancel()
		return nil, errors.New("网易云的播放地址无效")
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := c.http.Do(req)
	if err != nil {
		cancel()
		return nil, requestError(req.URL.Host, err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		cancel()
		return nil, fmt.Errorf("下载网易云的音频：HTTP %d", resp.StatusCode)
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
