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
	account, err := s.Accounts.Register(ctx, c.member.ID, bearer(c.r), c.clientIP, req.Username, req.Password)
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

func (s *server) createPairingCode(ctx context.Context, c call) (any, error) {
	if _, err := protocol.DecodeCreatePairingCodeRequest(c.body); err != nil {
		return nil, err
	}
	code, err := s.Accounts.CreatePairingCode(ctx, c.member.ID, bearer(c.r))
	if err != nil {
		return nil, err
	}
	// 配对码放在片段里，不会出现在请求和 Referer 中。
	image, err := qrDataURI(s.PublicURL + "/pair#" + code.Code)
	if err != nil {
		return nil, err
	}
	return protocol.CreatePairingCodeResponse{Code: code.Code, QrImage: image, ExpiresAtMs: code.ExpiresAt.UnixMilli()}, nil
}

func (s *server) redeemPairingCode(ctx context.Context, c call) (any, error) {
	req, err := protocol.DecodeRedeemPairingCodeRequest(c.body)
	if err != nil {
		return nil, err
	}
	session, account, err := s.Accounts.RedeemPairingCode(ctx, c.member, bearer(c.r), c.clientIP, req.Code)
	if err != nil {
		return nil, err
	}
	return protocol.RedeemPairingCodeResponse{MemberToken: session.MemberToken, Member: memberDTO(session.Member), Account: accountDTO(account)}, nil
}

func accountDTO(a identity.Account) protocol.Account {
	dto := protocol.Account{Github: a.GitHub}
	if a.Username != "" {
		dto.Username = &a.Username
	}
	if a.Email != "" {
		dto.Email = &a.Email
	}
	return dto
}
