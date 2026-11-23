package qq

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/dihemoe/alisten/internal/app/port"
)

const (
	// QQ 音乐返回的 keyExpiresIn 通常是 72 小时。按有效期的一半与 24 小时中的较小值提前刷新，
	// 既不会过于频繁地请求登录接口，也给临时故障留出充足的重试窗口。
	qqProactiveRefreshMaxAge = 24 * time.Hour
	// 刷新凭据已被平台明确拒绝时，短时间内不再对同一份旧凭据重复发起刷新请求。
	// 管理员重新扫码保存新凭据后，新旧凭据不同，冷却记录自然不再命中。
	qqRefreshRejectCooldown = 10 * time.Minute
)

type refreshState struct {
	group          singleflight.Group
	mu             sync.Mutex
	rejectedCookie string
	rejectedUntil  time.Time
}

func (s *refreshState) isRejected(cookie string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rejectedCookie == cookie && now.Before(s.rejectedUntil)
}

func (s *refreshState) markRejected(cookie string, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rejectedCookie = cookie
	s.rejectedUntil = now.Add(qqRefreshRejectCooldown)
}

// 平台明确拒绝刷新时返回包装了 port.ErrSourceSignedOut 的错误。
func (c *Client) RefreshLogin(ctx context.Context) error {
	if c.cookie.store == nil {
		return nil
	}
	cookie, err := c.cookie.reload(ctx)
	if err != nil {
		return err
	}
	if strings.TrimSpace(cookie) == "" {
		return nil
	}
	creds := parseQQCookies(cookie)
	if !canRefreshQQCredentials(creds, cookie) || !needsProactiveRefresh(creds, time.Now()) {
		return nil
	}
	_, err = c.refreshCookie(ctx, cookie, false)
	return err
}

// 无法刷新或平台明确拒绝刷新时返回 false 和 nil，交由调用方按登录失效处理。
func (c *Client) refreshExpiredCookie(ctx context.Context, staleCookie string) (bool, error) {
	if c.cookie.store == nil || c.refresh.isRejected(staleCookie, time.Now()) {
		return false, nil
	}
	refreshed, err := c.refreshCookie(ctx, staleCookie, true)
	if errors.Is(err, port.ErrSourceSignedOut) {
		return false, nil
	}
	return refreshed, err
}

