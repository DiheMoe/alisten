package api

import (
	"context"

	"github.com/dihemoe/alisten/protocol"
)

func (s *server) getSite(ctx context.Context, _ call) (protocol.GetSiteResponse, error) {
	settings, err := s.Site.Settings(ctx)
	if err != nil {
		return protocol.GetSiteResponse{}, err
	}
	githubClientID, err := s.GitHub.ClientID(ctx)
	if err != nil {
		return protocol.GetSiteResponse{}, err
	}
	return protocol.GetSiteResponse{BrandName: settings.BrandName, RegistrationOpen: settings.RegistrationOpen, GithubLogin: githubClientID != ""}, nil
}

func (s *server) adminUpdateSite(ctx context.Context, _ call, req protocol.AdminUpdateSiteRequest) (protocol.AdminUpdateSiteResponse, error) {
	name, err := s.Site.SetBrandName(ctx, req.BrandName)
	if err != nil {
		return protocol.AdminUpdateSiteResponse{}, err
	}
	return protocol.AdminUpdateSiteResponse{BrandName: name}, nil
}

func (s *server) adminOpenRegistration(ctx context.Context, _ call) error {
	return s.Site.SetRegistrationOpen(ctx, true)
}

func (s *server) adminCloseRegistration(ctx context.Context, _ call) error {
	return s.Site.SetRegistrationOpen(ctx, false)
}
