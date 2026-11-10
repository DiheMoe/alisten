package shortlink

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
)

const (
	maxRedirects = 5
	timeout      = 5 * time.Second
)

type Resolver struct {
	allowed func(host string) bool
	done    func(link string) bool
	client  *http.Client
}

var _ port.ShortLinks = (*Resolver)(nil)

// allowed 检查每一跳（包括起点）的主机。跳转到 done 认得的地址时就停下，不再请求它：
// 网易云的短链接最后一跳是很慢的歌单页面，逐跳请求到底会超过总超时。
func New(allowed func(host string) bool, done func(link string) bool) *Resolver {
	return &Resolver{
		allowed: allowed,
		done:    done,
		// 跳转由 Resolve 逐跳处理，这样每一跳的主机都能先检查再发请求。
		client: &http.Client{
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

func (r *Resolver) Resolve(ctx context.Context, link string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	current := link
	for hop := 0; ; hop++ {
		u, err := url.Parse(current)
		if err != nil {
			return "", fmt.Errorf("解析链接: %w", err)
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return "", fmt.Errorf("不支持的协议 %q", u.Scheme)
		}
		if !r.allowed(u.Hostname()) {
			return "", fmt.Errorf("跳转到不受信任的主机 %q", u.Hostname())
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, current, nil)
		if err != nil {
			return "", err
		}
		resp, err := r.client.Do(req)
		if err != nil {
			return "", fmt.Errorf("请求短链接: %w", err)
		}
		_ = resp.Body.Close()

		switch resp.StatusCode {
		case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther,
			http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		default:
			return current, nil
		}
		if hop == maxRedirects {
			return "", errors.New("跳转次数过多")
		}
		next, err := resp.Location()
		if err != nil {
			return "", fmt.Errorf("读取跳转地址: %w", err)
		}
		current = next.String()
		if r.done(current) {
			return current, nil
		}
	}
}
