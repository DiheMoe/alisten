package api

import (
	"context"

	"github.com/dihemoe/alisten/protocol"
)

func (s *server) listMyListens(ctx context.Context, c call) (any, error) {
	listens, err := s.Listens.List(ctx, c.member.ID)
	if err != nil {
		return nil, err
	}
	items := make([]protocol.MyListen, len(listens))
	for i, l := range listens {
		items[i] = protocol.MyListen{Track: trackDTO(l.Track), Meta: s.trackMeta(l.Meta), HeardAtMs: l.HeardAt.UnixMilli()}
	}
	return protocol.ListMyListensResponse{Items: items}, nil
}

func (s *server) removeMyListen(ctx context.Context, c call) (any, error) {
	req, err := protocol.DecodeRemoveMyListenRequest(c.body)
	if err != nil {
		return nil, err
	}
	return nil, s.Listens.Remove(ctx, c.member.ID, trackOf(req.Track))
}

func (s *server) clearMyListens(ctx context.Context, c call) (any, error) {
	return nil, s.Listens.Clear(ctx, c.member.ID)
}

func (s *server) enableListenRecording(ctx context.Context, c call) (any, error) {
	if _, err := protocol.DecodeEnableListenRecordingRequest(c.body); err != nil {
		return nil, err
	}
	return nil, s.Listens.SetRecording(ctx, c.member.ID, true)
}

func (s *server) disableListenRecording(ctx context.Context, c call) (any, error) {
	return nil, s.Listens.SetRecording(ctx, c.member.ID, false)
}
