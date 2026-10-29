package netease

import (
	"context"
	"strconv"

	"github.com/dihemoe/alisten/internal/app/port"
)

const accountAPI = "https://music.163.com/weapi/nuser/account/get"

func (c *Client) CurrentAccount(ctx context.Context) (*port.SourceAccount, error) {
	form, err := weapi(map[string]any{"csrf_token": ""})
	if err != nil {
		return nil, err
	}
	var resp struct {
		base
		Profile struct {
			UserID   int64  `json:"userId"`
			Nickname string `json:"nickname"`
			VipType  int    `json:"vipType"`
		} `json:"profile"`
	}
	if err := c.call(ctx, "account", accountAPI, form, &resp); err != nil {
		return nil, err
	}
	if resp.Profile.UserID == 0 {
		return nil, nil
	}
	account := &port.SourceAccount{
		ID:       strconv.FormatInt(resp.Profile.UserID, 10),
		VIP:      new(bool),
		Verified: true,
	}
	*account.VIP = resp.Profile.VipType != 0
	if resp.Profile.Nickname != "" {
		account.Name = &resp.Profile.Nickname
	}
	return account, nil
}
