package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/dihemoe/alisten/internal/app/port"
)

var _ port.Site = (*Store)(nil)

func (s *Store) Settings(ctx context.Context) (port.SiteSettings, error) {
	row, err := s.q.SiteSettings(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return port.SiteSettings{RegistrationOpen: true}, nil
	}
	if err != nil {
		return port.SiteSettings{}, fmt.Errorf("读取站点设置：%w", err)
	}
	return port.SiteSettings{BrandName: orEmpty(row.BrandName), RegistrationOpen: row.RegistrationOpen}, nil
}

func (s *Store) SetBrandName(ctx context.Context, name string) error {
	if err := s.q.SetBrandName(ctx, &name); err != nil {
		return fmt.Errorf("保存品牌名：%w", err)
	}
	return nil
}

func (s *Store) SetRegistrationOpen(ctx context.Context, open bool) error {
	if err := s.q.SetRegistrationOpen(ctx, open); err != nil {
		return fmt.Errorf("保存注册开关：%w", err)
	}
	return nil
}
