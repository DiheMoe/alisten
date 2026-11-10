package identity

import (
	"context"
	"errors"
	"net/url"
	"slices"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/app/port/porttest"
	"github.com/dihemoe/alisten/internal/domain/track"
)

// 授权码就是 GitHub 用户 ID 的名字：code-42 换到 42，不认识的授权码换取失败。GitHub 上有 client-1 和 client-2 两个 OAuth App，
// Client Secret 对不上时换取失败。
type fakeOAuth struct {
	users   map[string]int64
	secrets map[string]string
	calls   atomic.Int32
}

func (f *fakeOAuth) AuthorizeURL(app port.GitHubOAuthApp, state, _ string) string {
	return "https://github.test/login/oauth/authorize?" + url.Values{"client_id": {app.ClientID}, "state": {state}}.Encode()
}

func (f *fakeOAuth) UserID(_ context.Context, app port.GitHubOAuthApp, code, _ string) (int64, error) {
	f.calls.Add(1)
	if secret, ok := f.secrets[app.ClientID]; !ok || secret != app.ClientSecret {
		return 0, errors.New("incorrect_client_credentials")
	}
	id, ok := f.users[code]
	if !ok {
		return 0, errors.New("bad_verification_code")
	}
	return id, nil
}

type githubFixture struct {
	g       *GitHub
	a       *Accounts
	s       *Service
	store   *porttest.Store
	q       *fakeQuota
	oauth   *fakeOAuth
	revoked func() [][]byte
}

func newGitHub(t *testing.T) githubFixture {
	t.Helper()
	a, s, store, q, revoked := newAccounts(t)
	oauth := &fakeOAuth{users: map[string]int64{"code-42": 42, "code-7": 7}, secrets: map[string]string{"client-1": "secret-1", "client-2": "secret-2"}}
	g := NewGitHub(a, store, oauth, porttest.NewCodes())
	if err := g.SetOAuthApp(t.Context(), port.GitHubOAuthApp{ClientID: "client-1", ClientSecret: "secret-1"}); err != nil {
		t.Fatal(err)
	}
	return githubFixture{g: g, a: a, s: s, store: store, q: q, oauth: oauth, revoked: revoked}
}

func clientIDOf(t *testing.T, authorizeURL string) string {
	t.Helper()
	u, err := url.Parse(authorizeURL)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query().Get("client_id")
}

func stateOf(t *testing.T, authorizeURL string) string {
	t.Helper()
	u, err := url.Parse(authorizeURL)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query().Get("state")
}

// 走完授权和回调，返回兑换码。
func (f githubFixture) authorize(t *testing.T, s Session, intent port.GitHubIntent, nonce, code string) string {
	t.Helper()
	// 绑定要输入当前密码，测试中的账号都用这个密码注册。
	authorizeURL, err := f.g.Authorize(t.Context(), s.Member.ID, clientIP, intent, port.GitHubWeb, nonce, "correct horse")
	if err != nil {
		t.Fatal(err)
	}
	_, exchange, err := f.g.Callback(t.Context(), stateOf(t, authorizeURL), code)
	if err != nil {
		t.Fatal(err)
	}
	return exchange
}

func (f githubFixture) flow(t *testing.T, s Session, intent port.GitHubIntent, code string) (GitHubResult, error) {
	t.Helper()
	exchange := f.authorize(t, s, intent, "nonce-"+s.MemberToken, code)
	return f.g.Exchange(t.Context(), s.Member, s.MemberToken, exchange, "nonce-"+s.MemberToken)
}

