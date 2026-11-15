package api

import (
	"context"
	"time"

	"github.com/dihemoe/alisten/internal/app/playlist"
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
		ShareID:     optional(p.ShareID),
	}
}

func (s *server) playlistItemDTO(item port.PlaylistItem) protocol.MyPlaylistItem {
	return protocol.MyPlaylistItem{
		Track: trackDTO(item.Track),
		Meta:  s.trackMeta(item.Meta),
	}
}

func (s *server) listMyPlaylists(ctx context.Context, c call) (protocol.ListMyPlaylistsResponse, error) {
	lists, err := s.Playlists.List(ctx, c.owner)
	if err != nil {
		return protocol.ListMyPlaylistsResponse{}, err
	}
	items := make([]protocol.MyPlaylist, len(lists))
	for i, p := range lists {
		items[i] = s.playlistDTO(p)
	}
	return protocol.ListMyPlaylistsResponse{Items: items}, nil
}

func (s *server) createMyPlaylist(ctx context.Context, c call, req protocol.CreateMyPlaylistRequest) (protocol.MyPlaylist, error) {
	p, err := s.Playlists.Create(ctx, c.owner, req.Name)
	if err != nil {
		return protocol.MyPlaylist{}, err
	}
	return s.playlistDTO(p), nil
}

func (s *server) renameMyPlaylist(ctx context.Context, c call, req protocol.RenameMyPlaylistRequest) error {
	return s.Playlists.Rename(ctx, c.owner, c.r.PathValue("id"), req.Name)
}

func (s *server) deleteMyPlaylist(ctx context.Context, c call) error {
	return s.Playlists.Delete(ctx, c.owner, c.r.PathValue("id"))
}

func (s *server) listMyPlaylistItems(ctx context.Context, c call) (protocol.ListMyPlaylistItemsResponse, error) {
	list, err := s.Playlists.Items(ctx, c.owner, c.r.PathValue("id"))
	if err != nil {
		return protocol.ListMyPlaylistItemsResponse{}, err
	}
	items := make([]protocol.MyPlaylistItem, len(list))
	for i, item := range list {
		items[i] = s.playlistItemDTO(item)
	}
	return protocol.ListMyPlaylistItemsResponse{Items: items}, nil
}

