package identity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/app/port/porttest"
	"github.com/dihemoe/alisten/internal/domain/member"
	"github.com/dihemoe/alisten/internal/domain/track"
)

const clientIP = "203.0.113.7"

type fakeQuota struct {
	mu        sync.Mutex
	taken     []string
	exhausted map[string]bool
}

func (q *fakeQuota) Take(_ context.Context, key string, burst int, refill time.Duration) (time.Duration, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if burst <= 0 || refill <= 0 {
		return 0, fmt.Errorf("额度参数不合理：burst=%d refill=%v", burst, refill)
	}
	q.taken = append(q.taken, key)
	if q.exhausted[key] {
		return time.Minute, nil
	}
	return 0, nil
}

// revoked 返回此前广播过的被删除的会话。
func newAccounts(t *testing.T) (a *Accounts, s *Service, store *porttest.Store, q *fakeQuota, revoked func() [][]byte) {
	t.Helper()
	store = porttest.New()
	node := porttest.NewRedis().Node("a")
	if err := node.SubscribeInstance(t.Context()); err != nil {
		t.Fatal(err)
	}
	gateway := newGateway(store, node, node)
	q = &fakeQuota{exhausted: map[string]bool{}}
	revoked = func() [][]byte {
		t.Helper()
		done, cancel := context.WithCancel(t.Context())
		cancel()
		var hashes [][]byte
		for {
			d, err := node.Receive(done)
			if err != nil {
				return hashes
			}
			if r, ok := d.(port.SessionsRevokedDelivery); ok {
				hashes = append(hashes, r.SessionTokenHashes...)
			}
		}
	}
	return NewAccounts(store, store, q, porttest.NewCodes(), porttest.NewDevices(), gateway), New(store, store, store, gateway), store, q, revoked
}

// 不计顺序。
func sameHashes(got [][]byte, tokens ...string) bool {
	want := make([]string, len(tokens))
	for i, token := range tokens {
		want[i] = string(hashSecret(token))
	}
	keys := make([]string, len(got))
	for i, h := range got {
		keys[i] = string(h)
	}
	slices.Sort(want)
	slices.Sort(keys)
	return slices.Equal(keys, want)
}

func issueTicket(t *testing.T, s *Service, session Session, roomID string) string {
	t.Helper()
	ticket, err := s.IssueTicket(t.Context(), session.Member.ID, session.MemberToken, roomID, "")
	if err != nil {
		t.Fatal(err)
	}
	return ticket
}

func ticketValid(t *testing.T, s *Service, ticket string) bool {
	t.Helper()
	_, err := s.VerifyTicket(t.Context(), ticket)
	if err != nil && !errors.Is(err, ErrInvalidTicket) {
		t.Fatal(err)
	}
	return err == nil
}

// 返回 token 当前所属的成员；token 已经失效时 ok 为 false。
func tokenOwner(t *testing.T, store *porttest.Store, token string) (m member.Member, ok bool) {
	t.Helper()
	m, err := store.MemberByToken(t.Context(), hashSecret(token))
	if errors.Is(err, port.ErrMemberNotFound) {
		return member.Member{}, false
	}
	if err != nil {
		t.Fatal(err)
	}
	return m, true
}

func registerAccount(t *testing.T, a *Accounts, s *Service, username, password string) Session {
	t.Helper()
	session := createMember(t, s)
	if _, err := a.Register(t.Context(), session.Member.ID, session.MemberToken, clientIP, username, password); err != nil {
		t.Fatal(err)
	}
	return session
}

