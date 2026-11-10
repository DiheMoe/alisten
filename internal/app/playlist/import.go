package playlist

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
	playlistlink "github.com/dihemoe/alisten/internal/domain/playlist"
	"github.com/dihemoe/alisten/internal/domain/track"
)

const (
	defaultImportName = "导入的歌单"
	// 解析短链接和导入平台歌单都要访问外部，各自按成员限流，额度相同。
	outboundBurst  = 10
	outboundRefill = 6 * time.Minute
)

var (
	ErrInvalidPlaylistLink = errors.New("不是网易云或 QQ 音乐的歌单链接")
	ErrUnsupportedPlaylist = errors.New("只能导入网易云或 QQ 音乐的歌单")
	ErrShortLinkFailed     = errors.New("没能打开这个短链接")
)

type ImportedTrack struct {
	Track                                   track.Track
	Title, Artist, Album, CoverURL, PageURL string
	Duration                                time.Duration
}

// 链接曲目的 Track.ID 必须已经按 track.LinkID 规范化。
func (s *Service) ImportFile(ctx context.Context, memberID, name string, tracks []ImportedTrack) (port.PlaylistRecord, int, error) {
	if err := s.registered(ctx, memberID); err != nil {
		return port.PlaylistRecord{}, 0, err
	}
	name, err := validName(name)
	if err != nil {
		name = defaultImportName
	}
	tracks = firstOccurrences(tracks, func(t ImportedTrack) track.Track { return t.Track })
	keys := make([]track.Track, len(tracks))
	for i, t := range tracks {
		keys[i] = t.Track
	}
	known, err := s.tracks.TrackMetas(ctx, keys)
	if err != nil {
		return port.PlaylistRecord{}, 0, err
	}
	items := make([]port.PlaylistItem, 0, len(tracks))
	for _, t := range tracks {
		if m, ok := known[t.Track]; ok {
			items = append(items, port.PlaylistItem{Track: t.Track, Meta: m})
		} else if strings.TrimSpace(t.Title) != "" {
			items = append(items, port.PlaylistItem{Track: t.Track, Meta: fileMeta(t)})
		}
	}
	return s.importItems(ctx, memberID, name, items)
}

func (s *Service) ResolveLink(ctx context.Context, memberID, raw string) (track.Source, string, error) {
	if err := s.registered(ctx, memberID); err != nil {
		return "", "", err
	}
	link, ok := playlistlink.ParseLink(raw)
	if !ok {
		return "", "", ErrInvalidPlaylistLink
	}
	if !link.Short {
		return link.Source, link.ID, nil
	}
	if err := s.take(ctx, "playlist-link:member:"+memberID, outboundBurst, outboundRefill); err != nil {
		return "", "", err
	}
	final, err := s.shortLinks.Resolve(ctx, link.URL)
	if err != nil {
		return "", "", fmt.Errorf("解析短链接 %s：%w：%w", link.URL, err, ErrShortLinkFailed)
	}
	resolved, ok := playlistlink.ParseLink(final)
	if !ok {
		return "", "", ErrInvalidPlaylistLink
	}
	if resolved.Short {
		return "", "", fmt.Errorf("短链接 %s 停在了 %s：%w", link.URL, final, ErrShortLinkFailed)
	}
	return resolved.Source, resolved.ID, nil
}

func (s *Service) ImportPlatform(ctx context.Context, memberID, name string, source track.Source, playlistID string) (port.PlaylistRecord, int, error) {
	if err := s.registered(ctx, memberID); err != nil {
		return port.PlaylistRecord{}, 0, err
	}
	name, err := validName(name)
	if err != nil {
		return port.PlaylistRecord{}, 0, err
	}
	if source != track.NetEase && source != track.QQ {
		return port.PlaylistRecord{}, 0, ErrUnsupportedPlaylist
	}
	if err := s.take(ctx, "playlist-import:member:"+memberID, outboundBurst, outboundRefill); err != nil {
		return port.PlaylistRecord{}, 0, err
	}
	songs, err := s.catalog.PlaylistSongs(ctx, source, playlistID)
	if err != nil {
		return port.PlaylistRecord{}, 0, err
	}
	songs = firstOccurrences(songs, func(song port.Song) track.Track { return song.Track })
	items := make([]port.PlaylistItem, len(songs))
	for i, song := range songs {
		items[i] = port.PlaylistItem(song)
	}
	return s.importItems(ctx, memberID, name, items)
}

func (s *Service) importItems(ctx context.Context, memberID, name string, items []port.PlaylistItem) (port.PlaylistRecord, int, error) {
	p, err := s.playlists.ImportPlaylist(ctx, memberID, name, items, maxPlaylists)
	if errors.Is(err, port.ErrPlaylistLimit) {
		return port.PlaylistRecord{}, 0, ErrTooManyPlaylists
	}
	if err != nil {
		return port.PlaylistRecord{}, 0, err
	}
	return p, len(items), nil
}

// 只看前 maxItems 首，其中重复的歌只留第一次出现的位置。
func firstOccurrences[T any](items []T, key func(T) track.Track) []T {
	items = items[:min(len(items), maxItems)]
	seen := make(map[track.Track]bool, len(items))
	kept := make([]T, 0, len(items))
	for _, item := range items {
		if k := key(item); !seen[k] {
			seen[k] = true
			kept = append(kept, item)
		}
	}
	return kept
}

// 曲目表里没有的歌只能用文件里的元数据。文件可以随意改写，所以地址只留平台自己的 https 地址。
func fileMeta(t ImportedTrack) track.Meta {
	m := track.Meta{Title: t.Title, Artist: t.Artist, Album: t.Album, Duration: t.Duration}
	switch id := t.Track.ID; t.Track.Source {
	case track.NetEase:
		m.CoverURL = platformURL(t.CoverURL, func(host string) bool { return strings.HasSuffix(host, ".music.126.net") })
		if m.PageURL = platformURL(t.PageURL, isHost("music.163.com")); m.PageURL == "" {
			m.PageURL = track.NetEasePageURL(id)
		}
	case track.QQ:
		m.CoverURL = platformURL(t.CoverURL, isHost("y.gtimg.cn"))
		if m.PageURL = platformURL(t.PageURL, isHost("y.qq.com")); m.PageURL == "" {
			m.PageURL = track.QQPageURL(id)
		}
	// B 站和链接曲目的封面存在导出站点的对象存储里，换一个站点就失效。
	case track.Bilibili:
		m.PageURL = track.BilibiliPageURL(id)
	case track.Link:
		m.PageURL = id
	}
	return m
}

func platformURL(raw string, allowed func(host string) bool) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || !allowed(strings.ToLower(u.Hostname())) {
		return ""
	}
	return raw
}

func isHost(want string) func(string) bool {
	return func(host string) bool { return host == want }
}
