package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/dihemoe/alisten/internal/adapter/out/postgres/internal/query"
	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/member"
)

var (
	_ port.Tickets         = (*Store)(nil)
	_ port.ExistingTickets = (*Store)(nil)
)

func (s *Store) CreateTicket(ctx context.Context, t port.NewTicket) error {
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		hash, err := q.LockRoomPassword(ctx, t.RoomID)
		if errors.Is(err, pgx.ErrNoRows) {
			return port.ErrRoomNotFound
		}
		if err != nil {
			return err
		}
		if hash != t.PasswordHash {
			return port.ErrPasswordChanged
		}
		return q.CreateTicket(ctx, query.CreateTicketParams{TokenHash: t.TokenHash, RoomID: t.RoomID, MemberID: t.MemberID, SessionTokenHash: t.SessionTokenHash})
	})
	if err != nil {
		return fmt.Errorf("签发房间 %s 的票据：%w", t.RoomID, sessionError(err))
	}
	return nil
}

func (s *Store) TicketHolder(ctx context.Context, tokenHash []byte) (port.TicketHolder, error) {
	row, err := s.q.TicketHolder(ctx, tokenHash)
	if errors.Is(err, pgx.ErrNoRows) {
		err = port.ErrTicketNotFound
	}
	if err != nil {
		return port.TicketHolder{}, fmt.Errorf("查找票据：%w", err)
	}
	return port.TicketHolder{
		RoomID:           row.RoomID,
		Member:           member.Member{ID: row.ID, Name: row.Name, AvatarHash: orEmpty(row.AvatarHash)},
		Bot:              row.Bot,
		SessionTokenHash: row.SessionTokenHash,
	}, nil
}

func (s *Store) ExistingTickets(ctx context.Context, tokenHashes [][]byte) ([][]byte, error) {
	existing, err := s.q.ExistingTickets(ctx, tokenHashes)
	if err != nil {
		return nil, fmt.Errorf("核对 %d 张票据：%w", len(tokenHashes), err)
	}
	return existing, nil
}

const foreignKeyViolation = "23503"

func sessionError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == foreignKeyViolation && pgErr.ConstraintName == "room_tickets_session_token_hash_fkey" {
		return port.ErrSessionNotFound
	}
	return err
}
