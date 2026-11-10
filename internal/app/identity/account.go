package identity

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/netip"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/app/realtime"
	"github.com/dihemoe/alisten/internal/domain/member"
)

var (
	ErrRegistrationClosed   = errors.New("注册已关闭")
	ErrAlreadyLoggedIn      = errors.New("已经登录了账号，先退出再登录其他账号")
	ErrInvalidCredentials   = errors.New("用户名或密码错误")
	ErrWrongCurrentPassword = errors.New("当前密码错误")
	ErrNotRegistered        = errors.New("还没有注册账号")
	ErrTooManyAttempts      = errors.New("尝试次数太多，请稍后再试。")
	ErrAccountNotFound      = errors.New("没有这个用户名的账号")
)

// 注册、登录和修改密码都要做一次 argon2id 计算，所以额度除了防止猜密码，也防止耗尽计算槽位。
const (
	registerBurst   = 5
	registerRefill  = 10 * time.Minute
	loginIPBurst    = 10
	loginIPRefill   = time.Minute
	loginUserBurst  = 5
	loginUserRefill = time.Minute
	passwordBurst   = 5
	passwordRefill  = time.Minute
)

const loginDeviceTTL = 180 * 24 * time.Hour

// 只绑定了 GitHub 的账号没有用户名，也没有密码。
type Account struct {
	Username string
	Email    string
	GitHub   bool
}

func accountOf(a port.Account) Account {
	return Account{Username: a.Username, Email: a.Email, GitHub: a.GitHubID != 0}
}

// 删除会话之后，用这些会话的票据建立的房间连接随之断开。网关立即通知各实例，通知丢失时，连接最迟在网关的一个核对周期之后断开。
type Accounts struct {
	members       port.Members
	site          port.Site
	quota         port.AccountQuota
	pairings      port.PairingCodes
	verifications port.GitHubVerifications
	devices       port.LoginDevices
	gateway       *realtime.Gateway
}

func NewAccounts(members port.Members, site port.Site, quota port.AccountQuota, pairings port.PairingCodes, verifications port.GitHubVerifications, devices port.LoginDevices, gateway *realtime.Gateway) *Accounts {
	return &Accounts{members: members, site: site, quota: quota, pairings: pairings, verifications: verifications, devices: devices, gateway: gateway}
}

// 游客没有账号，ok 为 false。
func (a *Accounts) Account(ctx context.Context, memberID string) (account Account, ok bool, err error) {
	profile, err := a.members.AccountProfile(ctx, memberID)
	if err != nil {
		return Account{}, false, err
	}
	return Account{Username: profile.Username, Email: profile.Email, GitHub: profile.GitHub}, profile.Registered(), nil
}

// 给游客设置用户名和密码，成员和成员的会话都不变。只绑定了 GitHub 的账号也用 Register 补设用户名和密码，见 addCredentials。
// token 是发出请求的成员 token，username 和 password 已经按协议校验过格式。
func (a *Accounts) Register(ctx context.Context, memberID, token, clientIP, username, password string) (Account, error) {
	profile, err := a.members.AccountProfile(ctx, memberID)
	if err != nil {
		return Account{}, err
	}
	switch {
	case profile.Username != "":
		return Account{}, port.ErrAlreadyRegistered
	case profile.GitHub:
		return a.addCredentials(ctx, memberID, token, clientIP, username, password, profile.Email)
	}
	settings, err := a.site.Settings(ctx)
	if err != nil {
		return Account{}, err
	}
	if !settings.RegistrationOpen {
		return Account{}, ErrRegistrationClosed
	}
	if err := a.take(ctx, "register:ip:"+quotaAddr(clientIP), registerBurst, registerRefill); err != nil {
		return Account{}, err
	}
	hash, err := HashPassword(ctx, password)
	if err != nil {
		return Account{}, err
	}
	if err := a.members.Register(ctx, memberID, username, hash); err != nil {
		return Account{}, err
	}
	return Account{Username: username, Email: profile.Email, GitHub: profile.GitHub}, nil
}

// 拿到会话的人不能抢先给只绑定了 GitHub 的账号设成自己的密码，所以发出请求的会话要在 10 分钟内用账号绑定的 GitHub 确认过身份
// （GitHub.Exchange 的 verify）。补设之后，账号除了这个会话，其他会话都失效，这与修改密码相同，对方会随之下线。
// 补设不是注册新账号，注册关闭时照样可以。
func (a *Accounts) addCredentials(ctx context.Context, memberID, token, clientIP, username, password, email string) (Account, error) {
	tokenHash := hashSecret(token)
	verified, err := a.verifications.Verified(ctx, tokenHash)
	if err != nil {
		return Account{}, err
	}
	if !verified {
		return Account{}, ErrGitHubUnverified
	}
	if err := a.take(ctx, "register:ip:"+quotaAddr(clientIP), registerBurst, registerRefill); err != nil {
		return Account{}, err
	}
	hash, err := HashPassword(ctx, password)
	if err != nil {
		return Account{}, err
	}
	// 失败时（比如用户名已被占用）GitHub 确认仍然保留，换个用户名可以重试。同一个账号只能补设一次，由存储保证。
	deleted, err := a.members.AddCredentials(ctx, memberID, username, hash, tokenHash)
	if err != nil {
		return Account{}, err
	}
	a.gateway.RevokeSessions(ctx, deleted)
	// 账号已经有了用户名，这次确认再也用不上了。删不掉也只是等确认过期，不影响补设的结果。
	_ = a.verifications.DeleteVerification(ctx, tokenHash)
	return Account{Username: username, Email: email, GitHub: true}, nil
}

