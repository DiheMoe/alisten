package netease

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
)

const (
	qrLoginCreateURL = "https://interface.music.163.com/api/login/qrcode/unikey"
	qrLoginCheckURL  = "https://interface.music.163.com/api/login/qrcode/client/login"
	qrLoginUserAgent = "Mozilla/5.0 (Windows NT 10.0; WOW64) AppleWebKit/537.36 (KHTML, like Gecko) Safari/537.36 Chrome/91.0.4472.164 NeteaseMusicDesktop/3.0.18.203152"
)

func (c *Client) CreateQRLogin(ctx context.Context) (port.QRLoginSession, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	var payload struct {
		base
		Unikey string `json:"unikey"`
	}
	start := time.Now()
	_, err := c.post(ctx, qrLoginCreateURL, url.Values{"type": {"3"}}, qrLoginUserAgent, "", &payload)
	if err == nil && payload.Code != http.StatusOK {
		err = fmt.Errorf("网易云的接口 login_create 返回错误码 %d", payload.Code)
	}
	if err == nil && payload.Unikey == "" {
		err = errors.New("网易云创建扫码登录的响应缺少 unikey")
	}
	c.observe("netease", "login_create", time.Since(start), err)
	if err != nil {
		return port.QRLoginSession{}, err
	}
	return port.QRLoginSession{
		Key:       payload.Unikey,
		LoginURL:  "https://music.163.com/login?codekey=" + url.QueryEscape(payload.Unikey),
		ExpiresAt: time.Now().Add(5 * time.Minute),
	}, nil
}

func (c *Client) CheckQRLogin(ctx context.Context, key string) (result port.QRLoginResult, err error) {
	if key == "" {
		return port.QRLoginResult{}, errors.New("网易云扫码登录的 key 为空")
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	var payload struct {
		base
		Message string `json:"message"`
		Cookie  string `json:"cookie"`
	}
	start := time.Now()
	defer func() { c.observe("netease", "login_check", time.Since(start), err) }()
	resp, err := c.post(ctx, qrLoginCheckURL, url.Values{"key": {key}, "type": {"3"}}, qrLoginUserAgent, "", &payload)
	if err != nil {
		return port.QRLoginResult{}, err
	}
	result = port.QRLoginResult{Status: neteaseLoginStatus(payload.Code), Message: payload.Message}
	if result.Status != port.QRLoginSuccess {
		return result, nil
	}
	cookie := payload.Cookie
	if cookie == "" {
		cookies := resp.Cookies()
		sort.SliceStable(cookies, func(i, j int) bool { return cookies[i].Name < cookies[j].Name })
		pairs := make([]string, len(cookies))
		for i, item := range cookies {
			pairs[i] = item.Name + "=" + item.Value
		}
		cookie = strings.Join(pairs, "; ")
	}
	if cookie == "" {
		return port.QRLoginResult{}, errors.New("网易云扫码登录成功的响应缺少 Cookie")
	}
	if err = c.cookie.Put(ctx, cookie); err != nil {
		return port.QRLoginResult{}, fmt.Errorf("保存网易云 Cookie：%w", err)
	}
	return result, nil
}

func neteaseLoginStatus(code int) port.QRLoginStatus {
	switch code {
	case 800:
		return port.QRLoginExpired
	case 801:
		return port.QRLoginWaiting
	case 802:
		return port.QRLoginScanned
	case 803:
		return port.QRLoginSuccess
	default:
		return port.QRLoginFailed
	}
}
