package qq

import (
	"context"
	"errors"
	"fmt"
	"net/url"

	"github.com/dihemoe/alisten/internal/app/port"
)

const playlistDetailAPI = "https://i.y.qq.com/qzone-music/fcg-bin/fcg_ucc_getcdinfo_byids_cp.fcg"

type playlistDetailResponse struct {
	Code   int `json:"code"`
	Cdlist []struct {
		Songlist []struct {
			SongMID   string   `json:"songmid"`
			SongName  string   `json:"songname"`
			AlbumName string   `json:"albumname"`
			AlbumMID  string   `json:"albummid"`
			Interval  int64    `json:"interval"`
			Singer    []singer `json:"singer"`
		} `json:"songlist"`
	} `json:"cdlist"`
}

func (r playlistDetailResponse) check() error {
	if r.Code != 0 {
		return fmt.Errorf("QQ 音乐返回错误码 %d", r.Code)
	}
	return nil
}

func (c *Client) PlaylistTracks(ctx context.Context, playlistID string) ([]port.Song, error) {
	return cached(c, "playlist_tracks "+playlistID, func() ([]port.Song, error) {
		var resp playlistDetailResponse
		query := url.Values{
			"type": {"1"}, "json": {"1"}, "utf8": {"1"}, "onlysong": {"0"}, "disstid": {playlistID},
			"format": {"json"}, "g_tk": {"5381"}, "loginUin": {"0"}, "hostUin": {"0"},
			"inCharset": {"utf8"}, "outCharset": {"utf-8"}, "notice": {"0"}, "platform": {"yqq"}, "needNewCode": {"0"},
		}
		if err := c.get(ctx, "qq", "playlist_tracks", playlistDetailAPI, query, desktopHeader, &resp); err != nil {
			return nil, err
		}
		if len(resp.Cdlist) == 0 {
			return nil, errors.New("QQ 音乐的歌单不存在或不可见")
		}
		// 这个接口一次返回全部曲目，实测 1137 首的歌单也完整，不需要翻页。
		songs := make([]port.Song, 0, len(resp.Cdlist[0].Songlist))
		for _, s := range resp.Cdlist[0].Songlist {
			// 没有 songmid 的条目是下架等不可用的歌，丢弃但保持其余歌曲的原顺序。
			if s.SongMID == "" {
				continue
			}
			songs = append(songs, song(s.SongMID, s.SongName, s.Singer, s.AlbumName, s.AlbumMID, s.Interval))
		}
		return songs, nil
	})
}
