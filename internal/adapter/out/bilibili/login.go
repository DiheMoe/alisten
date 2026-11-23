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
		return port.QRLoginSession{}, errors.New("哔哩哔哩没有返回二维码 key")
	}
	// QRImage 留空，由入口层按 LoginURL 生成二维码图片。
	return port.QRLoginSession{
		Key:       data.QRCodeKey,
		LoginURL:  data.URL,
		ExpiresAt: time.Now().Add(qrLoginTTL),
	}, nil
}

func (c *Client) CheckQRLogin(ctx context.Context, key string) (result port.QRLoginResult, err error) {
	if strings.TrimSpace(key) == "" {
		return port.QRLoginResult{}, errors.New("哔哩哔哩扫码登录的 key 为空")
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
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return port.QRLoginResult{}, fmt.Errorf("解析 哔哩哔哩扫码结果：%w", err)
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
	// Set-Cookie 只写入服务端存储，不放进返回给前端的 QRLoginResult。
	cookie := joinCookies(cookies)
	if cookie == "" {
		return port.QRLoginResult{}, errors.New("哔哩哔哩扫码成功但响应没有返回 Cookie")
	}
	if err := c.cookie.Put(ctx, cookie); err != nil {
		return port.QRLoginResult{}, fmt.Errorf("保存 哔哩哔哩凭据：%w", err)
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
		if strings.TrimSpace(cookie.Name) != "" && cookie.Value != "" {
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

// fetchNav 不检查顶层 code，因为未登录时平台返回 -101，由调用方按 isLogin 判断登录是否失效。
func (c *Client) fetchNav(ctx context.Context, op, cookie string) (d navData, err error) {
	start := time.Now()
	defer func() { c.report(op, start, err) }()
	body, _, err := c.get(ctx, navURL, cookie)
	if err != nil {
		return navData{}, err
	}
	var payload struct {
		Code    int     `json:"code"`
		Message string  `json:"message"`
		Data    navData `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return navData{}, fmt.Errorf("解析 哔哩哔哩账号信息：%w", err)
	}
	if payload.Code != 0 && payload.Code != -101 {
		return navData{}, classifyCode(payload.Code, payload.Message)
	}
	return payload.Data, nil
}

func (n navData) vip() bool {
	return n.IsLogin && n.VipStatus == 1 && n.VipType > 0
}

func (c *Client) CredentialStatus(ctx context.Context) (port.CredentialStatus, error) {
	cookie, err := c.cookie.Reload(ctx)
	if err != nil {
		return port.CredentialStatus{}, fmt.Errorf("读取 哔哩哔哩凭据：%w", err)
	}
	if strings.TrimSpace(cookie) == "" {
		return port.CredentialStatus{}, nil
	}
	nav, err := c.fetchNav(ctx, "credential_status", cookie)
	if err != nil {
		return port.CredentialStatus{}, err
	}
	if !nav.IsLogin {
		return port.CredentialStatus{Set: true, SignedOut: true}, nil
	}
	vip := nav.vip()
	account := &port.SourceAccount{
		ID:       strconv.FormatInt(nav.Mid, 10),
		VIP:      &vip,
		Verified: true,
	}
	if name := cleanText(nav.Uname); name != "" {
		account.Name = &name
	}
	return port.CredentialStatus{Set: true, Account: account}, nil
}
