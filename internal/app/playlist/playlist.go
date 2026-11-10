package playlist

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/dihemoe/alisten/internal/app/catalog"
	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/track"
)

const (
	maxPlaylists  = 50
	maxItems      = 1000
	maxNameLength = 50
	// 收藏可能要向音源查询元数据：额度防止批量请求让共用的音源账号被平台限流。
	addBurst  = 30
	addRefill = 2 * time.Second
)

var (
	ErrNotRegistered    = errors.New("注册账号之后才能使用歌单")
	ErrInvalidName      = errors.New("name：去掉首尾空白后应有 1 到 50 个字符，其中至少一个看得见，不能包含控制字符")
	ErrTooManyPlaylists = errors.New("最多只能建 50 个歌单")
	ErrPlaylistFull     = errors.New("一个歌单最多 1000 首歌")
	ErrTrackNotReady    = errors.New("这首歌还在准备，准备好之后再保存")
	ErrTooFast          = errors.New("操作太频繁，请稍后再试。")
)

type Service struct {
	playlists  port.Playlists
	members    port.Members
	tracks     port.TrackLookup
	catalog    *catalog.Service
	quota      port.AccountQuota
	shortLinks port.ShortLinks
}

func New(playlists port.Playlists, members port.Members, tracks port.TrackLookup, catalog *catalog.Service, quota port.AccountQuota, shortLinks port.ShortLinks) *Service {
	return &Service{playlists: playlists, members: members, tracks: tracks, catalog: catalog, quota: quota, shortLinks: shortLinks}
}

func (s *Service) List(ctx context.Context, memberID string) ([]port.PlaylistRecord, error) {
	if err := s.registered(ctx, memberID); err != nil {
		return nil, err
	}
	return s.playlists.MemberPlaylists(ctx, memberID)
}

func (s *Service) Create(ctx context.Context, memberID, name string) (port.PlaylistRecord, error) {
	if err := s.registered(ctx, memberID); err != nil {
		return port.PlaylistRecord{}, err
	}
	name, err := validName(name)
	if err != nil {
		return port.PlaylistRecord{}, err
	}
	p, err := s.playlists.CreatePlaylist(ctx, memberID, name, maxPlaylists)
	if errors.Is(err, port.ErrPlaylistLimit) {
		return port.PlaylistRecord{}, ErrTooManyPlaylists
	}
	return p, err
}

func (s *Service) Rename(ctx context.Context, memberID, id, name string) error {
	if err := s.registered(ctx, memberID); err != nil {
		return err
	}
	name, err := validName(name)
	if err != nil {
		return err
	}
	return s.playlists.RenamePlaylist(ctx, memberID, id, name)
}

func (s *Service) Delete(ctx context.Context, memberID, id string) error {
	if err := s.registered(ctx, memberID); err != nil {
		return err
	}
	return s.playlists.DeletePlaylist(ctx, memberID, id)
}

func (s *Service) Items(ctx context.Context, memberID, id string) ([]port.PlaylistItem, error) {
	if err := s.registered(ctx, memberID); err != nil {
		return nil, err
	}
	return s.playlists.PlaylistItems(ctx, memberID, id)
}

// 链接曲目的 t.ID 必须已经按 track.LinkID 规范化。
func (s *Service) Add(ctx context.Context, memberID, id string, t track.Track) (port.PlaylistItem, error) {
	if err := s.registered(ctx, memberID); err != nil {
		return port.PlaylistItem{}, err
	}
	if err := s.take(ctx, "playlist:member:"+memberID, addBurst, addRefill); err != nil {
		return port.PlaylistItem{}, err
	}
	meta, err := s.meta(ctx, t)
	if err != nil {
		return port.PlaylistItem{}, err
	}
	item := port.PlaylistItem{Track: t, Meta: meta}
	err = s.playlists.AddPlaylistItem(ctx, memberID, id, item, maxItems)
	if errors.Is(err, port.ErrPlaylistLimit) {
		return port.PlaylistItem{}, ErrPlaylistFull
	}
	if err != nil {
		return port.PlaylistItem{}, err
	}
	return item, nil
}

func (s *Service) Remove(ctx context.Context, memberID, id string, tracks []track.Track) error {
	if err := s.registered(ctx, memberID); err != nil {
		return err
	}
	return s.playlists.RemovePlaylistItems(ctx, memberID, id, tracks)
}

func (s *Service) Containing(ctx context.Context, memberID string, t track.Track) ([]string, error) {
	if err := s.registered(ctx, memberID); err != nil {
		return nil, err
	}
	return s.playlists.TrackPlaylists(ctx, memberID, t)
}

func (s *Service) Copy(ctx context.Context, memberID, fromID, toID string, tracks []track.Track) (int, error) {
	if err := s.registered(ctx, memberID); err != nil {
		return 0, err
	}
	n, err := s.playlists.CopyPlaylistItems(ctx, memberID, fromID, toID, tracks, maxItems)
	if errors.Is(err, port.ErrPlaylistLimit) {
		return 0, ErrPlaylistFull
	}
	return n, err
}

func (s *Service) Move(ctx context.Context, memberID, id string, tracks []track.Track, before *track.Track) error {
	if err := s.registered(ctx, memberID); err != nil {
		return err
	}
	return s.playlists.MovePlaylistItems(ctx, memberID, id, tracks, before)
}

func (s *Service) registered(ctx context.Context, memberID string) error {
	profile, err := s.members.AccountProfile(ctx, memberID)
	if err != nil {
		return err
	}
	if profile.Username == "" {
		return ErrNotRegistered
	}
	return nil
}

func (s *Service) take(ctx context.Context, key string, burst int, refill time.Duration) error {
	wait, err := s.quota.Take(ctx, key, burst, refill)
	if err != nil {
		return err
	}
	if wait > 0 {
		return ErrTooFast
	}
	return nil
}

// 链接曲目准备好之前没有元数据，音源也查不到。
func (s *Service) meta(ctx context.Context, t track.Track) (track.Meta, error) {
	if t.Source != track.Link {
		return s.catalog.TrackMeta(ctx, t)
	}
	p, ok, err := s.tracks.PreparedTrack(ctx, t)
	if err != nil {
		return track.Meta{}, err
	}
	if !ok {
		return track.Meta{}, ErrTrackNotReady
	}
	return p.Meta, nil
}

func validName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if utf8.RuneCountInString(name) > maxNameLength || strings.ContainsFunc(name, unicode.IsControl) || !strings.ContainsFunc(name, visible) {
		return "", ErrInvalidName
	}
	return name, nil
}

// unicode.IsGraphic 把 Zs 类的空白也算作 graphic。
func visible(r rune) bool {
	return unicode.IsGraphic(r) && !unicode.IsSpace(r)
}
