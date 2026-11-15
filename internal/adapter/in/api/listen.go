package api

import (
	"context"

	"github.com/dihemoe/alisten/protocol"
)

func (s *server) listMyListens(ctx context.Context, c call) (protocol.ListMyListensResponse, error) {
	listens, err := s.Listens.List(ctx, c.caller.Member.ID)
	if err != nil {
		return protocol.ListMyListensResponse{}, err
	}
	items := make([]protocol.MyListen, len(listens))
	for i, l := range listens {
		items[i] = protocol.MyListen{Track: trackDTO(l.Track), Meta: s.trackMeta(l.Meta), HeardAtMs: l.HeardAt.UnixMilli()}
	}
	return protocol.ListMyListensResponse{Items: items}, nil
}

func (s *server) removeMyListen(ctx context.Context, c call, req protocol.RemoveMyListenRequest) error {
	return s.Listens.Remove(ctx, c.caller.Member.ID, trackOf(req.Track))
}

func (s *server) clearMyListens(ctx context.Context, c call) error {
	return s.Listens.Clear(ctx, c.caller.Member.ID)
}

func (s *server) enableListenRecording(ctx context.Context, c call) error {
	return s.Listens.SetRecording(ctx, c.caller.Member.ID, true)
}

func (s *server) disableListenRecording(ctx context.Context, c call) error {
	return s.Listens.SetRecording(ctx, c.caller.Member.ID, false)
}