func (s *server) addMyPlaylistItem(ctx context.Context, c call, req protocol.AddMyPlaylistItemRequest) (protocol.AddMyPlaylistItemResponse, error) {
	item, err := s.Playlists.Add(ctx, c.owner, c.r.PathValue("id"), trackOf(req.Track))
	if err != nil {
		return protocol.AddMyPlaylistItemResponse{}, err
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

func (s *server) removeMyPlaylistItems(ctx context.Context, c call, req protocol.RemoveMyPlaylistItemsRequest) error {
	return s.Playlists.Remove(ctx, c.owner, c.r.PathValue("id"), tracksOf(req.Tracks))
}

func (s *server) listMyTrackPlaylists(ctx context.Context, c call, req protocol.ListMyTrackPlaylistsRequest) (protocol.ListMyTrackPlaylistsResponse, error) {
	ids, err := s.Playlists.Containing(ctx, c.owner, trackOf(req.Track))
	if err != nil {
		return protocol.ListMyTrackPlaylistsResponse{}, err
	}
	return protocol.ListMyTrackPlaylistsResponse{PlaylistIds: ids}, nil
}

func (s *server) copyMyPlaylistItems(ctx context.Context, c call, req protocol.CopyMyPlaylistItemsRequest) (protocol.CopyMyPlaylistItemsResponse, error) {
	n, err := s.Playlists.Copy(ctx, c.owner, req.From, c.r.PathValue("id"), tracksOf(req.Tracks))
	if err != nil {
		return protocol.CopyMyPlaylistItemsResponse{}, err
	}
	return protocol.CopyMyPlaylistItemsResponse{Added: int64(n)}, nil
}

func (s *server) moveMyPlaylistItems(ctx context.Context, c call, req protocol.MoveMyPlaylistItemsRequest) error {
	var before *track.Track
	if req.Before != nil {
		t := trackOf(*req.Before)
		before = &t
	}
	return s.Playlists.Move(ctx, c.owner, c.r.PathValue("id"), tracksOf(req.Tracks), before)
}

func (s *server) importMyPlaylist(ctx context.Context, c call, req protocol.ImportMyPlaylistRequest) (protocol.ImportMyPlaylistResponse, error) {
	tracks := make([]playlist.ImportedTrack, len(req.Tracks))
	for i, t := range req.Tracks {
		tracks[i] = playlist.ImportedTrack{
			Track:    trackOf(t.Track),
			Title:    t.Title,
			Artist:   deref(t.Artist),
			Album:    deref(t.Album),
			CoverURL: deref(t.CoverURL),
			PageURL:  deref(t.PageURL),
			Duration: time.Duration(t.DurationMs) * time.Millisecond,
		}
	}
	p, n, err := s.Playlists.ImportFile(ctx, c.owner, req.Name, tracks)
	if err != nil {
		return protocol.ImportMyPlaylistResponse{}, err
	}
	return protocol.ImportMyPlaylistResponse{Playlist: s.playlistDTO(p), Imported: int64(n)}, nil
}

func (s *server) resolveMyPlaylistLink(ctx context.Context, c call, req protocol.ResolveMyPlaylistLinkRequest) (protocol.ResolveMyPlaylistLinkResponse, error) {
	source, id, err := s.Playlists.ResolveLink(ctx, c.owner, req.Link)
	if err != nil {
		return protocol.ResolveMyPlaylistLinkResponse{}, err
	}
	return protocol.ResolveMyPlaylistLinkResponse{Source: protocol.Source(source), PlaylistID: id}, nil
}

func (s *server) importMyPlatformPlaylist(ctx context.Context, c call, req protocol.ImportMyPlatformPlaylistRequest) (protocol.ImportMyPlatformPlaylistResponse, error) {
	p, n, err := s.Playlists.ImportPlatform(ctx, c.owner, req.Name, track.Source(req.Source), req.PlaylistID)
	if err != nil {
		return protocol.ImportMyPlatformPlaylistResponse{}, err
	}
	return protocol.ImportMyPlatformPlaylistResponse{Playlist: s.playlistDTO(p), Imported: int64(n)}, nil
}

func (s *server) shareMyPlaylist(ctx context.Context, c call) (protocol.ShareMyPlaylistResponse, error) {
	shareID, err := s.Playlists.Share(ctx, c.owner, c.r.PathValue("id"))
	if err != nil {
		return protocol.ShareMyPlaylistResponse{}, err
	}
	return protocol.ShareMyPlaylistResponse{ShareID: shareID}, nil
}

func (s *server) unshareMyPlaylist(ctx context.Context, c call) error {
	return s.Playlists.Unshare(ctx, c.owner, c.r.PathValue("id"))
}

func (s *server) getSharedPlaylist(ctx context.Context, c call) (protocol.SharedPlaylist, error) {
	p, err := s.Playlists.Shared(ctx, c.r.PathValue("shareId"))
	if err != nil {
		return protocol.SharedPlaylist{}, err
	}
	items := make([]protocol.MyPlaylistItem, len(p.Items))
	for i, item := range p.Items {
		items[i] = s.playlistItemDTO(item)
	}
	return protocol.SharedPlaylist{
		Name:            p.Name,
		Favorites:       p.Favorites,
		OwnerName:       p.OwnerName,
		OwnerAvatarHash: p.OwnerAvatarHash,
		Items:           items,
	}, nil
}

func (s *server) copySharedPlaylist(ctx context.Context, c call, req protocol.CopySharedPlaylistRequest) (protocol.MyPlaylist, error) {
	p, err := s.Playlists.CopyShared(ctx, c.owner, c.r.PathValue("shareId"), req.Name)
	if err != nil {
		return protocol.MyPlaylist{}, err
	}
	return s.playlistDTO(p), nil
}