// current 是发出请求的成员，currentToken 是 current 的成员 token。只有游客能登录，成功后签发账号的新会话，游客的这个会话随即失效。
// device 是这台设备此前登录时拿到的登录设备凭证，没有时为空字符串，已经按协议校验过格式。成功时返回这台设备此后要带上的凭证。
// 带来的凭证属于这个账号，并且签发之后密码没变过，就续期并原样返回，否则签发新的。
func (a *Accounts) Login(ctx context.Context, current member.Member, currentToken, clientIP, username, password, device string) (Session, Account, string, error) {
	if err := a.requireGuest(ctx, current.ID); err != nil {
		return Session{}, Account{}, "", err
	}
	// 按 IP 限制一个来源的尝试次数，按用户名限制一个账号被尝试的次数，这样换着 IP 猜同一个账号也会受限。
	// 带着这个账号的凭证时改按凭证计数，这样别人反复猜密码耗尽了用户名额度，账号主人的设备仍然能登录。
	if err := a.take(ctx, "login:ip:"+quotaAddr(clientIP), loginIPBurst, loginIPRefill); err != nil {
		return Session{}, Account{}, "", err
	}
	known, err := a.knownDevice(ctx, device, username)
	if err != nil {
		return Session{}, Account{}, "", err
	}
	quotaKey := "login:user:" + username
	if known {
		quotaKey = "login:device:" + hex.EncodeToString(hashSecret(device))
	}
	if err := a.take(ctx, quotaKey, loginUserBurst, loginUserRefill); err != nil {
		return Session{}, Account{}, "", err
	}
	account, err := a.verify(ctx, username, password)
	if err != nil {
		return Session{}, Account{}, "", err
	}
	if !known {
		device, _ = newSecret()
	}
	// 先存凭证再签发会话。凭证存不了时，游客的会话还在，可以重试。签发会话失败时，留下的凭证到期自行失效。
	if err := a.devices.SaveDevice(ctx, hashSecret(device), account.Member.ID, passwordStamp(account.PasswordHash), loginDeviceTTL); err != nil {
		return Session{}, Account{}, "", err
	}
	session, err := a.signIn(ctx, currentToken, account)
	if errors.Is(err, port.ErrAccountPasswordChanged) {
		return Session{}, Account{}, "", ErrInvalidCredentials
	}
	if err != nil {
		return Session{}, Account{}, "", err
	}
	return session, accountOf(account), device, nil
}

