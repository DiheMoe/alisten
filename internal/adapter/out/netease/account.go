package netease

import (
	"context"
	"fmt"
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

func (c *Client) accountInfo(ctx context.Context) (accountInfo, error) {
	form, err := weapi(map[string]any{"csrf_token": ""})
	if err != nil {
		return accountInfo{}, err
	}
	var resp struct {
		base
		Profile accountInfo `json:"profile"`
	}
	if err := c.call(ctx, "account", accountAPI, form, &resp); err != nil {
		return accountInfo{}, err
	}
	return resp.Profile, nil
}

func (c *Client) CurrentAccount(ctx context.Context) (*port.SourceAccount, error) {
	cookie, err := c.cookie.get(ctx)
	if err != nil {
		return nil, err
	}
	// 没有保存过凭据是"从未登录"，不是"登录失效"。
	if strings.TrimSpace(cookie) == "" {
		return nil, nil
	}
	info, err := c.accountInfo(ctx)
	if err != nil {
		return nil, err
	}
	// 平台对失效凭据返回空的个人资料，与账号接口的其他失败分开。
	if info.UserID == 0 {
		return nil, fmt.Errorf("查询网易云登录状态：%w", port.ErrSourceSignedOut)
	}
	account := &port.SourceAccount{
		ID:       strconv.FormatInt(info.UserID, 10),
		VIP:      new(bool),
		Verified: true,
	}
	*account.VIP = info.VipType != 0
	if info.Nickname != "" {
		account.Name = &info.Nickname
	}
	return account, nil
}