func TestGitHubRegistersThenLogsIn(t *testing.T) {
	f := newGitHub(t)
	first := createMember(t, f.s)
	result, err := f.flow(t, first, port.GitHubLogin, "code-42")
	if err != nil {
		t.Fatal(err)
	}
	if result.Session != nil || result.Member != first.Member || result.Account != (Account{GitHub: true}) {
		t.Errorf("首次用 GitHub 登录 = %#v，期望注册：成员和会话都不变，账号只绑定了 GitHub", result)
	}
	if _, ok := tokenOwner(t, f.store, first.MemberToken); !ok {
		t.Error("注册不更换会话")
	}
	if lists, err := f.store.MemberPlaylists(t.Context(), first.Member.ID); err != nil || len(lists) != 1 || !lists[0].Favorites {
		t.Errorf("注册之后的歌单 = %#v, %v，期望一个“我喜欢”", lists, err)
	}

	guest := createMember(t, f.s)
	f.revoked()
	result, err = f.flow(t, guest, port.GitHubLogin, "code-42")
	if err != nil {
		t.Fatal(err)
	}
	if result.Session == nil || result.Session.Member != first.Member || result.Member != first.Member || result.Account != (Account{GitHub: true}) {
		t.Fatalf("用已绑定的 GitHub 登录 = %#v，期望登录第一次注册的账号", result)
	}
	if m, ok := tokenOwner(t, f.store, result.Session.MemberToken); !ok || m != first.Member {
		t.Error("登录应签发账号的新会话")
	}
	if _, ok := tokenOwner(t, f.store, guest.MemberToken); ok {
		t.Error("登录之后游客原来的 token 应失效")
	}
	if got := f.revoked(); !sameHashes(got, guest.MemberToken) {
		t.Errorf("广播的被删除的会话 = %x，期望游客原来的会话", got)
	}
}

func TestGitHubLoginMovesGuestListensIntoAccount(t *testing.T) {
	f := newGitHub(t)
	owner := createMember(t, f.s)
	if _, err := f.flow(t, owner, port.GitHubLogin, "code-42"); err != nil {
		t.Fatal(err)
	}
	guest := createMember(t, f.s)
	heard := time.Date(2026, 11, 10, 20, 0, 0, 0, time.UTC)
	sunny := port.Listen{Track: track.Track{Source: track.NetEase, ID: "186016"}, Meta: track.Meta{Title: "晴天"}, HeardAt: heard}
	rain := port.Listen{Track: track.Track{Source: track.NetEase, ID: "185811"}, Meta: track.Meta{Title: "七里香"}, HeardAt: heard.Add(time.Minute)}
	if err := f.store.RecordListen(t.Context(), []string{owner.Member.ID}, sunny); err != nil {
		t.Fatal(err)
	}
	if err := f.store.RecordListen(t.Context(), []string{guest.Member.ID}, rain); err != nil {
		t.Fatal(err)
	}
	if _, err := f.flow(t, guest, port.GitHubLogin, "code-42"); err != nil {
		t.Fatal(err)
	}
	if listens, err := f.store.MemberListens(t.Context(), owner.Member.ID); err != nil || !slices.Equal(listens, []port.Listen{rain, sunny}) {
		t.Errorf("用 GitHub 登录之后账号的历史 = %#v, %v，期望并入游客听过的《七里香》", listens, err)
	}
	if listens, err := f.store.MemberListens(t.Context(), guest.Member.ID); err != nil || len(listens) != 0 {
		t.Errorf("用 GitHub 登录之后游客的历史 = %#v, %v，期望移走", listens, err)
	}
}

func TestGitHubRegistrationClosed(t *testing.T) {
	f := newGitHub(t)
	alice := registerAccount(t, f.a, f.s, "alice", "correct horse")
	if err := f.store.SetRegistrationOpen(t.Context(), false); err != nil {
		t.Fatal(err)
	}
	if _, err := f.flow(t, alice, port.GitHubBind, "code-7"); err != nil {
		t.Errorf("关闭注册时已有账号绑定 GitHub = %v，期望照常", err)
	}
	guest := createMember(t, f.s)
	if _, err := f.flow(t, guest, port.GitHubLogin, "code-42"); !errors.Is(err, ErrRegistrationClosed) {
		t.Errorf("关闭注册时首次用 GitHub 登录 = %v，期望 ErrRegistrationClosed", err)
	}
	if _, registered, _ := f.a.Account(t.Context(), guest.Member.ID); registered {
		t.Error("关闭注册时游客不应变成账号")
	}
	result, err := f.flow(t, guest, port.GitHubLogin, "code-7")
	if err != nil || result.Session == nil || result.Session.Member != alice.Member {
		t.Errorf("关闭注册时用已绑定的 GitHub 登录 = %#v, %v，期望登录 alice", result, err)
	}
}

