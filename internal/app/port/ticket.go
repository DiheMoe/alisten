package port

import (
	"context"
	"errors"

	"github.com/dihemoe/alisten/internal/domain/member"
)

var (
	ErrTicketNotFound  = errors.New("票据不存在")
	ErrPasswordChanged = errors.New("房间密码已修改")
)

type NewTicket struct {
	TokenHash []byte
	RoomID    string
	MemberID  string
	// 签发前校验密码时依据的密码哈希，空字符串表示房间没有密码。
	PasswordHash string
}

type TicketHolder struct {
	RoomID string
	// 持有者的当前资料。
	Member member.Member
	// 票据是常驻房间的 API Key，持有者是这个房间的 bot 成员。
	Bot bool
}

type Tickets interface {
	// 房间的密码哈希已经不是 t.PasswordHash 时返回 ErrPasswordChanged：密码在校验之后被修改过，
	// 设置或修改密码时的作废已经执行，此时签发的票据不会再被作废。
	CreateTicket(ctx context.Context, t NewTicket) error
	TicketHolder(ctx context.Context, tokenHash []byte) (TicketHolder, error)
}
