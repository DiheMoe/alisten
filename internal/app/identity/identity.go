package identity

import (
	"context"
	"errors"

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
)

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
	// 只用来计算头像哈希，不保存；空字符串表示清除头像。
	Email *string
}

func (s *Service) UpdateProfile(ctx context.Context, memberID string, p Profile) (member.Member, error) {
	var avatar *string
	if p.Email != nil {
		hash := avatarHash(*p.Email)
		avatar = &hash
	}
	m, err := s.members.UpdateMember(ctx, memberID, p.Name, avatar)
	if err != nil {
		return member.Member{}, err
	}
	if err := s.gateway.MemberUpdated(ctx, m); err != nil {
		return member.Member{}, err
	}
	return m, nil
}

func (s *Service) IssueTicket(ctx context.Context, memberID, roomID, password string) (string, error) {
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
	err = s.tickets.CreateTicket(ctx, port.NewTicket{TokenHash: hash, RoomID: r.Info.ID, MemberID: memberID, PasswordHash: r.PasswordHash})
	if errors.Is(err, port.ErrPasswordChanged) {
		// 刚才是按旧密码校验的；客户端重新输入密码后，按新密码校验。
		return "", ErrWrongPassword
	}
	if err != nil {
		return "", err
	}
	return ticket, nil
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
	return s.gateway.Join(logctx.WithMember(logctx.WithRoom(ctx, r.Info.ID), holder.Member.ID), r.Info.ID, holder.Member)
}

// 每次都准备好 bot 成员的 token：房间还没有 bot 成员时由存储用它创建，这个随机值不交给任何人。
func NewAPIKey() (string, port.NewAPIKey) {
	key, hash := newSecret()
	_, botHash := newSecret()
	return key, port.NewAPIKey{TokenHash: hash, BotTokenHash: botHash}
}
