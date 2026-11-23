package api

import (
	"context"

	"github.com/dihemoe/alisten/protocol"
)

func (s *server) adminSetYouTubeCredential(ctx context.Context, _ call, req protocol.AdminSetYouTubeCredentialRequest) error {
	return s.Admin.SetYouTubeCookies(ctx, req.Cookies)
}

func (s *server) adminDeleteYouTubeCredential(ctx context.Context, _ call) error {
	return s.Admin.ClearYouTubeCredential(ctx)
}
