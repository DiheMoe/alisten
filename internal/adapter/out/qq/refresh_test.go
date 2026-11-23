package qq

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/lib/cookiecache"
)

type cookieStore struct {
	mu      sync.Mutex
	value   string
	saveErr error
}

func (s *cookieStore) load(context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.value, nil
}

func (s *cookieStore) save(_ context.Context, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.saveErr != nil {
		return s.saveErr
	}
	s.value = value
	return nil
}

func (s *cookieStore) swap(_ context.Context, expected, value string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.saveErr != nil {
		return false, s.saveErr
	}
	if s.value != expected {
		return false, nil
	}
	s.value = value
	return true, nil
}

func (s *cookieStore) get() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.value
}

func (s *cookieStore) set(value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.value = value
}

func newRefreshTestClient(t *testing.T, store *cookieStore, handler http.HandlerFunc) (*Client, *observed, *bytes.Buffer) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		checkClientIP(t, r)
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	target, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	obs := &observed{}
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	client := New(&http.Client{Transport: redirect{target}}, store.load, store.save, store.swap, obs.record, logger)
	return client, obs, &logs
}

type refreshPayload struct {
	Comm map[string]string `json:"comm"`
	Req  struct {
		Module string `json:"module"`
		Method string `json:"method"`
		Param  struct {
			OpenID       string `json:"openid"`
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
			ExpiredIn    int    `json:"expired_in"`
			MusicID      any    `json:"musicid"`
			MusicKey     string `json:"musickey"`
			RefreshKey   string `json:"refresh_key"`
			LoginMode    int    `json:"loginMode"`
		} `json:"param"`
	} `json:"req"`
}

func decodeRefreshPayload(t *testing.T, r *http.Request) refreshPayload {
	t.Helper()
	if r.Method != http.MethodPost {
		t.Errorf("刷新请求方法 = %s，期望 POST", r.Method)
	}
	if got := r.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("刷新请求 Content-Type = %q", got)
	}
	if got := r.Header.Get("Referer"); got != "https://y.qq.com/" {
		t.Errorf("刷新请求 Referer = %q", got)
	}
	if got := r.Header.Get("Origin"); got != "https://y.qq.com" {
		t.Errorf("刷新请求 Origin = %q", got)
	}
	var p refreshPayload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		t.Fatalf("解析刷新请求体：%v", err)
	}
	return p
}

func TestRefreshLoginProactiveQQ(t *testing.T) {
	created := time.Now().Add(-25 * time.Hour).Unix()
	initial := "access_token=old-at; keyExpiresIn=259200; loginType=2; musicid=123456; musickey=Q_H_L_old; musickeyCreateTime=" +
		strconv.FormatInt(created, 10) + "; openid=openid-1; qm_keyst=Q_H_L_old; qqmusic_key=Q_H_L_old; refresh_token=old-rt; uin=123456"
	store := &cookieStore{value: initial}
	newCreated := time.Now().Unix()

	var hits atomic.Int32
	client, obs, logs := newRefreshTestClient(t, store, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		p := decodeRefreshPayload(t, r)
		if p.Comm["tmeLoginType"] != "2" || p.Comm["platform"] != "yqq.json" || p.Comm["ct"] != "24" || p.Comm["cv"] != "4747474" {
			t.Errorf("刷新 comm = %#v", p.Comm)
		}
		if p.Req.Module != "music.login.LoginServer" || p.Req.Method != "Login" {
			t.Errorf("刷新模块和方法 = %s %s", p.Req.Module, p.Req.Method)
		}
		if p.Req.Param.LoginMode != 2 || p.Req.Param.OpenID != "openid-1" || p.Req.Param.AccessToken != "old-at" ||
			p.Req.Param.RefreshToken != "old-rt" || p.Req.Param.MusicKey != "Q_H_L_old" || p.Req.Param.RefreshKey != "" {
			t.Errorf("刷新参数 = %#v", p.Req.Param)
		}
		if got, ok := p.Req.Param.MusicID.(float64); !ok || int64(got) != 123456 {
			t.Errorf("musicid = %#v，期望整数 123456", p.Req.Param.MusicID)
		}
		body(`{"code":0,"req":{"code":0,"data":{"musickey":"Q_H_L_new","musicid":123456,"access_token":"new-at","refresh_token":"new-rt","openid":"openid-1","expired_at":1800000000,"keyExpiresIn":259200,"musickeyCreateTime":`+
			strconv.FormatInt(newCreated, 10)+`,"loginType":2}}}`)(w, r)
	})

	if err := client.RefreshLogin(t.Context()); err != nil {
		t.Fatalf("主动刷新失败：%v", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("刷新请求次数 = %d，期望 1", hits.Load())
	}
	requireQQCookies(t, store.get(), map[string]string{
		"musickey":           "Q_H_L_new",
		"qm_keyst":           "Q_H_L_new",
		"qqmusic_key":        "Q_H_L_new",
		"access_token":       "new-at",
		"refresh_token":      "new-rt",
		"openid":             "openid-1",
		"musicid":            "123456",
		"uin":                "123456",
		"keyExpiresIn":       "259200",
		"musickeyCreateTime": strconv.FormatInt(newCreated, 10),
		"loginType":          "2",
	})
	if strings.Contains(store.get(), "Q_H_L_old") {
		t.Errorf("刷新后的 Cookie 仍包含旧的 musickey：%q", store.get())
	}
	if strings.Contains(logs.String(), "Q_H_L_new") || strings.Contains(logs.String(), "old-rt") || strings.Contains(logs.String(), "new-rt") {
		t.Errorf("刷新日志泄露了凭据：%s", logs.String())
	}

	// 再次调用不应请求上游，因为刚刷新过的凭据还没到下一次刷新窗口。
	if err := client.RefreshLogin(t.Context()); err != nil {
		t.Fatalf("第二次主动刷新：%v", err)
	}
	if hits.Load() != 1 {
		t.Errorf("刚刷新完又发起了第 %d 次刷新请求", hits.Load())
	}
	obs.check(t, "qq login_refresh")
}

