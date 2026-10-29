package api

import (
	"context"

	"github.com/dihemoe/alisten/protocol"
)

func (s *server) adminSetYoutubeCredential(ctx context.Context, c call) (any, error) {
	req, err := protocol.DecodeAdminSetYoutubeCredentialRequest(c.body)
	if err != nil {
		return nil, err
	}
	return nil, s.Admin.SetYouTubeCookies(ctx, req.Cookies)
}

func (s *server) adminDeleteYoutubeCredential(ctx context.Context, _ call) (any, error) {
	return nil, s.Admin.ClearYouTubeCookies(ctx)
}
