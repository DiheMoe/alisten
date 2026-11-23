package playlist

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/dihemoe/alisten/internal/app/catalog"
	"github.com/dihemoe/alisten/internal/app/identity"
	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/track"
)

const (
	maxPlaylists  = 50
	maxItems      = 1000
	maxNameLength = 50
	// 收藏可能要向音乐平台查询元数据。这份额度防止批量请求让共用的音乐平台账号被平台限流。
	addBurst  = 30
	addRefill = 2 * time.Second
)

var (
	ErrInvalidName      = errors.New("name：去掉首尾空白后应有 1 到 50 个字符，其中至少一个看得见，不能包含控制字符")
	ErrTooManyPlaylists = errors.New("最多只能建 50 个歌单")
	ErrPlaylistFull     = errors.New("一个歌单最多 1000 首歌")
	ErrTrackNotReady    = errors.New("这首歌还在准备，准备好之后再保存")
	ErrTooFast          = errors.New("操作太频繁，请稍后再试。")
)

type Service struct {
	playlists  port.Playlists
	tracks     port.TrackLookup
	catalog    *catalog.Service
	quota      port.Quota
	shortLinks port.ShortLinks
}

func New(playlists port.Playlists, tracks port.TrackLookup, catalog *catalog.Service, quota port.Quota, shortLinks port.ShortLinks) *Service {
	return &Service{playlists: playlists, tracks: tracks, catalog: catalog, quota: quota, shortLinks: shortLinks}
}

func (s *Service) List(ctx context.Context, owner identity.AccountOwner) ([]port.PlaylistRecord, error) {
	return s.playlists.MemberPlaylists(ctx, owner.MemberID())
}

func (s *Service) Create(ctx context.Context, owner identity.AccountOwner, name string) (port.PlaylistRecord, error) {
	name, err := validName(name)
	if err != nil {
		return port.PlaylistRecord{}, err
	}
	p, err := s.playlists.CreatePlaylist(ctx, owner.MemberID(), name, maxPlaylists)
	if errors.Is(err, port.ErrPlaylistLimit) {
		return port.PlaylistRecord{}, ErrTooManyPlaylists
	}
	return p, err
}

func (s *Service) Rename(ctx context.Context, owner identity.AccountOwner, id, name string) error {
	name, err := validName(name)
	if err != nil {
		return err
	}
	return s.playlists.RenamePlaylist(ctx, owner.MemberID(), id, name)
}

func (s *Service) Delete(ctx context.Context, owner identity.AccountOwner, id string) error {
	return s.playlists.DeletePlaylist(ctx, owner.MemberID(), id)
}

func (s *Service) Items(ctx context.Context, owner identity.AccountOwner, id string) ([]port.PlaylistItem, error) {
	return s.playlists.PlaylistItems(ctx, owner.MemberID(), id)
}

// 链接曲目的 t.ID 必须已经按 track.LinkID 规范化。
func (s *Service) Add(ctx context.Context, owner identity.AccountOwner, id string, t track.Track) (port.PlaylistItem, error) {
	if err := s.take(ctx, "playlist:member:"+owner.MemberID(), addBurst, addRefill); err != nil {
		return port.PlaylistItem{}, err
	}
	meta, err := s.meta(ctx, t)
	if err != nil {
		return port.PlaylistItem{}, err
	}
	item := port.PlaylistItem{Track: t, Meta: meta}
	err = s.playlists.AddPlaylistItem(ctx, owner.MemberID(), id, item, maxItems)
	if errors.Is(err, port.ErrPlaylistLimit) {
		return port.PlaylistItem{}, ErrPlaylistFull
	}
	if err != nil {
		return port.PlaylistItem{}, err
	}
	return item, nil
}

func (s *Service) Remove(ctx context.Context, owner identity.AccountOwner, id string, tracks []track.Track) error {
	return s.playlists.RemovePlaylistItems(ctx, owner.MemberID(), id, tracks)
}

func (s *Service) Containing(ctx context.Context, owner identity.AccountOwner, t track.Track) ([]string, error) {
	return s.playlists.TrackPlaylists(ctx, owner.MemberID(), t)
}

func (s *Service) Copy(ctx context.Context, owner identity.AccountOwner, fromID, toID string, tracks []track.Track) (int, error) {
	n, err := s.playlists.CopyPlaylistItems(ctx, owner.MemberID(), fromID, toID, tracks, maxItems)
	if errors.Is(err, port.ErrPlaylistLimit) {
		return 0, ErrPlaylistFull
	}
	return n, err
}

func (s *Service) Move(ctx context.Context, owner identity.AccountOwner, id string, tracks []track.Track, before *track.Track) error {
	return s.playlists.MovePlaylistItems(ctx, owner.MemberID(), id, tracks, before)
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

// 链接曲目准备好之前没有元数据，音乐平台也查不到。
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