// 未知、过期、属于别的账号，或者签发之后账号的密码变过（修改、重置、补设）的凭证，都当作没带。
func (a *Accounts) knownDevice(ctx context.Context, device, username string) (bool, error) {
	if device == "" {
		return false, nil
	}
	memberID, stamp, err := a.devices.DeviceMember(ctx, hashSecret(device))
	if errors.Is(err, port.ErrDeviceNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	account, err := a.members.AccountByUsername(ctx, username)
	if errors.Is(err, port.ErrMemberNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return account.Member.ID == memberID && bytes.Equal(stamp, passwordStamp(account.PasswordHash)), nil
}

// 登录设备凭证要在改密码之后失效，所以凭证绑定签发时的密码。否则知道旧密码的人可以反复登录，先囤下一批凭证，
// 等账号改了密码，每个凭证仍各有一份独立的猜测额度，按用户名的额度就被绕过了。凭证只存密码哈希的指纹，不存哈希本身。
func passwordStamp(passwordHash string) []byte {
	sum := sha256.Sum256([]byte(passwordHash))
	return sum[:16]
}

// 只有游客能登录，已经登录了账号的设备要先退出。
func (a *Accounts) requireGuest(ctx context.Context, memberID string) error {
	if _, registered, err := a.Account(ctx, memberID); err != nil {
		return err
	} else if registered {
		return ErrAlreadyLoggedIn
	}
	return nil
}

// 给发出请求的游客签发 account 的新会话，游客的这个会话随即失效。account.PasswordHash 是校验时读到的密码哈希。
// 如果此后密码被修改或重置过，返回 port.ErrAccountPasswordChanged，由调用方按各自的登录方式告诉用户。
func (a *Accounts) signIn(ctx context.Context, currentToken string, account port.Account) (Session, error) {
	token, hash := newSecret()
	guest := hashSecret(currentToken)
	err := a.members.ReplaceSession(ctx, guest, account.Member.ID, hash, account.PasswordHash)
	if errors.Is(err, port.ErrSessionNotFound) {
		return Session{}, ErrInvalidMemberToken
	}
	if err != nil {
		return Session{}, err
	}
	a.gateway.RevokeSessions(ctx, [][]byte{guest})
	return Session{MemberToken: token, Member: account.Member}, nil
}

func (a *Accounts) verify(ctx context.Context, username, password string) (port.Account, error) {
	account, err := a.members.AccountByUsername(ctx, username)
	// 空的密码哈希表示没有设置过密码，任何密码都不对。这时和用户名不存在一样，也做一次同样的计算。
	if errors.Is(err, port.ErrMemberNotFound) || (err == nil && account.PasswordHash == "") {
		dummy, err := dummyPasswordHash()
		if err != nil {
			return port.Account{}, err
		}
		if _, err := checkPassword(ctx, dummy, password); err != nil {
			return port.Account{}, err
		}
		return port.Account{}, ErrInvalidCredentials
	}
	if err != nil {
		return port.Account{}, err
	}
	ok, err := checkPassword(ctx, account.PasswordHash, password)
	if err != nil {
		return port.Account{}, err
	}
	if !ok {
		return port.Account{}, ErrInvalidCredentials
	}
	return account, nil
}

func (a *Accounts) Logout(ctx context.Context, token string) error {
	hash := hashSecret(token)
	if err := a.members.DeleteSession(ctx, hash); err != nil {
		return err
	}
	a.gateway.RevokeSessions(ctx, [][]byte{hash})
	return nil
}

// 修改成功后，账号除了发出请求的这个会话，其他会话都失效。
func (a *Accounts) ChangePassword(ctx context.Context, memberID, token, currentPassword, newPassword string) error {
	profile, err := a.members.AccountProfile(ctx, memberID)
	if err != nil {
		return err
	}
	username := profile.Username
	switch {
	case profile.GitHub && username == "":
		return port.ErrNoPassword
	case username == "":
		return ErrNotRegistered
	}
	account, err := a.checkCurrentPassword(ctx, memberID, username, currentPassword)
	if err != nil {
		return err
	}
	hash, err := HashPassword(ctx, newPassword)
	if err != nil {
		return err
	}
	// 校验之后密码可能已经变了。被重置或被别的设备改过时，发出请求的会话已经被删掉，按 token 失效处理。同一个会话先改过时，当前密码已经不对。
	deleted, err := a.members.ChangePassword(ctx, memberID, account.PasswordHash, hash, hashSecret(token))
	switch {
	case errors.Is(err, port.ErrSessionNotFound):
		return ErrInvalidMemberToken
	case errors.Is(err, port.ErrAccountPasswordChanged):
		return ErrWrongCurrentPassword
	case err != nil:
		return err
	}
	a.gateway.RevokeSessions(ctx, deleted)
	return nil
}

// 修改密码、绑定和解绑 GitHub 都要输入当前密码，不能只凭一个会话就更换登录凭证。否则拿到会话的人换上自己的凭证之后，
// 本人修改密码、后台重置密码都赶不走他。额度按成员计数，这几种操作共用。
func (a *Accounts) checkCurrentPassword(ctx context.Context, memberID, username, password string) (port.Account, error) {
	if err := a.take(ctx, "password:member:"+memberID, passwordBurst, passwordRefill); err != nil {
		return port.Account{}, err
	}
	account, err := a.verify(ctx, username, password)
	if errors.Is(err, ErrInvalidCredentials) {
		return port.Account{}, ErrWrongCurrentPassword
	}
	return account, err
}

// Find 和 ResetPassword 只供后台使用，调用方要先校验管理 token。
func (a *Accounts) Find(ctx context.Context, username string) (port.Account, error) {
	account, err := a.members.AccountByUsername(ctx, username)
	if errors.Is(err, port.ErrMemberNotFound) {
		return port.Account{}, ErrAccountNotFound
	}
	return account, err
}

// 生成随机的新密码，账号在所有设备上的会话随即失效。新密码只出现在返回值中。
func (a *Accounts) ResetPassword(ctx context.Context, username string) (string, error) {
	account, err := a.Find(ctx, username)
	if err != nil {
		return "", err
	}
	password := newPassword()
	hash, err := HashPassword(ctx, password)
	if err != nil {
		return "", err
	}
	deleted, err := a.members.SetPassword(ctx, account.Member.ID, hash)
	if err != nil {
		return "", err
	}
	a.gateway.RevokeSessions(ctx, deleted)
	return password, nil
}

// IPv6 客户端通常分到整个 /64，可以在里面随意换地址，所以按 /64 计数。clientIP 由适配器给出，解析不了时原样使用。
func quotaAddr(clientIP string) string {
	addr, err := netip.ParseAddr(clientIP)
	if err != nil {
		return clientIP
	}
	if addr = addr.Unmap(); addr.Is4() {
		return addr.String()
	}
	return netip.PrefixFrom(addr, 64).Masked().String()
}

func (a *Accounts) take(ctx context.Context, key string, burst int, refill time.Duration) error {
	wait, err := a.quota.Take(ctx, key, burst, refill)
	if err != nil {
		return err
	}
	if wait > 0 {
		return ErrTooManyAttempts
	}
	return nil
}
