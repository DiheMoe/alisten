package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/dihemoe/alisten/internal/adapter/out/postgres/internal/query"
	"github.com/dihemoe/alisten/internal/app/port"
)

var _ port.APIKeys = (*Store)(nil)

const botName = "bot"

func (s *Store) RoomAPIKeyStatus(ctx context.Context, roomID string) (port.APIKeyStatus, error) {
	createdAt, err := s.q.BotTicketCreatedAt(ctx, roomID)
	if errors.Is(err, pgx.ErrNoRows) {
		return port.APIKeyStatus{}, nil
	}
	if err != nil {
		return port.APIKeyStatus{}, fmt.Errorf("读取房间 %s 的 API Key 状态：%w", roomID, err)
	}
	return port.APIKeyStatus{Set: true, CreatedAt: createdAt}, nil
}

func (s *Store) ResetRoomAPIKey(ctx context.Context, roomID string, k port.NewAPIKey) error {
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		bot, err := q.LockRoomBot(ctx, roomID)
		if errors.Is(err, pgx.ErrNoRows) {
			return port.ErrRoomNotFound
		}
		if err != nil {
			return err
		}
		if bot != nil {
			if err := q.DeleteMemberTickets(ctx, query.DeleteMemberTicketsParams{RoomID: roomID, MemberID: *bot}); err != nil {
				return err
			}
		} else {
			m, err := q.CreateMember(ctx, query.CreateMemberParams{TokenHash: k.BotTokenHash, Name: botName})
			if err != nil {
				return err
			}
			if err := q.SetRoomBot(ctx, query.SetRoomBotParams{ID: roomID, BotMemberID: m.ID}); err != nil {
				return err
			}
			bot = &m.ID
		}
		return q.CreateTicket(ctx, query.CreateTicketParams{TokenHash: k.TokenHash, RoomID: roomID, MemberID: *bot})
	})
	if err != nil {
		return fmt.Errorf("重置房间 %s 的 API Key：%w", roomID, err)
	}
	return nil
}
