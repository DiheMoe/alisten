package site

import (
	"context"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/dihemoe/alisten/internal/app/port"
)

const (
	defaultBrandName   = "Alisten"
	maxBrandNameLength = 20
)

var ErrInvalidBrandName = errors.New("brandName：去掉首尾空白后应有 1 到 20 个字符，其中至少一个看得见，不能包含控制字符")

type Service struct {
	site port.Site
}

func New(site port.Site) *Service {
	return &Service{site: site}
}

func (s *Service) BrandName(ctx context.Context) (string, error) {
	name, err := s.site.BrandName(ctx)
	if err != nil {
		return "", err
	}
	if name == "" {
		return defaultBrandName, nil
	}
	return name, nil
}

func (s *Service) SetBrandName(ctx context.Context, name string) (string, error) {
	name = strings.TrimSpace(name)
	if utf8.RuneCountInString(name) > maxBrandNameLength || strings.ContainsFunc(name, unicode.IsControl) || !strings.ContainsFunc(name, visible) {
		return "", ErrInvalidBrandName
	}
	if err := s.site.SetBrandName(ctx, name); err != nil {
		return "", err
	}
	return name, nil
}

// unicode.IsGraphic 把 Zs 类的空白也算作 graphic。
func visible(r rune) bool {
	return unicode.IsGraphic(r) && !unicode.IsSpace(r)
}
