package qq

import (
	"context"
	"encoding/json"
	"errors"
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

func (c *Client) CurrentAccount(ctx context.Context) (*port.SourceAccount, error) {
	cookie, err := c.cookie.get(ctx)
	if err != nil {
		return nil, err
	}
	id := qqAccountID(cookie)
	if id == "" {
		return nil, nil
	}
	payload, err := json.Marshal(map[string]any{
		"comm": map[string]any{
			"ct": 24, "cv": 4747474, "platform": "yqq.json", "uin": id,
			"g_tk": 5381, "g_tk_new_20200303": 5381,
			"authst": qqAccountCookieValue(cookie, "qm_keyst"),
			"format": "json", "inCharset": "utf8", "outCharset": "utf-8", "notice": 0, "needNewCode": 1,
		},
		"req": map[string]any{"module": "VipLogin.VipLoginInter", "method": "vip_login_base", "param": map[string]any{}},
	})
	if err != nil {
		return nil, err
	}
	var resp accountResponse
	query := url.Values{"data": {string(payload)}}
	if err := c.get(ctx, "qq", "account", musicuAPI, query, desktopHeader, &resp); err != nil {
		return nil, err
	}
	// 非零业务码是 QQ 音乐认为登录无效，与网易云的个人资料为空同样处理：没有账号，不算查询失败。
	if *resp.Code != 0 || *resp.Req.Code != 0 {
		return nil, nil
	}
	account := &port.SourceAccount{ID: id, Verified: true}
	// identity 缺失时会员状态拿不准，留 nil 让界面显示未知。
	if resp.Req.Data.Identity != nil {
		account.VIP = new(bool)
		*account.VIP = resp.Req.Data.Identity.VIP != 0 || resp.Req.Data.SVIP != 0
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
		return strings.TrimSpace(strings.TrimLeft(strings.TrimPrefix(value, "o"), "0"))
	}
	return ""
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