func TestRefreshLoginSkipsWhenNotNeeded(t *testing.T) {
	fresh := time.Now().Add(-1 * time.Hour).Unix()
	for _, tc := range []struct {
		name   string
		cookie string
	}{
		{"未配置凭据", ""},
		{"缺少刷新字段", "uin=123456; qm_keyst=Q_H_L_only"},
		{"未到刷新窗口", "musicid=123456; musickey=Q_H_L_fresh; refresh_token=rt; keyExpiresIn=259200; musickeyCreateTime=" + strconv.FormatInt(fresh, 10)},
		{"微信 loginType", "loginType=1; musicid=998877; musickey=key; refresh_key=wx-rk; refresh_token=wx-rt"},
		{"微信 W_X 前缀", "musicid=998877; musickey=W_X_old; refresh_key=wx-rk; refresh_token=wx-rt"},
		{"微信 wxuin", "wxuin=998877; musickey=key; refresh_token=wx-rt; wxaccess_token=wx-at"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &cookieStore{value: tc.cookie}
			client, obs, _ := newRefreshTestClient(t, store, func(w http.ResponseWriter, r *http.Request) {
				t.Errorf("不应发起刷新请求：%s %s", r.Method, r.URL)
				w.WriteHeader(http.StatusInternalServerError)
			})
			if err := client.RefreshLogin(t.Context()); err != nil {
				t.Fatalf("RefreshLogin = %v，期望 nil", err)
			}
			obs.check(t)
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func newInMemoryRefreshClient(t *testing.T, store *cookieStore, rt roundTripFunc) (*Client, *observed) {
	t.Helper()
	obs := &observed{}
	api := &http.Client{
		Transport:     rt,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	// 不构造带后台清理协程的 LRU 缓存，因为 synctest 要求退出前气泡内的 goroutine 全部结束。
	client := &Client{
		http:    &http.Client{Transport: rt},
		api:     api,
		cookie:  cookiecache.NewWithSwap(store.load, store.save, store.swap),
		observe: obs.record,
		logger:  testLogger,
	}
	return client, obs
}

func jsonResponse(r *http.Request, payload string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(payload)),
		Request:    r,
	}
}

func TestRefreshLoginRespectsShortLifetimeHalfWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		created := time.Now().Unix()
		store := &cookieStore{
			value: "musicid=123456; musickey=Q_H_L_short; refresh_token=rt; keyExpiresIn=7200; musickeyCreateTime=" + strconv.FormatInt(created, 10),
		}
		var hits atomic.Int32
		client, _ := newInMemoryRefreshClient(t, store, func(r *http.Request) (*http.Response, error) {
			hits.Add(1)
			return jsonResponse(r, `{"code":0,"req":{"code":0,"data":{"musickey":"Q_H_L_short2","musicid":123456,"keyExpiresIn":7200,"musickeyCreateTime":`+
				strconv.FormatInt(time.Now().Unix(), 10)+`}}}`), nil
		})
		time.Sleep(time.Hour - time.Second)
		if err := client.RefreshLogin(t.Context()); err != nil || hits.Load() != 0 {
			t.Fatalf("未过半期时刷新次数 = %d, err = %v", hits.Load(), err)
		}
		time.Sleep(time.Second)
		if err := client.RefreshLogin(t.Context()); err != nil || hits.Load() != 1 {
			t.Fatalf("过半期后刷新次数 = %d, err = %v", hits.Load(), err)
		}
	})
}

