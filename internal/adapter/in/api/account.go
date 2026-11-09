package api

import (
	"context"

	"github.com/dihemoe/alisten/internal/app/identity"
	"github.com/dihemoe/alisten/protocol"
)

func (s *server) register(ctx context.Context, c call) (any, error) {
	req, err := protocol.DecodeRegisterRequest(c.body)
	if err != nil {
		return nil, err
	}
	account, err := s.Accounts.Register(ctx, c.member.ID, c.clientIP, req.Username, req.Password)
	if err != nil {
		return nil, err
	}
	return protocol.RegisterResponse{Account: accountDTO(account)}, nil
}

func (s *server) login(ctx context.Context, c call) (any, error) {
	req, err := protocol.DecodeLoginRequest(c.body)
	if err != nil {
		return nil, err
	}
	session, account, device, err := s.Accounts.Login(ctx, c.member, bearer(c.r), c.clientIP, req.Username, req.Password, deref(req.Device))
	if err != nil {
		return nil, err
	}
	return protocol.LoginResponse{MemberToken: session.MemberToken, Member: memberDTO(session.Member), Account: accountDTO(account), Device: device}, nil
}

func (s *server) logout(ctx context.Context, c call) (any, error) {
	return nil, s.Accounts.Logout(ctx, bearer(c.r))
}

func (s *server) changePassword(ctx context.Context, c call) (any, error) {
	req, err := protocol.DecodeChangePasswordRequest(c.body)
	if err != nil {
		return nil, err
	}
	return nil, s.Accounts.ChangePassword(ctx, c.member.ID, bearer(c.r), req.CurrentPassword, req.NewPassword)
}

func (s *server) adminGetAccount(ctx context.Context, c call) (any, error) {
	username := c.r.PathValue("username")
	if !usernamePattern.MatchString(username) {
		return nil, identity.ErrAccountNotFound
	}
	account, err := s.Accounts.Find(ctx, username)
	if err != nil {
		return nil, err
	}
	return protocol.AdminAccount{Member: memberDTO(account.Member), Username: account.Username}, nil
}

func (s *server) adminResetAccountPassword(ctx context.Context, c call) (any, error) {
	if _, err := protocol.DecodeAdminResetAccountPasswordRequest(c.body); err != nil {
		return nil, err
	}
	username := c.r.PathValue("username")
	if !usernamePattern.MatchString(username) {
		return nil, identity.ErrAccountNotFound
	}
	password, err := s.Accounts.ResetPassword(ctx, username)
	if err != nil {
		return nil, err
	}
	return protocol.AdminResetAccountPasswordResponse{Password: password}, nil
}

func accountDTO(a identity.Account) protocol.Account {
	dto := protocol.Account{Username: a.Username}
	if a.Email != "" {
		dto.Email = &a.Email
	}
	return dto
}
