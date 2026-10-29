package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/dihemoe/alisten/internal/adapter/out/postgres/internal/query"
	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/member"
)

var _ port.Members = (*Store)(nil)

func (s *Store) CreateMember(ctx context.Context, tokenHash []byte, name string) (member.Member, error) {
	row, err := s.q.CreateMember(ctx, query.CreateMemberParams{TokenHash: tokenHash, Name: name})
	if err != nil {
		return member.Member{}, fmt.Errorf("创建成员：%w", err)
	}
	return memberOf(row), nil
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

func (s *Store) UpdateMember(ctx context.Context, id string, name, avatarHash *string) (member.Member, error) {
	row, err := s.q.UpdateMember(ctx, query.UpdateMemberParams{ID: id, Name: name, AvatarHash: avatarHash})
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
