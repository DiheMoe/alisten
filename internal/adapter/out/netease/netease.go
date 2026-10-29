package netease

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/track"
)

const (
	linuxAPI    = "https://music.163.com/api/linux/forward"
	detailAPI   = "https://music.163.com/weapi/v3/song/detail"
	playlistAPI = "https://music.163.com/weapi/v3/playlist/detail"
	lyricAPI    = "https://music.163.com/weapi/song/lyric"
	similarAPI  = "https://music.163.com/weapi/discovery/simiSong"
	weapiURLAPI = "https://music.163.com/weapi/song/enhance/player/url"
	eapiURLAPI  = "https://interface3.music.163.com/eapi/song/enhance/player/url/v1"
)

const (
	searchTracks    = 1
	searchPlaylists = 1000
	detailBatch     = 500
)

var (
	_ port.NetEaseSource = (*Client)(nil)
	_ port.PlayURLs      = (*Client)(nil)
)

type (
	artist struct {
		Name string `json:"name"`
	}

	album struct {
		Name   string `json:"name"`
		PicURL string `json:"picUrl"`
	}

	song struct {
		ID       int64    `json:"id"`
		Name     string   `json:"name"`
		Artists  []artist `json:"ar"`
		Album    album    `json:"al"`
		Duration int64    `json:"dt"`
	}

	similarSong struct {
		ID       int64    `json:"id"`
		Name     string   `json:"name"`
		Artists  []artist `json:"artists"`
		Album    album    `json:"album"`
		Duration int64    `json:"duration"`
	}

	playlist struct {
		ID          int64  `json:"id"`
		Name        string `json:"name"`
		CoverImgURL string `json:"coverImgUrl"`
		Description string `json:"description"`
		Creator     struct {
			Nickname string `json:"nickname"`
		} `json:"creator"`
		TrackCount int64 `json:"trackCount"`
		PlayCount  int64 `json:"playCount"`
	}

	urlResponse struct {
		base
		Data []struct {
			URL           string `json:"url"`
			FreeTrialInfo any    `json:"freeTrialInfo"`
		} `json:"data"`
	}
)

func (c *Client) SearchTracks(ctx context.Context, keyword string) ([]port.Song, error) {
	return cached(c, "search_tracks "+keyword, func() ([]port.Song, error) {
		var resp struct {
			base
			Result struct {
				Songs []song `json:"songs"`
			} `json:"result"`
		}
		if err := c.search(ctx, "search_tracks", keyword, searchTracks, &resp); err != nil {
			return nil, err
		}
		songs := make([]port.Song, len(resp.Result.Songs))
		for i, s := range resp.Result.Songs {
			songs[i] = s.song()
		}
		return songs, nil
	})
}

func (c *Client) SearchPlaylists(ctx context.Context, keyword string) ([]port.Playlist, error) {
	return cached(c, "search_playlists "+keyword, func() ([]port.Playlist, error) {
		var resp struct {
			base
			Result struct {
				Playlists []playlist `json:"playlists"`
			} `json:"result"`
		}
		if err := c.search(ctx, "search_playlists", keyword, searchPlaylists, &resp); err != nil {
			return nil, err
		}
		playlists := make([]port.Playlist, len(resp.Result.Playlists))
		for i, p := range resp.Result.Playlists {
			playlists[i] = port.Playlist{
				ID:          strconv.FormatInt(p.ID, 10),
				Name:        p.Name,
				CoverURL:    p.CoverImgURL,
				Creator:     p.Creator.Nickname,
				Description: p.Description,
				TrackCount:  p.TrackCount,
				PlayCount:   p.PlayCount,
			}
		}
		return playlists, nil
	})
}

func (c *Client) search(ctx context.Context, op, keyword string, kind int, out response) error {
	form, err := linux(map[string]any{
		"method": "POST",
		"url":    "http://music.163.com/api/cloudsearch/pc",
		"params": map[string]any{"s": keyword, "type": kind, "offset": 0, "limit": 100},
	})
	if err != nil {
		return err
	}
	return c.call(ctx, op, linuxAPI, form, out)
}

func (c *Client) PlaylistTracks(ctx context.Context, playlistID string) ([]port.Song, error) {
	return cached(c, "playlist_tracks "+playlistID, func() ([]port.Song, error) {
		form, err := weapi(map[string]any{"id": playlistID, "n": 0, "csrf_token": ""})
		if err != nil {
			return nil, err
		}
		var resp struct {
			base
			Playlist struct {
				TrackIDs []struct {
					ID int64 `json:"id"`
				} `json:"trackIds"`
			} `json:"playlist"`
		}
		if err := c.call(ctx, "playlist_tracks", playlistAPI, form, &resp); err != nil {
			return nil, err
		}
		ids := make([]string, len(resp.Playlist.TrackIDs))
		for i, t := range resp.Playlist.TrackIDs {
			ids[i] = strconv.FormatInt(t.ID, 10)
		}
		var songs []port.Song
		for start := 0; start < len(ids); start += detailBatch {
			batch, err := c.details(ctx, ids[start:min(start+detailBatch, len(ids))])
			if err != nil {
				return nil, err
			}
			songs = append(songs, batch...)
		}
		return songs, nil
	})
}

