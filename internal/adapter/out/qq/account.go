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

var qqAccountIDNames = []string{
	"wxuin", "uin", "ptui_loginuin", "luin", "pt2gguin", "superuin", "p_uin", "musicid", "userid", "user_id",
}

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

func (c *Client) accountRequest(ctx context.Context, cookie string, creds map[string]string, id, module, method string, param map[string]any, out response) error {
	payload, err := json.Marshal(map[string]any{
		"comm": map[string]any{
			"ct": 24, "cv": 4747474, "platform": "yqq.json", "uin": id,
			"g_tk": 5381, "g_tk_new_20200303": 5381,
			"authst": creds["qm_keyst"],
			"format": "json", "inCharset": "utf8", "outCharset": "utf-8", "notice": 0, "needNewCode": 1,
		},
		"req": map[string]any{"module": module, "method": method, "param": param},
	})
	if err != nil {
		return err
	}
	query := url.Values{"data": {string(payload)}}
	return c.getWithCookie(ctx, "account", musicuAPI, query, desktopHeader, cookie, out)
}

// loginState 用「我喜欢」歌单判断登录是否仍然有效，因为它是必须登录才能读的接口，登录失效时固定返回业务码 10004。
// 不能用会员信息接口判断，因为失效的会话在会员接口上也会返回非会员的默认数据，会让后台把掉登录误判成非会员。
func (c *Client) loginState(ctx context.Context, cookie string, creds map[string]string, id string) (bool, error) {
	param := map[string]any{"disstid": 0, "dirid": 201, "tag": true, "song_begin": 0, "song_num": 1, "userinfo": true, "orderlist": true}
	var resp accountResponse
	if err := c.accountRequest(ctx, cookie, creds, id, "music.srfDissInfo.DissInfo", "CgiGetDiss", param, &resp); err != nil {
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

func (c *Client) CredentialStatus(ctx context.Context) (port.CredentialStatus, error) {
	cookie, err := c.cookie.Reload(ctx)
	if err != nil {
		return port.CredentialStatus{}, err
	}
	return c.credentialStatus(ctx, cookie, true)
}

func (c *Client) credentialStatus(ctx context.Context, cookie string, allowRefresh bool) (port.CredentialStatus, error) {
	if strings.TrimSpace(cookie) == "" {
		return port.CredentialStatus{}, nil
	}
	creds := parseQQCookies(cookie)
	id := qqAccountID(creds)
	if id == "" {
		return port.CredentialStatus{Set: true}, nil
	}
	valid, err := c.loginState(ctx, cookie, creds, id)
	if err != nil {
		return port.CredentialStatus{}, err
	}
	if !valid {
		if allowRefresh {
			refreshed, err := c.refreshExpiredCookie(ctx, cookie)
			if err != nil {
				return port.CredentialStatus{}, err
			}
			if refreshed {
				latest, err := c.cookie.Get(ctx)
				if err != nil {
					return port.CredentialStatus{}, err
				}
				return c.credentialStatus(ctx, latest, false)
			}
		}
		return port.CredentialStatus{Set: true, SignedOut: true}, nil
	}
	account := &port.SourceAccount{ID: id, Verified: true}
	// 确认登录有效之后才查会员，查不到就保持 nil 显示未知。QQ 音乐网页接口对自有账号隐藏昵称，拿不到就不显示。
	var vipResp accountResponse
	if err := c.accountRequest(ctx, cookie, creds, id, "VipLogin.VipLoginInter", "vip_login_base", map[string]any{}, &vipResp); err == nil {
		if *vipResp.Code == 0 && *vipResp.Req.Code == 0 && vipResp.Req.Data.Identity != nil {
			vip := vipResp.Req.Data.Identity.VIP != 0 || vipResp.Req.Data.SVIP != 0
			account.VIP = &vip
		}
	}
	return port.CredentialStatus{Set: true, Account: account}, nil
}

func qqAccountID(creds map[string]string) string {
	for _, name := range qqAccountIDNames {
		if value := creds[name]; value != "" {
			return trimQQUIN(value)
		}
	}
	return ""
}

func trimQQUIN(value string) string {
	return strings.TrimSpace(strings.TrimLeft(strings.TrimPrefix(value, "o"), "0"))
}
