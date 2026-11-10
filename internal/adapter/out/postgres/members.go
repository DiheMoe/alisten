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

var _ port.Members = (*Store)(nil)

func (s *Store) CreateMember(ctx context.Context, tokenHash []byte, name string) (member.Member, error) {
	var m member.Member
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		row, err := q.CreateMember(ctx, name)
		if err != nil {
			return err
		}
		m = memberOf(row)
		return q.CreateSession(ctx, query.CreateSessionParams{TokenHash: tokenHash, MemberID: row.ID})
	})
	if err != nil {
		return member.Member{}, fmt.Errorf("创建成员：%w", err)
	}
	return m, nil
}

func (s *Store) MemberByToken(ctx context.Context, tokenHash []byte) (member.Member, error) {
	row, err := s.q.MemberByToken(ctx, tokenHash)
	if errors.Is(err, pgx.ErrNoRows) {
		err = port.ErrMemberNotFound
	}
	if err != nil {
		return member.Member{}, fmt.Errorf("按 token 查找成员：%w", err)
	}
	return memberOf(row), nil
}

func (s *Store) UpdateMember(ctx context.Context, id string, name, avatarHash, email *string) (member.Member, error) {
	row, err := s.q.UpdateMember(ctx, query.UpdateMemberParams{ID: id, Name: name, AvatarHash: avatarHash, Email: email})
	if errors.Is(err, pgx.ErrNoRows) {
		err = port.ErrMemberNotFound
	}
	if err != nil {
		return member.Member{}, fmt.Errorf("修改成员 %s：%w", id, err)
	}
	return memberOf(row), nil
}

func memberOf(row query.Member) member.Member {
	return member.Member{ID: row.ID, Name: row.Name, AvatarHash: orEmpty(row.AvatarHash)}
}

func (s *Store) AccountProfile(ctx context.Context, memberID string) (port.AccountProfile, error) {
	row, err := s.q.MemberAccountProfile(ctx, memberID)
	if errors.Is(err, pgx.ErrNoRows) {
		err = port.ErrMemberNotFound
	}
	if err != nil {
		return port.AccountProfile{}, fmt.Errorf("读取成员 %s 的账号资料：%w", memberID, err)
	}
	return port.AccountProfile{Username: orEmpty(row.Username), Email: orEmpty(row.Email)}, nil
}

func (s *Store) Register(ctx context.Context, memberID, username, passwordHash string) error {
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		n, err := q.RegisterMember(ctx, query.RegisterMemberParams{ID: memberID, Username: &username, PasswordHash: &passwordHash})
		if err != nil {
			return usernameError(err)
		}
		if n == 0 {
			return port.ErrAlreadyRegistered
		}
		return q.CreateFavorites(ctx, memberID)
	})
	if err != nil {
		return fmt.Errorf("注册成员 %s：%w", memberID, err)
	}
	return nil
}

func usernameError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation && pgErr.ConstraintName == "members_username_key" {
		return port.ErrUsernameTaken
	}
	return err
}

func (s *Store) AccountByUsername(ctx context.Context, username string) (port.Account, error) {
	row, err := s.q.AccountByUsername(ctx, &username)
	if errors.Is(err, pgx.ErrNoRows) {
		err = port.ErrMemberNotFound
	}
	if err != nil {
		return port.Account{}, fmt.Errorf("按用户名查找成员：%w", err)
	}
	return port.Account{Member: memberOf(row), Username: orEmpty(row.Username), Email: orEmpty(row.Email), PasswordHash: orEmpty(row.PasswordHash)}, nil
}

func (s *Store) ReplaceSession(ctx context.Context, oldTokenHash []byte, memberID string, newTokenHash []byte, passwordHash string) error {
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		current, err := q.LockMemberPassword(ctx, memberID)
		if err != nil {
			return err
		}
		if current != passwordHash {
			return port.ErrAccountPasswordChanged
		}
		previous, err := q.TakeSession(ctx, oldTokenHash)
		if errors.Is(err, pgx.ErrNoRows) {
			return port.ErrSessionNotFound
		}
		if err != nil {
			return err
		}
		if err := q.CreateSession(ctx, query.CreateSessionParams{TokenHash: newTokenHash, MemberID: memberID}); err != nil {
			return err
		}
		if previous == memberID {
			return nil
		}
		return moveListens(ctx, q, previous, memberID)
	})
	if err != nil {
		return fmt.Errorf("给成员 %s 更换会话：%w", memberID, err)
	}
	return nil
}

func (s *Store) DeleteSession(ctx context.Context, tokenHash []byte) error {
	if _, err := s.q.DeleteSession(ctx, tokenHash); err != nil {
		return fmt.Errorf("删除会话：%w", err)
	}
	return nil
}

func (s *Store) SetPassword(ctx context.Context, memberID, passwordHash string) ([][]byte, error) {
	var deleted [][]byte
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		deleted, err = setPassword(ctx, s.q.WithTx(tx), memberID, passwordHash, nil)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("修改成员 %s 的密码：%w", memberID, err)
	}
	return deleted, nil
}

func (s *Store) ChangePassword(ctx context.Context, memberID, currentPasswordHash, passwordHash string, keepTokenHash []byte) ([][]byte, error) {
	var deleted [][]byte
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		// 先锁住成员行，再用新的语句查会话：并发的修改或重置提交之后才开始查，才能看到它删掉的会话。
		current, err := q.LockAccountPassword(ctx, memberID)
		if errors.Is(err, pgx.ErrNoRows) {
			return port.ErrMemberNotFound
		}
		if err != nil {
			return err
		}
		kept, err := q.MemberHasSession(ctx, query.MemberHasSessionParams{TokenHash: keepTokenHash, MemberID: memberID})
		if err != nil {
			return err
		}
		if !kept {
			return port.ErrSessionNotFound
		}
		if current != currentPasswordHash {
			return port.ErrAccountPasswordChanged
		}
		deleted, err = setPassword(ctx, q, memberID, passwordHash, keepTokenHash)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("修改成员 %s 的密码：%w", memberID, err)
	}
	return deleted, nil
}

func setPassword(ctx context.Context, q *query.Queries, memberID, passwordHash string, keepTokenHash []byte) ([][]byte, error) {
	n, err := q.SetMemberPassword(ctx, query.SetMemberPasswordParams{ID: memberID, PasswordHash: &passwordHash})
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, port.ErrMemberNotFound
	}
	return q.DeleteMemberSessions(ctx, query.DeleteMemberSessionsParams{MemberID: memberID, Keep: keepTokenHash})
}