func TestRegister(t *testing.T) {
	a, s, store, q, _ := newAccounts(t)
	guest := createMember(t, s)
	account, err := a.Register(t.Context(), guest.Member.ID, guest.MemberToken, clientIP, "alice", "correct horse")
	if err != nil || account != (Account{Username: "alice"}) {
		t.Fatalf("Register = %#v, %v", account, err)
	}
	if got, ok, err := a.Account(t.Context(), guest.Member.ID); err != nil || !ok || got != account {
		t.Errorf("注册之后 Account = %#v, %v, %v", got, ok, err)
	}
	if m, ok := tokenOwner(t, store, guest.MemberToken); !ok || m != guest.Member {
		t.Error("注册不更换会话：原来的成员 token 应仍然有效")
	}
	stored, err := store.AccountByUsername(t.Context(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := checkPassword(t.Context(), stored.PasswordHash, "correct horse"); err != nil || !ok {
		t.Errorf("存储中应是密码的 argon2id 哈希：%v, %v", ok, err)
	}
	if want := []string{"register:ip:" + clientIP}; !slices.Equal(q.taken, want) {
		t.Errorf("取的额度 = %v，期望 %v", q.taken, want)
	}
}

func TestAccountKeepsEmail(t *testing.T) {
	a, s, _, _, _ := newAccounts(t)
	alice := registerAccount(t, a, s, "alice", "correct horse")
	guest := createMember(t, s)

	if _, err := s.UpdateProfile(t.Context(), alice.Member.ID, Profile{Email: ptr(" Alice@Example.COM ")}); err != nil {
		t.Fatal(err)
	}
	if got, ok, err := a.Account(t.Context(), alice.Member.ID); err != nil || !ok || got != (Account{Username: "alice", Email: "Alice@Example.COM"}) {
		t.Errorf("保存邮箱之后 Account = %#v, %v, %v", got, ok, err)
	}
	other := createMember(t, s)
	_, account, _, err := a.Login(t.Context(), other.Member, other.MemberToken, clientIP, "alice", "correct horse", "")
	if err != nil || account != (Account{Username: "alice", Email: "Alice@Example.COM"}) {
		t.Errorf("另一台设备登录得到 %#v, %v，期望带邮箱", account, err)
	}
	if _, err := s.UpdateProfile(t.Context(), alice.Member.ID, Profile{Email: ptr("  ")}); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := a.Account(t.Context(), alice.Member.ID); got.Email != "" {
		t.Errorf("清除之后邮箱 = %q", got.Email)
	}

	if _, err := s.UpdateProfile(t.Context(), guest.Member.ID, Profile{Email: ptr("g@example.com")}); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := a.Account(t.Context(), guest.Member.ID); ok {
		t.Error("游客设置邮箱之后仍然没有账号")
	}
}

func TestGuestHasNoAccount(t *testing.T) {
	a, s, _, _, _ := newAccounts(t)
	if _, ok, err := a.Account(t.Context(), createMember(t, s).Member.ID); err != nil || ok {
		t.Errorf("游客的 Account = %v, %v，期望没有账号", ok, err)
	}
}

func TestRegisterRejected(t *testing.T) {
	a, s, store, q, _ := newAccounts(t)
	alice := registerAccount(t, a, s, "alice", "correct horse")
	other := createMember(t, s)
	if _, err := a.Register(t.Context(), other.Member.ID, other.MemberToken, clientIP, "alice", "another horse"); !errors.Is(err, port.ErrUsernameTaken) {
		t.Errorf("用户名已被占用时 = %v，期望 ErrUsernameTaken", err)
	}
	if _, err := a.Register(t.Context(), alice.Member.ID, alice.MemberToken, clientIP, "alice2", "another horse"); !errors.Is(err, port.ErrAlreadyRegistered) {
		t.Errorf("已经注册过时 = %v，期望 ErrAlreadyRegistered", err)
	}

	guest := createMember(t, s)
	q.exhausted["register:ip:"+clientIP] = true
	if _, err := a.Register(t.Context(), guest.Member.ID, guest.MemberToken, clientIP, "bob", "correct horse"); !errors.Is(err, ErrTooManyAttempts) {
		t.Errorf("额度用完时 = %v，期望 ErrTooManyAttempts", err)
	}
	if _, ok, _ := a.Account(t.Context(), guest.Member.ID); ok {
		t.Error("额度用完时不应注册")
	}

	if err := store.SetRegistrationOpen(t.Context(), false); err != nil {
		t.Fatal(err)
	}
	q.taken = nil
	if _, err := a.Register(t.Context(), guest.Member.ID, guest.MemberToken, "198.51.100.1", "bob", "correct horse"); !errors.Is(err, ErrRegistrationClosed) {
		t.Errorf("关闭注册时 = %v，期望 ErrRegistrationClosed", err)
	}
	if len(q.taken) != 0 {
		t.Errorf("关闭注册时不应消耗额度：%v", q.taken)
	}
}

func TestLogin(t *testing.T) {
	a, s, store, q, revoked := newAccounts(t)
	alice := registerAccount(t, a, s, "alice", "correct horse")
	guest := createMember(t, s)
	id := createRoom(t, store, "")
	guestTicket, aliceTicket := issueTicket(t, s, guest, id), issueTicket(t, s, alice, id)
	q.taken = nil
	session, account, _, err := a.Login(t.Context(), guest.Member, guest.MemberToken, clientIP, "alice", "correct horse", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := revoked(); !sameHashes(got, guest.MemberToken) {
		t.Errorf("广播的被删除的会话 = %x，期望游客原来的会话", got)
	}
	if ticketValid(t, s, guestTicket) || !ticketValid(t, s, aliceTicket) {
		t.Error("登录之后只有游客原来的会话的票据失效")
	}
	if session.Member != alice.Member || account != (Account{Username: "alice"}) {
		t.Errorf("Login = %#v, %#v，期望登录 alice", session.Member, account)
	}
	if session.MemberToken == alice.MemberToken || session.MemberToken == guest.MemberToken {
		t.Error("登录应签发新的成员 token")
	}
	if m, ok := tokenOwner(t, store, session.MemberToken); !ok || m != alice.Member {
		t.Errorf("新 token 属于 %#v，期望 alice", m)
	}
	if _, ok := tokenOwner(t, store, guest.MemberToken); ok {
		t.Error("登录之后游客原来的 token 应失效")
	}
	if _, ok := tokenOwner(t, store, alice.MemberToken); !ok {
		t.Error("登录不影响账号在其他设备上的会话")
	}
	if want := []string{"login:ip:" + clientIP, "login:user:alice"}; !slices.Equal(q.taken, want) {
		t.Errorf("取的额度 = %v，期望 %v", q.taken, want)
	}
}

func TestLoginMovesGuestListensIntoAccount(t *testing.T) {
	a, s, store, _, _ := newAccounts(t)
	alice := registerAccount(t, a, s, "alice", "correct horse")
	guest := createMember(t, s)
	heard := time.Date(2026, 11, 10, 20, 0, 0, 0, time.UTC)
	sunny := port.Listen{Track: track.Track{Source: track.NetEase, ID: "186016"}, Meta: track.Meta{Title: "晴天"}, HeardAt: heard}
	rain := port.Listen{Track: track.Track{Source: track.NetEase, ID: "185811"}, Meta: track.Meta{Title: "七里香"}, HeardAt: heard.Add(time.Minute)}
	if err := store.RecordListen(t.Context(), []string{alice.Member.ID}, sunny); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordListen(t.Context(), []string{guest.Member.ID}, rain); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := a.Login(t.Context(), guest.Member, guest.MemberToken, clientIP, "alice", "correct horse", ""); err != nil {
		t.Fatal(err)
	}
	if listens, err := store.MemberListens(t.Context(), alice.Member.ID); err != nil || !slices.Equal(listens, []port.Listen{rain, sunny}) {
		t.Errorf("登录之后账号的历史 = %#v, %v，期望并入游客听过的《七里香》", listens, err)
	}
	if listens, err := store.MemberListens(t.Context(), guest.Member.ID); err != nil || len(listens) != 0 {
		t.Errorf("登录之后游客的历史 = %#v, %v，期望移走", listens, err)
	}
}

func TestLoginRejected(t *testing.T) {
	a, s, store, q, _ := newAccounts(t)
	alice := registerAccount(t, a, s, "alice", "correct horse")
	guest := createMember(t, s)
	for _, tc := range []struct{ name, username, password string }{
		{"密码错误", "alice", "wrong horse"},
		{"没有这个用户名", "nobody", "correct horse"},
	} {
		if _, _, _, err := a.Login(t.Context(), guest.Member, guest.MemberToken, clientIP, tc.username, tc.password, ""); !errors.Is(err, ErrInvalidCredentials) {
			t.Errorf("%s：Login = %v，期望 ErrInvalidCredentials", tc.name, err)
		}
	}
	if _, ok := tokenOwner(t, store, guest.MemberToken); !ok {
		t.Error("登录失败时游客的 token 应仍然有效")
	}
	if _, _, _, err := a.Login(t.Context(), alice.Member, alice.MemberToken, clientIP, "alice", "correct horse", ""); !errors.Is(err, ErrAlreadyLoggedIn) {
		t.Errorf("已注册的成员登录 = %v，期望 ErrAlreadyLoggedIn", err)
	}
	q.exhausted["login:user:alice"] = true
	if _, _, _, err := a.Login(t.Context(), guest.Member, guest.MemberToken, "198.51.100.1", "alice", "correct horse", ""); !errors.Is(err, ErrTooManyAttempts) {
		t.Errorf("用户名的额度用完时换一个 IP = %v，期望 ErrTooManyAttempts", err)
	}
	q.exhausted["login:ip:"+clientIP] = true
	if _, _, _, err := a.Login(t.Context(), guest.Member, guest.MemberToken, clientIP, "bob", "correct horse", ""); !errors.Is(err, ErrTooManyAttempts) {
		t.Errorf("IP 的额度用完时换一个用户名 = %v，期望 ErrTooManyAttempts", err)
	}
}

type deviceSave struct {
	hash     string
	memberID string
	stamp    string
	ttl      time.Duration
}

// 记下每次保存的登录设备凭证；fail 不为 nil 时保存失败。
type recordingDevices struct {
	*porttest.Devices
	saves []deviceSave
	fail  error
}

func (d *recordingDevices) SaveDevice(ctx context.Context, deviceHash []byte, memberID string, passwordStamp []byte, ttl time.Duration) error {
	if d.fail != nil {
		return d.fail
	}
	d.saves = append(d.saves, deviceSave{hash: string(deviceHash), memberID: memberID, stamp: string(passwordStamp), ttl: ttl})
	return d.Devices.SaveDevice(ctx, deviceHash, memberID, passwordStamp, ttl)
}

func newDeviceAccounts(t *testing.T) (*Accounts, *Service, *porttest.Store, *fakeQuota, *recordingDevices) {
	t.Helper()
	store := porttest.New()
	node := porttest.NewRedis().Node("a")
	gateway := newGateway(store, node, node)
	q := &fakeQuota{exhausted: map[string]bool{}}
	devices := &recordingDevices{Devices: porttest.NewDevices()}
	return NewAccounts(store, store, q, porttest.NewCodes(), devices, gateway), New(store, store, store, gateway), store, q, devices
}

func deviceQuota(device string) string {
	return "login:device:" + hex.EncodeToString(hashSecret(device))
}

// 账号当前密码哈希的指纹，即 SHA-256 的前 16 字节：凭证只存它，不存密码哈希本身。
func passwordStampOf(t *testing.T, store *porttest.Store, username string) string {
	t.Helper()
	account, err := store.AccountByUsername(t.Context(), username)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(account.PasswordHash))
	return string(sum[:16])
}

// 登录成功时签发绑定账号的凭证；带着它再登录，扣的是凭证自己的额度，返回同一个凭证并重新计算 180 天的有效期。
func TestLoginIssuesDevice(t *testing.T) {
	a, s, store, q, devices := newDeviceAccounts(t)
	alice := registerAccount(t, a, s, "alice", "correct horse")
	guest := createMember(t, s)
	_, _, device, err := a.Login(t.Context(), guest.Member, guest.MemberToken, clientIP, "alice", "correct horse", "")
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`).MatchString(device) {
		t.Fatalf("凭证 %q 应是 43 个 base64url 字符", device)
	}
	issued := deviceSave{hash: string(hashSecret(device)), memberID: alice.Member.ID, stamp: passwordStampOf(t, store, "alice"), ttl: 180 * 24 * time.Hour}
	if !slices.Equal(devices.saves, []deviceSave{issued}) {
		t.Errorf("保存的凭证 = %#v，期望只保存凭证的哈希，绑定 alice 和她当前密码的指纹，有效期 180 天", devices.saves)
	}

	q.taken = nil
	next := createMember(t, s)
	session, _, again, err := a.Login(t.Context(), next.Member, next.MemberToken, clientIP, "alice", "correct horse", device)
	if err != nil || session.Member != alice.Member {
		t.Fatalf("带凭证登录 = %#v, %v", session.Member, err)
	}
	if again != device {
		t.Errorf("带凭证登录返回 %q，期望同一个凭证 %q", again, device)
	}
	if want := []string{"login:ip:" + clientIP, deviceQuota(device)}; !slices.Equal(q.taken, want) {
		t.Errorf("取的额度 = %v，期望 %v：带着这个账号的凭证不扣用户名额度", q.taken, want)
	}
	if !slices.Equal(devices.saves, []deviceSave{issued, issued}) {
		t.Errorf("保存的凭证 = %#v，期望续期同一个凭证", devices.saves)
	}

	other := createMember(t, s)
	if _, _, fresh, err := a.Login(t.Context(), other.Member, other.MemberToken, clientIP, "alice", "correct horse", ""); err != nil || fresh == device || fresh == "" {
		t.Errorf("不带凭证登录返回 %q, %v，期望签发新的凭证", fresh, err)
	}
}

// 别人反复猜密码耗尽了账号的用户名额度，带着这个账号的凭证的设备仍然能登录。
func TestLoginWithDeviceWhileUsernameQuotaExhausted(t *testing.T) {
	a, s, _, q, _ := newDeviceAccounts(t)
	alice := registerAccount(t, a, s, "alice", "correct horse")
	guest := createMember(t, s)
	_, _, device, err := a.Login(t.Context(), guest.Member, guest.MemberToken, clientIP, "alice", "correct horse", "")
	if err != nil {
		t.Fatal(err)
	}
	q.exhausted["login:user:alice"] = true
	next := createMember(t, s)
	if _, _, _, err := a.Login(t.Context(), next.Member, next.MemberToken, clientIP, "alice", "correct horse", ""); !errors.Is(err, ErrTooManyAttempts) {
		t.Fatalf("用户名额度用完时不带凭证 = %v，期望 ErrTooManyAttempts", err)
	}
	session, _, _, err := a.Login(t.Context(), next.Member, next.MemberToken, clientIP, "alice", "correct horse", device)
	if err != nil || session.Member != alice.Member {
		t.Errorf("用户名额度用完时带凭证 = %#v, %v，期望登录 alice", session.Member, err)
	}
}

// 未知的、别的账号的凭证都当作没带：扣用户名额度，成功时签发这个账号的新凭证。
func TestLoginWithForeignDeviceUsesUsernameQuota(t *testing.T) {
	a, s, store, q, devices := newDeviceAccounts(t)
	alice := registerAccount(t, a, s, "alice", "correct horse")
	registerAccount(t, a, s, "bob", "battery staple")
	guest := createMember(t, s)
	_, _, bobDevice, err := a.Login(t.Context(), guest.Member, guest.MemberToken, clientIP, "bob", "battery staple", "")
	if err != nil {
		t.Fatal(err)
	}
	unknown, _ := newSecret()
	for _, tc := range []struct{ name, device string }{{"bob 的凭证", bobDevice}, {"未知的凭证", unknown}} {
		q.taken, devices.saves = nil, nil
		next := createMember(t, s)
		session, _, device, err := a.Login(t.Context(), next.Member, next.MemberToken, clientIP, "alice", "correct horse", tc.device)
		if err != nil || session.Member != alice.Member {
			t.Fatalf("%s：Login = %#v, %v，期望登录 alice", tc.name, session.Member, err)
		}
		if want := []string{"login:ip:" + clientIP, "login:user:alice"}; !slices.Equal(q.taken, want) {
			t.Errorf("%s：取的额度 = %v，期望 %v", tc.name, q.taken, want)
		}
		if device == tc.device || !slices.Equal(devices.saves, []deviceSave{{hash: string(hashSecret(device)), memberID: alice.Member.ID, stamp: passwordStampOf(t, store, "alice"), ttl: 180 * 24 * time.Hour}}) {
			t.Errorf("%s：返回 %q，保存的凭证 = %#v，期望签发绑定 alice 的新凭证", tc.name, device, devices.saves)
		}
	}
	if member, _, err := devices.DeviceMember(t.Context(), hashSecret(bobDevice)); err != nil || member == alice.Member.ID {
		t.Errorf("bob 的凭证属于 %q, %v，期望不被改绑", member, err)
	}

	q.exhausted["login:user:alice"] = true
	next := createMember(t, s)
	if _, _, _, err := a.Login(t.Context(), next.Member, next.MemberToken, clientIP, "alice", "correct horse", bobDevice); !errors.Is(err, ErrTooManyAttempts) {
		t.Errorf("用户名额度用完时带 bob 的凭证登录 alice = %v，期望 ErrTooManyAttempts", err)
	}
}

func TestLoginDeviceQuotaExhausted(t *testing.T) {
	a, s, _, q, _ := newDeviceAccounts(t)
	registerAccount(t, a, s, "alice", "correct horse")
	guest := createMember(t, s)
	_, _, device, err := a.Login(t.Context(), guest.Member, guest.MemberToken, clientIP, "alice", "correct horse", "")
	if err != nil {
		t.Fatal(err)
	}
	q.exhausted[deviceQuota(device)] = true
	q.taken = nil
	next := createMember(t, s)
	if _, _, _, err := a.Login(t.Context(), next.Member, next.MemberToken, clientIP, "alice", "correct horse", device); !errors.Is(err, ErrTooManyAttempts) {
		t.Errorf("凭证额度用完时 = %v，期望 ErrTooManyAttempts", err)
	}
	if want := []string{"login:ip:" + clientIP, deviceQuota(device)}; !slices.Equal(q.taken, want) {
		t.Errorf("取的额度 = %v，期望 %v：凭证额度用完时不改扣用户名额度", q.taken, want)
	}
}

func TestLoginFailureIssuesNoDevice(t *testing.T) {
	a, s, _, _, devices := newDeviceAccounts(t)
	registerAccount(t, a, s, "alice", "correct horse")
	guest := createMember(t, s)
	_, _, device, err := a.Login(t.Context(), guest.Member, guest.MemberToken, clientIP, "alice", "correct horse", "")
	if err != nil {
		t.Fatal(err)
	}
	devices.saves = nil
	next := createMember(t, s)
	for _, tc := range []struct{ name, username, device string }{{"不带凭证", "nobody", ""}, {"带着凭证", "alice", device}} {
		if _, _, got, err := a.Login(t.Context(), next.Member, next.MemberToken, clientIP, tc.username, "wrong horse", tc.device); !errors.Is(err, ErrInvalidCredentials) || got != "" {
			t.Errorf("%s：密码错误时 Login 返回凭证 %q, %v，期望不返回凭证", tc.name, got, err)
		}
	}
	if len(devices.saves) != 0 {
		t.Errorf("登录失败时保存了凭证：%#v", devices.saves)
	}
}

// 凭证要在签发会话之前存好：存不了时整个登录失败，游客的会话不变，可以重试。
func TestLoginFailsWhenDeviceCannotBeSaved(t *testing.T) {
	a, s, store, _, devices := newDeviceAccounts(t)
	registerAccount(t, a, s, "alice", "correct horse")
	guest := createMember(t, s)
	devices.fail = porttest.ErrRedisDown
	if _, _, _, err := a.Login(t.Context(), guest.Member, guest.MemberToken, clientIP, "alice", "correct horse", ""); !errors.Is(err, porttest.ErrRedisDown) {
		t.Fatalf("凭证存不了时 Login = %v，期望 ErrRedisDown", err)
	}
	if m, ok := tokenOwner(t, store, guest.MemberToken); !ok || m != guest.Member {
		t.Error("凭证存不了时不应签发会话，游客的会话应仍然有效")
	}
	devices.fail = nil
	if _, _, _, err := a.Login(t.Context(), guest.Member, guest.MemberToken, clientIP, "alice", "correct horse", ""); err != nil {
		t.Errorf("恢复之后重试 = %v，期望成功", err)
	}
}

// 知道旧密码的人反复登录囤下的凭证，在修改、重置密码之后都当作没带：用户名额度用完时一律被限流。
// 用新密码登录成功时签发新的凭证；没改过密码的账号，凭证照常有效。
func TestLoginDeviceExpiresWhenPasswordChanges(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(t *testing.T, a *Accounts, alice Session) string
	}{
		{"修改密码", func(t *testing.T, a *Accounts, alice Session) string {
			if err := a.ChangePassword(t.Context(), alice.Member.ID, alice.MemberToken, "correct horse", "new horse"); err != nil {
				t.Fatal(err)
			}
			return "new horse"
		}},
		{"重置密码", func(t *testing.T, a *Accounts, _ Session) string {
			password, err := a.ResetPassword(t.Context(), "alice")
			if err != nil {
				t.Fatal(err)
			}
			return password
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, s, _, q, _ := newDeviceAccounts(t)
			alice := registerAccount(t, a, s, "alice", "correct horse")
			bob := registerAccount(t, a, s, "bob", "battery staple")
			login := func(username, password, device string) (Session, string, error) {
				guest := createMember(t, s)
				session, _, issued, err := a.Login(t.Context(), guest.Member, guest.MemberToken, clientIP, username, password, device)
				return session, issued, err
			}
			var hoarded []string
			for range 3 {
				_, device, err := login("alice", "correct horse", "")
				if err != nil {
					t.Fatal(err)
				}
				hoarded = append(hoarded, device)
			}
			_, bobDevice, err := login("bob", "battery staple", "")
			if err != nil {
				t.Fatal(err)
			}

			password := tc.change(t, a, alice)
			q.exhausted["login:user:alice"] = true
			q.exhausted["login:user:bob"] = true
			for i, device := range hoarded {
				q.taken = nil
				if _, _, err := login("alice", "wrong horse", device); !errors.Is(err, ErrTooManyAttempts) {
					t.Errorf("第 %d 个旧凭证猜新密码 = %v，期望 ErrTooManyAttempts", i, err)
				}
				if want := []string{"login:ip:" + clientIP, "login:user:alice"}; !slices.Equal(q.taken, want) {
					t.Errorf("第 %d 个旧凭证取的额度 = %v，期望 %v", i, q.taken, want)
				}
			}
			q.taken = nil
			if session, _, err := login("bob", "battery staple", bobDevice); err != nil || session.Member != bob.Member {
				t.Errorf("bob 的密码没变，带凭证登录 = %#v, %v，期望登录 bob", session.Member, err)
			}
			if want := []string{"login:ip:" + clientIP, deviceQuota(bobDevice)}; !slices.Equal(q.taken, want) {
				t.Errorf("bob 带凭证取的额度 = %v，期望 %v", q.taken, want)
			}

			delete(q.exhausted, "login:user:alice")
			session, renewed, err := login("alice", password, hoarded[0])
			if err != nil || session.Member != alice.Member {
				t.Fatalf("用新密码带旧凭证登录 = %#v, %v，期望登录 alice", session.Member, err)
			}
			if renewed == hoarded[0] {
				t.Error("用新密码带旧凭证登录返回了旧凭证，期望签发新的")
			}
			q.exhausted["login:user:alice"] = true
			q.taken = nil
			if _, again, err := login("alice", password, renewed); err != nil || again != renewed {
				t.Errorf("用户名额度用完时带新凭证登录 = %q, %v，期望登录并返回同一个凭证", again, err)
			}
			if want := []string{"login:ip:" + clientIP, deviceQuota(renewed)}; !slices.Equal(q.taken, want) {
				t.Errorf("带新凭证取的额度 = %v，期望 %v", q.taken, want)
			}
		})
	}
}

func TestAccountQuotaCountsIPv6ByNetwork(t *testing.T) {
	a, s, _, q, _ := newAccounts(t)
	guest := createMember(t, s)
	q.exhausted["login:ip:2001:db8:1:2::/64"] = true
	for _, tc := range []struct {
		ip, key string
		want    error
	}{
		{"2001:db8:1:2::1", "login:ip:2001:db8:1:2::/64", ErrTooManyAttempts},
		{"2001:db8:1:2:aaaa:bbbb:cccc:dddd", "login:ip:2001:db8:1:2::/64", ErrTooManyAttempts},
		{"2001:db8:1:3::1", "login:ip:2001:db8:1:3::/64", ErrInvalidCredentials},
		{"203.0.113.7", "login:ip:203.0.113.7", ErrInvalidCredentials},
		{"::ffff:203.0.113.7", "login:ip:203.0.113.7", ErrInvalidCredentials},
	} {
		q.taken = nil
		_, _, _, err := a.Login(t.Context(), guest.Member, guest.MemberToken, tc.ip, "nobody", "wrong horse", "")
		if !errors.Is(err, tc.want) {
			t.Errorf("从 %s 登录 = %v，期望 %v", tc.ip, err, tc.want)
		}
		if len(q.taken) == 0 || q.taken[0] != tc.key {
			t.Errorf("从 %s 登录取的额度 = %v，期望先取 %s", tc.ip, q.taken, tc.key)
		}
	}

	q.taken = nil
	if _, err := a.Register(t.Context(), guest.Member.ID, guest.MemberToken, "2001:db8:1:2:aaaa:bbbb:cccc:dddd", "alice", "correct horse"); err != nil {
		t.Fatal(err)
	}
	if want := []string{"register:ip:2001:db8:1:2::/64"}; !slices.Equal(q.taken, want) {
		t.Errorf("从 IPv6 地址注册取的额度 = %v，期望 %v", q.taken, want)
	}
}

func TestLogout(t *testing.T) {
	a, s, store, _, revoked := newAccounts(t)
	alice := registerAccount(t, a, s, "alice", "correct horse")
	id := createRoom(t, store, "")
	aliceTicket := issueTicket(t, s, alice, id)
	revoked()
	if err := a.Logout(t.Context(), alice.MemberToken); err != nil {
		t.Fatal(err)
	}
	if _, ok := tokenOwner(t, store, alice.MemberToken); ok {
		t.Error("退出登录之后 token 应失效")
	}
	if got := revoked(); !sameHashes(got, alice.MemberToken) {
		t.Errorf("退出登录之后广播的会话 = %x，期望当前会话", got)
	}
	if ticketValid(t, s, aliceTicket) {
		t.Error("退出登录之后这个会话的票据应失效")
	}
}

func TestChangePassword(t *testing.T) {
	a, s, store, q, revoked := newAccounts(t)
	alice := registerAccount(t, a, s, "alice", "correct horse")
	guest := createMember(t, s)
	other, _, _, err := a.Login(t.Context(), guest.Member, guest.MemberToken, clientIP, "alice", "correct horse", "")
	if err != nil {
		t.Fatal(err)
	}
	id := createRoom(t, store, "")
	aliceTicket, otherTicket := issueTicket(t, s, alice, id), issueTicket(t, s, other, id)
	revoked()
	q.taken = nil
	if err := a.ChangePassword(t.Context(), alice.Member.ID, alice.MemberToken, "wrong horse", "battery staple"); !errors.Is(err, ErrWrongCurrentPassword) {
		t.Errorf("当前密码错误时 = %v，期望 ErrWrongCurrentPassword", err)
	}
	if err := a.ChangePassword(t.Context(), alice.Member.ID, alice.MemberToken, "correct horse", "battery staple"); err != nil {
		t.Fatal(err)
	}
	if want := []string{"password:member:" + alice.Member.ID, "password:member:" + alice.Member.ID}; !slices.Equal(q.taken, want) {
		t.Errorf("取的额度 = %v，期望 %v", q.taken, want)
	}
	if _, ok := tokenOwner(t, store, alice.MemberToken); !ok {
		t.Error("修改密码不影响发出请求的会话")
	}
	if _, ok := tokenOwner(t, store, other.MemberToken); ok {
		t.Error("修改密码之后其他会话应失效")
	}
	if got := revoked(); !sameHashes(got, other.MemberToken) {
		t.Errorf("修改密码之后广播的会话 = %x，期望其他设备的会话", got)
	}
	if !ticketValid(t, s, aliceTicket) || ticketValid(t, s, otherTicket) {
		t.Error("修改密码之后只有其他设备的票据失效")
	}
	next := createMember(t, s)
	if _, _, _, err := a.Login(t.Context(), next.Member, next.MemberToken, clientIP, "alice", "correct horse", ""); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("用旧密码登录 = %v，期望 ErrInvalidCredentials", err)
	}
	if _, _, _, err := a.Login(t.Context(), next.Member, next.MemberToken, clientIP, "alice", "battery staple", ""); err != nil {
		t.Errorf("用新密码登录 = %v", err)
	}
	stranger := createMember(t, s)
	if err := a.ChangePassword(t.Context(), stranger.Member.ID, stranger.MemberToken, "correct horse", "battery staple"); !errors.Is(err, ErrNotRegistered) {
		t.Errorf("游客修改密码 = %v，期望 ErrNotRegistered", err)
	}
}

func TestResetPassword(t *testing.T) {
	a, s, store, _, revoked := newAccounts(t)
	alice := registerAccount(t, a, s, "alice", "correct horse")
	aliceTicket := issueTicket(t, s, alice, createRoom(t, store, ""))
	if _, err := a.Find(t.Context(), "nobody"); !errors.Is(err, ErrAccountNotFound) {
		t.Errorf("Find 不存在的用户名 = %v，期望 ErrAccountNotFound", err)
	}
	if _, err := a.ResetPassword(t.Context(), "nobody"); !errors.Is(err, ErrAccountNotFound) {
		t.Errorf("ResetPassword 不存在的用户名 = %v，期望 ErrAccountNotFound", err)
	}
	if found, err := a.Find(t.Context(), "alice"); err != nil || found.Member != alice.Member || found.Username != "alice" {
		t.Errorf("Find = %#v, %v", found, err)
	}
	password, err := a.ResetPassword(t.Context(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[A-Za-z0-9_-]{16}$`).MatchString(password) {
		t.Errorf("新密码 %q 应是 16 个 base64url 字符", password)
	}
	if _, ok := tokenOwner(t, store, alice.MemberToken); ok {
		t.Error("重置密码之后账号的所有会话应失效")
	}
	if got := revoked(); !sameHashes(got, alice.MemberToken) {
		t.Errorf("重置密码之后广播的会话 = %x，期望账号的所有会话", got)
	}
	if ticketValid(t, s, aliceTicket) {
		t.Error("重置密码之后账号的票据应失效")
	}
	guest := createMember(t, s)
	if _, _, _, err := a.Login(t.Context(), guest.Member, guest.MemberToken, clientIP, "alice", password, ""); err != nil {
		t.Errorf("用重置后的密码登录 = %v", err)
	}
}

type noRevocation struct {
	port.Bus
}

func (noRevocation) PublishSessionsRevoked(context.Context, [][]byte) error {
	return porttest.ErrRedisDown
}

// 会话已经删除，断开的通知发不出去时照常成功：用这些会话的票据建立的连接由网关定期核对凭证时断开。
func TestAccountOperationsSucceedWhenRevocationFails(t *testing.T) {
	store := porttest.New()
	node := porttest.NewRedis().Node("a")
	gateway := newGateway(store, node, noRevocation{node})
	s := New(store, store, store, gateway)
	a := NewAccounts(store, store, &fakeQuota{exhausted: map[string]bool{}}, porttest.NewCodes(), porttest.NewDevices(), gateway)
	alice := registerAccount(t, a, s, "alice", "correct horse")

	guest := createMember(t, s)
	other, _, _, err := a.Login(t.Context(), guest.Member, guest.MemberToken, clientIP, "alice", "correct horse", "")
	if err != nil {
		t.Fatalf("广播失败时 Login = %v，期望成功", err)
	}
	if _, ok := tokenOwner(t, store, guest.MemberToken); ok {
		t.Error("游客原来的会话应已经删除")
	}
	if m, ok := tokenOwner(t, store, other.MemberToken); !ok || m.ID != alice.Member.ID {
		t.Error("新会话应已经签发")
	}

	if err := a.ChangePassword(t.Context(), alice.Member.ID, alice.MemberToken, "correct horse", "battery staple"); err != nil {
		t.Errorf("广播失败时 ChangePassword = %v，期望成功", err)
	}
	if _, ok := tokenOwner(t, store, other.MemberToken); ok {
		t.Error("修改密码之后其他设备的会话应已经删除")
	}

	password, err := a.ResetPassword(t.Context(), "alice")
	if err != nil || password == "" {
		t.Fatalf("广播失败时 ResetPassword = %q, %v，期望返回新密码", password, err)
	}
	if _, ok := tokenOwner(t, store, alice.MemberToken); ok {
		t.Error("重置密码之后账号的所有会话应已经删除")
	}
	next := createMember(t, s)
	session, _, _, err := a.Login(t.Context(), next.Member, next.MemberToken, clientIP, "alice", password, "")
	if err != nil {
		t.Fatalf("用返回的新密码登录 = %v", err)
	}

	if err := a.Logout(t.Context(), session.MemberToken); err != nil {
		t.Errorf("广播失败时 Logout = %v，期望成功", err)
	}
	if _, ok := tokenOwner(t, store, session.MemberToken); ok {
		t.Error("退出的会话应已经删除")
	}
}

// 在登录校验密码之后、签发新会话之前修改账号的密码。
type passwordChangedMembers struct {
	*porttest.Store
}

func (s passwordChangedMembers) ReplaceSession(ctx context.Context, oldTokenHash []byte, memberID string, newTokenHash []byte, passwordHash string) error {
	if _, err := s.SetPassword(ctx, memberID, "changed"); err != nil {
		return err
	}
	return s.Store.ReplaceSession(ctx, oldTokenHash, memberID, newTokenHash, passwordHash)
}

func TestLoginRejectedWhenGuestSessionOrPasswordChangesMeanwhile(t *testing.T) {
	a, s, store, _, _ := newAccounts(t)
	registerAccount(t, a, s, "alice", "correct horse")
	guest := createMember(t, s)
	if err := store.DeleteSession(t.Context(), hashSecret(guest.MemberToken)); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := a.Login(t.Context(), guest.Member, guest.MemberToken, clientIP, "alice", "correct horse", ""); !errors.Is(err, ErrInvalidMemberToken) {
		t.Errorf("游客的会话在校验之后失效，Login = %v，期望 ErrInvalidMemberToken", err)
	}

	node := porttest.NewRedis().Node("a")
	changing := NewAccounts(passwordChangedMembers{store}, store, &fakeQuota{exhausted: map[string]bool{}}, porttest.NewCodes(), porttest.NewDevices(), newGateway(store, node, node))
	guest = createMember(t, s)
	if _, _, _, err := changing.Login(t.Context(), guest.Member, guest.MemberToken, clientIP, "alice", "correct horse", ""); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("密码在校验之后被修改，Login = %v，期望 ErrInvalidCredentials", err)
	}
	if _, ok := tokenOwner(t, store, guest.MemberToken); !ok {
		t.Error("登录失败时游客的 token 应仍然有效")
	}
}

