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
	playURLAPI = "https://u.y.qq.com/cgi-bin/musicu.fcg"
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
	filenames := []string{
		"F000" + songMID + songMID + ".flac",
		"M800" + songMID + songMID + ".mp3",
		"M500" + songMID + songMID + ".mp3",
	}
	payload, err := json.Marshal(map[string]any{
		"req_1": map[string]any{
			"module": "music.vkey.GetVkey",
			"method": "UrlGetVkey",
			"param": map[string]any{
				"filename":  filenames,
				"guid":      "0",
				"songmid":   []string{songMID},
				"songtype":  []int{0},
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
	if err := c.get(ctx, "qq", "play_url", playURLAPI, query, desktopHeader, &resp); err != nil {
		return "", err
	}
	for _, filename := range filenames {
		for _, item := range resp.Req1.Data.Items {
			if item.Filename == filename && item.PURL != "" {
				return playURLCDN + item.PURL, nil
			}
		}
	}
	return "", &port.RejectedError{Err: errors.New("QQ 音乐没有提供这首歌的播放地址")}
}
