package netease

import (
	"context"
	"strconv"
	"strings"

	"github.com/dihemoe/alisten/internal/app/port"
)

const accountAPI = "https://music.163.com/weapi/nuser/account/get"

type accountInfo struct {
	UserID   int64  `json:"userId"`
	Nickname string `json:"nickname"`
	VipType  int    `json:"vipType"`
}

func (c *Client) accountInfo(ctx context.Context, cookie string) (accountInfo, error) {
	form, err := weapi(map[string]any{"csrf_token": ""})
	if err != nil {
		return accountInfo{}, err
	}
	var resp struct {
		base
		Profile accountInfo `json:"profile"`
	}
	if err := c.callWithCookie(ctx, "account", accountAPI, form, cookie, &resp); err != nil {
		return accountInfo{}, err
	}
	return resp.Profile, nil
}

func (c *Client) CredentialStatus(ctx context.Context) (port.CredentialStatus, error) {
	cookie, err := c.cookie.Reload(ctx)
	if err != nil {
		return port.CredentialStatus{}, err
	}
	if strings.TrimSpace(cookie) == "" {
		return port.CredentialStatus{}, nil
	}
	info, err := c.accountInfo(ctx, cookie)
	if err != nil {
		return port.CredentialStatus{}, err
	}
	// 平台对失效凭据返回空的个人资料，这里与账号接口的其他失败区分开。
	if info.UserID == 0 {
		return port.CredentialStatus{Set: true, SignedOut: true}, nil
	}
	vip := info.VipType != 0
	account := &port.SourceAccount{
		ID:       strconv.FormatInt(info.UserID, 10),
		VIP:      &vip,
		Verified: true,
	}
	if name := text(info.Nickname); name != "" {
		account.Name = &name
	}
	return port.CredentialStatus{Set: true, Account: account}, nil
}
