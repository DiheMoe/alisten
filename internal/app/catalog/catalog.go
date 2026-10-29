package catalog

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/room"
	"github.com/dihemoe/alisten/internal/domain/track"
)

const (
	searchBurst  = 10
	searchRefill = time.Minute
	// 最长等待与上游接口 30 秒的超时之和不超过客户端 60 秒的请求超时。
	maxSearchWait    = 30 * time.Second
	storeTimeout     = 10 * time.Second
	autoDJCandidates = 25
	autoDJLimit      = 10
)

var ErrRateLimited = errors.New("搜索过于频繁，请稍后再试。")

// RejectedLinkError 表示链接属于某个音源，但不能点播，例如 B 站的番剧和直播；它的文本就是返回给点歌人的原因。
type RejectedLinkError struct {
	Reason string
}

func (e *RejectedLinkError) Error() string { return e.Reason }

type Service struct {
	netease  port.NetEaseSource
	qq       port.Source
	bilibili port.BilibiliSource
	links    port.BilibiliLinks
	tracks   port.Tracks
	history  port.PlayHistory
	rooms    port.RoomDirectory
	quota    port.SearchQuota
}

func New(netease port.NetEaseSource, qq port.Source, bilibili port.BilibiliSource, links port.BilibiliLinks, tracks port.Tracks, history port.PlayHistory, rooms port.RoomDirectory, quota port.SearchQuota) *Service {
	return &Service{netease: netease, qq: qq, bilibili: bilibili, links: links, tracks: tracks, history: history, rooms: rooms, quota: quota}
}

// SearchTracks 只在上游一次取回的结果中分页，page 从 1 开始；total 是取回的结果数。
func (s *Service) SearchTracks(ctx context.Context, roomID string, source track.Source, keyword string, page, size int64) (songs []port.Song, total int64, err error) {
	src, err := s.searchable(source)
	if err != nil {
		return nil, 0, err
	}
	if err := s.take(ctx, roomID); err != nil {
		return nil, 0, err
	}
	all, err := src.SearchTracks(ctx, keyword)
	if err != nil {
		return nil, 0, upstream(source, "搜索歌曲", err)
	}
	return paginate(all, page, size), int64(len(all)), nil
}

func (s *Service) SearchPlaylists(ctx context.Context, roomID string, source track.Source, keyword string, page, size int64) (playlists []port.Playlist, total int64, err error) {
	src, err := s.searchable(source)
	if err != nil {
		return nil, 0, err
	}
	if err := s.take(ctx, roomID); err != nil {
		return nil, 0, err
	}
	all, err := src.SearchPlaylists(ctx, keyword)
	if err != nil {
		return nil, 0, upstream(source, "搜索歌单", err)
	}
	return paginate(all, page, size), int64(len(all)), nil
}

func (s *Service) PlaylistTracks(ctx context.Context, roomID string, source track.Source, playlistID string) ([]port.Song, error) {
	if source != track.NetEase {
		return nil, unsupported(source, "歌单内的歌曲")
	}
	if err := s.take(ctx, roomID); err != nil {
		return nil, err
	}
	songs, err := s.netease.PlaylistTracks(ctx, playlistID)
	if err != nil {
		return nil, upstream(source, "获取歌单内的歌曲", err)
	}
	return songs, nil
}

func (s *Service) RecommendList(ctx context.Context, roomID string) ([]port.Song, error) {
	if err := s.take(ctx, roomID); err != nil {
		return nil, err
	}
	songs, err := s.candidates(ctx, roomID, true)
	if errors.Is(err, port.ErrProviderFailed) {
		return nil, nil
	}
	return songs, err
}

func (s *Service) TrackMeta(ctx context.Context, t track.Track) (track.Meta, error) {
	lookup, err := s.metaSource(t.Source)
	if err != nil {
		return track.Meta{}, err
	}
	readCtx, cancel := context.WithTimeout(ctx, storeTimeout)
	p, ok, err := s.tracks.PreparedTrack(readCtx, t)
	cancel()
	if err != nil {
		return track.Meta{}, err
	}
	if ok {
		return p.Meta, nil
	}
	m, err := lookup(ctx, t.ID)
	if err != nil {
		return track.Meta{}, upstream(t.Source, "查询元数据", err)
	}
	// B 站的适配器不填页面地址，平台的网址规则在领域中。
	if t.Source == track.Bilibili {
		m.PageURL = track.BilibiliPageURL(t.ID)
	}
	return m, nil
}