// 校验当前密码读到旧的密码哈希之后，先执行 meanwhile 再继续。
type passwordReadMembers struct {
	*porttest.Store
	meanwhile func()
}

func (s passwordReadMembers) AccountByUsername(ctx context.Context, username string) (port.Account, error) {
	account, err := s.Store.AccountByUsername(ctx, username)
	s.meanwhile()
	return account, err
}

func TestChangePasswordRejectedWhenPasswordChangesMeanwhile(t *testing.T) {
	a, s, store, _, _ := newAccounts(t)
	alice := registerAccount(t, a, s, "alice", "correct horse")
	node := porttest.NewRedis().Node("a")
	racing := func(meanwhile func()) *Accounts {
		return NewAccounts(passwordReadMembers{store, meanwhile}, store, &fakeQuota{exhausted: map[string]bool{}}, porttest.NewCodes(), porttest.NewDevices(), newGateway(store, node, node))
	}

	var reset string
	resetting := racing(func() {
		var err error
		if reset, err = a.ResetPassword(t.Context(), "alice"); err != nil {
			t.Fatal(err)
		}
	})
	if err := resetting.ChangePassword(t.Context(), alice.Member.ID, alice.MemberToken, "correct horse", "battery staple"); !errors.Is(err, ErrInvalidMemberToken) {
		t.Errorf("校验当前密码之后后台重置了密码，ChangePassword = %v，期望 ErrInvalidMemberToken", err)
	}
	guest := createMember(t, s)
	session, _, _, err := a.Login(t.Context(), guest.Member, guest.MemberToken, clientIP, "alice", reset, "")
	if err != nil {
		t.Fatalf("用重置出来的密码登录 = %v，期望成功", err)
	}

	changing := racing(func() {
		if err := a.ChangePassword(t.Context(), session.Member.ID, session.MemberToken, reset, "first change"); err != nil {
			t.Fatal(err)
		}
	})
	if err := changing.ChangePassword(t.Context(), session.Member.ID, session.MemberToken, reset, "second change"); !errors.Is(err, ErrWrongCurrentPassword) {
		t.Errorf("校验当前密码之后同一个会话已经改了密码，ChangePassword = %v，期望 ErrWrongCurrentPassword", err)
	}
	next := createMember(t, s)
	if _, _, _, err := a.Login(t.Context(), next.Member, next.MemberToken, clientIP, "alice", "first change", ""); err != nil {
		t.Errorf("用先完成的那次修改的密码登录 = %v，期望成功", err)
	}
}