func TestRefreshLoginErrors(t *testing.T) {
	baseCookie := "musicid=123456; musickey=Q_H_L_old; refresh_token=rt-secret"

	t.Run("平台拒绝刷新返回登录失效", func(t *testing.T) {
		store := &cookieStore{value: baseCookie}
		client, obs, logs := newRefreshTestClient(t, store, func(w http.ResponseWriter, r *http.Request) {
			body(`{"code":0,"req":{"code":200001,"data":{}}}`)(w, r)
		})
		err := client.RefreshLogin(t.Context())
		if !errors.Is(err, port.ErrSourceSignedOut) {
			t.Fatalf("错误 = %v，期望 ErrSourceSignedOut", err)
		}
		if store.get() != baseCookie {
			t.Errorf("刷新失败时改写了 Cookie：%q", store.get())
		}
		if strings.Contains(logs.String(), "rt-secret") || strings.Contains(logs.String(), "Q_H_L_old") {
			t.Errorf("失败日志泄露了凭据：%s", logs.String())
		}
		obs.check(t, "qq login_refresh 失败")
	})

	t.Run("保存刷新结果失败返回可重试错误且不污染缓存", func(t *testing.T) {
		saveErr := errors.New("数据库暂时不可用")
		store := &cookieStore{value: baseCookie, saveErr: saveErr}
		client, _, _ := newRefreshTestClient(t, store, func(w http.ResponseWriter, r *http.Request) {
			body(`{"code":0,"req":{"code":0,"data":{"musickey":"Q_H_L_new","musicid":123456}}}`)(w, r)
		})
		err := client.RefreshLogin(t.Context())
		if !errors.Is(err, saveErr) || errors.Is(err, port.ErrSourceSignedOut) {
			t.Fatalf("保存失败的错误 = %v，期望包装 %v", err, saveErr)
		}
		if cached, err := client.cookie.Get(t.Context()); err != nil || cached != baseCookie {
			t.Errorf("保存失败后的本地缓存 = %q, %v，期望保留旧值", cached, err)
		}
	})

	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
	}{
		{"HTTP 502", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) }},
		{"顶层错误码非零", body(`{"code":1,"req":{"code":0,"data":{"musickey":"Q_H_L_new"}}}`)},
		{"缺少新音乐 key", body(`{"code":0,"req":{"code":0,"data":{"musicid":123456}}}`)},
		{"响应 JSON 畸形", body(`{"code":0,`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &cookieStore{value: baseCookie}
			client, _, _ := newRefreshTestClient(t, store, tc.handler)
			err := client.RefreshLogin(t.Context())
			if err == nil || errors.Is(err, port.ErrSourceSignedOut) {
				t.Fatalf("临时故障的错误 = %v，期望可重试的非登录失效错误", err)
			}
			if store.get() != baseCookie {
				t.Errorf("刷新失败时改写了 Cookie：%q", store.get())
			}
		})
	}
}

