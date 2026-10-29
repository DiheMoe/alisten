package bilibili

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
)

func (c *Client) CreateQRLogin(ctx context.Context) (session port.QRLoginSession, err error) {
	start := time.Now()
	defer func() { c.report("login_create", start, err) }()
	var data struct {
		URL       string `json:"url"`
		QRCodeKey string `json:"qrcode_key"`
	}
	if err := c.getData(ctx, qrGenerateURL, "", &data); err != nil {
		return port.QRLoginSession{}, err
	}
	if strings.TrimSpace(data.QRCodeKey) == "" {
		return port.QRLoginSession{}, errors.New("平台没有返回二维码 key")
	}
	// QRImage 留空：调用方按 LoginURL 生成二维码图片。
	return port.QRLoginSession{
		Key:       data.QRCodeKey,
		LoginURL:  data.URL,
		ExpiresAt: time.Now().Add(qrLoginTTL),
	}, nil
}

func (c *Client) CheckQRLogin(ctx context.Context, key string) (result port.QRLoginResult, err error) {
	if strings.TrimSpace(key) == "" {
		return port.QRLoginResult{}, errors.New("二维码 key 为空")
	}
	start := time.Now()
	defer func() { c.report("login_check", start, err) }()
	body, cookies, err := c.get(ctx, qrPollURL+"?qrcode_key="+url.QueryEscape(key), "")
	if err != nil {
		return port.QRLoginResult{}, err
	}
	var payload struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			Code         int    `json:"code"`
			Message      string `json:"message"`
			RefreshToken string `json:"refresh_token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return port.QRLoginResult{}, fmt.Errorf("解析扫码结果：%w", err)
	}
	if payload.Code != 0 {
		return port.QRLoginResult{}, classifyCode(payload.Code, payload.Message)
	}
	result = port.QRLoginResult{
		Status:  qrStatus(payload.Data.Code),
		Message: cmp.Or(strings.TrimSpace(payload.Data.Message), strings.TrimSpace(payload.Message)),
	}
	if payload.Data.Code != 0 {
		return result, nil
	}
	// Set-Cookie 和 refresh_token 只写入服务端凭据，不放入 QRLoginResult。
	cookie := joinCookies(cookies)
	if cookie == "" {
		return port.QRLoginResult{}, errors.New("扫码成功但平台没有返回 Cookie")
	}
	if err := c.saveCredential(ctx, encodeCredential(cookie, payload.Data.RefreshToken)); err != nil {
		return port.QRLoginResult{}, fmt.Errorf("保存 Bilibili 凭据：%w", err)
	}
	return result, nil
}

func qrStatus(code int) port.QRLoginStatus {
	switch code {
	case 0:
		return port.QRLoginSuccess
	case 86038:
		return port.QRLoginExpired
	case 86090:
		return port.QRLoginScanned
	case 86101:
		return port.QRLoginWaiting
	default:
		return port.QRLoginFailed
	}
}

func joinCookies(cookies []*http.Cookie) string {
	parts := make([]string, 0, len(cookies))
	for _, cookie := range cookies {
		if strings.TrimSpace(cookie.Name) != "" {
			parts = append(parts, cookie.Name+"="+cookie.Value)
		}
	}
	sort.Strings(parts)
	return strings.Join(parts, "; ")
}

type navData struct {
	IsLogin   bool   `json:"isLogin"`
	Uname     string `json:"uname"`
	Mid       int64  `json:"mid"`
	VipStatus int    `json:"vipStatus"`
	VipType   int    `json:"vipType"`
}

// fetchNav 不检查信封的 code：未登录时平台返回 -101，由调用方按 isLogin 区分。
func (c *Client) fetchNav(ctx context.Context, op, cookie string) (d navData, err error) {
	start := time.Now()
	defer func() { c.report(op, start, err) }()
	body, _, err := c.get(ctx, navURL, cookie)
	if err != nil {
		return navData{}, err
	}
	var payload struct {
		Data navData `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return navData{}, fmt.Errorf("解析账号信息：%w", err)
	}
	return payload.Data, nil
}

func (n navData) vip() bool {
	return n.IsLogin && n.VipStatus == 1 && n.VipType > 0
}

func (c *Client) CredentialStatus(ctx context.Context) (port.CredentialStatus, error) {
	raw, err := c.loadCredential(ctx)
	if err != nil {
		return port.CredentialStatus{}, fmt.Errorf("读取 Bilibili 凭据：%w", err)
	}
	if raw == "" {
		return port.CredentialStatus{}, nil
	}
	cred, err := decodeCredential(raw)
	if err != nil {
		return port.CredentialStatus{}, err
	}
	nav, err := c.fetchNav(ctx, "credential_status", cred.Cookie)
	if err != nil {
		return port.CredentialStatus{}, err
	}
	if !nav.IsLogin {
		return port.CredentialStatus{Set: true}, nil
	}
	name, vip := nav.Uname, nav.vip()
	return port.CredentialStatus{
		Set: true,
		Account: &port.SourceAccount{
			ID:       strconv.FormatInt(nav.Mid, 10),
			Name:     &name,
			VIP:      &vip,
			Verified: true,
		},
	}, nil
}