func TestGitHubBind(t *testing.T) {
	f := newGitHub(t)
	alice := registerAccount(t, f.a, f.s, "alice", "correct horse")
	bob := registerAccount(t, f.a, f.s, "bob", "correct horse")
	guest := createMember(t, f.s)
	result, err := f.flow(t, alice, port.GitHubBind, "code-42")
	if err != nil {
		t.Fatal(err)
	}
	if result.Session != nil || result.Member != alice.Member || result.Account != (Account{Username: "alice", GitHub: true}) {
		t.Errorf("绑定 = %#v，期望 alice 绑定了 GitHub，会话不变", result)
	}
	if _, err := f.flow(t, bob, port.GitHubBind, "code-42"); !errors.Is(err, port.ErrGitHubTaken) {
		t.Errorf("绑定已经属于别人的 GitHub = %v，期望 ErrGitHubTaken", err)
	}
	for _, tc := range []struct {
		name    string
		session Session
		intent  port.GitHubIntent
		want    error
	}{
		{"已经绑定了 GitHub 的账号再绑定", alice, port.GitHubBind, port.ErrGitHubAlreadyBound},
		{"游客绑定", guest, port.GitHubBind, ErrNotRegistered},
		{"已经登录了账号的设备用 GitHub 登录", bob, port.GitHubLogin, ErrAlreadyLoggedIn},
	} {
		if _, err := f.g.Authorize(t.Context(), tc.session.Member.ID, clientIP, tc.intent, port.GitHubWeb, "nonce", "correct horse"); !errors.Is(err, tc.want) {
			t.Errorf("%s = %v，期望 %v", tc.name, err, tc.want)
		}
	}
	result, err = f.flow(t, guest, port.GitHubLogin, "code-42")
	if err != nil || result.Session == nil || result.Session.Member != alice.Member || result.Account != (Account{Username: "alice", GitHub: true}) {
		t.Errorf("绑定之后用 GitHub 登录 = %#v, %v，期望登录 alice", result, err)
	}
}

// 兑换码只能由发起授权的那台设备带着自己保存的 nonce 兑换。所以别人把自己发起的授权发给你，你授权之后得到的兑换码对他也没用。
func TestGitHubExchangeChecksNonceAndMember(t *testing.T) {
	f := newGitHub(t)
	alice := registerAccount(t, f.a, f.s, "alice", "correct horse")
	mallory := registerAccount(t, f.a, f.s, "mallory", "correct horse")

	exchange := f.authorize(t, mallory, port.GitHubBind, "mallory-nonce", "code-42")
	if _, err := f.g.Exchange(t.Context(), alice.Member, alice.MemberToken, exchange, "alice-nonce"); !errors.Is(err, ErrGitHubLoginExpired) {
		t.Errorf("别的设备兑换 = %v，期望 ErrGitHubLoginExpired", err)
	}
	if _, err := f.g.Exchange(t.Context(), mallory.Member, mallory.MemberToken, exchange, "mallory-nonce"); !errors.Is(err, ErrGitHubLoginExpired) {
		t.Errorf("兑换失败之后再兑换 = %v，期望兑换码已经作废", err)
	}

	exchange = f.authorize(t, mallory, port.GitHubBind, "mallory-nonce", "code-42")
	if _, err := f.g.Exchange(t.Context(), mallory.Member, mallory.MemberToken, exchange, "guessed-nonce"); !errors.Is(err, ErrGitHubLoginExpired) {
		t.Errorf("nonce 不对 = %v，期望 ErrGitHubLoginExpired", err)
	}
	if _, err := f.g.Exchange(t.Context(), mallory.Member, mallory.MemberToken, "unknown", "mallory-nonce"); !errors.Is(err, ErrGitHubLoginExpired) {
		t.Errorf("不存在的兑换码 = %v，期望 ErrGitHubLoginExpired", err)
	}
	if p, _ := f.store.AccountProfile(t.Context(), mallory.Member.ID); p.GitHub {
		t.Error("兑换失败时不应绑定")
	}

	guest := createMember(t, f.s)
	exchange = f.authorize(t, guest, port.GitHubLogin, "nonce", "code-42")
	if _, err := f.a.Register(t.Context(), guest.Member.ID, guest.MemberToken, clientIP, "guest", "correct horse"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.g.Exchange(t.Context(), guest.Member, guest.MemberToken, exchange, "nonce"); !errors.Is(err, ErrAlreadyLoggedIn) {
		t.Errorf("发起登录之后注册了账号再兑换 = %v，期望 ErrAlreadyLoggedIn", err)
	}
}

func TestGitHubCallback(t *testing.T) {
	f := newGitHub(t)
	guest := createMember(t, f.s)
	f.q.taken = nil
	authorizeURL, err := f.g.Authorize(t.Context(), guest.Member.ID, clientIP, port.GitHubLogin, port.GitHubWeb, "nonce", "")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"github:ip:" + clientIP}; !slices.Equal(f.q.taken, want) {
		t.Errorf("取的额度 = %v，期望 %v", f.q.taken, want)
	}
	state := stateOf(t, authorizeURL)
	if len(state) != 43 {
		t.Errorf("state = %q，期望 256 位随机值的 base64url", state)
	}
	if _, _, err := f.g.Callback(t.Context(), state, "unknown-code"); !errors.Is(err, ErrGitHubFailed) {
		t.Errorf("GitHub 换取失败 = %v，期望 ErrGitHubFailed", err)
	}
	calls := f.oauth.calls.Load()
	if _, _, err := f.g.Callback(t.Context(), state, "code-42"); !errors.Is(err, ErrGitHubRequestExpired) {
		t.Errorf("同一个 state 再回调一次 = %v，期望 ErrGitHubRequestExpired", err)
	}
	if _, _, err := f.g.Callback(t.Context(), "forged", "code-42"); !errors.Is(err, ErrGitHubRequestExpired) {
		t.Errorf("不存在的 state = %v，期望 ErrGitHubRequestExpired", err)
	}
	if got := f.oauth.calls.Load(); got != calls {
		t.Error("state 无效时不应请求 GitHub")
	}
	f.q.exhausted["github:ip:"+clientIP] = true
	if _, err := f.g.Authorize(t.Context(), guest.Member.ID, clientIP, port.GitHubLogin, port.GitHubWeb, "nonce", ""); !errors.Is(err, ErrTooManyAttempts) {
		t.Errorf("额度用完时发起授权 = %v，期望 ErrTooManyAttempts", err)
	}
}