func (c *Client) refreshCookie(ctx context.Context, staleCookie string, reloadFirst bool) (bool, error) {
	// 共享同一个刷新请求的调用方可能各自提前取消，所以脱离单个调用方的取消信号，
	// 让各子步骤用自己的超时完成刷新并落库，同时每个等待方仍然尊重自己的 ctx。
	baseCtx := context.WithoutCancel(ctx)
	ch := c.refresh.group.DoChan(staleCookie, func() (v any, err error) {
		// 刷新在独立的 goroutine 中执行，panic 回不到处理请求的 goroutine。net/http 无法恢复这样的 panic，进程会崩溃。
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("刷新 QQ 音乐登录时 panic：%v\n%s", r, debug.Stack())
			}
		}()
		if cur, ok := c.cookie.current(); ok && cur != staleCookie {
			return cur != "", nil
		}
		latest := staleCookie
		if reloadFirst {
			latest, err = c.cookie.reload(baseCtx)
			if err != nil {
				return false, err
			}
			if strings.TrimSpace(latest) == "" {
				return false, nil
			}
			if latest != staleCookie {
				return true, nil
			}
		}
		creds := parseQQCookies(latest)
		if !canRefreshQQCredentials(creds, latest) {
			return false, nil
		}
		start := time.Now()
		newCookie, err := c.doRefreshLogin(baseCtx, creds, latest)
		c.observe("qq", "login_refresh", time.Since(start), err)
		if err != nil {
			var rejected *qqLoginRejectedError
			if errors.As(err, &rejected) {
				c.refresh.markRejected(staleCookie, time.Now())
				c.logger.WarnContext(baseCtx, "自动刷新 QQ 音乐登录凭据失败，需要管理员重新扫码登录", "source", "qq", "error", err)
				return false, fmt.Errorf("自动刷新 QQ 音乐登录：%w", port.ErrSourceSignedOut)
			}
			c.logger.WarnContext(baseCtx, "自动刷新 QQ 音乐登录凭据失败", "source", "qq", "error", err)
			return false, err
		}
		saved, err := c.cookie.putIfUnchanged(baseCtx, staleCookie, newCookie)
		if err != nil {
			return false, fmt.Errorf("保存刷新后的 QQ Cookie：%w", err)
		}
		if saved {
			c.logger.InfoContext(baseCtx, "已自动刷新 QQ 音乐登录凭据", "source", "qq")
		}
		return true, nil
	})
	select {
	case res := <-ch:
		if res.Err != nil {
			return false, res.Err
		}
		return res.Val.(bool), nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

func needsProactiveRefresh(creds map[string]string, now time.Time) bool {
	createTime, err1 := strconv.ParseInt(creds["musickeyCreateTime"], 10, 64)
	expiresIn, err2 := strconv.ParseInt(creds["keyExpiresIn"], 10, 64)
	if err1 == nil && err2 == nil && createTime > 0 && expiresIn > 0 {
		createdAt := time.Unix(createTime, 0)
		lifetime := time.Duration(expiresIn) * time.Second
		return now.Sub(createdAt) >= min(lifetime/2, qqProactiveRefreshMaxAge)
	}
	return true
}

func (c *Client) doRefreshLogin(ctx context.Context, creds map[string]string, cookie string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	requestBody, err := buildQQRefreshRequest(creds, cookie)
	if err != nil {
		return "", err
	}
	header := http.Header{
		"Content-Type": {"application/json"},
		"Referer":      {"https://y.qq.com/"},
		"Origin":       {"https://y.qq.com"},
	}
	resp, err := qqLoginRequest(ctx, c.api, http.MethodPost, qqLoginMusicURL, bytes.NewReader(requestBody), header)
	if err != nil {
		return "", err
	}
	musicBody, err := readQQLoginBody(resp)
	if err != nil {
		return "", err
	}
	refreshed, err := mergeQQRefreshCredentials(creds, resp, musicBody)
	if err != nil {
		return "", err
	}
	if refreshed == "" {
		return "", errors.New("QQ 音乐刷新登录未返回音乐 key")
	}
	return refreshed, nil
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

func isWeChatQQCredentials(creds map[string]string) bool {
	return creds["loginType"] == "1" ||
		strings.HasPrefix(qqCredentialMusicKey(creds), "W_X") ||
		creds["wxuin"] != "" ||
		creds["wxaccess_token"] != ""
}

func canRefreshQQCredentials(creds map[string]string, cookie string) bool {
	// 微信登录的刷新协议尚未实测确认，遇到微信凭据直接跳过刷新。
	if isWeChatQQCredentials(creds) {
		return false
	}
	if creds["refresh_key"] == "" && creds["refresh_token"] == "" {
		return false
	}
	return creds["musicid"] != "" || qqAccountID(cookie) != ""
}

func qqCredentialMusicKey(creds map[string]string) string {
	for _, name := range qqMusicKeyNames {
		if creds[name] != "" {
			return creds[name]
		}
	}
	return ""
}

func qqRefreshMusicID(creds map[string]string, cookie string) any {
	raw := creds["musicid"]
	if raw == "" {
		raw = qqAccountID(cookie)
	}
	if raw == "" {
		return 0
	}
	if trimmed := trimQQUIN(raw); trimmed != "" {
		if n, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
			return n
		}
	}
	return raw
}

func buildQQRefreshRequest(creds map[string]string, cookie string) ([]byte, error) {
	expiredIn := 0
	if raw := creds["expired_in"]; raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			expiredIn = n
		}
	}
	return json.Marshal(map[string]any{
		"comm": map[string]any{
			"ct":            "24",
			"cv":            "4747474",
			"platform":      "yqq.json",
			"chid":          "0",
			"format":        "json",
			"inCharset":     "utf-8",
			"outCharset":    "utf-8",
			"notice":        "0",
			"need_new_code": "1",
			"tmeLoginType":  "2",
		},
		"req": map[string]any{
			"module": "music.login.LoginServer",
			"method": "Login",
			"param": map[string]any{
				"openid":        creds["openid"],
				"access_token":  creds["access_token"],
				"refresh_token": creds["refresh_token"],
				"expired_in":    expiredIn,
				"musicid":       qqRefreshMusicID(creds, cookie),
				"musickey":      qqCredentialMusicKey(creds),
				"refresh_key":   creds["refresh_key"],
				"loginMode":     2,
			},
		},
	})
}

func mergeQQRefreshCredentials(oldCreds map[string]string, resp *http.Response, musicBody []byte) (string, error) {
	credentials := maps.Clone(oldCreds)
	// 先清掉旧的音乐 key 和时效字段，否则刷新响应只返回其中一个字段名时会把旧值留下来。
	for _, name := range qqMusicKeyNames {
		delete(credentials, name)
	}
	for _, name := range []string{"expired_in", "expired_at", "keyExpiresIn", "musickeyCreateTime"} {
		delete(credentials, name)
	}
	if err := mergeQQMusicLoginResponse(credentials, resp, musicBody); err != nil {
		return "", err
	}
	return serializeQQLoginCredentials(credentials), nil
}