func TestPlayURLAutoRefreshesExpiredCookie(t *testing.T) {
	initial := "musicid=123456; musickey=Q_H_L_old; qm_keyst=Q_H_L_old; qqmusic_key=Q_H_L_old; refresh_token=rt-1; uin=123456"
	store := &cookieStore{value: initial}
	var refreshHits atomic.Int32

	client, obs, _ := newRefreshTestClient(t, store, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			refreshHits.Add(1)
			body(`{"code":0,"req":{"code":0,"data":{"musickey":"Q_H_L_new","musicid":123456,"refresh_token":"rt-2"}}}`)(w, r)
			return
		}
		cookie := r.Header.Get("Cookie")
		if strings.Contains(r.URL.Query().Get("data"), "DissInfo") {
			if strings.Contains(cookie, "Q_H_L_new") {
				body(accountValidResponse)(w, r)
			} else {
				body(`{"code":0,"req":{"code":10004,"data":{}}}`)(w, r)
			}
			return
		}
		if strings.Contains(cookie, "Q_H_L_new") {
			body(`{"code":0,"req_1":{"code":0,"data":{"midurlinfo":[{"filename":"F000idid.flac","purl":"fresh.flac"}]}}}`)(w, r)
			return
		}
		body(`{"code":0,"req_1":{"code":0,"data":{"midurlinfo":[]}}}`)(w, r)
	})

	got, err := client.PlayURL(t.Context(), "id")
	if err != nil || got != "https://ws.stream.qqmusic.qq.com/fresh.flac" {
		t.Fatalf("PlayURL = %q, %v，期望刷新后拿到播放地址", got, err)
	}
	if refreshHits.Load() != 1 {
		t.Errorf("刷新次数 = %d，期望 1", refreshHits.Load())
	}
	requireQQCookies(t, store.get(), map[string]string{
		"musickey": "Q_H_L_new", "qm_keyst": "Q_H_L_new", "qqmusic_key": "Q_H_L_new", "refresh_token": "rt-2",
	})
	obs.check(t, "qq play_url", "qq account", "qq login_refresh", "qq play_url")
}

func TestPlayURLUsesCookieRefreshedByAnotherInstance(t *testing.T) {
	oldCookie := "musicid=123456; musickey=Q_H_L_old; qm_keyst=Q_H_L_old; refresh_token=rt-1; uin=123456"
	newCookie := "musicid=123456; musickey=Q_H_L_other; qm_keyst=Q_H_L_other; refresh_token=rt-2; uin=123456"
	store := &cookieStore{value: oldCookie}

	client, obs, _ := newRefreshTestClient(t, store, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			t.Error("存储里已有新凭据，不应再调用刷新接口")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		cookie := r.Header.Get("Cookie")
		if strings.Contains(r.URL.Query().Get("data"), "DissInfo") {
			// 模拟其他实例此时已经写回了新凭据。
			store.set(newCookie)
			body(`{"code":0,"req":{"code":10004,"data":{}}}`)(w, r)
			return
		}
		if strings.Contains(cookie, "Q_H_L_other") {
			body(`{"code":0,"req_1":{"code":0,"data":{"midurlinfo":[{"filename":"M800idid.mp3","purl":"from-other.mp3"}]}}}`)(w, r)
			return
		}
		body(`{"code":0,"req_1":{"code":0,"data":{"midurlinfo":[]}}}`)(w, r)
	})

	got, err := client.PlayURL(t.Context(), "id")
	if err != nil || got != "https://ws.stream.qqmusic.qq.com/from-other.mp3" {
		t.Fatalf("PlayURL = %q, %v，期望复用到其他实例刷新后的凭据", got, err)
	}
	obs.check(t, "qq play_url", "qq account", "qq play_url")
}

