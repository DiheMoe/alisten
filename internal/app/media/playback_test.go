package media

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/track"
)

var t0 = time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)

type playURLs struct {
	mu    sync.Mutex
	urls  map[string]string
	errs  map[string]error
	calls int
	hold  chan struct{}
}

func (p *playURLs) PlayURL(ctx context.Context, id string) (string, error) {
	p.mu.Lock()
	p.calls++
	hold := p.hold
	p.mu.Unlock()
	if hold != nil {
		<-hold
	}
	if _, ok := ctx.Deadline(); !ok {
		return "", errors.New("解析没有期限")
	}
	// 像真实的音源那样：请求的 ctx 结束了，解析就失败。
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return p.urls[id], p.errs[id]
}

func (p *playURLs) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

type inRooms map[track.Track]bool

func (r inRooms) TrackInUse(_ context.Context, t track.Track) (bool, error) {
	return r[t], nil
}

func queued(ids ...string) inRooms {
	r := inRooms{}
	for _, id := range ids {
		r[track.Track{Source: track.NetEase, ID: id}] = true
	}
	return r
}

var errDown = errors.New("连接中断")

type brokenRooms struct{}

func (brokenRooms) TrackInUse(context.Context, track.Track) (bool, error) {
	return false, errDown
}

func TestPlaybackURL(t *testing.T) {
	urls := &playURLs{
		urls: map[string]string{"1": "http://m701.music.126.net/a.mp3?vuutv=x%2By", "2": "https://m701.music.126.net/b.mp3", "4": "ftp://m701.music.126.net/d.mp3", "7": "https://:8080/e.mp3"},
		errs: map[string]error{"5": &port.RejectedError{Err: errors.New("网易云没有提供这首歌的播放地址")}},
	}
	p := NewPlayback(map[track.Source]port.PlayURLs{track.NetEase: urls}, queued("1", "2", "4", "5", "7"))
	for id, want := range map[string]string{"1": "https://m701.music.126.net/a.mp3?vuutv=x%2By", "2": "https://m701.music.126.net/b.mp3"} {
		if got, err := p.URL(t.Context(), track.Track{Source: track.NetEase, ID: id}, t0); err != nil || got != want {
			t.Errorf("第 %s 首的地址 = %q, %v，期望 %q：只把 http 换成 https，其余原样保留", id, got, err, want)
		}
	}
	for _, id := range []string{"4", "5", "7"} {
		if _, err := p.URL(t.Context(), track.Track{Source: track.NetEase, ID: id}, t0); !errors.Is(err, port.ErrProviderFailed) {
			t.Errorf("第 %s 首 = %v，期望 ErrProviderFailed", id, err)
		}
	}
	if _, err := p.URL(t.Context(), track.Track{Source: track.QQ, ID: "1"}, t0); !errors.Is(err, ErrNoPlayback) {
		t.Errorf("没有登记的来源 = %v，期望 ErrNoPlayback", err)
	}
}

func TestPlaybackOnlyForTracksInRooms(t *testing.T) {
	urls := &playURLs{urls: map[string]string{"1": "https://m701.music.126.net/a.mp3", "2": "https://m701.music.126.net/b.mp3"}}
	rooms := queued("1")
	sources := map[track.Source]port.PlayURLs{track.NetEase: urls}
	p := NewPlayback(sources, rooms)
	sunny, rain := track.Track{Source: track.NetEase, ID: "1"}, track.Track{Source: track.NetEase, ID: "2"}

	if _, err := p.URL(t.Context(), rain, t0); !errors.Is(err, ErrNotInRoom) || urls.count() != 0 {
		t.Errorf("不在房间里的歌 = %v，解析了 %d 次，期望 ErrNotInRoom，不请求音源", err, urls.count())
	}
	if got, err := p.URL(t.Context(), sunny, t0); err != nil || got != "https://m701.music.126.net/a.mp3" {
		t.Errorf("房间里的歌 = %q, %v", got, err)
	}
	delete(rooms, sunny)
	if _, err := p.URL(t.Context(), sunny, t0); !errors.Is(err, ErrNotInRoom) {
		t.Errorf("移出房间之后 = %v，期望 ErrNotInRoom：复用的地址也只给房间里的歌", err)
	}
	if _, err := NewPlayback(sources, brokenRooms{}).URL(t.Context(), sunny, t0); !errors.Is(err, errDown) || errors.Is(err, port.ErrProviderFailed) {
		t.Errorf("查不了房间时 = %v，期望原样返回存储的错误", err)
	}
	if n := urls.count(); n != 1 {
		t.Errorf("解析了 %d 次，期望只有房间里的那一次", n)
	}
}

