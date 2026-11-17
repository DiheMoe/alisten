package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/dihemoe/alisten/internal/adapter/out/postgres/internal/query"
	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/room"
)

var _ port.RoomDirectory = (*Store)(nil)

func (s *Store) CreateRoom(ctx context.Context, r port.NewRoom) (string, error) {
	id, err := s.q.CreateRoom(ctx, query.CreateRoomParams{
		Kind:         kindName(r.Kind),
		Slug:         r.Slug,
		Name:         r.Name,
		Description:  r.Description,
		PasswordHash: r.PasswordHash,
		Capacity:     int32(r.Capacity),
	})
	if err != nil {
		return "", fmt.Errorf("创建房间：%w", slugError(err))
	}
	return id, nil
}

func (s *Store) ListRooms(ctx context.Context) ([]port.RoomRecord, error) {
	rows, err := s.q.ListRooms(ctx)
	if err != nil {
		return nil, fmt.Errorf("列出房间：%w", err)
	}
	records := make([]port.RoomRecord, len(rows))
	for i, row := range rows {
		records[i] = roomRecord(row)
	}
	return records, nil
}

func (s *Store) FindRoom(ctx context.Context, ref string) (port.RoomRecord, error) {
	var row query.Room
	var err error
	// 别名最长 32 个字符，不会是 UUID 的形式。
	if isUUID(ref) {
		row, err = s.q.RoomByID(ctx, ref)
	} else {
		row, err = s.q.RoomBySlug(ctx, ref)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		err = port.ErrRoomNotFound
	}
	if err != nil {
		return port.RoomRecord{}, fmt.Errorf("查找房间 %s：%w", ref, err)
	}
	return roomRecord(row), nil
}

func (s *Store) SetRoomSlug(ctx context.Context, id, slug string) error {
	if err := affected(s.q.SetRoomSlug(ctx, query.SetRoomSlugParams{ID: id, Slug: slug})); err != nil {
		return fmt.Errorf("修改房间 %s：%w", id, slugError(err))
	}
	return nil
}

func (s *Store) SetRoomPassword(ctx context.Context, id, passwordHash string) error {
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if err := affected(q.SetRoomPassword(ctx, query.SetRoomPasswordParams{ID: id, PasswordHash: passwordHash})); err != nil {
			return err
		}
		// 取消密码时，已经签发的票据保持有效。
		if passwordHash == "" {
			return nil
		}
		return q.DeleteRoomTicketsExceptBot(ctx, id)
	})
	if err != nil {
		return fmt.Errorf("修改房间 %s：%w", id, err)
	}
	return nil
}

func affected(rows int64, err error) error {
	if err == nil && rows == 0 {
		return port.ErrRoomNotFound
	}
	return err
}

func slugError(err error) error {
	if violates(err, uniqueViolation, "rooms_slug_key") {
		return port.ErrSlugTaken
	}
	return err
}

func roomRecord(row query.Room) port.RoomRecord {
	return port.RoomRecord{Info: roomInfo(row), Slug: orEmpty(row.Slug), PasswordHash: orEmpty(row.PasswordHash)}
}

func roomInfo(row query.Room) room.Info {
	return room.Info{ID: row.ID, Name: row.Name, Description: row.Description, Kind: kindOf(row.Kind), Capacity: int(row.Capacity)}
}

func kindName(k room.Kind) string {
	if k == room.Persistent {
		return "persistent"
	}
	return "ephemeral"
}

func kindOf(name string) room.Kind {
	if name == "persistent" {
		return room.Persistent
	}
	return room.Ephemeral
}
