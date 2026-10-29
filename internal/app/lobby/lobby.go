package lobby

import (
	"context"
	"fmt"
	"time"

	"github.com/dihemoe/alisten/internal/app/identity"
	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/app/realtime"
	"github.com/dihemoe/alisten/internal/domain/room"
)

// 对外只表明房间是否有密码，不暴露密码哈希。
type Room struct {
	Info        room.Info
	Slug        string
	HasPassword bool
	Online      int
}

type NewRoom struct {
	Name        string
	Description string
	Password    string
}

const onlineTimeout = 5 * time.Second

type Service struct {
	rooms  port.RoomDirectory
	online port.OnlineCounts
}

func New(rooms port.RoomDirectory, online port.OnlineCounts) *Service {
	return &Service{rooms: rooms, online: online}
}

func (s *Service) ListRooms(ctx context.Context) ([]Room, error) {
	records, err := s.rooms.ListRooms(ctx)
	if err != nil {
		return nil, err
	}
	return s.listed(ctx, records)
}

// ref 必须是 UUID 或合法别名的形式：存储按形式区分两者，而且含有 U+0000 的文本会让 PostgreSQL 报错。
func (s *Service) FindRoom(ctx context.Context, ref string) (Room, error) {
	r, err := s.rooms.FindRoom(ctx, ref)
	if err != nil {
		return Room{}, err
	}
	rooms, err := s.listed(ctx, []port.RoomRecord{r})
	if err != nil {
		return Room{}, err
	}
	return rooms[0], nil
}

func (s *Service) CreateRoom(ctx context.Context, r NewRoom) (string, error) {
	hash, err := identity.HashPassword(ctx, r.Password)
	if err != nil {
		return "", err
	}
	return s.rooms.CreateRoom(ctx, port.NewRoom{
		Kind:         room.Ephemeral,
		Name:         r.Name,
		Description:  r.Description,
		PasswordHash: hash,
	})
}

// 在线人数读不出来时不能当作 0，整个请求按 UNAVAILABLE 失败。
func (s *Service) listed(ctx context.Context, records []port.RoomRecord) ([]Room, error) {
	ids := make([]string, len(records))
	for i, r := range records {
		ids[i] = r.Info.ID
	}
	ctx, cancel := context.WithTimeout(ctx, onlineTimeout)
	defer cancel()
	counts, err := s.online.OnlineCounts(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("%w：%w", realtime.ErrUnavailable, err)
	}
	rooms := make([]Room, len(records))
	for i, r := range records {
		rooms[i] = Room{Info: r.Info, Slug: r.Slug, HasPassword: r.PasswordHash != "", Online: counts[i]}
	}
	return rooms, nil
}
