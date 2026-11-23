package qq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/track"
)

const (
	searchAPI         = "https://c.y.qq.com/soso/fcgi-bin/search_for_qq_cp"
	searchPlaylistAPI = "https://c.y.qq.com/soso/fcgi-bin/client_music_search_songlist"
	detailAPI         = "https://c.y.qq.com/v8/fcg-bin/fcg_play_single_song.fcg"

	// search_for_qq_cp 的 n 传得比 30 大也最多返回 30 首歌，totalnum 的上限是 600。
	searchTrackBatch      = 30
	maxTrackSearchResults = 600
	// client_music_search_songlist 的 num_per_page 超过 50 会报 -3002。
	// display_num 会给出更大的数字，但偏移量从 500 起不再返回歌单，page_no 到 50 还会报 -3001。
	searchPlaylistBatch      = 50
	maxPlaylistSearchResults = 500
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

var _ port.PlaylistSource = (*Client)(nil)

type singer struct {
	Name string `json:"name"`
}

type searchPage[T any] struct {
	items []T
	total int64
}

func (c *Client) SearchTracks(ctx context.Context, keyword string, page, size int64) ([]port.Song, int64, error) {
	if page-1 > (maxTrackSearchResults-1)/size {
		return nil, 0, nil
	}
	res, err := cached(c, fmt.Sprintf("search_tracks %d %d %s", page, size, keyword), func() (searchPage[port.Song], error) {
		return collectPages(page, size, searchTrackBatch, maxTrackSearchResults, func(pageIndex, batch int64) ([]port.Song, int64, error) {
			var resp struct {
				status
				Data struct {
					Song struct {
						TotalNum int64 `json:"totalnum"`
						List     []struct {
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
			query := url.Values{
				"w":      {keyword},
				"format": {"json"},
				"p":      {strconv.FormatInt(pageIndex+1, 10)},
				"n":      {strconv.FormatInt(batch, 10)},
			}
			if err := c.get(ctx, "search_tracks", searchAPI, query, mobileHeader, &resp); err != nil {
				return nil, 0, err
			}
			songs := make([]port.Song, len(resp.Data.Song.List))
			for i, s := range resp.Data.Song.List {
				songs[i] = song(s.SongMID, s.SongName, s.Singer, s.AlbumName, s.AlbumMID, s.Interval)
			}
			return songs, resp.Data.Song.TotalNum, nil
		})
	})
	return res.items, res.total, err
}

func (c *Client) SearchPlaylists(ctx context.Context, keyword string, page, size int64) ([]port.Playlist, int64, error) {
	if page-1 > (maxPlaylistSearchResults-1)/size {
		return nil, 0, nil
	}
	res, err := cached(c, fmt.Sprintf("search_playlists %d %d %s", page, size, keyword), func() (searchPage[port.Playlist], error) {
		return collectPages(page, size, searchPlaylistBatch, maxPlaylistSearchResults, func(pageIndex, batch int64) ([]port.Playlist, int64, error) {
			var resp struct {
				status
				Data struct {
					DisplayNum int64 `json:"display_num"`
					List       []struct {
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
				"query":        {keyword},
				"page_no":      {strconv.FormatInt(pageIndex, 10)},
				"num_per_page": {strconv.FormatInt(batch, 10)},
				"format":       {"json"},
				"remoteplace":  {"txt.yqq.playlist"},
				"flag_qc":      {"0"},
			}
			if err := c.get(ctx, "search_playlists", searchPlaylistAPI, query, desktopHeader, &resp); err != nil {
				return nil, 0, err
			}
			playlists := make([]port.Playlist, len(resp.Data.List))
			for i, p := range resp.Data.List {
				playlists[i] = port.Playlist{
					ID:          text(p.DissID),
					Name:        text(html.UnescapeString(p.DissName)),
					CoverURL:    text(p.ImgURL),
					Creator:     text(html.UnescapeString(p.Creator.Name)),
					Description: text(html.UnescapeString(p.Introduction)),
					TrackCount:  p.SongCount,
					PlayCount:   p.ListenNum,
				}
			}
			return playlists, resp.Data.DisplayNum, nil
		})
	})
	return res.items, res.total, err
}

func collectPages[T any](page, size, maxBatch, maxResults int64, fetchPage func(pageIndex, batch int64) ([]T, int64, error)) (searchPage[T], error) {
	start := (page - 1) * size
	end := min(start+size, maxResults)
	batch := min(size, maxBatch)
	firstIdx := start / batch
	lastIdx := (end - 1) / batch

	var out []T
	var total int64
	for idx := firstIdx; idx <= lastIdx; idx++ {
		list, rawTotal, err := fetchPage(idx, batch)
		if err != nil {
			return searchPage[T]{}, err
		}
		if idx == firstIdx {
			total = min(max(rawTotal, 0), maxResults)
			if total == 0 && start == 0 {
				total = int64(len(list))
			}
			if start >= total {
				return searchPage[T]{total: total}, nil
			}
			end = min(end, total)
			lastIdx = (end - 1) / batch
		}
		if len(list) == 0 {
			break
		}
		base := idx * batch
		lo := min(max(start-base, 0), int64(len(list)))
		hi := min(max(end-base, 0), int64(len(list)))
		out = append(out, list[lo:hi]...)
	}
	return searchPage[T]{items: out, total: total}, nil
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
		if err := c.get(ctx, "track_detail", detailAPI, query, mobileHeader, &resp); err != nil {
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

// 行级 LRC 原文、逐字歌词（YRC 语法）和行级翻译 LRC，后两者可为空。
type lyricResult struct {
	LRC   string
	Words string
	Trans string
}

type playLyricResponse struct {
	Code    *int `json:"code"`
	Request struct {
		Code *int `json:"code"`
		Data struct {
			Lyric string `json:"lyric"`
			Trans string `json:"trans"`
		} `json:"data"`
	} `json:"request"`
}

func (r playLyricResponse) check() error {
	if r.Code == nil || r.Request.Code == nil {
		return errors.New("QQ 音乐歌词响应的 code 或 request.code 缺失或为 null")
	}
	if *r.Code != 0 {
		return &interfaceError{fmt.Errorf("QQ 音乐返回错误码 %d", *r.Code)}
	}
	if *r.Request.Code != 0 {
		return &interfaceError{fmt.Errorf("QQ 音乐歌词请求返回错误码 %d", *r.Request.Code)}
	}
	return nil
}

// lyricOrDegrade 在歌词接口失败时就地重试一次，仍失败或解析、解密失败就降级为无歌词并记 WARN，不让歌词阻塞播放。
// 先重试一次，是因为曲目表里有记录的歌不会重新准备，网络抖动后直接降级会让这首歌永久没有歌词。
// 调用方的 ctx 已取消或超时时不降级，否则任务退出会把空歌词写进曲目表。
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
	c.logger.WarnContext(ctx, "歌词获取失败，按无歌词处理", "source", "qq", "track_id", id, "error", err)
	return lyricResult{}, nil
}

func (c *Client) lyric(ctx context.Context, id string) (lyricResult, error) {
	return cached(c, "lyric "+id, func() (lyricResult, error) {
		payload, err := json.Marshal(map[string]any{
			"comm": map[string]any{"ct": 11, "cv": "1003006", "platform": "yqq.json", "needNewCode": 0},
			"request": map[string]any{
				"module": "music.musichallSong.PlayLyricInfo",
				"method": "GetPlayLyricInfo",
				"param": map[string]any{
					"songMID": id, "songID": 0, "songName": "", "singerName": "", "albumName": "",
					"crypt": 1, "ct": 19, "cv": 2111, "interval": 0, "type": 0,
					"qrc": 1, "qrc_t": 0, "trans": 1, "trans_t": 0, "roma": 0, "roma_t": 0, "lrc_t": 0,
				},
			},
		})
		if err != nil {
			return lyricResult{}, err
		}
		var resp playLyricResponse
		query := url.Values{"data": {string(payload)}}
		if err := c.get(ctx, "lyric", musicuAPI, query, lyricHeader, &resp); err != nil {
			return lyricResult{}, err
		}
		return parsePlayLyric(resp.Request.Data.Lyric, resp.Request.Data.Trans)
	})
}

func parsePlayLyric(lyricHex, transHex string) (lyricResult, error) {
	var out lyricResult
	if lyricHex != "" {
		plain, err := decryptQRCHex(lyricHex)
		if err != nil {
			return lyricResult{}, fmt.Errorf("解密 QQ 音乐的歌词：%w", err)
		}
		tags, lines := parseQRC(plain)
		if len(lines) == 0 {
			// 没有逐字数据时 LyricContent 里直接是行级 LRC。
			out.LRC = text(plain)
		} else {
			out.LRC = text(renderLRCLines(tags, lines))
			if hasWordTimings(lines) {
				out.Words = renderYRCLines(lines)
			}
		}
	}
	if transHex != "" {
		// 翻译解密后是行级 LRC。翻译属于附加信息，解密失败只丢弃翻译。
		if plain, err := decryptQRCHex(transHex); err == nil {
			out.Trans = cleanTrans(plain)
		}
	}
	return out, nil
}

var transLineRe = regexp.MustCompile(`^\[\d+:\d+(?:\.\d+)?\](.*)$`)

// 翻译里的标签行和代表无翻译的 // 占位行不参与前端配对。
func cleanTrans(lrc string) string {
	var b strings.Builder
	for _, row := range strings.Split(lrc, "\n") {
		m := transLineRe.FindStringSubmatch(strings.TrimSpace(row))
		if m == nil {
			continue
		}
		if body := strings.TrimSpace(m[1]); body == "" || body == "//" {
			continue
		}
		b.WriteString(row)
		b.WriteByte('\n')
	}
	return text(b.String())
}

func hasWordTimings(lines []qrcLine) bool {
	for _, l := range lines {
		if len(l.Words) > 0 {
			return true
		}
	}
	return false
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
			PageURL:  track.QQPageURL(mid),
			Duration: max(0, time.Duration(seconds)*time.Second),
		},
	}
}