func TestPlayURLAutoRefreshOutcomes(t *testing.T) {
	baseCookie := "musicid=123456; musickey=Q_H_L_old; qm_keyst=Q_H_L_old; refresh_token=rt-1; uin=123456"

	t.Run("微信凭据失效时跳过刷新直接报告失效", func(t *testing.T) {
		store := &cookieStore{value: "musicid=998877; musickey=W_X_old; qm_keyst=W_X_old; refresh_key=wx-rk; refresh_token=wx-rt; uin=998877"}
		client, obs, _ := newRefreshTestClient(t, store, func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				t.Error("微信凭据不应发起刷新请求")
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			if strings.Contains(r.URL.Query().Get("data"), "DissInfo") {
				body(`{"code":0,"req":{"code":10004,"data":{}}}`)(w, r)
				return
			}
			body(`{"code":0,"req_1":{"code":0,"data":{"midurlinfo":[]}}}`)(w, r)
		})
		if _, err := client.PlayURL(t.Context(), "id"); !errors.Is(err, port.ErrSourceSignedOut) {
			t.Fatalf("错误 = %v，期望 ErrSourceSignedOut", err)
		}
		obs.check(t, "qq play_url", "qq account")
	})

	t.Run("刷新被平台拒绝后冷却期内不重复请求", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			store := &cookieStore{value: baseCookie}
			var refreshHits atomic.Int32
			client, _ := newInMemoryRefreshClient(t, store, func(r *http.Request) (*http.Response, error) {
				if r.Method == http.MethodPost {
					refreshHits.Add(1)
					return jsonResponse(r, `{"code":0,"req":{"code":200001,"data":{}}}`), nil
				}
				if strings.Contains(r.URL.Query().Get("data"), "DissInfo") {
					return jsonResponse(r, `{"code":0,"req":{"code":10004,"data":{}}}`), nil
				}
				return jsonResponse(r, `{"code":0,"req_1":{"code":0,"data":{"midurlinfo":[]}}}`), nil
			})
			for range 2 {
				if _, err := client.PlayURL(t.Context(), "id"); !errors.Is(err, port.ErrSourceSignedOut) {
					t.Fatalf("错误 = %v，期望 ErrSourceSignedOut", err)
				}
			}
			if refreshHits.Load() != 1 {
				t.Fatalf("冷却期内刷新请求次数 = %d，期望 1", refreshHits.Load())
			}
			time.Sleep(qqRefreshRejectCooldown)
			if _, err := client.PlayURL(t.Context(), "id"); !errors.Is(err, port.ErrSourceSignedOut) {
				t.Fatalf("冷却期结束后错误 = %v，期望 ErrSourceSignedOut", err)
			}
			if refreshHits.Load() != 2 {
				t.Errorf("冷却期结束后刷新请求次数 = %d，期望 2", refreshHits.Load())
			}
		})
	})

	t.Run("刷新遇到临时故障时返回可重试错误", func(t *testing.T) {
		store := &cookieStore{value: baseCookie}
		client, _, _ := newRefreshTestClient(t, store, func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			if strings.Contains(r.URL.Query().Get("data"), "DissInfo") {
				body(`{"code":0,"req":{"code":10004,"data":{}}}`)(w, r)
				return
			}
			body(`{"code":0,"req_1":{"code":0,"data":{"midurlinfo":[]}}}`)(w, r)
		})
		_, err := client.PlayURL(t.Context(), "id")
		var rejected *port.RejectedError
		if err == nil || errors.Is(err, port.ErrSourceSignedOut) || errors.As(err, &rejected) {
			t.Fatalf("临时刷新故障的错误 = %v，期望普通可重试错误", err)
		}
	})

	t.Run("刷新成功但歌曲本身无版权时返回音源拒绝", func(t *testing.T) {
		store := &cookieStore{value: baseCookie}
		client, _, _ := newRefreshTestClient(t, store, func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				body(`{"code":0,"req":{"code":0,"data":{"musickey":"Q_H_L_new","musicid":123456}}}`)(w, r)
				return
			}
			if strings.Contains(r.URL.Query().Get("data"), "DissInfo") {
				if strings.Contains(r.Header.Get("Cookie"), "Q_H_L_new") {
					body(accountValidResponse)(w, r)
				} else {
					body(`{"code":0,"req":{"code":10004,"data":{}}}`)(w, r)
				}
				return
			}
			body(`{"code":0,"req_1":{"code":0,"data":{"midurlinfo":[]}}}`)(w, r)
		})
		_, err := client.PlayURL(t.Context(), "id")
		var rejected *port.RejectedError
		if !errors.As(err, &rejected) {
			t.Fatalf("刷新后仍无播放地址的错误 = %v，期望音源拒绝", err)
		}
	})

	t.Run("刷新后登录仍无效时只重试一次并报告失效", func(t *testing.T) {
		store := &cookieStore{value: baseCookie}
		var refreshHits atomic.Int32
		client, _, _ := newRefreshTestClient(t, store, func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				refreshHits.Add(1)
				body(`{"code":0,"req":{"code":0,"data":{"musickey":"Q_H_L_still_bad","musicid":123456}}}`)(w, r)
				return
			}
			if strings.Contains(r.URL.Query().Get("data"), "DissInfo") {
				body(`{"code":0,"req":{"code":10004,"data":{}}}`)(w, r)
				return
			}
			body(`{"code":0,"req_1":{"code":0,"data":{"midurlinfo":[]}}}`)(w, r)
		})
		if _, err := client.PlayURL(t.Context(), "id"); !errors.Is(err, port.ErrSourceSignedOut) {
			t.Fatalf("错误 = %v，期望 ErrSourceSignedOut", err)
		}
		if refreshHits.Load() != 1 {
			t.Errorf("刷新次数 = %d，期望只刷新 1 次", refreshHits.Load())
		}
	})
}

