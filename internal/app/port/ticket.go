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
	// 发出请求的会话的 token 哈希。这个会话删除时，票据随之失效。
	SessionTokenHash []byte
}

type TicketHolder struct {
	RoomID string
	// 持有者的当前资料。
	Member member.Member
	// 票据是常驻房间的 API Key，持有者是这个房间的 bot 成员。
	Bot bool
	// 票据所属的会话的 token 哈希，API Key 没有会话，为 nil。
	SessionTokenHash []byte
}

type Tickets interface {
	// 房间的密码哈希已经不是 t.PasswordHash 时返回 ErrPasswordChanged。这说明密码在校验之后被修改过。
	// 设置或修改密码时作废票据的那一步已经执行过，如果这时签发票据，这张票据就不会被作废。t.SessionTokenHash 的会话已经不存在时返回 ErrSessionNotFound。
	CreateTicket(ctx context.Context, t NewTicket) error
	TicketHolder(ctx context.Context, tokenHash []byte) (TicketHolder, error)
}

// 网关定期用 ExistingTickets 核对连接所用的凭证。凭证不在存储中，就当作已撤销，断开连接。所以票据只在作废时删除。
// 如果按时间清理过期的票据，仍在使用这些票据的连接也会被断开。
type ExistingTickets interface {
	// 返回 tokenHashes 中仍然存在的票据（包括 API Key），每个只出现一次，顺序不定。
	ExistingTickets(ctx context.Context, tokenHashes [][]byte) ([][]byte, error)
}
