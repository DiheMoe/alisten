package identity

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

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
	ErrTooManyGuests      = errors.New("操作太频繁，请稍后再试。")
	ErrTooManyRoomGuesses = errors.New("这个房间的密码尝试次数太多，请稍后再试。")
)

// 创建成员按来源 IP 限制次数，因为任何人都能创建成员，每次都往存储里写一个成员和一个会话。同一个 Wi-Fi 下的人共用一个 IP，
// 突发额度要够一群人同时打开网页。
const (
	guestBurst  = 30
	guestRefill = time.Minute
)

// 换取票据按来源 IP 限制次数，因为每次都往存储里写一行，有密码的房间还要算一次 argon2id。网页会保存换到的票据，重进同一个房间不再换，
// 所以正常使用用不了几次。有密码的房间还按房间限制校验密码的次数，这样换着 IP 猜同一个房间的密码也会受限。这份额度要在算 argon2id
// 之前判断，这时还不知道密码对不对，所以输对的也算在内。输对的次数本来就少，几乎不占额度。
const (
	ticketIPBurst    = 10
	ticketIPRefill   = time.Minute
	ticketRoomBurst  = 20
	ticketRoomRefill = time.Minute
)

var emailPattern = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

type Service struct {
	members port.Members
	rooms   port.RoomDirectory
	tickets port.Tickets
	quota   port.Quota
	gateway *realtime.Gateway
}

func New(members port.Members, rooms port.RoomDirectory, tickets port.Tickets, quota port.Quota, gateway *realtime.Gateway) *Service {
	return &Service{members: members, rooms: rooms, tickets: tickets, quota: quota, gateway: gateway}
}

type Session struct {
	MemberToken string
	Member      member.Member
}

func (s *Service) CreateMember(ctx context.Context, clientIP string) (Session, error) {
	if err := takeQuota(ctx, s.quota, "member:ip:"+QuotaAddr(clientIP), guestBurst, guestRefill, ErrTooManyGuests); err != nil {
		return Session{}, err
	}
	token, hash := newSecret()
	m, err := s.members.CreateMember(ctx, hash, guestName())
	if err != nil {
		return Session{}, err
	}
	return Session{MemberToken: token, Member: m}, nil
}

// 发出请求的会话。TokenHash 是这个会话的成员 token 的哈希，用例靠它识别、保留或作废这个会话。
// 用 API Key 发出的房间请求没有会话，TokenHash 为空。
type Caller struct {
	Member    member.Member
	TokenHash []byte
	// 成员已经注册时是这个成员的 AccountOwner，否则为零值。只有 Authenticate 填写，所以改动 Member 换不出别人的 AccountOwner。
	owner AccountOwner
}

// 已注册的成员。字段不导出，别的包只能造出零值。非零值只能经 Caller.AccountOwner 取得，所以它说明 identity 确认过这个成员已经注册。
type AccountOwner struct {
	memberID string
}

func (o AccountOwner) MemberID() string {
	return o.memberID
}

// Caller 不是 Authenticate 返回的，或者成员是游客时，返回 ErrNotRegistered。
func (c Caller) AccountOwner() (AccountOwner, error) {
	if c.owner == (AccountOwner{}) {
		return AccountOwner{}, ErrNotRegistered
	}
	return c.owner, nil
}

func (s *Service) Authenticate(ctx context.Context, memberToken string) (Caller, error) {
	if memberToken == "" {
		return Caller{}, ErrInvalidMemberToken
	}
	hash := hashSecret(memberToken)
	m, registered, err := s.members.MemberByToken(ctx, hash)
	if errors.Is(err, port.ErrMemberNotFound) {
		return Caller{}, ErrInvalidMemberToken
	}
	if err != nil {
		return Caller{}, err
	}
	caller := Caller{Member: m, TokenHash: hash}
	if registered {
		caller.owner = AccountOwner{memberID: m.ID}
	}
	return caller, nil
}

