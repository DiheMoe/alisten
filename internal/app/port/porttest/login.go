package porttest

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
)

var (
	_ port.QRLoginSource   = (*NetEaseQRLogin)(nil)
	_ port.QQQRLoginSource = (*QQQRLogin)(nil)
	_ port.QRLoginSource   = (*BilibiliQRLogin)(nil)
)

const (
	QQQRImage     = "data:image/png;base64,iVBORw0KGgo="
	WeChatQRImage = "https://wx.example/qr.png"
)

// 查询时不检查会话的过期时间。登录成功时也不像真实的音乐平台适配器那样保存登录状态。
type qrLogins struct {
	mu            sync.Mutex
	created       int
	logins        map[string]qrLogin
	failure       error
	status        port.CredentialStatus
	statusFailure error
}

type qrLogin struct {
	kind   string
	result port.QRLoginResult
}

func (l *qrLogins) create(kind string) (string, time.Time, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.failure != nil {
		return "", time.Time{}, l.failure
	}
	l.created++
	key := fmt.Sprintf("%s-%d", kind, l.created)
	l.logins[key] = qrLogin{kind: kind, result: port.QRLoginResult{Status: port.QRLoginWaiting}}
	return key, time.Now().Add(2 * time.Minute), nil
}

func (l *qrLogins) check(kind, key string) (port.QRLoginResult, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.failure != nil {
		return port.QRLoginResult{}, l.failure
	}
	if login, ok := l.logins[key]; ok && login.kind == kind {
		return login.result, nil
	}
	return port.QRLoginResult{Status: port.QRLoginExpired}, nil
}

// key 必须已经生成过。
func (l *qrLogins) SetResult(key string, result port.QRLoginResult) {
	l.mu.Lock()
	defer l.mu.Unlock()
	login := l.logins[key]
	login.result = result
	l.logins[key] = login
}

func (l *qrLogins) Fail(err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.failure = err
}

// account 为 nil 表示已保存登录信息但未识别到账号。
func (l *qrLogins) SetCredential(account *port.SourceAccount) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.status, l.statusFailure = port.CredentialStatus{Set: true, Account: account}, nil
}

func (l *qrLogins) SetSignedOut() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.status, l.statusFailure = port.CredentialStatus{Set: true, SignedOut: true}, nil
}

func (l *qrLogins) FailStatus(err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.statusFailure = err
}

func (l *qrLogins) CredentialStatus(context.Context) (port.CredentialStatus, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.statusFailure != nil {
		return port.CredentialStatus{}, l.statusFailure
	}
	return l.status, nil
}

// 像网易云那样只给登录链接。
type NetEaseQRLogin struct{ qrLogins }

func NewNetEaseQRLogin() *NetEaseQRLogin {
	return &NetEaseQRLogin{qrLogins{logins: map[string]qrLogin{}}}
}

func (l *NetEaseQRLogin) CreateQRLogin(context.Context) (port.QRLoginSession, error) {
	key, expires, err := l.create("netease")
	if err != nil {
		return port.QRLoginSession{}, err
	}
	return port.QRLoginSession{Key: key, LoginURL: "https://netease.example/login?key=" + key, ExpiresAt: expires}, nil
}

func (l *NetEaseQRLogin) CheckQRLogin(_ context.Context, key string) (port.QRLoginResult, error) {
	return l.check("netease", key)
}

// QQ 扫码只给 data: 的图片，微信扫码给登录链接和 https 的图片。
type QQQRLogin struct{ qrLogins }

func NewQQQRLogin() *QQQRLogin {
	return &QQQRLogin{qrLogins{logins: map[string]qrLogin{}}}
}

func (l *QQQRLogin) CreateQRLogin(_ context.Context, typ port.QQLoginType) (port.QRLoginSession, error) {
	key, expires, err := l.create(string(typ))
	if err != nil {
		return port.QRLoginSession{}, err
	}
	if typ == port.QQLoginWeChat {
		return port.QRLoginSession{Key: key, LoginURL: "https://wx.example/login?key=" + key, QRImage: WeChatQRImage, ExpiresAt: expires}, nil
	}
	return port.QRLoginSession{Key: key, QRImage: QQQRImage, ExpiresAt: expires}, nil
}

func (l *QQQRLogin) CheckQRLogin(_ context.Context, typ port.QQLoginType, key string) (port.QRLoginResult, error) {
	return l.check(string(typ), key)
}
