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
	defaultBrandName             = "Alisten"
	maxBrandNameLength           = 20
	defaultEphemeralRoomCapacity = 16
)

var ErrInvalidBrandName = errors.New("brandName：去掉首尾空白后应有 1 到 20 个字符，其中至少一个看得见，不能包含控制字符")

type Service struct {
	site port.Site
}

func New(site port.Site) *Service {
	return &Service{site: site}
}

type Settings struct {
	BrandName             string
	RegistrationOpen      bool
	EphemeralRoomCapacity int
}

func (s *Service) Settings(ctx context.Context) (Settings, error) {
	settings, err := s.site.Settings(ctx)
	if err != nil {
		return Settings{}, err
	}
	name := settings.BrandName
	if name == "" {
		name = defaultBrandName
	}
	capacity := settings.EphemeralRoomCapacity
	if capacity == 0 {
		capacity = defaultEphemeralRoomCapacity
	}
	return Settings{BrandName: name, RegistrationOpen: settings.RegistrationOpen, EphemeralRoomCapacity: capacity}, nil
}

func (s *Service) SetRegistrationOpen(ctx context.Context, open bool) error {
	return s.site.SetRegistrationOpen(ctx, open)
}

// 只影响之后创建的临时房间，已经创建的临时房间保持原来的上限。
func (s *Service) SetEphemeralRoomCapacity(ctx context.Context, capacity int) error {
	return s.site.SetEphemeralRoomCapacity(ctx, capacity)
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
