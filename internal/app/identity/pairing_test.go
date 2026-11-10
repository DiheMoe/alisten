package identity

import (
	"errors"
	"regexp"
	"slices"
	"testing"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/app/port/porttest"
	"github.com/dihemoe/alisten/internal/domain/track"
)

func TestPairingCodeAlphabet(t *testing.T) {
	format := regexp.MustCompile(`^[23456789ABCDEFGHJKLMNPQRSTUVWXYZ]{8}$`)
	seen := map[rune]bool{}
	codes := map[string]bool{}
	for range 1000 {
		code := newPairingCode()
		if !format.MatchString(code) {
			t.Fatalf("配对码 %q 应是 8 位，不含 0、O、1、I", code)
		}
		codes[code] = true
		for _, r := range code {
			seen[r] = true
		}
	}
	if len(seen) != 32 {
		t.Errorf("1000 个配对码只用到 %d 个字符，期望字母表中的 32 个都会出现", len(seen))
	}
	if len(codes) < 999 {
		t.Errorf("1000 个配对码中只有 %d 个不同", len(codes))
	}
}

func createPairingCode(t *testing.T, a *Accounts, s Session) string {
	t.Helper()
	code, err := a.CreatePairingCode(t.Context(), s.Member.ID, s.MemberToken)
	if err != nil {
		t.Fatal(err)
	}
	return code.Code
}

func TestPairDevice(t *testing.T) {
	a, s, store, q, revoked := newAccounts(t)
	alice := registerAccount(t, a, s, "alice", "correct horse")
	guest := createMember(t, s)
	guestTicket := issueTicket(t, s, guest, createRoom(t, store, ""))
	q.taken = nil
	before := time.Now()
	code, err := a.CreatePairingCode(t.Context(), alice.Member.ID, alice.MemberToken)
	if err != nil {
		t.Fatal(err)
	}
	if code.ExpiresAt.Before(before.Add(5*time.Minute-time.Second)) || code.ExpiresAt.After(time.Now().Add(5*time.Minute)) {
		t.Errorf("配对码在 %v 过期，期望 5 分钟之后", code.ExpiresAt.Sub(before))
	}
	revoked()
	session, account, err := a.RedeemPairingCode(t.Context(), guest.Member, guest.MemberToken, clientIP, code.Code)
	if err != nil {
		t.Fatal(err)
	}
	if session.Member != alice.Member || account != (Account{Username: "alice"}) {
		t.Errorf("兑换配对码 = %#v, %#v，期望登录 alice", session.Member, account)
	}
	if m, ok := tokenOwner(t, store, session.MemberToken); !ok || m != alice.Member {
		t.Error("兑换配对码应签发账号的新会话")
	}
	if _, ok := tokenOwner(t, store, guest.MemberToken); ok {
		t.Error("兑换之后游客原来的 token 应失效")
	}
	if got := revoked(); !sameHashes(got, guest.MemberToken) || ticketValid(t, s, guestTicket) {
		t.Errorf("广播的被删除的会话 = %x，期望游客原来的会话", got)
	}
	if _, ok := tokenOwner(t, store, alice.MemberToken); !ok {
		t.Error("兑换不影响生成配对码的会话")
	}
	if want := []string{"pairing:member:" + alice.Member.ID, "pairing:ip:" + clientIP}; !slices.Equal(q.taken, want) {
		t.Errorf("取的额度 = %v，期望 %v", q.taken, want)
	}

	other := createMember(t, s)
	if _, _, err := a.RedeemPairingCode(t.Context(), other.Member, other.MemberToken, clientIP, code.Code); !errors.Is(err, ErrInvalidPairingCode) {
		t.Errorf("同一个配对码再兑换一次 = %v，期望 ErrInvalidPairingCode", err)
	}
	if _, _, err := a.RedeemPairingCode(t.Context(), other.Member, other.MemberToken, clientIP, "ABCD2345"); !errors.Is(err, ErrInvalidPairingCode) {
		t.Errorf("不存在的配对码 = %v，期望 ErrInvalidPairingCode", err)
	}
	if _, ok := tokenOwner(t, store, other.MemberToken); !ok {
		t.Error("兑换失败时游客的 token 应仍然有效")
	}
}

func TestPairingMovesGuestListensIntoAccount(t *testing.T) {
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
	code := createPairingCode(t, a, alice)
	if _, _, err := a.RedeemPairingCode(t.Context(), guest.Member, guest.MemberToken, clientIP, code); err != nil {
		t.Fatal(err)
	}
	if listens, err := store.MemberListens(t.Context(), alice.Member.ID); err != nil || !slices.Equal(listens, []port.Listen{rain, sunny}) {
		t.Errorf("兑换配对码之后账号的历史 = %#v, %v，期望并入游客听过的《七里香》", listens, err)
	}
	if listens, err := store.MemberListens(t.Context(), guest.Member.ID); err != nil || len(listens) != 0 {
		t.Errorf("兑换配对码之后游客的历史 = %#v, %v，期望移走", listens, err)
	}
}