func TestCredentialStatusAutoRefreshesExpiredCookie(t *testing.T) {
	store := &cookieStore{
		value: "musicid=123456; musickey=Q_H_L_old; qm_keyst=Q_H_L_old; refresh_token=rt-1; uin=123456",
	}
	client, obs, _ := newRefreshTestClient(t, store, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			body(`{"code":0,"req":{"code":0,"data":{"musickey":"Q_H_L_new","musicid":123456,"refresh_token":"rt-2"}}}`)(w, r)
			return
		}
		data := r.URL.Query().Get("data")
		if strings.Contains(data, "DissInfo") {
			if strings.Contains(r.Header.Get("Cookie"), "Q_H_L_new") {
				body(accountValidResponse)(w, r)
			} else {
				body(`{"code":0,"req":{"code":10004,"data":{}}}`)(w, r)
			}
			return
		}
		fixture(t, "account-vip.json")(w, r)
	})

	status, err := client.CredentialStatus(t.Context())
	if err != nil {
		t.Fatalf("CredentialStatus = %v", err)
	}
	if status.SignedOut || status.Account == nil || status.Account.ID != "123456" || !status.Account.Verified || status.Account.VIP == nil || !*status.Account.VIP {
		t.Fatalf("刷新后的凭据状态 = %+v，期望已验证的 VIP 账号", status)
	}
	obs.check(t, "qq account", "qq login_refresh", "qq account", "qq account")
}

func TestConcurrentRefreshCoalescesAndSurvivesCallerCancel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		initial := "musicid=123456; musickey=Q_H_L_old; qm_keyst=Q_H_L_old; refresh_token=rt-1; uin=123456"
		store := &cookieStore{value: initial}
		entered := make(chan struct{})
		release := make(chan struct{})
		var refreshHits atomic.Int32

		client, _ := newInMemoryRefreshClient(t, store, func(r *http.Request) (*http.Response, error) {
			if r.Method == http.MethodPost {
				if refreshHits.Add(1) == 1 {
					close(entered)
					<-release
				}
				return jsonResponse(r, `{"code":0,"req":{"code":0,"data":{"musickey":"Q_H_L_new","musicid":123456}}}`), nil
			}
			if strings.Contains(r.URL.Query().Get("data"), "DissInfo") {
				if strings.Contains(r.Header.Get("Cookie"), "Q_H_L_new") {
					return jsonResponse(r, accountValidResponse), nil
				}
				return jsonResponse(r, `{"code":0,"req":{"code":10004,"data":{}}}`), nil
			}
			if strings.Contains(r.Header.Get("Cookie"), "Q_H_L_new") {
				return jsonResponse(r, `{"code":0,"req_1":{"code":0,"data":{"midurlinfo":[{"filename":"F000idid.flac","purl":"shared.flac"}]}}}`), nil
			}
			return jsonResponse(r, `{"code":0,"req_1":{"code":0,"data":{"midurlinfo":[]}}}`), nil
		})

		ctx1, cancel1 := context.WithCancel(t.Context())
		defer cancel1()
		err1 := make(chan error, 1)
		go func() {
			_, err := client.PlayURL(ctx1, "id")
			err1 <- err
		}()
		<-entered

		res2 := make(chan string, 1)
		err2 := make(chan error, 1)
		go func() {
			u, err := client.PlayURL(t.Context(), "id")
			res2 <- u
			err2 <- err
		}()
		// 等第二个调用也进入 singleflight 阻塞等待后，再取消第一个调用。
		synctest.Wait()
		cancel1()
		if err := <-err1; !errors.Is(err, context.Canceled) {
			t.Fatalf("被取消的调用错误 = %v，期望 context.Canceled", err)
		}
		close(release)
		if err := <-err2; err != nil {
			t.Fatalf("同行的第二个调用失败：%v", err)
		}
		if got := <-res2; got != "https://ws.stream.qqmusic.qq.com/shared.flac" {
			t.Fatalf("第二个调用的播放地址 = %q", got)
		}
		if refreshHits.Load() != 1 {
			t.Errorf("并发刷新次数 = %d，期望合并为 1 次", refreshHits.Load())
		}
	})
}

