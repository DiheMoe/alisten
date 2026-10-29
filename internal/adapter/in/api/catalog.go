package api

import (
	"context"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/track"
	"github.com/dihemoe/alisten/protocol"
)

func (s *server) answer(ctx context.Context, roomID string, data any) (resp any, handled bool, err error) {
	switch d := data.(type) {
	case protocol.SearchTracksRequest:
		songs, total, err := s.Catalog.SearchTracks(ctx, roomID, track.Source(d.Source), d.Keyword, d.Page, d.PageSize)
		if err != nil {
			return nil, true, err
		}
		return protocol.SearchTracksResponse{Items: summaries(songs), Total: total}, true, nil
	case protocol.SearchPlaylistsRequest:
		found, total, err := s.Catalog.SearchPlaylists(ctx, roomID, track.Source(d.Source), d.Keyword, d.Page, d.PageSize)
		if err != nil {
			return nil, true, err
		}
		return protocol.SearchPlaylistsResponse{Items: playlists(d.Source, found), Total: total}, true, nil
	case protocol.PlaylistTracksRequest:
		songs, err := s.Catalog.PlaylistTracks(ctx, roomID, track.Source(d.Source), d.PlaylistID)
		if err != nil {
			return nil, true, err
		}
		return protocol.PlaylistTracksResponse{Items: summaries(songs), Total: int64(len(songs))}, true, nil
	case protocol.RecommendListRequest:
		songs, err := s.Catalog.RecommendList(ctx, roomID)
		if err != nil {
			return nil, true, err
		}
		return protocol.RecommendListResponse{Items: summaries(songs)}, true, nil
	}
	return nil, false, nil
}

func summaries(songs []port.Song) []protocol.TrackSummary {
	items := make([]protocol.TrackSummary, len(songs))
	for i, s := range songs {
		items[i] = protocol.TrackSummary{Track: protocol.Track{Source: protocol.Source(s.Track.Source), ID: s.Track.ID}, Meta: meta(s.Meta)}
	}
	return items
}

func playlists(source protocol.Source, found []port.Playlist) []protocol.PlaylistSummary {
	items := make([]protocol.PlaylistSummary, len(found))
	for i, p := range found {
		items[i] = protocol.PlaylistSummary{
			Source:      source,
			ID:          p.ID,
			Name:        p.Name,
			CoverURL:    p.CoverURL,
			Creator:     p.Creator,
			Description: p.Description,
			TrackCount:  p.TrackCount,
			PlayCount:   p.PlayCount,
		}
	}
	return items
}
