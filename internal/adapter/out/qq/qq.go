package qq

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/track"
)

const (
	searchAPI         = "https://c.y.qq.com/soso/fcgi-bin/search_for_qq_cp"
	searchPlaylistAPI = "https://c.y.qq.com/soso/fcgi-bin/client_music_search_songlist"
	detailAPI         = "https://c.y.qq.com/v8/fcg-bin/fcg_play_single_song.fcg"
	lyricAPI          = "https://c.y.qq.com/lyric/fcgi-bin/fcg_query_lyric_new.fcg"
)

var (
	mobileHeader = http.Header{
		"User-Agent": {"Mozilla/5.0 (iPhone; CPU iPhone OS 9_1 like Mac OS X) AppleWebKit/601.1.46 (KHTML, like Gecko) Version/9.0 Mobile/13B143 Safari/601.1"},
		"Referer":    {"http://m.y.qq.com"},
	}
	desktopHeader = http.Header{
		"User-Agent": {"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/119.0.0.0 Safari/537.36"},
		"Referer":    {"https://y.qq.com/portal/search.html"},
	}
	lyricHeader = http.Header{
		"User-Agent": mobileHeader["User-Agent"],
		"Referer":    {"https://y.qq.com/portal/player.html"},
	}
	audioHeader = http.Header{
		"User-Agent": desktopHeader["User-Agent"],
	}
)

var _ port.Source = (*Client)(nil)

type singer struct {
	Name string `json:"name"`
}

func (c *Client) SearchTracks(ctx context.Context, keyword string) ([]port.Song, error) {
	return cached(c, "search_tracks "+keyword, func() ([]port.Song, error) {
		var resp struct {
			status
			Data struct {
				Song struct {
					List []struct {
						SongMID   string   `json:"songmid"`
						SongName  string   `json:"songname"`
						Singer    []singer `json:"singer"`
						AlbumName string   `json:"albumname"`
						AlbumMID  string   `json:"albummid"`
						Interval  int64    `json:"interval"`
					} `json:"list"`
				} `json:"song"`
			} `json:"data"`
		}
		query := url.Values{"w": {keyword}, "format": {"json"}, "p": {"1"}, "n": {"20"}}
		if err := c.get(ctx, "qq", "search_tracks", searchAPI, query, mobileHeader, &resp); err != nil {
			return nil, err
		}
		songs := make([]port.Song, len(resp.Data.Song.List))
		for i, s := range resp.Data.Song.List {
			songs[i] = song(s.SongMID, s.SongName, s.Singer, s.AlbumName, s.AlbumMID, s.Interval)
		}
		return songs, nil
	})
}

func (c *Client) SearchPlaylists(ctx context.Context, keyword string) ([]port.Playlist, error) {
	return cached(c, "search_playlists "+keyword, func() ([]port.Playlist, error) {
		var resp struct {
			status
			Data struct {
				List []struct {
					DissID       string `json:"dissid"`
					DissName     string `json:"dissname"`
					ImgURL       string `json:"imgurl"`
					Introduction string `json:"introduction"`
					Creator      struct {
						Name string `json:"name"`
					} `json:"creator"`
					ListenNum int64 `json:"listennum"`
					SongCount int64 `json:"song_count"`
				} `json:"list"`
			} `json:"data"`
		}
		query := url.Values{
			"query": {keyword}, "page_no": {"0"}, "num_per_page": {"20"}, "format": {"json"},
			"remoteplace": {"txt.yqq.playlist"}, "flag_qc": {"0"},
		}
		if err := c.get(ctx, "qq", "search_playlists", searchPlaylistAPI, query, desktopHeader, &resp); err != nil {
			return nil, err
		}
		playlists := make([]port.Playlist, len(resp.Data.List))
		for i, p := range resp.Data.List {
			playlists[i] = port.Playlist{
				ID:          p.DissID,
				Name:        html.UnescapeString(p.DissName),
				CoverURL:    p.ImgURL,
				Creator:     html.UnescapeString(p.Creator.Name),
				Description: html.UnescapeString(p.Introduction),
				TrackCount:  p.SongCount,
				PlayCount:   p.ListenNum,
			}
		}
		return playlists, nil
	})
}

