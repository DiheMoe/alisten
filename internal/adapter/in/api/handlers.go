package api

import (
	"context"
	"regexp"

	"github.com/dihemoe/alisten/internal/app/admin"
	"github.com/dihemoe/alisten/internal/app/identity"
	"github.com/dihemoe/alisten/internal/app/lobby"
	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/member"
	"github.com/dihemoe/alisten/internal/domain/room"
	"github.com/dihemoe/alisten/protocol"
)

// 路径中的房间 ID 和别名不经过协议解码。格式不正确的房间 ID 或别名一定不存在，其中含有 U+0000 等字符的还会让
// PostgreSQL 报错，所以直接按不存在处理。
var (
	uuidPattern = regexp.MustCompile(`^(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	slugPattern = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)
)

func (s *server) createMember(ctx context.Context, c call) (any, error) {
	if _, err := protocol.DecodeCreateMemberRequest(c.body); err != nil {
		return nil, err
	}
	session, err := s.Identity.CreateMember(ctx)
	if err != nil {
		return nil, err
	}
	return protocol.CreateMemberResponse{MemberToken: session.MemberToken, Member: memberDTO(session.Member)}, nil
}

func (s *server) getMe(_ context.Context, c call) (any, error) {
	return protocol.GetMeResponse{Member: memberDTO(c.member)}, nil
}

func (s *server) updateMe(ctx context.Context, c call) (any, error) {
	req, err := protocol.DecodeUpdateMeRequest(c.body)
	if err != nil {
		return nil, err
	}
	m, err := s.Identity.UpdateProfile(ctx, c.member.ID, identity.Profile{Name: req.Name, Email: req.Email})
	if err != nil {
		return nil, err
	}
	return protocol.UpdateMeResponse{Member: memberDTO(m)}, nil
}

func (s *server) listRooms(ctx context.Context, _ call) (any, error) {
	rooms, err := s.Lobby.ListRooms(ctx)
	if err != nil {
		return nil, err
	}
	return protocol.ListRoomsResponse{Items: listings(rooms)}, nil
}

func (s *server) getRoom(ctx context.Context, c call) (any, error) {
	ref := c.r.PathValue("ref")
	if !uuidPattern.MatchString(ref) && !slugPattern.MatchString(ref) {
		return nil, port.ErrRoomNotFound
	}
	r, err := s.Lobby.FindRoom(ctx, ref)
	if err != nil {
		return nil, err
	}
	return listing(r), nil
}

func (s *server) createRoom(ctx context.Context, c call) (any, error) {
	req, err := protocol.DecodeCreateRoomRequest(c.body)
	if err != nil {
		return nil, err
	}
	id, err := s.Lobby.CreateRoom(ctx, lobby.NewRoom{Name: req.Name, Description: req.Description, Password: deref(req.Password)})
	if err != nil {
		return nil, err
	}
	return located{location: "/api/rooms/" + id, body: protocol.CreateRoomResponse{ID: id}}, nil
}

func (s *server) createTicket(ctx context.Context, c call) (any, error) {
	id := c.r.PathValue("id")
	if !uuidPattern.MatchString(id) {
		return nil, port.ErrRoomNotFound
	}
	req, err := protocol.DecodeCreateTicketRequest(c.body)
	if err != nil {
		return nil, err
	}
	ticket, err := s.Identity.IssueTicket(ctx, c.member.ID, id, deref(req.Password))
	if err != nil {
		return nil, err
	}
	return protocol.CreateTicketResponse{Ticket: ticket}, nil
}

func (s *server) adminListRooms(ctx context.Context, _ call) (any, error) {
	rooms, err := s.Admin.ListRooms(ctx)
	if err != nil {
		return nil, err
	}
	return protocol.AdminListRoomsResponse{Items: listings(rooms)}, nil
}

func (s *server) adminCreateRoom(ctx context.Context, c call) (any, error) {
	req, err := protocol.DecodeAdminCreateRoomRequest(c.body)
	if err != nil {
		return nil, err
	}
	id, err := s.Admin.CreateRoom(ctx, admin.NewRoom{
		Name:        req.Name,
		Description: req.Description,
		Slug:        deref(req.Slug),
		Password:    deref(req.Password),
	})
	if err != nil {
		return nil, err
	}
	return located{location: "/api/rooms/" + id, body: protocol.AdminCreateRoomResponse{ID: id}}, nil
}

func (s *server) adminGetNeteaseCredential(ctx context.Context, _ call) (any, error) {
	status, err := s.Admin.NeteaseCredentialStatus(ctx)
	if err != nil {
		return nil, err
	}
	return protocol.AdminGetNeteaseCredentialResponse{Set: status.Set, Account: sourceAccountDTO(status.Account)}, nil
}

func (s *server) adminGetQQCredential(ctx context.Context, _ call) (any, error) {
	status, err := s.Admin.QQCredentialStatus(ctx)
	if err != nil {
		return nil, err
	}
	return protocol.AdminGetQQCredentialResponse{Set: status.Set, Account: sourceAccountDTO(status.Account)}, nil
}

func (s *server) adminGetBilibiliCredential(ctx context.Context, _ call) (any, error) {
	status, err := s.Admin.BilibiliCredentialStatus(ctx)
	if err != nil {
		return nil, err
	}
	return protocol.AdminGetBilibiliCredentialResponse{Set: status.Set, Account: sourceAccountDTO(status.Account)}, nil
}

func (s *server) adminGetYoutubeCredential(ctx context.Context, _ call) (any, error) {
	status, err := s.Admin.YouTubeCookiesStatus(ctx)
	if err != nil {
		return nil, err
	}
	resp := protocol.AdminGetYoutubeCredentialResponse{Set: status.Set}
	// 零值的 UnixMilli 是负数，协议约定没有填写时为 0。
	if !status.UpdatedAt.IsZero() {
		resp.UpdatedAtMs = status.UpdatedAt.UnixMilli()
	}
	return resp, nil
}

func sourceAccountDTO(account *port.SourceAccount) *protocol.SourceAccount {
	if account == nil {
		return nil
	}
	return &protocol.SourceAccount{ID: account.ID, Name: account.Name, Vip: account.VIP, Verified: account.Verified}
}

func memberDTO(m member.Member) protocol.Member {
	return protocol.Member{ID: m.ID, Name: m.Name, AvatarHash: m.AvatarHash}
}

func listings(rooms []lobby.Room) []protocol.RoomListing {
	items := make([]protocol.RoomListing, len(rooms))
	for i, r := range rooms {
		items[i] = listing(r)
	}
	return items
}

func listing(r lobby.Room) protocol.RoomListing {
	l := protocol.RoomListing{
		ID:          r.Info.ID,
		Name:        r.Info.Name,
		Description: r.Info.Description,
		Kind:        roomKind(r.Info.Kind),
		HasPassword: r.HasPassword,
		OnlineCount: int64(r.Online),
	}
	if r.Slug != "" {
		l.Slug = &r.Slug
	}
	return l
}

func roomKind(k room.Kind) protocol.RoomKind {
	if k == room.Persistent {
		return protocol.RoomKindPersistent
	}
	return protocol.RoomKindEphemeral
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