// 发起授权的客户端记在 state 对应的记录里。回调成功、换取失败和 GitHub 没有授权时，都按记录跳回发起授权的客户端。
func TestGitHubCallbackReturnsClient(t *testing.T) {
	f := newGitHub(t)
	guest := createMember(t, f.s)
	start := func(client port.GitHubClient) string {
		t.Helper()
		authorizeURL, err := f.g.Authorize(t.Context(), guest.Member.ID, clientIP, port.GitHubLogin, client, "nonce", "")
		if err != nil {
			t.Fatal(err)
		}
		return stateOf(t, authorizeURL)
	}
	for _, client := range []port.GitHubClient{port.GitHubWeb, port.GitHubApp} {
		if got, exchange, err := f.g.Callback(t.Context(), start(client), "code-42"); err != nil || got != client || exchange == "" {
			t.Errorf("客户端 %v 发起的授权回调 = %v, %q, %v，期望跳回发起授权的客户端", client, got, exchange, err)
		}
		if got, _, err := f.g.Callback(t.Context(), start(client), "unknown-code"); !errors.Is(err, ErrGitHubFailed) || got != client {
			t.Errorf("客户端 %v 发起的授权换取失败 = %v, %v，期望 ErrGitHubFailed 并跳回发起授权的客户端", client, got, err)
		}

		state := start(client)
		if got, err := f.g.Cancel(t.Context(), state); err != nil || got != client {
			t.Errorf("客户端 %v 发起的授权没有得到授权 = %v, %v，期望跳回发起授权的客户端", client, got, err)
		}
		calls := f.oauth.calls.Load()
		if _, _, err := f.g.Callback(t.Context(), state, "code-42"); !errors.Is(err, ErrGitHubRequestExpired) {
			t.Errorf("没有得到授权之后用同一个 state 回调 = %v，期望 state 已经作废", err)
		}
		if _, err := f.g.Cancel(t.Context(), state); !errors.Is(err, ErrGitHubRequestExpired) {
			t.Errorf("没有得到授权之后再取消同一个 state = %v，期望 ErrGitHubRequestExpired", err)
		}
		if got := f.oauth.calls.Load(); got != calls {
			t.Error("state 已经作废时不应请求 GitHub")
		}
	}
	if _, err := f.g.Cancel(t.Context(), "forged"); !errors.Is(err, ErrGitHubRequestExpired) {
		t.Errorf("不存在的 state 没有得到授权 = %v，期望 ErrGitHubRequestExpired", err)
	}
}

