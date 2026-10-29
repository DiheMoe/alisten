package port

import (
	"context"
	"errors"

	"github.com/dihemoe/alisten/internal/domain/member"
)

var ErrMemberNotFound = errors.New("成员不存在")

// 成员 token 只以 SHA-256 哈希的形式出现在端口中。
type Members interface {
	CreateMember(ctx context.Context, tokenHash []byte, name string) (member.Member, error)
	MemberByToken(ctx context.Context, tokenHash []byte) (member.Member, error)
	// name 或 avatarHash 为 nil 时保持原值。
	UpdateMember(ctx context.Context, id string, name, avatarHash *string) (member.Member, error)
}
