package media

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/track"
)

const (
	resolveTimeout = 30 * time.Second
	// 各平台播放地址的有效期未知，1 分钟是保守的取值。
	reuseWindow = time.Minute
	reuseLimit  = 4096
)

var (
	ErrNoPlayback = errors.New("没有接入这个音源的播放地址")
	ErrNotInRoom  = errors.New("这首歌不在任何房间中")
)

type Playback struct {
	sources map[track.Source]port.PlayURLs
	usage   port.TrackUsage
	calls   singleflight.Group

	mu       sync.Mutex
	resolved map[track.Track]resolvedURL
}

type resolvedURL struct {
	url     string
	expires time.Time
}

func NewPlayback(sources map[track.Source]port.PlayURLs, usage port.TrackUsage) *Playback {
	return &Playback{sources: sources, usage: usage, resolved: map[track.Track]resolvedURL{}}
}

// 只为至少出现在一个房间的条目中的歌解析：/media 不需要凭证，登录的账号不能被用来解析任意的歌。这项检查每次都做，
// 通过之后才复用 1 分钟内取到的地址。返回的地址总是 https：页面是 https 的，浏览器会拦截 http 的音频。
func (p *Playback) URL(ctx context.Context, t track.Track, now time.Time) (string, error) {
	source, ok := p.sources[t.Source]
	if !ok {
		return "", ErrNoPlayback
	}
	inRoom, err := p.usage.TrackInUse(ctx, t)
	if err != nil {
		return "", err
	}
	if !inRoom {
		return "", ErrNotInRoom
	}
	if target, ok := p.reuse(t, now); ok {
		return target, nil
	}
	// 合并的解析不随发起它的请求取消：同时等待的其他请求还要用它的结果。
	result := p.calls.DoChan(string(t.Source)+"/"+t.ID, func() (v any, err error) {
		// 解析在独立的 goroutine 中执行，panic 回不到处理请求的 goroutine，net/http 无法恢复它，会让进程崩溃。
		defer func() {
			if r := recover(); r != nil {
				err = &panicError{value: r, stack: debug.Stack()}
			}
		}()
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), resolveTimeout)
		defer cancel()
		return source.PlayURL(ctx, t.ID)
	})
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case r := <-result:
		var panicked *panicError
		if errors.As(r.Err, &panicked) {
			// 音源实现中的 panic 是程序缺陷，不是音源出错：交给调用方当作内部错误记录。
			return "", panicked
		}
		if r.Err != nil {
			return "", fmt.Errorf("解析 %s %s 的播放地址：%w：%w", t.Source, t.ID, r.Err, port.ErrProviderFailed)
		}
		target, err := secure(r.Val.(string))
		if err != nil {
			return "", err
		}
		p.remember(t, target, now)
		return target, nil
	}
}

func (p *Playback) reuse(t track.Track, now time.Time) (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	r, ok := p.resolved[t]
	return r.url, ok && now.Before(r.expires)
}

func (p *Playback) remember(t track.Track, target string, now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	maps.DeleteFunc(p.resolved, func(_ track.Track, r resolvedURL) bool { return !now.Before(r.expires) })
	if _, ok := p.resolved[t]; !ok && len(p.resolved) >= reuseLimit {
		return
	}
	p.resolved[t] = resolvedURL{url: target, expires: now.Add(reuseWindow)}
}

type panicError struct {
	value any
	stack []byte
}

func (e *panicError) Error() string {
	return fmt.Sprintf("解析播放地址时 panic：%v\n%s", e.value, e.stack)
}

// 只替换 scheme，其余部分原样保留：签名过的地址重新编码之后可能失效。
func secure(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return "", fmt.Errorf("音源给出的播放地址不是 http 或 https 的绝对地址：%w", port.ErrProviderFailed)
	}
	return "https" + raw[strings.Index(raw, ":"):], nil
}