func TestPairingRejected(t *testing.T) {
	a, s, _, q, _ := newAccounts(t)
	alice := registerAccount(t, a, s, "alice", "correct horse")
	bob := registerAccount(t, a, s, "bob", "correct horse")
	guest := createMember(t, s)
	q.taken = nil
	if _, err := a.CreatePairingCode(t.Context(), guest.Member.ID, guest.MemberToken); !errors.Is(err, ErrNotRegistered) {
		t.Errorf("游客生成配对码 = %v，期望 ErrNotRegistered", err)
	}
	if len(q.taken) != 0 {
		t.Errorf("游客不应消耗额度：%v", q.taken)
	}
	code := createPairingCode(t, a, alice)
	if _, _, err := a.RedeemPairingCode(t.Context(), bob.Member, bob.MemberToken, clientIP, code); !errors.Is(err, ErrAlreadyLoggedIn) {
		t.Errorf("已经登录了账号的设备兑换 = %v，期望 ErrAlreadyLoggedIn", err)
	}
	q.exhausted["pairing:ip:"+clientIP] = true
	if _, _, err := a.RedeemPairingCode(t.Context(), guest.Member, guest.MemberToken, clientIP, code); !errors.Is(err, ErrTooManyAttempts) {
		t.Errorf("额度用完时兑换 = %v，期望 ErrTooManyAttempts", err)
	}
	if _, _, err := a.RedeemPairingCode(t.Context(), guest.Member, guest.MemberToken, "198.51.100.1", code); err != nil {
		t.Errorf("被拒绝的兑换不应用掉配对码：%v", err)
	}
	q.exhausted["pairing:member:"+bob.Member.ID] = true
	if _, err := a.CreatePairingCode(t.Context(), bob.Member.ID, bob.MemberToken); !errors.Is(err, ErrTooManyAttempts) {
		t.Errorf("额度用完时生成配对码 = %v，期望 ErrTooManyAttempts", err)
	}
}

// 让其他设备退出的办法是修改密码或者重置密码，所以此前生成的配对码要随生成它的会话一起作废，否则拿到配对码的人还能登录进来。
func TestPairingCodeDiesWithItsSession(t *testing.T) {
	a, s, store, _, _ := newAccounts(t)
	alice := registerAccount(t, a, s, "alice", "correct horse")
	login := func() Session {
		t.Helper()
		guest := createMember(t, s)
		session, _, _, err := a.Login(t.Context(), guest.Member, guest.MemberToken, clientIP, "alice", "correct horse", "")
		if err != nil {
			t.Fatal(err)
		}
		return session
	}
	redeem := func(code string) error {
		t.Helper()
		guest := createMember(t, s)
		_, _, err := a.RedeemPairingCode(t.Context(), guest.Member, guest.MemberToken, clientIP, code)
		return err
	}

	other := login()
	code := createPairingCode(t, a, other)
	if err := a.Logout(t.Context(), other.MemberToken); err != nil {
		t.Fatal(err)
	}
	if err := redeem(code); !errors.Is(err, ErrInvalidPairingCode) {
		t.Errorf("生成配对码的会话退出之后兑换 = %v，期望 ErrInvalidPairingCode", err)
	}

	other = login()
	code = createPairingCode(t, a, other)
	if err := a.ChangePassword(t.Context(), alice.Member.ID, alice.MemberToken, "correct horse", "correct horse"); err != nil {
		t.Fatal(err)
	}
	if err := redeem(code); !errors.Is(err, ErrInvalidPairingCode) {
		t.Errorf("另一台设备修改密码之后兑换 = %v，期望 ErrInvalidPairingCode", err)
	}

	code = createPairingCode(t, a, alice)
	if err := a.ChangePassword(t.Context(), alice.Member.ID, alice.MemberToken, "correct horse", "correct horse"); err != nil {
		t.Fatal(err)
	}
	if err := redeem(code); err != nil {
		t.Errorf("生成配对码的会话自己修改了密码，它仍然有效，兑换 = %v", err)
	}

	code = createPairingCode(t, a, alice)
	if _, err := a.ResetPassword(t.Context(), "alice"); err != nil {
		t.Fatal(err)
	}
	if err := redeem(code); !errors.Is(err, ErrInvalidPairingCode) {
		t.Errorf("重置密码之后兑换 = %v，期望 ErrInvalidPairingCode", err)
	}
	if _, ok := tokenOwner(t, store, alice.MemberToken); ok {
		t.Fatal("重置密码应删除账号的全部会话")
	}
}

func TestPairGitHubOnlyAccount(t *testing.T) {
	a, s, store, _, _ := newAccounts(t)
	gh := githubAccount(t, s, store, 42)
	code := createPairingCode(t, a, gh)
	guest := createMember(t, s)
	session, account, err := a.RedeemPairingCode(t.Context(), guest.Member, guest.MemberToken, clientIP, code)
	if err != nil || session.Member != gh.Member || account != (Account{GitHub: true}) {
		t.Errorf("兑换只绑定了 GitHub 的账号的配对码 = %#v, %#v, %v", session.Member, account, err)
	}
}

// 读到账号的密码哈希之后、签发会话之前，密码被修改或重置。修改时删除了其他会话，所以这次兑换也不能留下新会话。
func TestRedeemRejectedWhenPasswordChangesMeanwhile(t *testing.T) {
	a, s, store, _, _ := newAccounts(t)
	alice := registerAccount(t, a, s, "alice", "correct horse")
	codes := porttest.NewCodes()
	node := porttest.NewRedis().Node("a")
	changing := NewAccounts(passwordChangedMembers{store}, store, &fakeQuota{exhausted: map[string]bool{}}, codes, codes, porttest.NewDevices(), newGateway(store, node, node))
	code, err := changing.CreatePairingCode(t.Context(), alice.Member.ID, alice.MemberToken)
	if err != nil {
		t.Fatal(err)
	}
	guest := createMember(t, s)
	if _, _, err := changing.RedeemPairingCode(t.Context(), guest.Member, guest.MemberToken, clientIP, code.Code); !errors.Is(err, ErrInvalidPairingCode) {
		t.Errorf("密码在兑换期间被修改 = %v，期望 ErrInvalidPairingCode", err)
	}
	if _, ok := tokenOwner(t, store, guest.MemberToken); !ok {
		t.Error("兑换失败时游客的 token 应仍然有效")
	}
}
