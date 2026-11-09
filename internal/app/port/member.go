package port

import (
	"context"
	"errors"

	"github.com/dihemoe/alisten/internal/domain/member"
)

var (
	ErrMemberNotFound         = errors.New("成员不存在")
	ErrUsernameTaken          = errors.New("用户名已被占用")
	ErrAlreadyRegistered      = errors.New("已经注册过了")
	ErrSessionNotFound        = errors.New("会话不存在")
	ErrAccountPasswordChanged = errors.New("账号密码已修改")
)

// 设置了用户名的成员。
type Account struct {
	Member       member.Member
	Username     string
	Email        string
	PasswordHash string
}

// 游客的两个字段都为空。
type AccountProfile struct {
	Username string
	Email    string
}

// 成员 token 只以 SHA-256 哈希的形式出现在端口中。一个成员 token 是成员的一个会话，同一个成员可以有多个会话。
// 删除会话时，签发给这个会话的票据一并删除。
type Members interface {
	// 创建游客和它的第一个会话。
	CreateMember(ctx context.Context, tokenHash []byte, name string) (member.Member, error)
	MemberByToken(ctx context.Context, tokenHash []byte) (member.Member, error)
	// name、avatarHash 或 email 为 nil 时保持原值，email 为空字符串表示清除。游客的 email 不保存。
	UpdateMember(ctx context.Context, id string, name, avatarHash, email *string) (member.Member, error)
	AccountProfile(ctx context.Context, memberID string) (AccountProfile, error)
	// 用户名已属于其他成员时返回 ErrUsernameTaken，成员已经注册过时返回 ErrAlreadyRegistered。
	Register(ctx context.Context, memberID, username, passwordHash string) error
	AccountByUsername(ctx context.Context, username string) (Account, error)
	// 在同一个事务中删除 oldTokenHash 的会话，给 memberID 创建 newTokenHash 的会话。oldTokenHash 的会话已经不存在时返回
	// ErrSessionNotFound；memberID 的密码哈希已经不是 passwordHash 时返回 ErrAccountPasswordChanged：密码在校验之后被修改过，
	// 修改时对会话的删除已经执行，此时创建的会话不会再被删除。两种情况都不做任何修改。
	ReplaceSession(ctx context.Context, oldTokenHash []byte, memberID string, newTokenHash []byte, passwordHash string) error
	DeleteSession(ctx context.Context, tokenHash []byte) error
	// 在同一个事务中修改已注册成员的密码哈希，并删除该成员的全部会话。返回被删除的会话的 token 哈希。成员是游客时返回 ErrMemberNotFound。
	SetPassword(ctx context.Context, memberID, passwordHash string) ([][]byte, error)
	// 与 SetPassword 相同，但保留发出请求的 keepTokenHash 的会话。这个会话已经不存在时返回 ErrSessionNotFound；密码哈希已经不是
	// 校验当前密码时读到的 currentPasswordHash 时返回 ErrAccountPasswordChanged：密码在校验之后被修改或重置过。两种情况都不做任何修改。
	ChangePassword(ctx context.Context, memberID, currentPasswordHash, passwordHash string, keepTokenHash []byte) ([][]byte, error)
}