func (c *Client) TrackMeta(ctx context.Context, id string) (track.Meta, error) {
	return cached(c, "track_meta "+id, func() (track.Meta, error) {
		var resp struct {
			status
			Data []struct {
				MID string `json:"mid"`
				// title 可包含 Live、伴奏这类版本说明，name 没有。
				Title  string   `json:"title"`
				Singer []singer `json:"singer"`
				Album  struct {
					MID  string `json:"mid"`
					Name string `json:"name"`
				} `json:"album"`
				Interval int64 `json:"interval"`
			} `json:"data"`
		}
		query := url.Values{"songmid": {id}, "format": {"json"}}
		if err := c.get(ctx, "qq", "track_detail", detailAPI, query, mobileHeader, &resp); err != nil {
			return track.Meta{}, err
		}
		if len(resp.Data) == 0 {
			return track.Meta{}, &port.RejectedError{Err: errors.New("QQ 音乐中没有这首歌")}
		}
		s := resp.Data[0]
		return song(s.MID, s.Title, s.Singer, s.Album.Name, s.Album.MID, s.Interval).Meta, nil
	})
}

func (c *Client) Resolve(ctx context.Context, id string) (port.Resolved, error) {
	meta, err := c.TrackMeta(ctx, id)
	if err != nil {
		return port.Resolved{}, err
	}
	lyric, err := c.lyric(ctx, id)
	if err != nil {
		return port.Resolved{}, err
	}
	audioURL, err := c.PlayURL(ctx, id)
	if err != nil {
		return port.Resolved{}, err
	}
	audio, err := c.download(ctx, audioURL)
	if err != nil {
		return port.Resolved{}, err
	}
	return port.Resolved{Meta: meta, Lyric: lyric, Audio: audio}, nil
}

type lyricResponse struct {
	Code  int    `json:"code"`
	Lyric string `json:"lyric"`
}

func (lyricResponse) check() error { return nil }

func (c *Client) lyric(ctx context.Context, id string) (string, error) {
	return cached(c, "lyric "+id, func() (string, error) {
		var resp lyricResponse
		query := url.Values{
			"songmid": {id}, "loginUin": {"0"}, "hostUin": {"0"}, "format": {"json"}, "inCharset": {"utf8"},
			"outCharset": {"utf-8"}, "notice": {"0"}, "platform": {"y.qq.json"}, "needNewCode": {"0"},
		}
		if err := c.get(ctx, "qq", "lyric", lyricAPI, query, lyricHeader, &resp); err != nil {
			return "", err
		}
		if resp.Code != 0 || resp.Lyric == "" {
			return "", nil
		}
		b, err := base64.StdEncoding.DecodeString(resp.Lyric)
		if err != nil {
			return "", fmt.Errorf("解码 QQ 音乐的歌词：%w", err)
		}
		return text(string(b)), nil
	})
}

func song(mid, title string, singers []singer, album, albumMID string, seconds int64) port.Song {
	names := make([]string, 0, len(singers))
	for _, s := range singers {
		if s.Name != "" {
			names = append(names, s.Name)
		}
	}
	cover := ""
	if albumMID != "" {
		cover = "https://y.gtimg.cn/music/photo_new/T002R300x300M000" + albumMID + ".jpg"
	}
	return port.Song{
		Track: track.Track{Source: track.QQ, ID: mid},
		Meta: track.Meta{
			Title:    text(title),
			Artist:   text(strings.Join(names, ", ")),
			Album:    text(album),
			CoverURL: text(cover),
			PageURL:  "https://y.qq.com/n/ryqq/songDetail/" + mid,
			Duration: time.Duration(seconds) * time.Second,
		},
	}
}
