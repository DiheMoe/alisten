package api

import (
	"context"
	"encoding/base64"
	"fmt"

	"rsc.io/qr"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/protocol"
)

func (s *server) adminStartNeteaseLogin(ctx context.Context, c call) (any, error) {
	if _, err := protocol.DecodeAdminStartNeteaseLoginRequest(c.body); err != nil {
		return nil, err
	}
	session, err := s.Admin.CreateNeteaseQRLogin(ctx)
	if err != nil {
		return nil, err
	}
	return qrLogin(session)
}

func (s *server) adminCheckNeteaseLogin(ctx context.Context, c call) (any, error) {
	req, err := protocol.DecodeAdminCheckNeteaseLoginRequest(c.body)
	if err != nil {
		return nil, err
	}
	result, err := s.Admin.CheckNeteaseQRLogin(ctx, req.Key)
	if err != nil {
		return nil, err
	}
	return qrLoginResult(result), nil
}

func (s *server) adminStartQQLogin(ctx context.Context, c call) (any, error) {
	req, err := protocol.DecodeAdminStartQQLoginRequest(c.body)
	if err != nil {
		return nil, err
	}
	session, err := s.Admin.CreateQQQRLogin(ctx, port.QQLoginType(req.Type))
	if err != nil {
		return nil, err
	}
	return qrLogin(session)
}

func (s *server) adminCheckQQLogin(ctx context.Context, c call) (any, error) {
	req, err := protocol.DecodeAdminCheckQQLoginRequest(c.body)
	if err != nil {
		return nil, err
	}
	result, err := s.Admin.CheckQQQRLogin(ctx, port.QQLoginType(req.Type), req.Key)
	if err != nil {
		return nil, err
	}
	return qrLoginResult(result), nil
}

func (s *server) adminStartBilibiliLogin(ctx context.Context, c call) (any, error) {
	if _, err := protocol.DecodeAdminStartBilibiliLoginRequest(c.body); err != nil {
		return nil, err
	}
	session, err := s.Admin.CreateBilibiliQRLogin(ctx)
	if err != nil {
		return nil, err
	}
	return qrLogin(session)
}

func (s *server) adminCheckBilibiliLogin(ctx context.Context, c call) (any, error) {
	req, err := protocol.DecodeAdminCheckBilibiliLoginRequest(c.body)
	if err != nil {
		return nil, err
	}
	result, err := s.Admin.CheckBilibiliQRLogin(ctx, req.Key)
	if err != nil {
		return nil, err
	}
	return qrLoginResult(result), nil
}

func qrLogin(session port.QRLoginSession) (protocol.QRLogin, error) {
	image := session.QRImage
	if image == "" {
		if session.LoginURL == "" {
			return protocol.QRLogin{}, fmt.Errorf("音源没有给出二维码图片和登录链接：%w", port.ErrProviderFailed)
		}
		code, err := qr.Encode(session.LoginURL, qr.M)
		if err != nil {
			return protocol.QRLogin{}, fmt.Errorf("把登录链接编码成二维码：%w：%w", err, port.ErrProviderFailed)
		}
		image = "data:image/png;base64," + base64.StdEncoding.EncodeToString(code.PNG())
	}
	return protocol.QRLogin{Key: session.Key, QrImage: image, ExpiresAtMs: session.ExpiresAt.UnixMilli()}, nil
}

func qrLoginResult(r port.QRLoginResult) protocol.QRLoginResult {
	return protocol.QRLoginResult{Status: protocol.QRLoginStatus(r.Status), Message: r.Message}
}