func TestPlaybackReusesURLForAMinute(t *testing.T) {
	urls := &playURLs{
		urls: map[string]string{"1": "http://m701.music.126.net/a.mp3", "3": "/relative.mp3"},
		errs: map[string]error{"6": errors.New("连接被重置")},
	}
	p := NewPlayback(map[track.Source]port.PlayURLs{track.NetEase: urls}, queued("1", "3", "6"))
	sunny := track.Track{Source: track.NetEase, ID: "1"}
	for i, step := range []struct {
		at    time.Duration
		calls int
	}{{0, 1}, {59 * time.Second, 1}, {time.Minute, 2}, {time.Minute + 59*time.Second, 2}, {2 * time.Minute, 3}} {
		got, err := p.URL(t.Context(), sunny, t0.Add(step.at))
		if err != nil || got != "https://m701.music.126.net/a.mp3" || urls.count() != step.calls {
			t.Errorf("第 %d 次请求（%v 之后）= %q, %v，共解析 %d 次，期望 %d 次", i+1, step.at, got, err, urls.count(), step.calls)
		}
	}

	for _, id := range []string{"3", "6"} {
		before := urls.count()
		for range 2 {
			if _, err := p.URL(t.Context(), track.Track{Source: track.NetEase, ID: id}, t0); !errors.Is(err, port.ErrProviderFailed) {
				t.Errorf("第 %s 首 = %v，期望 ErrProviderFailed", id, err)
			}
		}
		if n := urls.count() - before; n != 2 {
			t.Errorf("第 %s 首请求两次解析了 %d 次，期望每次都重新解析：失败不复用", id, n)
		}
	}
}