// 名额占满、请求已经取消时，要做密码计算的请求以 context.Canceled 结束，不做的直接返回结果。
func TestLoginDoesPasswordWorkForUnknownUsername(t *testing.T) {
	a, s, _, _, _ := newAccounts(t)
	registerAccount(t, a, s, "alice", "correct horse")
	guest := createMember(t, s)
	if _, err := dummyPasswordHash(); err != nil {
		t.Fatal(err)
	}
	occupyArgonSlots(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, username := range []string{"alice", "nobody"} {
		if _, _, _, err := a.Login(ctx, guest.Member, guest.MemberToken, clientIP, username, "wrong horse", ""); !errors.Is(err, context.Canceled) {
			t.Errorf("用户名 %s：Login = %v，期望同样等待密码计算的名额", username, err)
		}
	}
}

// 绑定 GitHub 的游客成为没有用户名和密码的账号。
func githubAccount(t *testing.T, s *Service, store *porttest.Store, githubID int64) Session {
	t.Helper()
	session := createMember(t, s)
	if err := store.BindGitHub(t.Context(), session.Member.ID, githubID); err != nil {
		t.Fatal(err)
	}
	return session
}

func TestGitHubOnlyAccount(t *testing.T) {
	a, s, store, _, _ := newAccounts(t)
	gh := githubAccount(t, s, store, 42)
	if got, ok, err := a.Account(t.Context(), gh.Member.ID); err != nil || !ok || got != (Account{GitHub: true}) {
		t.Errorf("只绑定了 GitHub 的账号 Account = %#v, %v, %v", got, ok, err)
	}
	if err := a.ChangePassword(t.Context(), gh.Member.ID, gh.MemberToken, "correct horse", "battery staple"); !errors.Is(err, port.ErrNoPassword) {
		t.Errorf("没有密码的账号修改密码 = %v，期望 ErrNoPassword", err)
	}
}

// 只绑定了 GitHub 的账号补设用户名和密码，不是注册新账号：注册关闭时也可以。
func TestGitHubAccountSetsPasswordWhileRegistrationClosed(t *testing.T) {
	a, s, store, q, _ := newAccounts(t)
	gh := githubAccount(t, s, store, 42)
	if _, err := s.UpdateProfile(t.Context(), gh.Member.ID, Profile{Email: ptr("gh@example.com")}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetRegistrationOpen(t.Context(), false); err != nil {
		t.Fatal(err)
	}
	guest := createMember(t, s)
	if _, err := a.Register(t.Context(), guest.Member.ID, guest.MemberToken, clientIP, "bob", "correct horse"); !errors.Is(err, ErrRegistrationClosed) {
		t.Fatalf("关闭注册时游客注册 = %v，期望 ErrRegistrationClosed", err)
	}
	q.taken = nil
	account, err := a.Register(t.Context(), gh.Member.ID, gh.MemberToken, clientIP, "alice", "correct horse")
	if want := (Account{Username: "alice", Email: "gh@example.com", GitHub: true}); err != nil || account != want {
		t.Fatalf("Register = %#v, %v，期望 %#v", account, err, want)
	}
	if want := []string{"register:ip:" + clientIP}; !slices.Equal(q.taken, want) {
		t.Errorf("取的额度 = %v，期望 %v", q.taken, want)
	}
	if _, _, _, err := a.Login(t.Context(), guest.Member, guest.MemberToken, clientIP, "alice", "correct horse", ""); err != nil {
		t.Errorf("设置密码之后用密码登录 = %v", err)
	}
	if _, err := a.Register(t.Context(), gh.Member.ID, gh.MemberToken, clientIP, "alice2", "correct horse"); !errors.Is(err, port.ErrAlreadyRegistered) {
		t.Errorf("再设置一次用户名 = %v，期望 ErrAlreadyRegistered", err)
	}
}

// 存储中的空密码哈希表示没有设置过密码，不是格式错误的哈希：按密码错误处理，不报内部错误。
func TestEmptyPasswordHashIsWrongPassword(t *testing.T) {
	a, s, store, _, _ := newAccounts(t)
	alice := createMember(t, s)
	if err := store.Register(t.Context(), alice.Member.ID, "alice", ""); err != nil {
		t.Fatal(err)
	}
	guest := createMember(t, s)
	for _, password := range []string{"", "correct horse"} {
		if _, _, _, err := a.Login(t.Context(), guest.Member, guest.MemberToken, clientIP, "alice", password, ""); !errors.Is(err, ErrInvalidCredentials) {
			t.Errorf("密码哈希为空时用 %q 登录 = %v，期望 ErrInvalidCredentials", password, err)
		}
	}
	if err := a.ChangePassword(t.Context(), alice.Member.ID, alice.MemberToken, "correct horse", "battery staple"); !errors.Is(err, ErrWrongCurrentPassword) {
		t.Errorf("密码哈希为空时修改密码 = %v，期望 ErrWrongCurrentPassword", err)
	}
}
