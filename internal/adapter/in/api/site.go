package api

import (
	"context"

	"github.com/dihemoe/alisten/protocol"
)

func (s *server) getSite(ctx context.Context, _ call) (any, error) {
	name, err := s.Site.BrandName(ctx)
	if err != nil {
		return nil, err
	}
	return protocol.GetSiteResponse{BrandName: name}, nil
}

func (s *server) adminUpdateSite(ctx context.Context, c call) (any, error) {
	req, err := protocol.DecodeAdminUpdateSiteRequest(c.body)
	if err != nil {
		return nil, err
	}
	name, err := s.Site.SetBrandName(ctx, req.BrandName)
	if err != nil {
		return nil, err
	}
	return protocol.AdminUpdateSiteResponse{BrandName: name}, nil
}
