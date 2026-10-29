package admin

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"slices"

	"github.com/dihemoe/alisten/internal/app/identity"
	"github.com/dihemoe/alisten/internal/app/lobby"
	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/app/realtime"
	"github.com/dihemoe/alisten/internal/domain/room"
)

var (
	ErrDisabled     = errors.New("后台没有启用：服务端没有配置管理 token。")
	ErrInvalidToken = errors.New("管理 token 无效")
)

// 除 Authorize 外的方法都不检查凭证，调用方要先用 Authorize 校验管理 token。
type Service struct {
	token       string
	lobby       *lobby.Service
	rooms       port.RoomDirectory
	keys        port.APIKeys
	credentials port.Credentials
	// 扫码登录成功时由音源保存登录状态，后台只得到二维码和扫码结果。
	netease  port.NetEaseQRLoginSource
	qq       port.QQQRLoginSource
	bilibili port.BilibiliQRLoginSource
	youtube  port.YouTubeCookies
	gateway  *realtime.Gateway
}

func New(token string, lobbyService *lobby.Service, rooms port.RoomDirectory, keys port.APIKeys, credentials port.Credentials, netease port.NetEaseQRLoginSource, qq port.QQQRLoginSource, bilibili port.BilibiliQRLoginSource, youtube port.YouTubeCookies, gateway *realtime.Gateway) *Service {
	return &Service{token: token, lobby: lobbyService, rooms: rooms, keys: keys, credentials: credentials, netease: netease, qq: qq, bilibili: bilibili, youtube: youtube, gateway: gateway}
}

func (s *Service) Authorize(token string) error {
	if s.token == "" {
		return ErrDisabled
	}
	// 比较哈希而不是原文：长度不同时 ConstantTimeCompare 立即返回，会泄露管理 token 的长度。
	want, got := sha256.Sum256([]byte(s.token)), sha256.Sum256([]byte(token))
	if subtle.ConstantTimeCompare(got[:], want[:]) != 1 {
		return ErrInvalidToken
	}
	return nil
}

func (s *Service) ListRooms(ctx context.Context) ([]lobby.Room, error) {
	rooms, err := s.lobby.ListRooms(ctx)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(rooms, func(r lobby.Room) bool { return r.Info.Kind != room.Persistent }), nil
}

type NewRoom struct {
	Name        string
	Description string
	// 空字符串表示没有别名。别名必须已经按格式校验：存储按格式区分 UUID 和别名，本身不做检查。
	Slug     string
	Password string
}

func (s *Service) CreateRoom(ctx context.Context, r NewRoom) (string, error) {
	hash, err := identity.HashPassword(ctx, r.Password)
	if err != nil {
		return "", err
	}
	return s.rooms.CreateRoom(ctx, port.NewRoom{
		Kind:         room.Persistent,
		Name:         r.Name,
		Description:  r.Description,
		Slug:         r.Slug,
		PasswordHash: hash,
	})
}

// nil 表示不修改。Slug 或 Password 为空字符串表示取消别名或密码。
type RoomChange struct {
	Name        *string
	Description *string
	// 别名必须已经按格式校验：存储按格式区分 UUID 和别名，本身不做检查。
	Slug     *string
	Password *string
}

// id 必须是 UUID。名称和描述由房间修改，房间落盘之后才返回。设置或修改密码会作废房间成员的票据并断开会话和成员的事件流，
// 这一步无法撤销，所以放在最后：前面任何一步失败时票据仍然有效，重试也不会再次作废重新签发的票据。
// 断开的通知发不出去时返回错误，此时票据已经作废。
// 别名在名称和描述之前修改，别名已被占用时不做任何修改。取消密码不影响已有的票据。
func (s *Service) UpdateRoom(ctx context.Context, id string, c RoomChange) error {
	r, err := s.persistentRoom(ctx, id)
	if err != nil {
		return err
	}
	var hash *string
	if c.Password != nil {
		h, err := identity.HashPassword(ctx, *c.Password)
		if err != nil {
			return err
		}
		hash = &h
	}
	if c.Slug != nil {
		if err := s.rooms.UpdateRoomAccess(ctx, r.Info.ID, port.AccessChange{Slug: c.Slug}); err != nil {
			return err
		}
	}
	if c.Name != nil || c.Description != nil {
		if err := s.gateway.UpdateInfo(ctx, r.Info.ID, c.Name, c.Description); err != nil {
			return err
		}
	}
	if hash == nil {
		return nil
	}
	if err := s.rooms.UpdateRoomAccess(ctx, r.Info.ID, port.AccessChange{PasswordHash: hash}); err != nil {
		return err
	}
	if *hash == "" {
		return nil
	}
	return s.gateway.Revoke(ctx, r.Info.ID)
}

// id 必须是 UUID。
func (s *Service) DeleteRoom(ctx context.Context, id string) error {
	r, err := s.persistentRoom(ctx, id)
	if err != nil {
		return err
	}
	return s.gateway.Delete(ctx, r.Info.ID)
}

// id 必须是 UUID。
func (s *Service) RoomAPIKeyStatus(ctx context.Context, id string) (port.APIKeyStatus, error) {
	r, err := s.persistentRoom(ctx, id)
	if err != nil {
		return port.APIKeyStatus{}, err
	}
	return s.keys.RoomAPIKeyStatus(ctx, r.Info.ID)
}