func TestRefreshDoesNotOverwriteNewerQRLogin(t *testing.T) {
	initial := "musicid=123456; musickey=Q_H_L_old; qm_keyst=Q_H_L_old; refresh_token=rt-1; uin=123456"
	adminCookie := "musicid=123456; musickey=Q_H_L_admin; qm_keyst=Q_H_L_admin; refresh_token=rt-admin; uin=123456"

	for _, tc := range []struct {
		name  string
		write func(t *testing.T, client *Client, store *cookieStore)
	}{
		{
			name: "同实例扫码写入",
			write: func(t *testing.T, client *Client, _ *cookieStore) {
				if err := client.cookie.Put(t.Context(), adminCookie); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "其他实例扫码写入",
			write: func(_ *testing.T, _ *Client, store *cookieStore) {
				store.set(adminCookie)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &cookieStore{value: initial}
			entered := make(chan struct{})
			release := make(chan struct{})

			client, _, _ := newRefreshTestClient(t, store, func(w http.ResponseWriter, r *http.Request) {
				close(entered)
				<-release
				body(`{"code":0,"req":{"code":0,"data":{"musickey":"Q_H_L_refreshed","musicid":123456}}}`)(w, r)
			})

			done := make(chan error, 1)
			go func() {
				done <- client.RefreshLogin(t.Context())
			}()
			<-entered
			tc.write(t, client, store)
			close(release)
			if err := <-done; err != nil {
				t.Fatalf("RefreshLogin = %v", err)
			}
			if got := store.get(); got != adminCookie {
				t.Errorf("存储的 Cookie 被旧刷新覆盖成 %q，期望保留 %q", got, adminCookie)
			}
			if cached, err := client.cookie.Get(t.Context()); err != nil || cached != adminCookie {
				t.Errorf("缓存的 Cookie = %q, %v，期望 %q", cached, err, adminCookie)
			}
		})
	}
}

func TestRefreshCookieRecoversSingleflightPanic(t *testing.T) {
	store := &cookieStore{
		value: "musicid=123456; musickey=Q_H_L_old; qm_keyst=Q_H_L_old; refresh_token=rt-1; uin=123456",
	}
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	api := &http.Client{
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			panic("boom")
		}),
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	client := &Client{
		http:    api,
		api:     api,
		cookie:  cookiecache.NewWithSwap(store.load, store.save, store.swap),
		observe: func(string, string, time.Duration, error) {},
		logger:  logger,
	}

	err := client.RefreshLogin(t.Context())
	if err == nil {
		t.Fatal("panic 时返回了 nil 错误")
	}
	if strings.Contains(err.Error(), "\n") || strings.Contains(err.Error(), "boom") {
		t.Errorf("panic 返回的错误包含堆栈或原始信息：%q", err.Error())
	}
	rawLog := strings.TrimSpace(logs.String())
	if strings.Contains(rawLog, "\n") {
		t.Fatalf("panic 输出了多行日志：%q", logs.String())
	}
	var entry map[string]any
	if err := json.Unmarshal([]byte(rawLog), &entry); err != nil {
		t.Fatalf("解析日志 JSON 失败：%v\n原始日志：%s", err, rawLog)
	}
	if entry["level"] != "ERROR" || entry["panic"] != "boom" {
		t.Errorf("日志字段 = %#v", entry)
	}
	if stack, ok := entry["stack"].(string); !ok || stack == "" {
		t.Errorf("日志缺少单行 JSON 内的 stack 字段：%#v", entry["stack"])
	}
}
