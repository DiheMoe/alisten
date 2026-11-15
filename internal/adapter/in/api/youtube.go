package api

import (
	"context"

	"github.com/dihemoe/alisten/protocol"
)

func (s *server) adminSetYoutubeCredential(ctx context.Context, _ call, req protocol.AdminSetYoutubeCredentialRequest) error {
	return s.Admin.SetYouTubeCookies(ctx, req.Cookies)
}

func (s *server) adminDeleteYoutubeCredential(ctx context.Context, _ call) error {
	return s.Admin.ClearYouTubeCookies(ctx)
}