// B 站的链接转成 B 站音源的曲目，其他链接仍是链接曲目：链接准备完成后才有元数据，在此之前以链接本身作为标题。
func (s *Service) ResolveLink(ctx context.Context, link string) (track.Track, track.Meta, error) {
	id, ok, err := s.links.Recognize(ctx, link)
	var rejected *port.RejectedError
	switch {
	case errors.As(err, &rejected):
		return track.Track{}, track.Meta{}, &RejectedLinkError{Reason: rejected.Error()}
	case err != nil:
		return track.Track{}, track.Meta{}, upstream(track.Bilibili, "识别链接", err)
	case !ok:
		return track.Track{Source: track.Link, ID: link}, track.Meta{Title: link, PageURL: link}, nil
	}
	if _, _, valid := track.ParseBilibiliID(id); !valid {
		return track.Track{}, track.Meta{}, fmt.Errorf("哔哩哔哩认出的曲目 ID %q 格式不对：%w", id, port.ErrProviderFailed)
	}
	t := track.Track{Source: track.Bilibili, ID: id}
	m, err := s.TrackMeta(ctx, t)
	return t, m, err
}

// 按关键词点歌的这次搜索不消耗搜索额度。
func (s *Service) FirstMatch(ctx context.Context, source track.Source, keyword string) (track.Track, track.Meta, error) {
	src, err := s.searchable(source)
	if err != nil {
		return track.Track{}, track.Meta{}, err
	}
	songs, err := src.SearchTracks(ctx, keyword)
	if err != nil {
		return track.Track{}, track.Meta{}, upstream(source, "搜索歌曲", err)
	}
	if len(songs) == 0 {
		return track.Track{}, track.Meta{}, fmt.Errorf("在%s搜索 %q 没有结果：%w", source, keyword, port.ErrProviderFailed)
	}
	return songs[0].Track, songs[0].Meta, nil
}

func (s *Service) Recommend(ctx context.Context, roomID string) (track.Track, track.Meta, bool, error) {
	songs, err := s.candidates(ctx, roomID, false)
	if err != nil || len(songs) == 0 {
		return track.Track{}, track.Meta{}, false, err
	}
	pick := songs[rand.IntN(len(songs))]
	return pick.Track, pick.Meta, true, nil
}

func (s *Service) candidates(ctx context.Context, roomID string, includeQueue bool) ([]port.Song, error) {
	readCtx, cancel := context.WithTimeout(ctx, storeTimeout)
	defer cancel()
	seeds, err := s.history.AutoDJSeeds(readCtx, roomID, includeQueue)
	if err != nil {
		return nil, err
	}
	excluded, err := s.history.AutoDJExclusions(readCtx, roomID)
	if err != nil {
		return nil, err
	}
	skip := make(map[track.Track]bool, len(seeds)+len(excluded))
	for _, t := range seeds {
		skip[t] = true
	}
	for _, t := range excluded {
		skip[t] = true
	}
	var picks []port.Song
	for _, seed := range seeds {
		if len(picks) > autoDJCandidates {
			break
		}
		similar, err := s.netease.SimilarTracks(ctx, seed.ID)
		if err != nil {
			return nil, upstream(track.NetEase, "获取相似歌曲", err)
		}
		for _, song := range similar {
			if !skip[song.Track] {
				skip[song.Track] = true
				picks = append(picks, song)
			}
		}
	}
	return picks[:min(len(picks), autoDJLimit)], nil
}

func (s *Service) take(ctx context.Context, roomID string) error {
	readCtx, cancel := context.WithTimeout(ctx, storeTimeout)
	r, err := s.rooms.FindRoom(readCtx, roomID)
	cancel()
	if err != nil {
		return err
	}
	if r.Info.Kind == room.Persistent {
		return nil
	}
	for {
		wait, err := s.quota.TakeSearch(ctx, roomID, searchBurst, searchRefill)
		if err != nil {
			return err
		}
		if wait == 0 {
			return nil
		}
		if wait > maxSearchWait {
			return ErrRateLimited
		}
		timer := time.NewTimer(wait)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		}
	}
}

func (s *Service) metaSource(source track.Source) (func(context.Context, string) (track.Meta, error), error) {
	switch source {
	case track.NetEase:
		return s.netease.TrackMeta, nil
	case track.QQ:
		return s.qq.TrackMeta, nil
	case track.Bilibili:
		return s.bilibili.TrackMeta, nil
	}
	return nil, unsupported(source, "点歌前查询元数据")
}

func (s *Service) searchable(source track.Source) (port.Source, error) {
	switch source {
	case track.NetEase:
		return s.netease, nil
	case track.QQ:
		return s.qq, nil
	}
	return nil, unsupported(source, "搜索")
}

func unsupported(source track.Source, what string) error {
	return fmt.Errorf("%s不支持%s：%w", source, what, port.ErrProviderFailed)
}

func upstream(source track.Source, what string, err error) error {
	return fmt.Errorf("在%s%s：%w：%w", source, what, err, port.ErrProviderFailed)
}

// page 从 1 开始，size 至少为 1；两者都来自客户端，乘积可能溢出。
func paginate[T any](items []T, page, size int64) []T {
	n := int64(len(items))
	if page-1 > n/size {
		return nil
	}
	start := (page - 1) * size
	return items[start:min(start+size, n)]
}
