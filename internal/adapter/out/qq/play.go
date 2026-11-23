package qq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"

	"github.com/dihemoe/alisten/internal/app/port"
)

const (
	musicuAPI  = "https://u.y.qq.com/cgi-bin/musicu.fcg"
	playURLCDN = "https://ws.stream.qqmusic.qq.com/"
)

type playURLResponse struct {
	Code *int `json:"code"`
	Req1 struct {
		Code *int `json:"code"`
		Data struct {
			Items []struct {
				Filename string `json:"filename"`
				PURL     string `json:"purl"`
			} `json:"midurlinfo"`
		} `json:"data"`
	} `json:"req_1"`
}

func (r playURLResponse) check() error {
	if r.Code == nil || r.Req1.Code == nil {
		return errors.New("QQ 音乐播放地址响应的 code 或 req_1.code 缺失或为 null")
	}
	if *r.Code != 0 {
		return fmt.Errorf("QQ 音乐返回错误码 %d", *r.Code)
	}
	if *r.Req1.Code != 0 {
		return fmt.Errorf("QQ 音乐播放地址请求返回错误码 %d", *r.Req1.Code)
	}
	return nil
}

var _ port.PlayURLs = (*Client)(nil)

func (c *Client) PlayURL(ctx context.Context, songMID string) (string, error) {
	return c.playURL(ctx, songMID, true)
}

func (c *Client) playURL(ctx context.Context, songMID string, allowRefresh bool) (string, error) {
	cookie, err := c.cookie.Get(ctx)
	if err != nil {
		return "", err
	}
	filenames := []string{
		"F000" + songMID + songMID + ".flac",
		"M800" + songMID + songMID + ".mp3",
		"M500" + songMID + songMID + ".mp3",
	}
	// vkey 接口按下标配对 filename 和 songmid，不配对的文件一律返回不存在。
	songmids := []string{songMID, songMID, songMID}
	songtypes := []int{0, 0, 0}
	payload, err := json.Marshal(map[string]any{
		"req_1": map[string]any{
			"module": "music.vkey.GetVkey",
			"method": "UrlGetVkey",
			"param": map[string]any{
				"filename":  filenames,
				"guid":      "0",
				"songmid":   songmids,
				"songtype":  songtypes,
				"uin":       "0",
				"loginflag": 1,
				"platform":  "20",
			},
		},
	})
	if err != nil {
		return "", err
	}
	var resp playURLResponse
	query := url.Values{"data": {string(payload)}}
	if err := c.getWithCookie(ctx, "play_url", musicuAPI, query, desktopHeader, cookie, &resp); err != nil {
		return "", err
	}
	for _, filename := range filenames {
		for _, item := range resp.Req1.Data.Items {
			if item.Filename == filename && item.PURL != "" {
				return playURLCDN + item.PURL, nil
			}
		}
	}
	// 拿不到播放地址时用严格登录态接口复核，因为 vkey 接口对失效凭据不一定直接报错，只有复核后才能区分登录失效与无版权。
	creds := parseQQCookies(cookie)
	if id := qqAccountID(creds); id != "" {
		valid, err := c.loginState(ctx, cookie, creds, id)
		if err != nil {
			return "", err
		}
		if !valid {
			if allowRefresh {
				refreshed, err := c.refreshExpiredCookie(ctx, cookie)
				if err != nil {
					return "", err
				}
				if refreshed {
					return c.playURL(ctx, songMID, false)
				}
			}
			return "", fmt.Errorf("获取播放地址：%w", port.ErrSourceSignedOut)
		}
	}
	return "", &port.RejectedError{Err: errors.New("QQ 音乐没有提供这首歌的播放地址")}
}