type Profile struct {
	Name *string
	// 用来计算头像哈希。已注册的账号还会同时保存邮箱本身。空字符串表示清除。
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

// 票据属于 caller 这个会话，所以会话删除时票据随之失效。
func (s *Service) IssueTicket(ctx context.Context, caller Caller, clientIP, roomID, password string) (string, error) {
	if err := takeQuota(ctx, s.quota, "ticket:ip:"+QuotaAddr(clientIP), ticketIPBurst, ticketIPRefill, ErrTooManyAttempts); err != nil {
		return "", err
	}
	r, err := s.rooms.FindRoom(ctx, roomID)
	if err != nil {
		return "", err
	}
	if r.PasswordHash != "" {
		if err := takeQuota(ctx, s.quota, "ticket:room:"+r.Info.ID, ticketRoomBurst, ticketRoomRefill, ErrTooManyRoomGuesses); err != nil {
			return "", err
		}
		ok, err := checkPassword(ctx, r.PasswordHash, password)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", ErrWrongPassword
		}
	}
	ticket, hash := newSecret()
	err = s.tickets.CreateTicket(ctx, port.NewTicket{TokenHash: hash, RoomID: r.Info.ID, MemberID: caller.Member.ID, PasswordHash: r.PasswordHash, SessionTokenHash: caller.TokenHash})
	if errors.Is(err, port.ErrPasswordChanged) {
		// 刚才是按旧密码校验的。客户端重新输入密码后，会按新密码校验。
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

// 连接在网关中登记的凭证，就是票据在存储中的哈希。网关定期拿凭证到存储中核对，两者不同的话，仍然有效的连接也会被当作已撤销而断开。
func ticketCredential(ticket string) []byte {
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
	// bot 只用 HTTP 接口，这样 bot 不会出现在在线列表中，重置 Key 之后也不会留下用旧 Key 建立的会话。
	if holder.Bot {
		return nil, ErrBotSession
	}
	session, err := s.gateway.Join(logctx.WithMember(logctx.WithRoom(ctx, r.Info.ID), holder.Member.ID), r.Info.ID, holder.Member, holder.SessionTokenHash, ticketCredential(ticket))
	if err != nil {
		return nil, err
	}
	if err := s.confirm(ctx, ticket, func() { s.gateway.Leave(session) }); err != nil {
		return nil, err
	}
	return session, nil
}

// ObserveRoom 用票据打开房间的事件流。holder 是调用方刚用 VerifyTicket 查到的持有者。结束时必须调用一次网关的 Unobserve。
func (s *Service) ObserveRoom(ctx context.Context, ticket string, holder port.TicketHolder) (*realtime.Observer, error) {
	o, err := s.gateway.Observe(ctx, holder.RoomID, holder.Member, holder.SessionTokenHash, holder.Bot, string(ticketCredential(ticket)))
	if err != nil {
		return nil, err
	}
	if err := s.confirm(ctx, ticket, func() { s.gateway.Unobserve(o) }); err != nil {
		return nil, err
	}
	return o, nil
}

// 作废票据时先删除票据，再通知断开。通知如果在会话或事件流生效之前发出，它们就收不到，所以生效之后要再确认一次票据。
// 确认失败或 panic 时调用 abandon。否则会话一直留在房间里，事件流一直占着名额和房间频道。
func (s *Service) confirm(ctx context.Context, ticket string, abandon func()) error {
	confirmed := false
	defer func() {
		if !confirmed {
			abandon()
		}
	}()
	if _, err := s.VerifyTicket(ctx, ticket); err != nil {
		return err
	}
	confirmed = true
	return nil
}

func takeQuota(ctx context.Context, quota port.Quota, key string, burst int, refill time.Duration, exhausted error) error {
	wait, err := quota.Take(ctx, key, burst, refill)
	if err != nil {
		return err
	}
	if wait > 0 {
		return exhausted
	}
	return nil
}

func NewAPIKey() (string, port.NewAPIKey) {
	key, hash := newSecret()
	return key, port.NewAPIKey{TokenHash: hash}
}
