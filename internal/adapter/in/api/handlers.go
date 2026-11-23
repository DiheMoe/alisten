package api

import (
	"context"

	"github.com/dihemoe/alisten/internal/app/admin"
	"github.com/dihemoe/alisten/internal/app/identity"
	"github.com/dihemoe/alisten/internal/app/lobby"
	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/member"
	"github.com/dihemoe/alisten/internal/domain/room"
	"github.com/dihemoe/alisten/protocol"
)

func (s *server) createMember(ctx context.Context, c call) (protocol.CreateMemberResponse, error) {
	session, err := s.Identity.CreateMember(ctx, c.clientIP)
	if err != nil {
		return protocol.CreateMemberResponse{}, err
	}
	return protocol.CreateMemberResponse{MemberToken: session.MemberToken, Member: memberDTO(session.Member)}, nil
}

func (s *server) getMe(ctx context.Context, c call) (protocol.GetMeResponse, error) {
	resp := protocol.GetMeResponse{Member: memberDTO(c.caller.Member)}
	account, registered, err := s.Accounts.Account(ctx, c.caller.Member.ID)
	if err != nil {
		return protocol.GetMeResponse{}, err
	}
	if registered {
		dto := accountDTO(account)
		resp.Account = &dto
	}
	if resp.ListenRecording, err = s.Listens.Recording(ctx, c.caller.Member.ID); err != nil {
		return protocol.GetMeResponse{}, err
	}
	return resp, nil
}

func (s *server) updateMe(ctx context.Context, c call, req protocol.UpdateMeRequest) (protocol.UpdateMeResponse, error) {
	m, err := s.Identity.UpdateProfile(ctx, c.caller.Member.ID, identity.Profile{Name: req.Name, Email: req.Email})
	if err != nil {
		return protocol.UpdateMeResponse{}, err
	}
	return protocol.UpdateMeResponse{Member: memberDTO(m)}, nil
}

func (s *server) listRooms(ctx context.Context, _ call) (protocol.ListRoomsResponse, error) {
	rooms, err := s.Lobby.ListRooms(ctx)
	if err != nil {
		return protocol.ListRoomsResponse{}, err
	}
	return protocol.ListRoomsResponse{Items: listings(rooms)}, nil
}

func (s *server) getRoom(ctx context.Context, c call) (protocol.RoomListing, error) {
	r, err := s.Lobby.FindRoom(ctx, c.r.PathValue("ref"))
	if err != nil {
		return protocol.RoomListing{}, err
	}
	return listing(r), nil
}

func (s *server) createRoom(ctx context.Context, c call, req protocol.CreateRoomRequest) (protocol.CreateRoomResponse, string, error) {
	id, err := s.Lobby.CreateRoom(ctx, c.clientIP, lobby.NewRoom{Name: req.Name, Description: req.Description, Password: deref(req.Password)})
	if err != nil {
		return protocol.CreateRoomResponse{}, "", err
	}
	return protocol.CreateRoomResponse{ID: id}, "/api/rooms/" + id, nil
}

func (s *server) createTicket(ctx context.Context, c call, req protocol.CreateTicketRequest) (protocol.CreateTicketResponse, error) {
	ticket, err := s.Identity.IssueTicket(ctx, c.caller, c.clientIP, c.r.PathValue("id"), deref(req.Password))
	if err != nil {
		return protocol.CreateTicketResponse{}, err
	}
	return protocol.CreateTicketResponse{Ticket: ticket}, nil
}

func (s *server) adminListRooms(ctx context.Context, _ call) (protocol.AdminListRoomsResponse, error) {
	rooms, err := s.Admin.ListRooms(ctx)
	if err != nil {
		return protocol.AdminListRoomsResponse{}, err
	}
	return protocol.AdminListRoomsResponse{Items: listings(rooms), PersistentRoomCapacity: admin.PersistentRoomCapacity}, nil
}

func (s *server) adminCreateRoom(ctx context.Context, _ call, req protocol.AdminCreateRoomRequest) (protocol.AdminCreateRoomResponse, string, error) {
	r := admin.NewRoom{
		Name:        req.Name,
		Description: req.Description,
		Slug:        deref(req.Slug),
		Password:    deref(req.Password),
	}
	if req.Capacity != nil {
		r.Capacity = int(*req.Capacity)
	}
	id, err := s.Admin.CreateRoom(ctx, r)
	if err != nil {
		return protocol.AdminCreateRoomResponse{}, "", err
	}
	return protocol.AdminCreateRoomResponse{ID: id}, "/api/rooms/" + id, nil
}

func (s *server) adminGetNetEaseCredential(ctx context.Context, _ call) (protocol.AdminGetNetEaseCredentialResponse, error) {
	status, err := s.Admin.NetEaseCredentialStatus(ctx)
	if err != nil {
		return protocol.AdminGetNetEaseCredentialResponse{}, err
	}
	return protocol.AdminGetNetEaseCredentialResponse{Set: status.Set, Account: sourceAccountDTO(status.Account), SignedOut: status.SignedOut}, nil
}

func (s *server) adminGetQQCredential(ctx context.Context, _ call) (protocol.AdminGetQQCredentialResponse, error) {
	status, err := s.Admin.QQCredentialStatus(ctx)
	if err != nil {
		return protocol.AdminGetQQCredentialResponse{}, err
	}
	return protocol.AdminGetQQCredentialResponse{Set: status.Set, Account: sourceAccountDTO(status.Account), SignedOut: status.SignedOut}, nil
}

func (s *server) adminGetBilibiliCredential(ctx context.Context, _ call) (protocol.AdminGetBilibiliCredentialResponse, error) {
	status, err := s.Admin.BilibiliCredentialStatus(ctx)
	if err != nil {
		return protocol.AdminGetBilibiliCredentialResponse{}, err
	}
	return protocol.AdminGetBilibiliCredentialResponse{Set: status.Set, Account: sourceAccountDTO(status.Account), SignedOut: status.SignedOut}, nil
}

func (s *server) adminGetYouTubeCredential(ctx context.Context, _ call) (protocol.AdminGetYouTubeCredentialResponse, error) {
	status, err := s.Admin.YouTubeCredentialStatus(ctx)
	if err != nil {
		return protocol.AdminGetYouTubeCredentialResponse{}, err
	}
	resp := protocol.AdminGetYouTubeCredentialResponse{Set: status.Set}
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
		OnlineCount: int64(r.OnlineCount),
		Capacity:    int64(r.Info.Capacity),
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
