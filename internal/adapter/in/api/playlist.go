package api

import (
	"context"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/track"
	"github.com/dihemoe/alisten/protocol"
)

func (s *server) playlistDTO(p port.PlaylistRecord) protocol.MyPlaylist {
	var cover *string
	if p.Cover != nil {
		url := s.coverURL(p.Cover.URL, p.Cover.Key)
		cover = &url
	}
	return protocol.MyPlaylist{
		ID:          p.ID,
		Name:        p.Name,
		Favorites:   p.Favorites,
		TrackCount:  int64(p.TrackCount),
		CreatedAtMs: p.CreatedAt.UnixMilli(),
		UpdatedAtMs: p.UpdatedAt.UnixMilli(),
		CoverURL:    cover,
	}
}

func (s *server) playlistItemDTO(item port.PlaylistItem) protocol.MyPlaylistItem {
	return protocol.MyPlaylistItem{
		Track: trackDTO(item.Track),
		Meta:  s.trackMeta(item.Meta),
	}
}

func (s *server) listMyPlaylists(ctx context.Context, c call) (any, error) {
	lists, err := s.Playlists.List(ctx, c.member.ID)
	if err != nil {
		return nil, err
	}
	items := make([]protocol.MyPlaylist, len(lists))
	for i, p := range lists {
		items[i] = s.playlistDTO(p)
	}
	return protocol.ListMyPlaylistsResponse{Items: items}, nil
}

func (s *server) createMyPlaylist(ctx context.Context, c call) (any, error) {
	req, err := protocol.DecodeCreateMyPlaylistRequest(c.body)
	if err != nil {
		return nil, err
	}
	p, err := s.Playlists.Create(ctx, c.member.ID, req.Name)
	if err != nil {
		return nil, err
	}
	return s.playlistDTO(p), nil
}

func (s *server) renameMyPlaylist(ctx context.Context, c call) (any, error) {
	req, err := protocol.DecodeRenameMyPlaylistRequest(c.body)
	if err != nil {
		return nil, err
	}
	return nil, s.Playlists.Rename(ctx, c.member.ID, c.r.PathValue("id"), req.Name)
}

func (s *server) deleteMyPlaylist(ctx context.Context, c call) (any, error) {
	return nil, s.Playlists.Delete(ctx, c.member.ID, c.r.PathValue("id"))
}

func (s *server) listMyPlaylistItems(ctx context.Context, c call) (any, error) {
	list, err := s.Playlists.Items(ctx, c.member.ID, c.r.PathValue("id"))
	if err != nil {
		return nil, err
	}
	items := make([]protocol.MyPlaylistItem, len(list))
	for i, item := range list {
		items[i] = s.playlistItemDTO(item)
	}
	return protocol.ListMyPlaylistItemsResponse{Items: items}, nil
}

func (s *server) addMyPlaylistItem(ctx context.Context, c call) (any, error) {
	req, err := protocol.DecodeAddMyPlaylistItemRequest(c.body)
	if err != nil {
		return nil, err
	}
	item, err := s.Playlists.Add(ctx, c.member.ID, c.r.PathValue("id"), trackOf(req.Track))
	if err != nil {
		return nil, err
	}
	return protocol.AddMyPlaylistItemResponse{Item: s.playlistItemDTO(item)}, nil
}

func tracksOf(in []protocol.Track) []track.Track {
	out := make([]track.Track, len(in))
	for i, t := range in {
		out[i] = trackOf(t)
	}
	return out
}

func (s *server) removeMyPlaylistItems(ctx context.Context, c call) (any, error) {
	req, err := protocol.DecodeRemoveMyPlaylistItemsRequest(c.body)
	if err != nil {
		return nil, err
	}
	return nil, s.Playlists.Remove(ctx, c.member.ID, c.r.PathValue("id"), tracksOf(req.Tracks))
}

func (s *server) listMyTrackPlaylists(ctx context.Context, c call) (any, error) {
	req, err := protocol.DecodeListMyTrackPlaylistsRequest(c.body)
	if err != nil {
		return nil, err
	}
	ids, err := s.Playlists.Containing(ctx, c.member.ID, trackOf(req.Track))
	if err != nil {
		return nil, err
	}
	return protocol.ListMyTrackPlaylistsResponse{PlaylistIds: ids}, nil
}

func (s *server) copyMyPlaylistItems(ctx context.Context, c call) (any, error) {
	req, err := protocol.DecodeCopyMyPlaylistItemsRequest(c.body)
	if err != nil {
		return nil, err
	}
	n, err := s.Playlists.Copy(ctx, c.member.ID, req.From, c.r.PathValue("id"), tracksOf(req.Tracks))
	if err != nil {
		return nil, err
	}
	return protocol.CopyMyPlaylistItemsResponse{Added: int64(n)}, nil
}

func (s *server) moveMyPlaylistItems(ctx context.Context, c call) (any, error) {
	req, err := protocol.DecodeMoveMyPlaylistItemsRequest(c.body)
	if err != nil {
		return nil, err
	}
	var before *track.Track
	if req.Before != nil {
		t := trackOf(*req.Before)
		before = &t
	}
	return nil, s.Playlists.Move(ctx, c.member.ID, c.r.PathValue("id"), tracksOf(req.Tracks), before)
}