func TestPlaybackRemembersAtMost4096Tracks(t *testing.T) {
	urls := &playURLs{urls: map[string]string{}}
	ids := make([]string, reuseLimit+1)
	for i := range ids {
		ids[i] = strconv.Itoa(i)
		urls.urls[ids[i]] = "https://m701.music.126.net/" + ids[i] + ".mp3"
	}
	p := NewPlayback(map[track.Source]port.PlayURLs{track.NetEase: urls}, queued(ids...))
	resolve := func(id string, at time.Duration) {
		t.Helper()
		if _, err := p.URL(t.Context(), track.Track{Source: track.NetEase, ID: id}, t0.Add(at)); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range ids {
		resolve(id, 0)
	}
	resolve(ids[0], time.Second)
	resolve(ids[reuseLimit], time.Second)
	if n := urls.count(); n != reuseLimit+2 {
		t.Errorf("解析了 %d 次，期望 %d 次：前 %d 首复用，满了之后的第 %d 首没有记下", n, reuseLimit+2, reuseLimit, reuseLimit+1)
	}
	resolve(ids[reuseLimit], time.Minute)
	resolve(ids[reuseLimit], time.Minute+time.Second)
	if n := urls.count(); n != reuseLimit+3 {
		t.Errorf("解析了 %d 次，期望 %d 次：写入时清掉过期的条目，之后能记下新的歌", n, reuseLimit+3)
	}
}

func TestPlaybackCoalescesConcurrentRequests(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		urls := &playURLs{urls: map[string]string{"1": "https://m701.music.126.net/a.mp3"}, hold: make(chan struct{})}
		release := sync.OnceFunc(func() { close(urls.hold) })
		defer release()
		p := NewPlayback(map[track.Source]port.PlayURLs{track.NetEase: urls}, queued("1"))
		sunny := track.Track{Source: track.NetEase, ID: "1"}
		first, cancelFirst := context.WithCancel(t.Context())
		firstDone := make(chan error, 1)
		go func() {
			_, err := p.URL(first, sunny, t0)
			firstDone <- err
		}()
		synctest.Wait()
		var wg sync.WaitGroup
		results := make([]string, 2)
		for i := range results {
			wg.Go(func() { results[i], _ = p.URL(t.Context(), sunny, t0) })
			synctest.Wait()
		}

		cancelFirst()
		synctest.Wait()
		select {
		case err := <-firstDone:
			if !errors.Is(err, context.Canceled) {
				t.Errorf("取消的请求返回 %v，期望 context.Canceled", err)
			}
		default:
			t.Error("发起解析的请求取消之后没有立即返回")
		}
		release()
		wg.Wait()
		for i, got := range results {
			if got != "https://m701.music.126.net/a.mp3" {
				t.Errorf("第 %d 个等待的请求得到 %q", i+1, got)
			}
		}
		if n := urls.count(); n != 1 {
			t.Errorf("同时到达的 3 个请求解析了 %d 次，期望 1 次", n)
		}
		if _, err := p.URL(t.Context(), sunny, t0.Add(reuseWindow)); err != nil || urls.count() != 2 {
			t.Errorf("1 分钟之后的请求 = %v，解析了 %d 次，期望重新解析", err, urls.count())
		}
	})
}

func TestPlaybackWaiterLeavesWithoutStoppingResolution(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		urls := &playURLs{urls: map[string]string{"1": "https://m701.music.126.net/a.mp3"}, hold: make(chan struct{})}
		release := sync.OnceFunc(func() { close(urls.hold) })
		defer release()
		p := NewPlayback(map[track.Source]port.PlayURLs{track.NetEase: urls}, queued("1"))
		sunny := track.Track{Source: track.NetEase, ID: "1"}
		stays := make(chan string, 1)
		go func() {
			got, _ := p.URL(t.Context(), sunny, t0)
			stays <- got
		}()
		synctest.Wait()
		leaving, leave := context.WithCancel(t.Context())
		leaves := make(chan error, 1)
		go func() {
			_, err := p.URL(leaving, sunny, t0)
			leaves <- err
		}()
		synctest.Wait()

		leave()
		synctest.Wait()
		select {
		case err := <-leaves:
			if !errors.Is(err, context.Canceled) {
				t.Errorf("离开的请求返回 %v，期望 context.Canceled", err)
			}
		default:
			t.Error("客户端离开之后请求没有立即返回")
		}
		select {
		case got := <-stays:
			t.Errorf("共用的解析还没有结束，另一个请求就返回了 %q", got)
		default:
		}
		release()
		synctest.Wait()
		if got := <-stays; got != "https://m701.music.126.net/a.mp3" {
			t.Errorf("留下的请求得到 %q", got)
		}
		if n := urls.count(); n != 1 {
			t.Errorf("解析了 %d 次，期望 1 次", n)
		}
	})
}

type panicking struct{}

func (panicking) PlayURL(context.Context, string) (string, error) { panic("音源出错了") }

func TestPlaybackTurnsPanicIntoError(t *testing.T) {
	p := NewPlayback(map[track.Source]port.PlayURLs{track.NetEase: panicking{}}, queued("1"))
	_, err := p.URL(t.Context(), track.Track{Source: track.NetEase, ID: "1"}, t0)
	if err == nil || !strings.Contains(err.Error(), "音源出错了") || errors.Is(err, port.ErrProviderFailed) {
		t.Errorf("音源 panic 时 = %v，期望带着 panic 内容、不属于音源出错的错误", err)
	}
}
