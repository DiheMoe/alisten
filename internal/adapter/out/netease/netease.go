package netease

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
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
	lyricAPI    = "https://music.163.com/weapi/song/lyric/v1"
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
	lyric, err := c.lyricOrDegrade(ctx, id)
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
	return port.Resolved{Meta: meta, Lyric: lyric.LRC, LyricWords: lyric.Words, LyricTrans: lyric.Trans, Audio: audio}, nil
}

// 行级 LRC 原文、逐字歌词（YRC 语法）、行级翻译 LRC，后两者可为空。
type lyricResult struct {
	LRC   string
	Words string
	Trans string
}

// lyricOrDegrade 让歌词问题不阻塞播放：接口失败就地重试一次，仍失败（或解析失败）则降级为无歌词并记 WARN。
// 曲目表里有记录的歌不会重新准备，网络抖动后直接降级会让这首歌永久没有歌词，所以先重试。
// 调用方的 ctx 被取消或超时不降级：任务退出时中断，不能把空歌词写进曲目表。
func (c *Client) lyricOrDegrade(ctx context.Context, id string) (lyricResult, error) {
	r, err := c.lyric(ctx, id)
	if err == nil || ctx.Err() != nil {
		return r, err
	}
	var iface *interfaceError
	if errors.As(err, &iface) {
		r, err = c.lyric(ctx, id)
		if err == nil || ctx.Err() != nil {
			return r, err
		}
	}
	c.logger.WarnContext(ctx, "歌词获取失败，按无歌词处理", "source", "netease", "track_id", id, "error", err)
	return lyricResult{}, nil
}

func (c *Client) lyric(ctx context.Context, id string) (lyricResult, error) {
	return cached(c, "lyric "+id, func() (lyricResult, error) {
		form, err := weapi(map[string]any{"csrf_token": "", "id": id, "lv": -1, "tv": -1, "rv": -1, "yv": -1})
		if err != nil {
			return lyricResult{}, err
		}
		var resp struct {
			base
			Lrc struct {
				Lyric string `json:"lyric"`
			} `json:"lrc"`
			Tlyric struct {
				Lyric string `json:"lyric"`
			} `json:"tlyric"`
			Yrc struct {
				Lyric string `json:"lyric"`
			} `json:"yrc"`
			Ytlrc struct {
				Lyric string `json:"lyric"`
			} `json:"ytlrc"`
		}
		if err := c.call(ctx, "lyric", lyricAPI, form, &resp); err != nil {
			return lyricResult{}, err
		}
		out := lyricResult{LRC: text(resp.Lrc.Lyric), Words: text(resp.Yrc.Lyric)}
		if out.LRC == "" && out.Words != "" {
			out.LRC = yrcToLRC(out.Words)
		}
		if trans := text(resp.Ytlrc.Lyric); trans != "" {
			out.Trans = trans
		} else {
			out.Trans = text(resp.Tlyric.Lyric)
		}
		return out, nil
	})
}

// 逐字歌词里没有行级原文时，剥掉字时间标记还原出行级 LRC。
var (
	yrcLineRe = regexp.MustCompile(`^\[(\d+),\d+\]`)
	yrcWordRe = regexp.MustCompile(`\(\d+,\d+,\d+\)`)
)

func yrcToLRC(yrc string) string {
	var b strings.Builder
	for _, row := range strings.Split(yrc, "\n") {
		m := yrcLineRe.FindStringSubmatch(row)
		if m == nil {
			continue
		}
		startMs, _ := strconv.Atoi(m[1])
		fmt.Fprintf(&b, "[%02d:%05.2f]%s\n", startMs/60000, float64(startMs%60000)/1000, yrcWordRe.ReplaceAllString(row, ""))
	}
	return b.String()
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
