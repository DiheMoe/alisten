package qq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/dihemoe/alisten/internal/app/port"
)

type accountResponse struct {
	Code *int `json:"code"`
	Req  struct {
		Code *int `json:"code"`
		Data struct {
			Identity *struct {
				VIP int `json:"vip"`
			} `json:"identity"`
			SVIP int `json:"svip"`
		} `json:"data"`
	} `json:"req"`
}

func (r accountResponse) check() error {
	if r.Code == nil || r.Req.Code == nil {
		return errors.New("QQ 音乐账号响应的 code 或 req.code 缺失或为 null")
	}
	return nil
}

func (c *Client) accountRequest(ctx context.Context, cookie, id string, module, method string, param map[string]any, out response) error {
	payload, err := json.Marshal(map[string]any{
		"comm": map[string]any{
			"ct": 24, "cv": 4747474, "platform": "yqq.json", "uin": id,
			"g_tk": 5381, "g_tk_new_20200303": 5381,
			"authst": qqAccountCookieValue(cookie, "qm_keyst"),
			"format": "json", "inCharset": "utf8", "outCharset": "utf-8", "notice": 0, "needNewCode": 1,
		},
		"req": map[string]any{"module": module, "method": method, "param": param},
	})
	if err != nil {
		return err
	}
	query := url.Values{"data": {string(payload)}}
	return c.getWithCookie(ctx, "qq", "account", musicuAPI, query, desktopHeader, cookie, out)
}

// loginState 用"我喜欢"歌单判断登录是否有效：它是必须登录才能读的接口，登录失效时返回业务码 10004。
// 会员信息接口不能用来判断：失效的会话也会返回非会员的默认数据，那就是后台把掉登录显示成非会员的原因。
func (c *Client) loginState(ctx context.Context, cookie, id string) (bool, error) {
	param := map[string]any{"disstid": 0, "dirid": 201, "tag": true, "song_begin": 0, "song_num": 1, "userinfo": true, "orderlist": true}
	var resp accountResponse
	if err := c.accountRequest(ctx, cookie, id, "music.srfDissInfo.DissInfo", "CgiGetDiss", param, &resp); err != nil {
		return false, err
	}
	switch {
	case *resp.Code == 0 && *resp.Req.Code == 0:
		return true, nil
	case *resp.Req.Code == 10004:
		return false, nil
	default:
		return false, fmt.Errorf("QQ 音乐登录状态查询返回错误码 %d", *resp.Req.Code)
	}
}

func (c *Client) CurrentAccount(ctx context.Context) (*port.SourceAccount, error) {
	return c.currentAccount(ctx, true)
}

func (c *Client) currentAccount(ctx context.Context, allowRefresh bool) (*port.SourceAccount, error) {
	cookie, err := c.cookie.get(ctx)
	if err != nil {
		return nil, err
	}
	id := qqAccountID(cookie)
	if id == "" {
		return nil, nil
	}
	valid, err := c.loginState(ctx, cookie, id)
	if err != nil {
		return nil, err
	}
	if !valid {
		if allowRefresh {
			refreshed, err := c.refreshExpiredCookie(ctx, cookie)
			if err != nil {
				return nil, err
			}
			if refreshed {
				return c.currentAccount(ctx, false)
			}
		}
		return nil, fmt.Errorf("查询 QQ 音乐登录状态：%w", port.ErrSourceSignedOut)
	}
	account := &port.SourceAccount{ID: id, Verified: true}
	// 只有登录确认有效之后才查会员，查不到就留 nil 显示未知。
	var vipResp accountResponse
	if err := c.accountRequest(ctx, cookie, id, "VipLogin.VipLoginInter", "vip_login_base", map[string]any{}, &vipResp); err == nil {
		if *vipResp.Code == 0 && *vipResp.Req.Code == 0 && vipResp.Req.Data.Identity != nil {
			account.VIP = new(bool)
			*account.VIP = vipResp.Req.Data.Identity.VIP != 0 || vipResp.Req.Data.SVIP != 0
		}
	}
	// QQ 音乐的网页接口对自有账号隐藏昵称，拿不到就不显示。
	return account, nil
}

func qqAccountID(cookie string) string {
	for _, name := range []string{"wxuin", "uin", "ptui_loginuin", "luin", "pt2gguin", "superuin", "p_uin", "musicid", "userid", "user_id"} {
		value := qqAccountCookieValue(cookie, name)
		if value == "" {
			continue
		}
		return trimQQUIN(value)
	}
	return ""
}

func trimQQUIN(value string) string {
	return strings.TrimSpace(strings.TrimLeft(strings.TrimPrefix(value, "o"), "0"))
}

func qqAccountCookieValue(cookie, name string) string {
	for part := range strings.SplitSeq(cookie, ";") {
		key, value, ok := strings.Cut(part, "=")
		if ok && strings.TrimSpace(key) == name {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
