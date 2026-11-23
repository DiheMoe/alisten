package api

import (
	"context"
	"encoding/base64"
	"fmt"

	"rsc.io/qr"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/protocol"
)

func (s *server) adminStartNetEaseLogin(ctx context.Context, _ call) (protocol.QRLogin, error) {
	session, err := s.Admin.CreateNetEaseQRLogin(ctx)
	if err != nil {
		return protocol.QRLogin{}, err
	}
	return qrLogin(session)
}

func (s *server) adminCheckNetEaseLogin(ctx context.Context, _ call, req protocol.AdminCheckNetEaseLoginRequest) (protocol.QRLoginResult, error) {
	result, err := s.Admin.CheckNetEaseQRLogin(ctx, req.Key)
	if err != nil {
		return protocol.QRLoginResult{}, err
	}
	return qrLoginResult(result), nil
}

func (s *server) adminStartQQLogin(ctx context.Context, _ call, req protocol.AdminStartQQLoginRequest) (protocol.QRLogin, error) {
	session, err := s.Admin.CreateQQQRLogin(ctx, port.QQLoginType(req.Type))
	if err != nil {
		return protocol.QRLogin{}, err
	}
	return qrLogin(session)
}

func (s *server) adminCheckQQLogin(ctx context.Context, _ call, req protocol.AdminCheckQQLoginRequest) (protocol.QRLoginResult, error) {
	result, err := s.Admin.CheckQQQRLogin(ctx, port.QQLoginType(req.Type), req.Key)
	if err != nil {
		return protocol.QRLoginResult{}, err
	}
	return qrLoginResult(result), nil
}

func (s *server) adminStartBilibiliLogin(ctx context.Context, _ call) (protocol.QRLogin, error) {
	session, err := s.Admin.CreateBilibiliQRLogin(ctx)
	if err != nil {
		return protocol.QRLogin{}, err
	}
	return qrLogin(session)
}

func (s *server) adminCheckBilibiliLogin(ctx context.Context, _ call, req protocol.AdminCheckBilibiliLoginRequest) (protocol.QRLoginResult, error) {
	result, err := s.Admin.CheckBilibiliQRLogin(ctx, req.Key)
	if err != nil {
		return protocol.QRLoginResult{}, err
	}
	return qrLoginResult(result), nil
}

func qrLogin(session port.QRLoginSession) (protocol.QRLogin, error) {
	image := session.QRImage
	if image == "" {
		if session.LoginURL == "" {
			return protocol.QRLogin{}, fmt.Errorf("音乐平台没有给出二维码图片和登录链接：%w", port.ErrSourceFailed)
		}
		var err error
		if image, err = qrDataURI(session.LoginURL); err != nil {
			return protocol.QRLogin{}, fmt.Errorf("把登录链接编码成二维码：%w：%w", err, port.ErrSourceFailed)
		}
	}
	return protocol.QRLogin{Key: session.Key, QrImage: image, ExpiresAtMs: session.ExpiresAt.UnixMilli()}, nil
}

func qrDataURI(text string) (string, error) {
	code, err := qr.Encode(text, qr.M)
	if err != nil {
		return "", err
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(code.PNG()), nil
}

func qrLoginResult(r port.QRLoginResult) protocol.QRLoginResult {
	return protocol.QRLoginResult{Status: protocol.QRLoginStatus(r.Status), Message: r.Message}
}