// id 必须是 UUID。还没有 Key 时生成，已经有时旧 Key 立即失效，用旧 Key 打开的事件流随之断开。
// 断开事件流的通知发不出去时返回错误，此时 Key 已经重置。
func (s *Service) ResetRoomAPIKey(ctx context.Context, id string) (string, error) {
	r, err := s.persistentRoom(ctx, id)
	if err != nil {
		return "", err
	}
	key, k := identity.NewAPIKey()
	if err := s.keys.ResetRoomAPIKey(ctx, r.Info.ID, k); err != nil {
		return "", err
	}
	if err := s.gateway.RevokeAPIKey(ctx, r.Info.ID); err != nil {
		return "", err
	}
	return key, nil
}

func (s *Service) persistentRoom(ctx context.Context, id string) (port.RoomRecord, error) {
	r, err := s.rooms.FindRoom(ctx, id)
	if err != nil {
		return port.RoomRecord{}, err
	}
	if r.Info.Kind != room.Persistent {
		return port.RoomRecord{}, port.ErrRoomNotFound
	}
	return r, nil
}

func (s *Service) SetYouTubeCookies(ctx context.Context, cookies string) error {
	normalized, err := normalizeCookies(cookies)
	if err != nil {
		return err
	}
	return s.youtube.SetCookies(ctx, normalized)
}

func (s *Service) ClearYouTubeCookies(ctx context.Context) error {
	return s.youtube.SetCookies(ctx, "")
}

func (s *Service) NeteaseCredentialStatus(ctx context.Context) (port.CredentialStatus, error) {
	cookie, err := s.credentials.NeteaseCookie(ctx)
	if err != nil {
		return port.CredentialStatus{}, err
	}
	status, err := credentialStatus(ctx, cookie, s.netease.CurrentAccount)
	if err != nil {
		return port.CredentialStatus{}, fmt.Errorf("查询网易云的账号资料：%w：%w", err, port.ErrProviderFailed)
	}
	return status, nil
}

func (s *Service) QQCredentialStatus(ctx context.Context) (port.CredentialStatus, error) {
	cookie, err := s.credentials.QQCookie(ctx)
	if err != nil {
		return port.CredentialStatus{}, err
	}
	status, err := credentialStatus(ctx, cookie, s.qq.CurrentAccount)
	if err != nil {
		return port.CredentialStatus{}, fmt.Errorf("查询 QQ 的账号资料：%w：%w", err, port.ErrProviderFailed)
	}
	return status, nil
}

func credentialStatus(ctx context.Context, cookie string, current func(context.Context) (*port.SourceAccount, error)) (port.CredentialStatus, error) {
	if cookie == "" {
		return port.CredentialStatus{}, nil
	}
	account, err := current(ctx)
	if err != nil {
		return port.CredentialStatus{Set: true}, err
	}
	return port.CredentialStatus{Set: true, Account: account}, nil
}

func (s *Service) CreateNeteaseQRLogin(ctx context.Context) (port.QRLoginSession, error) {
	session, err := s.netease.CreateQRLogin(ctx)
	if err != nil {
		return port.QRLoginSession{}, fmt.Errorf("生成网易云的登录二维码：%w：%w", err, port.ErrProviderFailed)
	}
	return session, nil
}

func (s *Service) CheckNeteaseQRLogin(ctx context.Context, key string) (port.QRLoginResult, error) {
	result, err := s.netease.CheckQRLogin(ctx, key)
	if err != nil {
		return port.QRLoginResult{}, fmt.Errorf("查询网易云的扫码结果：%w：%w", err, port.ErrProviderFailed)
	}
	return result, nil
}

func (s *Service) CreateQQQRLogin(ctx context.Context, typ port.QQLoginType) (port.QRLoginSession, error) {
	session, err := s.qq.CreateQRLogin(ctx, typ)
	if err != nil {
		return port.QRLoginSession{}, fmt.Errorf("生成 QQ 的登录二维码：%w：%w", err, port.ErrProviderFailed)
	}
	return session, nil
}

func (s *Service) CheckQQQRLogin(ctx context.Context, typ port.QQLoginType, key string) (port.QRLoginResult, error) {
	result, err := s.qq.CheckQRLogin(ctx, typ, key)
	if err != nil {
		return port.QRLoginResult{}, fmt.Errorf("查询 QQ 的扫码结果：%w：%w", err, port.ErrProviderFailed)
	}
	return result, nil
}

func (s *Service) BilibiliCredentialStatus(ctx context.Context) (port.CredentialStatus, error) {
	status, err := s.bilibili.CredentialStatus(ctx)
	if err != nil {
		return port.CredentialStatus{}, fmt.Errorf("查询 B 站的登录状态：%w：%w", err, port.ErrProviderFailed)
	}
	return status, nil
}

func (s *Service) CreateBilibiliQRLogin(ctx context.Context) (port.QRLoginSession, error) {
	session, err := s.bilibili.CreateQRLogin(ctx)
	if err != nil {
		return port.QRLoginSession{}, fmt.Errorf("生成 B 站的登录二维码：%w：%w", err, port.ErrProviderFailed)
	}
	return session, nil
}

func (s *Service) CheckBilibiliQRLogin(ctx context.Context, key string) (port.QRLoginResult, error) {
	result, err := s.bilibili.CheckQRLogin(ctx, key)
	if err != nil {
		return port.QRLoginResult{}, fmt.Errorf("查询 B 站的扫码结果：%w：%w", err, port.ErrProviderFailed)
	}
	return result, nil
}

func (s *Service) YouTubeCookiesStatus(ctx context.Context) (port.YouTubeCookiesStatus, error) {
	status, err := s.youtube.Status(ctx)
	if err != nil {
		return port.YouTubeCookiesStatus{}, fmt.Errorf("查询 YouTube cookies 的状态：%w", err)
	}
	return status, nil
}
