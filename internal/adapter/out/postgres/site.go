package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/dihemoe/alisten/internal/app/port"
)

var _ port.Site = (*Store)(nil)

func (s *Store) BrandName(ctx context.Context) (string, error) {
	name, err := s.q.BrandName(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("读取品牌名：%w", err)
	}
	return name, nil
}

func (s *Store) SetBrandName(ctx context.Context, name string) error {
	if err := s.q.SetBrandName(ctx, name); err != nil {
		return fmt.Errorf("保存品牌名：%w", err)
	}
	return nil
}