func (c *Client) TrackMeta(ctx context.Context, id string) (track.Meta, error) {
	return cached(c, "track_meta "+id, func() (track.Meta, error) {
		songs, err := c.details(ctx, []string{id})
		if err != nil {
			return track.Meta{}, err
		}
		if len(songs) == 0 {
			return track.Meta{}, &port.RejectedError{Err: errors.New("网易云中没有这首歌")}
		}
		return songs[0].Meta, nil
	})
}

func (c *Client) details(ctx context.Context, ids []string) ([]port.Song, error) {
	refs := make([]map[string]string, len(ids))
	for i, id := range ids {
		refs[i] = map[string]string{"id": id}
	}
	cJSON, err := json.Marshal(refs)
	if err != nil {
		return nil, err
	}
	idsJSON, err := json.Marshal(ids)
	if err != nil {
		return nil, err
	}
	form, err := weapi(map[string]any{"c": string(cJSON), "ids": string(idsJSON)})
	if err != nil {
		return nil, err
	}
	var resp struct {
		base
		Songs []song `json:"songs"`
	}
	if err := c.call(ctx, "track_detail", detailAPI, form, &resp); err != nil {
		return nil, err
	}
	found := make(map[string]port.Song, len(resp.Songs))
	for _, s := range resp.Songs {
		found[strconv.FormatInt(s.ID, 10)] = s.song()
	}
	songs := make([]port.Song, 0, len(ids))
	for _, id := range ids {
		if s, ok := found[id]; ok {
			songs = append(songs, s)
		}
	}
	return songs, nil
}

func (c *Client) SimilarTracks(ctx context.Context, id string) ([]port.Song, error) {
	return cached(c, "similar "+id, func() ([]port.Song, error) {
		form, err := weapi(map[string]any{"songid": id, "limit": 50, "offset": 0})
		if err != nil {
			return nil, err
		}
		var resp struct {
			base
			Songs []similarSong `json:"songs"`
		}
		if err := c.call(ctx, "similar", similarAPI, form, &resp); err != nil {
			return nil, err
		}
		songs := make([]port.Song, len(resp.Songs))
		for i, s := range resp.Songs {
			songs[i] = toSong(s.ID, s.Name, s.Artists, s.Album, s.Duration)
		}
		return songs, nil
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

func (c *Client) lyric(ctx context.Context, id string) (string, error) {
	return cached(c, "lyric "+id, func() (string, error) {
		form, err := weapi(map[string]any{"csrf_token": "", "id": id, "lv": -1, "tv": -1})
		if err != nil {
			return "", err
		}
		var resp struct {
			base
			Lrc struct {
				Lyric string `json:"lyric"`
			} `json:"lrc"`
		}
		if err := c.call(ctx, "lyric", lyricAPI, form, &resp); err != nil {
			return "", err
		}
		return text(resp.Lrc.Lyric), nil
	})
}

func (c *Client) PlayURL(ctx context.Context, id string) (string, error) {
	eapiForm, err := eapi(eapiURLAPI, map[string]any{
		"ids":        []json.Number{json.Number(id)},
		"level":      "exhigh",
		"encodeType": "flac",
		"header":     `{"os":"pc","appver":"","osver":"","deviceId":"pyncm!","requestId":"12345678"}`,
	})
	if err != nil {
		return "", err
	}
	var exhigh urlResponse
	exhighErr := c.call(ctx, "play_url", eapiURLAPI, eapiForm, &exhigh)
	if u := exhigh.url(); exhighErr == nil && u != "" {
		return u, nil
	}
	weapiForm, err := weapi(map[string]any{"ids": []string{id}, "br": 320000})
	if err != nil {
		return "", err
	}
	var standard urlResponse
	standardErr := c.call(ctx, "play_url", weapiURLAPI, weapiForm, &standard)
	if u := standard.url(); standardErr == nil && u != "" {
		return u, nil
	}
	if err := errors.Join(exhighErr, standardErr); err != nil {
		return "", err
	}
	if exhigh.trial() || standard.trial() {
		return "", &port.RejectedError{Err: errors.New("这首歌需要网易云会员账号才能完整播放，请管理员在后台设置会员账号的 Cookie")}
	}
	return "", &port.RejectedError{Err: errors.New("网易云没有提供这首歌的播放地址")}
}

func (r urlResponse) trial() bool {
	return len(r.Data) > 0 && r.Data[0].FreeTrialInfo != nil
}

func (r urlResponse) url() string {
	if len(r.Data) == 0 || r.trial() {
		return ""
	}
	return r.Data[0].URL
}

func (s song) song() port.Song {
	return toSong(s.ID, s.Name, s.Artists, s.Album, s.Duration)
}

func toSong(id int64, name string, artists []artist, al album, durationMs int64) port.Song {
	sid := strconv.FormatInt(id, 10)
	names := make([]string, 0, len(artists))
	for _, a := range artists {
		if a.Name != "" {
			names = append(names, a.Name)
		}
	}
	return port.Song{
		Track: track.Track{Source: track.NetEase, ID: sid},
		Meta: track.Meta{
			Title:    text(name),
			Artist:   text(strings.Join(names, ", ")),
			Album:    text(al.Name),
			CoverURL: text(al.PicURL),
			PageURL:  "https://music.163.com/#/song?id=" + sid,
			Duration: time.Duration(durationMs) * time.Millisecond,
		},
	}
}

func text(s string) string {
	return strings.ReplaceAll(strings.ToValidUTF8(s, "\uFFFD"), "\x00", "")
}