// state 10 分钟内有效，兑换码 1 分钟内有效。
func TestGitHubCodesExpire(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newGitHub(t)
		guest := createMember(t, f.s)
		authorizeURL, err := f.g.Authorize(t.Context(), guest.Member.ID, clientIP, port.GitHubLogin, port.GitHubWeb, "nonce", "")
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(10*time.Minute + time.Second)
		if _, _, err := f.g.Callback(t.Context(), stateOf(t, authorizeURL), "code-42"); !errors.Is(err, ErrGitHubRequestExpired) {
			t.Errorf("10 分钟之后回调 = %v，期望 ErrGitHubRequestExpired", err)
		}

		authorizeURL, err = f.g.Authorize(t.Context(), guest.Member.ID, clientIP, port.GitHubLogin, port.GitHubWeb, "nonce", "")
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(9 * time.Minute)
		_, exchange, err := f.g.Callback(t.Context(), stateOf(t, authorizeURL), "code-42")
		if err != nil {
			t.Fatalf("9 分钟之后回调 = %v，期望 state 仍然有效", err)
		}
		time.Sleep(time.Minute + time.Second)
		if _, err := f.g.Exchange(t.Context(), guest.Member, guest.MemberToken, exchange, "nonce"); !errors.Is(err, ErrGitHubLoginExpired) {
			t.Errorf("1 分钟之后兑换 = %v，期望 ErrGitHubLoginExpired", err)
		}
	})
}

// 只凭会话不能更换登录凭证，所以绑定 GitHub 要输入当前密码，额度与修改密码共用。
func TestGitHubBindRequiresCurrentPassword(t *testing.T) {
	f := newGitHub(t)
	alice := registerAccount(t, f.a, f.s, "alice", "correct horse")
	f.q.taken = nil
	for _, password := range []string{"", "wrong horse"} {
		if _, err := f.g.Authorize(t.Context(), alice.Member.ID, clientIP, port.GitHubBind, port.GitHubWeb, "nonce", password); !errors.Is(err, ErrWrongCurrentPassword) {
			t.Errorf("当前密码为 %q 时发起绑定 = %v，期望 ErrWrongCurrentPassword", password, err)
		}
	}
	if want := []string{"password:member:" + alice.Member.ID, "password:member:" + alice.Member.ID}; !slices.Equal(f.q.taken, want) {
		t.Errorf("取的额度 = %v，期望 %v", f.q.taken, want)
	}
	if result, err := f.flow(t, alice, port.GitHubBind, "code-42"); err != nil || result.Account != (Account{Username: "alice", GitHub: true}) {
		t.Errorf("输入正确的当前密码之后绑定 = %#v, %v", result, err)
	}
	bob := registerAccount(t, f.a, f.s, "bob", "correct horse")
	f.q.exhausted["password:member:"+bob.Member.ID] = true
	if _, err := f.g.Authorize(t.Context(), bob.Member.ID, clientIP, port.GitHubBind, port.GitHubWeb, "nonce", "correct horse"); !errors.Is(err, ErrTooManyAttempts) {
		t.Errorf("额度用完时发起绑定 = %v，期望 ErrTooManyAttempts", err)
	}
}

