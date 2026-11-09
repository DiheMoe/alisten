package identity

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/app/realtime"
	"github.com/dihemoe/alisten/internal/domain/member"
	"github.com/dihemoe/alisten/internal/lib/logctx"
)

var (
	ErrInvalidMemberToken = errors.New("成员 token 无效")
	ErrInvalidTicket      = errors.New("票据无效")
	ErrWrongPassword      = errors.New("房间密码错误")
	ErrBotSession         = errors.New("API Key 只能用于 HTTP 接口")
	ErrInvalidEmail       = errors.New("email：格式不正确，应形如 name@example.com")
)

var emailPattern = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

type Service struct {
	members port.Members
	rooms   port.RoomDirectory
	tickets port.Tickets
	gateway *realtime.Gateway
}

func New(members port.Members, rooms port.RoomDirectory, tickets port.Tickets, gateway *realtime.Gateway) *Service {
	return &Service{members: members, rooms: rooms, tickets: tickets, gateway: gateway}
}

type Session struct {
	MemberToken string
	Member      member.Member
}

func (s *Service) CreateMember(ctx context.Context) (Session, error) {
	token, hash := newSecret()
	m, err := s.members.CreateMember(ctx, hash, guestName())
	if err != nil {
		return Session{}, err
	}
	return Session{MemberToken: token, Member: m}, nil
}

func (s *Service) Authenticate(ctx context.Context, memberToken string) (member.Member, error) {
	if memberToken == "" {
		return member.Member{}, ErrInvalidMemberToken
	}
	m, err := s.members.MemberByToken(ctx, hashSecret(memberToken))
	if errors.Is(err, port.ErrMemberNotFound) {
		return member.Member{}, ErrInvalidMemberToken
	}
	return m, err
}

type Profile struct {
	Name *string
	// 用来计算头像哈希；已注册的账号同时保存它。空字符串表示清除。
	Email *string
}

func (s *Service) UpdateProfile(ctx context.Context, memberID string, p Profile) (member.Member, error) {
	var avatar, email *string
	if p.Email != nil {
		trimmed := strings.TrimSpace(*p.Email)
		if trimmed != "" && !emailPattern.MatchString(trimmed) {
			return member.Member{}, ErrInvalidEmail
		}
		hash := avatarHash(trimmed)
		avatar, email = &hash, &trimmed
	}
	m, err := s.members.UpdateMember(ctx, memberID, p.Name, avatar, email)
	if err != nil {
		return member.Member{}, err
	}
	if err := s.gateway.MemberUpdated(ctx, m); err != nil {
		return member.Member{}, err
	}
	return m, nil
}

// memberToken 是发出请求的会话，票据属于这个会话：会话删除时票据随之失效。
func (s *Service) IssueTicket(ctx context.Context, memberID, memberToken, roomID, password string) (string, error) {
	r, err := s.rooms.FindRoom(ctx, roomID)
	if err != nil {
		return "", err
	}
	if r.PasswordHash != "" {
		ok, err := checkPassword(ctx, r.PasswordHash, password)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", ErrWrongPassword
		}
	}
	ticket, hash := newSecret()
	err = s.tickets.CreateTicket(ctx, port.NewTicket{TokenHash: hash, RoomID: r.Info.ID, MemberID: memberID, PasswordHash: r.PasswordHash, SessionTokenHash: hashSecret(memberToken)})
	if errors.Is(err, port.ErrPasswordChanged) {
		// 刚才是按旧密码校验的；客户端重新输入密码后，按新密码校验。
		return "", ErrWrongPassword
	}
	if errors.Is(err, port.ErrSessionNotFound) {
		// 会话在鉴权之后被删除（例如另一台设备退出登录或修改了密码），此时成员 token 确实已经失效。
		return "", ErrInvalidMemberToken
	}
	if err != nil {
		return "", err
	}
	return ticket, nil
}

// 连接在网关中登记的凭证，与票据在存储中的哈希相同：网关定期拿它到存储中核对，不同的话仍然有效的连接也会被当作已撤销而断开。
func TicketCredential(ticket string) []byte {
	return hashSecret(ticket)
}

func (s *Service) VerifyTicket(ctx context.Context, ticket string) (port.TicketHolder, error) {
	if ticket == "" {
		return port.TicketHolder{}, ErrInvalidTicket
	}
	holder, err := s.tickets.TicketHolder(ctx, hashSecret(ticket))
	if errors.Is(err, port.ErrTicketNotFound) {
		return port.TicketHolder{}, ErrInvalidTicket
	}
	return holder, err
}

func (s *Service) EnterRoom(ctx context.Context, roomID, ticket string) (*realtime.Session, error) {
	r, err := s.rooms.FindRoom(ctx, roomID)
	if err != nil {
		return nil, err
	}
	holder, err := s.VerifyTicket(ctx, ticket)
	if err != nil {
		return nil, err
	}
	if holder.RoomID != r.Info.ID {
		return nil, ErrInvalidTicket
	}
	// bot 只用 HTTP 接口：它不出现在在线列表中，重置 Key 之后也不会留下用旧 Key 建立的会话。
	if holder.Bot {
		return nil, ErrBotSession
	}
	session, err := s.gateway.Join(logctx.WithMember(logctx.WithRoom(ctx, r.Info.ID), holder.Member.ID), r.Info.ID, holder.Member, holder.SessionTokenHash, TicketCredential(ticket))
	if err != nil {
		return nil, err
	}
	// 作废时先删除票据、再通知断开：会话加入网关之前发出的通知它收不到，所以加入之后再确认一次票据。
	if _, err := s.VerifyTicket(ctx, ticket); err != nil {
		s.gateway.Leave(session)
		return nil, err
	}
	return session, nil
}

func NewAPIKey() (string, port.NewAPIKey) {
	key, hash := newSecret()
	return key, port.NewAPIKey{TokenHash: hash}
}
