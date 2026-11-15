package api

import (
	"context"

	"github.com/dihemoe/alisten/internal/app/identity"
	"github.com/dihemoe/alisten/protocol"
)

func (s *server) register(ctx context.Context, c call, req protocol.RegisterRequest) (protocol.RegisterResponse, error) {
	account, err := s.Accounts.Register(ctx, c.caller, c.clientIP, req.Username, req.Password)
	if err != nil {
		return protocol.RegisterResponse{}, err
	}
	return protocol.RegisterResponse{Account: accountDTO(account)}, nil
}

func (s *server) login(ctx context.Context, c call, req protocol.LoginRequest) (protocol.LoginResponse, error) {
	session, account, device, err := s.Accounts.Login(ctx, c.caller, c.clientIP, req.Username, req.Password, deref(req.Device))
	if err != nil {
		return protocol.LoginResponse{}, err
	}
	return protocol.LoginResponse{MemberToken: session.MemberToken, Member: memberDTO(session.Member), Account: accountDTO(account), Device: device}, nil
}

func (s *server) logout(ctx context.Context, c call) error {
	return s.Accounts.Logout(ctx, c.caller)
}

func (s *server) changePassword(ctx context.Context, c call, req protocol.ChangePasswordRequest) error {
	return s.Accounts.ChangePassword(ctx, c.caller, req.CurrentPassword, req.NewPassword)
}

func (s *server) adminGetAccount(ctx context.Context, c call) (protocol.AdminAccount, error) {
	m, account, err := s.Accounts.Find(ctx, c.r.PathValue("username"))
	if err != nil {
		return protocol.AdminAccount{}, err
	}
	return protocol.AdminAccount{Member: memberDTO(m), Username: account.Username}, nil
}

func (s *server) adminResetAccountPassword(ctx context.Context, c call) (protocol.AdminResetAccountPasswordResponse, error) {
	password, err := s.Accounts.ResetPassword(ctx, c.r.PathValue("username"))
	if err != nil {
		return protocol.AdminResetAccountPasswordResponse{}, err
	}
	return protocol.AdminResetAccountPasswordResponse{Password: password}, nil
}

func (s *server) createPairingCode(ctx context.Context, c call) (protocol.CreatePairingCodeResponse, error) {
	code, err := s.Accounts.CreatePairingCode(ctx, c.caller)
	if err != nil {
		return protocol.CreatePairingCodeResponse{}, err
	}
	// 配对码放在片段里，不会出现在请求和 Referer 中。
	image, err := qrDataURI(s.PublicURL + "/pair#" + code.Code)
	if err != nil {
		return protocol.CreatePairingCodeResponse{}, err
	}
	return protocol.CreatePairingCodeResponse{Code: code.Code, QrImage: image, ExpiresAtMs: code.ExpiresAt.UnixMilli()}, nil
}

func (s *server) redeemPairingCode(ctx context.Context, c call, req protocol.RedeemPairingCodeRequest) (protocol.RedeemPairingCodeResponse, error) {
	session, account, err := s.Accounts.RedeemPairingCode(ctx, c.caller, c.clientIP, req.Code)
	if err != nil {
		return protocol.RedeemPairingCodeResponse{}, err
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