func TestGitHubUnbind(t *testing.T) {
	f := newGitHub(t)
	gh := createMember(t, f.s)
	if _, err := f.flow(t, gh, port.GitHubLogin, "code-42"); err != nil {
		t.Fatal(err)
	}
	if err := f.g.Unbind(t.Context(), gh.Member.ID, "correct horse"); !errors.Is(err, port.ErrNoPassword) {
		t.Errorf("没有密码的账号解绑 = %v，期望 ErrNoPassword", err)
	}
	if _, err := f.flow(t, gh, port.GitHubVerify, "code-42"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.a.Register(t.Context(), gh.Member.ID, gh.MemberToken, clientIP, "octocat", "correct horse"); err != nil {
		t.Fatal(err)
	}
	f.q.taken = nil
	for _, password := range []string{"", "wrong horse"} {
		if err := f.g.Unbind(t.Context(), gh.Member.ID, password); !errors.Is(err, ErrWrongCurrentPassword) {
			t.Errorf("当前密码为 %q 时解绑 = %v，期望 ErrWrongCurrentPassword", password, err)
		}
	}
	if want := []string{"password:member:" + gh.Member.ID, "password:member:" + gh.Member.ID}; !slices.Equal(f.q.taken, want) {
		t.Errorf("取的额度 = %v，期望 %v", f.q.taken, want)
	}
	if account, _, _ := f.a.Account(t.Context(), gh.Member.ID); !account.GitHub {
		t.Error("当前密码不对时不应解绑")
	}
	if err := f.g.Unbind(t.Context(), gh.Member.ID, "correct horse"); err != nil {
		t.Fatal(err)
	}
	if account, ok, err := f.a.Account(t.Context(), gh.Member.ID); err != nil || !ok || account != (Account{Username: "octocat"}) {
		t.Errorf("解绑之后 Account = %#v, %v, %v", account, ok, err)
	}
	if err := f.g.Unbind(t.Context(), createMember(t, f.s).Member.ID, "correct horse"); !errors.Is(err, ErrNotRegistered) {
		t.Errorf("游客解绑 = %v，期望 ErrNotRegistered", err)
	}
}

// 拿到账号一个会话的人（比如借用过的设备）不知道密码，就不能把账号的 GitHub 换成自己的。这样本人修改密码或后台重置之后，他无法再用 GitHub 登录回来。
func TestGitHubCannotBeSwappedWithSessionAlone(t *testing.T) {
	f := newGitHub(t)
	alice := registerAccount(t, f.a, f.s, "alice", "correct horse")
	bob := registerAccount(t, f.a, f.s, "bob", "correct horse")
	if _, err := f.flow(t, alice, port.GitHubBind, "code-42"); err != nil {
		t.Fatal(err)
	}
	borrowed := createMember(t, f.s)
	stolen, _, _, err := f.a.Login(t.Context(), borrowed.Member, borrowed.MemberToken, clientIP, "alice", "correct horse", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.g.Unbind(t.Context(), stolen.Member.ID, "guessed horse"); !errors.Is(err, ErrWrongCurrentPassword) {
		t.Errorf("不知道密码时解绑 = %v，期望 ErrWrongCurrentPassword", err)
	}
	if _, err := f.g.Authorize(t.Context(), bob.Member.ID, clientIP, port.GitHubBind, port.GitHubWeb, "nonce", "guessed horse"); !errors.Is(err, ErrWrongCurrentPassword) {
		t.Errorf("不知道密码时给没有绑定 GitHub 的账号发起绑定 = %v，期望 ErrWrongCurrentPassword", err)
	}
	if err := f.a.ChangePassword(t.Context(), alice.Member.ID, alice.MemberToken, "correct horse", "battery staple"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.a.ResetPassword(t.Context(), "alice"); err != nil {
		t.Fatal(err)
	}
	attacker := createMember(t, f.s)
	result, err := f.flow(t, attacker, port.GitHubLogin, "code-7")
	if err != nil || result.Session != nil || result.Member != attacker.Member {
		t.Errorf("用自己的 GitHub 登录 = %#v, %v，期望只注册了新账号，进不了 alice", result, err)
	}
	if account, err := f.store.AccountByGitHub(t.Context(), 42); err != nil || account.Member != alice.Member {
		t.Errorf("GitHub 42 属于 %#v, %v，期望仍是 alice", account.Member, err)
	}
}

// 只绑定了 GitHub 的账号补设用户名和密码之前，要用账号绑定的 GitHub 确认身份。
func TestGitHubVerify(t *testing.T) {
	f := newGitHub(t)
	gh := createMember(t, f.s)
	if _, err := f.flow(t, gh, port.GitHubLogin, "code-42"); err != nil {
		t.Fatal(err)
	}
	alice := registerAccount(t, f.a, f.s, "alice", "correct horse")
	for _, tc := range []struct {
		name    string
		session Session
		want    error
	}{
		{"设置了密码的账号", alice, port.ErrAlreadyRegistered},
		{"游客", createMember(t, f.s), ErrNotRegistered},
	} {
		if _, err := f.g.Authorize(t.Context(), tc.session.Member.ID, clientIP, port.GitHubVerify, port.GitHubWeb, "nonce", ""); !errors.Is(err, tc.want) {
			t.Errorf("%s发起确认 = %v，期望 %v", tc.name, err, tc.want)
		}
	}

	if _, err := f.flow(t, gh, port.GitHubVerify, "code-7"); !errors.Is(err, ErrGitHubMismatch) {
		t.Errorf("用别的 GitHub 确认 = %v，期望 ErrGitHubMismatch", err)
	}
	if _, err := f.a.Register(t.Context(), gh.Member.ID, gh.MemberToken, clientIP, "octocat", "correct horse"); !errors.Is(err, ErrGitHubUnverified) {
		t.Errorf("用别的 GitHub 确认之后补设 = %v，期望 ErrGitHubUnverified", err)
	}
	result, err := f.flow(t, gh, port.GitHubVerify, "code-42")
	if err != nil || result.Session != nil || result.Member != gh.Member || result.Account != (Account{GitHub: true}) {
		t.Fatalf("用绑定的 GitHub 确认 = %#v, %v，期望成员和会话都不变", result, err)
	}
	if _, ok := tokenOwner(t, f.store, gh.MemberToken); !ok {
		t.Error("确认不更换会话")
	}
	if account, err := f.a.Register(t.Context(), gh.Member.ID, gh.MemberToken, clientIP, "octocat", "correct horse"); err != nil || account != (Account{Username: "octocat", GitHub: true}) {
		t.Errorf("确认之后补设 = %#v, %v", account, err)
	}
}

// 用 GitHub 确认身份之后 10 分钟内要补设用户名和密码。
func TestGitHubVerificationExpires(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newGitHub(t)
		gh := createMember(t, f.s)
		if _, err := f.flow(t, gh, port.GitHubLogin, "code-42"); err != nil {
			t.Fatal(err)
		}
		if _, err := f.flow(t, gh, port.GitHubVerify, "code-42"); err != nil {
			t.Fatal(err)
		}
		time.Sleep(10*time.Minute + time.Second)
		if _, err := f.a.Register(t.Context(), gh.Member.ID, gh.MemberToken, clientIP, "octocat", "correct horse"); !errors.Is(err, ErrGitHubUnverified) {
			t.Errorf("确认 10 分钟之后补设 = %v，期望 ErrGitHubUnverified", err)
		}
	})
}

// 读到账号之后、签发会话之前，密码被修改或重置。修改时删除了其他会话，所以这次登录也不能留下新会话。
func TestGitHubLoginRejectedWhenPasswordChangesMeanwhile(t *testing.T) {
	f := newGitHub(t)
	alice := registerAccount(t, f.a, f.s, "alice", "correct horse")
	if _, err := f.flow(t, alice, port.GitHubBind, "code-42"); err != nil {
		t.Fatal(err)
	}
	node := porttest.NewRedis().Node("a")
	changing := NewAccounts(passwordChangedMembers{f.store}, f.store, &fakeQuota{exhausted: map[string]bool{}}, porttest.NewCodes(), porttest.NewCodes(), porttest.NewDevices(), newGateway(f.store, node, node))
	f.g = NewGitHub(changing, f.store, f.oauth, porttest.NewCodes())
	guest := createMember(t, f.s)
	if _, err := f.flow(t, guest, port.GitHubLogin, "code-42"); !errors.Is(err, ErrGitHubLoginExpired) {
		t.Errorf("密码在登录期间被修改 = %v，期望 ErrGitHubLoginExpired", err)
	}
	if _, ok := tokenOwner(t, f.store, guest.MemberToken); !ok {
		t.Error("登录失败时游客的 token 应仍然有效")
	}
}

// 后台保存的 OAuth App：读取只给出 Client ID，授权和换取令牌都用最近一次保存的，清除之后就关闭了 GitHub 登录。
func TestGitHubOAuthApp(t *testing.T) {
	f := newGitHub(t)
	guest := createMember(t, f.s)
	if id, err := f.g.ClientID(t.Context()); err != nil || id != "client-1" {
		t.Errorf("ClientID = %q, %v，期望 client-1", id, err)
	}
	if err := f.g.SetOAuthApp(t.Context(), port.GitHubOAuthApp{ClientID: "client-2", ClientSecret: "secret-2"}); err != nil {
		t.Fatal(err)
	}
	if id, err := f.g.ClientID(t.Context()); err != nil || id != "client-2" {
		t.Errorf("换成 client-2 之后 ClientID = %q, %v", id, err)
	}
	authorizeURL, err := f.g.Authorize(t.Context(), guest.Member.ID, clientIP, port.GitHubLogin, port.GitHubWeb, "nonce", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := clientIDOf(t, authorizeURL); got != "client-2" {
		t.Errorf("授权地址的 client_id = %q，期望新保存的 client-2", got)
	}
	if _, exchange, err := f.g.Callback(t.Context(), stateOf(t, authorizeURL), "code-42"); err != nil || exchange == "" {
		t.Errorf("用新的 Client Secret 换取 = %q, %v", exchange, err)
	}

	if err := f.g.DeleteOAuthApp(t.Context()); err != nil {
		t.Fatal(err)
	}
	if id, err := f.g.ClientID(t.Context()); err != nil || id != "" {
		t.Errorf("清除之后 ClientID = %q, %v，期望空字符串", id, err)
	}
	if err := f.g.DeleteOAuthApp(t.Context()); err != nil {
		t.Errorf("没有配置时再清除 = %v，期望什么也不做", err)
	}
}

// 没有配置 OAuth App 时，发起授权、兑换和解绑都返回 ErrGitHubDisabled。授权进行中关闭了 GitHub 登录时，回调也失败，
// 但照样跳回发起授权的客户端。重新开启之后恢复。
func TestGitHubDisabled(t *testing.T) {
	f := newGitHub(t)
	alice := registerAccount(t, f.a, f.s, "alice", "correct horse")
	if _, err := f.flow(t, alice, port.GitHubBind, "code-42"); err != nil {
		t.Fatal(err)
	}
	guest := createMember(t, f.s)
	pending, err := f.g.Authorize(t.Context(), guest.Member.ID, clientIP, port.GitHubLogin, port.GitHubApp, "nonce", "")
	if err != nil {
		t.Fatal(err)
	}
	exchange := f.authorize(t, guest, port.GitHubLogin, "nonce", "code-7")
	if err := f.g.DeleteOAuthApp(t.Context()); err != nil {
		t.Fatal(err)
	}

	f.q.taken = nil
	for _, intent := range []port.GitHubIntent{port.GitHubLogin, port.GitHubBind, port.GitHubVerify} {
		if _, err := f.g.Authorize(t.Context(), guest.Member.ID, clientIP, intent, port.GitHubWeb, "nonce", "correct horse"); !errors.Is(err, ErrGitHubDisabled) {
			t.Errorf("关闭之后发起 %v = %v，期望 ErrGitHubDisabled", intent, err)
		}
	}
	if len(f.q.taken) != 0 {
		t.Errorf("关闭之后发起授权取了额度 %v", f.q.taken)
	}
	calls := f.oauth.calls.Load()
	if client, _, err := f.g.Callback(t.Context(), stateOf(t, pending), "code-42"); !errors.Is(err, ErrGitHubDisabled) || client != port.GitHubApp {
		t.Errorf("授权进行中关闭，回调 = %v, %v，期望 ErrGitHubDisabled 并跳回 App", client, err)
	}
	if client, _, err := f.g.Callback(t.Context(), "forged", "code-42"); !errors.Is(err, ErrGitHubDisabled) || client != 0 {
		t.Errorf("关闭之后不存在的 state 回调 = %v, %v，期望 ErrGitHubDisabled：重新发起也没有用，不提示过期", client, err)
	}
	if got := f.oauth.calls.Load(); got != calls {
		t.Error("关闭之后不应请求 GitHub")
	}
	if _, err := f.g.Exchange(t.Context(), guest.Member, guest.MemberToken, exchange, "nonce"); !errors.Is(err, ErrGitHubDisabled) {
		t.Errorf("关闭之后兑换 = %v，期望 ErrGitHubDisabled", err)
	}
	if err := f.g.Unbind(t.Context(), alice.Member.ID, "correct horse"); !errors.Is(err, ErrGitHubDisabled) {
		t.Errorf("关闭之后解绑 = %v，期望 ErrGitHubDisabled", err)
	}
	if p, err := f.store.AccountProfile(t.Context(), alice.Member.ID); err != nil || !p.GitHub {
		t.Errorf("关闭之后账号的绑定 = %#v, %v，期望不变", p, err)
	}
	if p, err := f.store.AccountProfile(t.Context(), guest.Member.ID); err != nil || p.Registered() {
		t.Errorf("关闭之后游客 = %#v, %v，期望兑换没有注册账号", p, err)
	}

	if err := f.g.SetOAuthApp(t.Context(), port.GitHubOAuthApp{ClientID: "client-1", ClientSecret: "secret-1"}); err != nil {
		t.Fatal(err)
	}
	if result, err := f.flow(t, guest, port.GitHubLogin, "code-7"); err != nil || result.Account != (Account{GitHub: true}) {
		t.Errorf("重新开启之后用 GitHub 注册 = %#v, %v", result, err)
	}
}
