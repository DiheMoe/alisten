package porttest

import (
	"crypto/sha256"
	"errors"
	"slices"
	"testing"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/member"
)

func sessionHash(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

func newGuest(t *testing.T, s ContractStore, token, name string) member.Member {
	t.Helper()
	m, err := s.CreateMember(t.Context(), sessionHash(token), name)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func sessionOwner(t *testing.T, s ContractStore, token string) string {
	t.Helper()
	m, _, err := s.MemberByToken(t.Context(), sessionHash(token))
	if errors.Is(err, port.ErrMemberNotFound) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return m.ID
}

func registeredByToken(t *testing.T, s ContractStore, token string) bool {
	t.Helper()
	_, registered, err := s.MemberByToken(t.Context(), sessionHash(token))
	if err != nil {
		t.Fatal(err)
	}
	return registered
}

// 不计顺序。
func sameHashes(got, want [][]byte) bool {
	key := func(hashes [][]byte) []string {
		s := make([]string, len(hashes))
		for i, h := range hashes {
			s[i] = string(h)
		}
		slices.Sort(s)
		return s
	}
	return slices.Equal(key(got), key(want))
}

func favoritesCount(t *testing.T, s ContractStore, memberID string) int {
	t.Helper()
	lists, err := s.MemberPlaylists(t.Context(), memberID)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, l := range lists {
		if l.Favorites {
			n++
		}
	}
	return n
}

func ptr(s string) *string { return &s }

func runMembersContract(t *testing.T, open func(t *testing.T) ContractStore) {
	t.Run("按 token 找到新建的游客", func(t *testing.T) {
		s := open(t)
		m := newGuest(t, s, "alice", "游客-1a2b")
		if want := (member.Member{ID: m.ID, Name: "游客-1a2b"}); m != want {
			t.Errorf("新成员 = %#v，期望 %#v", m, want)
		}
		got, registered, err := s.MemberByToken(t.Context(), sessionHash("alice"))
		if err != nil || got != m || registered {
			t.Errorf("MemberByToken = %#v, %v, %v，期望未注册的 %#v", got, registered, err, m)
		}
		if _, _, err := s.MemberByToken(t.Context(), sessionHash("bob")); !errors.Is(err, port.ErrMemberNotFound) {
			t.Errorf("未知 token 的错误 = %v，期望 ErrMemberNotFound", err)
		}
		if _, err := s.CreateMember(t.Context(), sessionHash("alice"), "重复"); err == nil {
			t.Error("token 哈希重复时应失败")
		}
	})

	t.Run("修改成员只覆盖传入的字段", func(t *testing.T) {
		s := open(t)
		m := newGuest(t, s, "alice", "游客-1a2b")
		got, err := s.UpdateMember(t.Context(), m.ID, ptr("甲"), nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if want := (member.Member{ID: m.ID, Name: "甲"}); got != want {
			t.Errorf("只改昵称 = %#v，期望 %#v", got, want)
		}
		got, err = s.UpdateMember(t.Context(), m.ID, nil, ptr("0bc83cb571cd1c50ba6f3e8a78ef1346"), nil)
		if err != nil {
			t.Fatal(err)
		}
		if want := (member.Member{ID: m.ID, Name: "甲", AvatarHash: "0bc83cb571cd1c50ba6f3e8a78ef1346"}); got != want {
			t.Errorf("只改头像 = %#v，期望 %#v", got, want)
		}
		if found, _, _ := s.MemberByToken(t.Context(), sessionHash("alice")); found != got {
			t.Errorf("修改后按 token 查到 %#v，期望 %#v", found, got)
		}
		got, err = s.UpdateMember(t.Context(), m.ID, nil, ptr(""), nil)
		if want := (member.Member{ID: m.ID, Name: "甲"}); err != nil || got != want {
			t.Errorf("清除头像 = %#v, %v，期望 %#v", got, err, want)
		}
		if _, err := s.UpdateMember(t.Context(), missingID, ptr("乙"), nil, nil); !errors.Is(err, port.ErrMemberNotFound) {
			t.Errorf("修改不存在的成员，错误 = %v，期望 ErrMemberNotFound", err)
		}
	})

	t.Run("邮箱只保存给账号", func(t *testing.T) {
		s := open(t)
		alice := newGuest(t, s, "alice", "游客-1a2b")
		guest := newGuest(t, s, "guest", "游客-3c4d")
		if err := s.Register(t.Context(), alice.ID, "alice", "hash-a"); err != nil {
			t.Fatal(err)
		}
		profile := func() port.AccountProfile {
			t.Helper()
			p, err := s.AccountProfile(t.Context(), alice.ID)
			if err != nil {
				t.Fatal(err)
			}
			return p
		}

		if _, err := s.UpdateMember(t.Context(), alice.ID, nil, nil, ptr("Alice@Example.COM")); err != nil {
			t.Fatal(err)
		}
		if got := profile(); got != (port.AccountProfile{Username: "alice", Email: "Alice@Example.COM"}) {
			t.Errorf("保存邮箱之后 AccountProfile = %#v", got)
		}
		if account, err := s.AccountByUsername(t.Context(), "alice"); err != nil || account.Email != "Alice@Example.COM" {
			t.Errorf("AccountByUsername 的邮箱 = %q, %v", account.Email, err)
		}
		if _, err := s.UpdateMember(t.Context(), alice.ID, ptr("甲"), nil, nil); err != nil {
			t.Fatal(err)
		}
		if got := profile().Email; got != "Alice@Example.COM" {
			t.Errorf("邮箱参数为 nil 时应保持原值，得到 %q", got)
		}
		if _, err := s.UpdateMember(t.Context(), alice.ID, nil, nil, ptr("b@example.com")); err != nil {
			t.Fatal(err)
		}
		if got := profile().Email; got != "b@example.com" {
			t.Errorf("更新邮箱之后 = %q", got)
		}
		if _, err := s.UpdateMember(t.Context(), alice.ID, nil, nil, ptr("")); err != nil {
			t.Fatal(err)
		}
		if got := profile().Email; got != "" {
			t.Errorf("清除邮箱之后 = %q，期望为空", got)
		}

		if _, err := s.UpdateMember(t.Context(), guest.ID, nil, nil, ptr("g@example.com")); err != nil {
			t.Fatal(err)
		}
		if p, err := s.AccountProfile(t.Context(), guest.ID); err != nil || p != (port.AccountProfile{}) {
			t.Errorf("游客传入邮箱之后的 AccountProfile = %#v, %v，期望各字段为空", p, err)
		}
	})

	t.Run("注册游客并按用户名查找账号", func(t *testing.T) {
		s := open(t)
		alice := newGuest(t, s, "alice", "游客-1a2b")
		bob := newGuest(t, s, "bob", "游客-3c4d")
		if profile, err := s.AccountProfile(t.Context(), alice.ID); err != nil || profile != (port.AccountProfile{}) {
			t.Fatalf("游客的 AccountProfile = %#v, %v，期望两个字段都为空", profile, err)
		}
		if registeredByToken(t, s, "alice") {
			t.Error("注册之前 MemberByToken 的 registered 应为 false")
		}
		if err := s.Register(t.Context(), alice.ID, "alice", "hash-a"); err != nil {
			t.Fatal(err)
		}
		if profile, err := s.AccountProfile(t.Context(), alice.ID); err != nil || profile != (port.AccountProfile{Username: "alice"}) {
			t.Errorf("注册之后的 AccountProfile = %#v, %v，期望只有用户名 alice", profile, err)
		}
		if !registeredByToken(t, s, "alice") {
			t.Error("注册之后 MemberByToken 的 registered 应为 true")
		}
		account, err := s.AccountByUsername(t.Context(), "alice")
		if want := (port.Account{Member: alice, Username: "alice", PasswordHash: "hash-a"}); err != nil || account != want {
			t.Errorf("AccountByUsername = %#v, %v，期望 %#v", account, err, want)
		}
		if err := s.Register(t.Context(), bob.ID, "alice", "hash-b"); !errors.Is(err, port.ErrUsernameTaken) {
			t.Errorf("用户名重复时 = %v，期望 ErrUsernameTaken", err)
		}
		if err := s.Register(t.Context(), alice.ID, "alice2", "hash-c"); !errors.Is(err, port.ErrAlreadyRegistered) {
			t.Errorf("已经注册过的成员再注册 = %v，期望 ErrAlreadyRegistered", err)
		}
		if _, err := s.AccountByUsername(t.Context(), "nobody"); !errors.Is(err, port.ErrMemberNotFound) {
			t.Errorf("不存在的用户名 = %v，期望 ErrMemberNotFound", err)
		}
		if _, err := s.AccountProfile(t.Context(), missingID); !errors.Is(err, port.ErrMemberNotFound) {
			t.Errorf("不存在的成员 = %v，期望 ErrMemberNotFound", err)
		}
	})

	t.Run("换会话、改密码和删除会话", func(t *testing.T) {
		s := open(t)
		alice := newGuest(t, s, "alice-1", "游客-1a2b")
		newGuest(t, s, "guest-1", "游客-3c4d")
		newGuest(t, s, "guest-2", "游客-5e6f")
		for _, session := range []struct{ guest, token string }{{"guest-1", "alice-2"}, {"guest-2", "alice-3"}} {
			if err := s.ReplaceSession(t.Context(), sessionHash(session.guest), alice.ID, sessionHash(session.token), ""); err != nil {
				t.Fatal(err)
			}
			if got := sessionOwner(t, s, session.guest); got != "" {
				t.Errorf("换掉之后 %s 仍属于成员 %s", session.guest, got)
			}
			if got := sessionOwner(t, s, session.token); got != alice.ID {
				t.Errorf("%s 属于成员 %q，期望 %s", session.token, got, alice.ID)
			}
		}
		if err := s.Register(t.Context(), alice.ID, "alice", "old"); err != nil {
			t.Fatal(err)
		}
		deleted, err := s.ChangePassword(t.Context(), alice.ID, "old", "new", sessionHash("alice-1"))
		if want := [][]byte{sessionHash("alice-2"), sessionHash("alice-3")}; err != nil || !sameHashes(deleted, want) {
			t.Errorf("ChangePassword 返回 %x, %v，期望被删除的 alice-2、alice-3", deleted, err)
		}
		if sessionOwner(t, s, "alice-1") != alice.ID || sessionOwner(t, s, "alice-2") != "" || sessionOwner(t, s, "alice-3") != "" {
			t.Error("ChangePassword 应只留下发出请求的会话")
		}
		if err := s.DeleteSession(t.Context(), sessionHash("alice-1")); err != nil {
			t.Fatal(err)
		}
		if got := sessionOwner(t, s, "alice-1"); got != "" {
			t.Errorf("删除之后 alice-1 仍属于成员 %s", got)
		}
	})

	t.Run("修改和重置密码删除其他会话", func(t *testing.T) {
		s := open(t)
		guest := newGuest(t, s, "guest", "游客-1a2b")
		if _, err := s.SetPassword(t.Context(), guest.ID, "hash"); !errors.Is(err, port.ErrMemberNotFound) {
			t.Errorf("给游客设置密码 = %v，期望 ErrMemberNotFound", err)
		}
		if _, err := s.ChangePassword(t.Context(), guest.ID, "", "hash", sessionHash("guest")); !errors.Is(err, port.ErrMemberNotFound) {
			t.Errorf("游客修改密码 = %v，期望 ErrMemberNotFound", err)
		}
		alice := newGuest(t, s, "alice-1", "游客-3c4d")
		if err := s.Register(t.Context(), alice.ID, "alice", "old"); err != nil {
			t.Fatal(err)
		}
		newGuest(t, s, "other", "游客-5e6f")
		if err := s.ReplaceSession(t.Context(), sessionHash("other"), alice.ID, sessionHash("alice-2"), "old"); err != nil {
			t.Fatal(err)
		}
		if deleted, err := s.ChangePassword(t.Context(), alice.ID, "old", "new", sessionHash("alice-1")); err != nil || !sameHashes(deleted, [][]byte{sessionHash("alice-2")}) {
			t.Errorf("ChangePassword 返回 %x, %v，期望被删除的 alice-2", deleted, err)
		}
		if account, err := s.AccountByUsername(t.Context(), "alice"); err != nil || account.PasswordHash != "new" {
			t.Errorf("修改之后的密码哈希 = %q, %v，期望 new", account.PasswordHash, err)
		}
		if sessionOwner(t, s, "alice-1") != alice.ID || sessionOwner(t, s, "alice-2") != "" {
			t.Error("修改密码应只留下指定的会话")
		}
		if deleted, err := s.SetPassword(t.Context(), alice.ID, "newer"); err != nil || !sameHashes(deleted, [][]byte{sessionHash("alice-1")}) {
			t.Errorf("SetPassword 返回 %x, %v，期望被删除的 alice-1", deleted, err)
		}
		if got := sessionOwner(t, s, "alice-1"); got != "" {
			t.Errorf("SetPassword 应删除全部会话，alice-1 仍属于 %s", got)
		}
	})

	t.Run("修改密码前检查会话和当前密码", func(t *testing.T) {
		s := open(t)
		alice := newGuest(t, s, "alice-1", "游客-1a2b")
		if err := s.Register(t.Context(), alice.ID, "alice", "h1"); err != nil {
			t.Fatal(err)
		}
		newGuest(t, s, "guest", "游客-3c4d")
		newGuest(t, s, "bob", "游客-5e6f")
		if err := s.ReplaceSession(t.Context(), sessionHash("guest"), alice.ID, sessionHash("alice-2"), "h1"); err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			name, current, keep string
			want                error
		}{
			{"发出请求的会话已经不存在", "h1", "gone", port.ErrSessionNotFound},
			{"会话属于别的成员", "h1", "bob", port.ErrSessionNotFound},
			{"密码哈希已经变了", "h0", "alice-1", port.ErrAccountPasswordChanged},
		} {
			if _, err := s.ChangePassword(t.Context(), alice.ID, tc.current, "h2", sessionHash(tc.keep)); !errors.Is(err, tc.want) {
				t.Errorf("%s时 = %v，期望 %v", tc.name, err, tc.want)
			}
		}
		if account, err := s.AccountByUsername(t.Context(), "alice"); err != nil || account.PasswordHash != "h1" {
			t.Errorf("失败之后的密码哈希 = %q, %v，期望不变的 h1", account.PasswordHash, err)
		}
		if sessionOwner(t, s, "alice-1") != alice.ID || sessionOwner(t, s, "alice-2") != alice.ID {
			t.Error("失败时不应删除任何会话")
		}
	})

	t.Run("换会话前检查游客的会话和密码", func(t *testing.T) {
		s := open(t)
		alice := newGuest(t, s, "alice-1", "游客-1a2b")
		if err := s.Register(t.Context(), alice.ID, "alice", "h1"); err != nil {
			t.Fatal(err)
		}
		newGuest(t, s, "guest", "游客-3c4d")

		if err := s.ReplaceSession(t.Context(), sessionHash("gone"), alice.ID, sessionHash("alice-2"), "h1"); !errors.Is(err, port.ErrSessionNotFound) {
			t.Errorf("游客的会话已经删除时 = %v，期望 ErrSessionNotFound", err)
		}
		if err := s.ReplaceSession(t.Context(), sessionHash("guest"), alice.ID, sessionHash("alice-2"), "h0"); !errors.Is(err, port.ErrAccountPasswordChanged) {
			t.Errorf("密码哈希已经变了时 = %v，期望 ErrAccountPasswordChanged", err)
		}
		if sessionOwner(t, s, "alice-2") != "" || sessionOwner(t, s, "guest") == "" {
			t.Error("失败时不应创建新会话，也不应删除游客的会话")
		}
		if err := s.ReplaceSession(t.Context(), sessionHash("guest"), alice.ID, sessionHash("alice-2"), "h1"); err != nil {
			t.Fatal(err)
		}
		if err := s.ReplaceSession(t.Context(), sessionHash("guest"), alice.ID, sessionHash("alice-3"), "h1"); !errors.Is(err, port.ErrSessionNotFound) {
			t.Errorf("同一个游客会话再登录一次 = %v，期望 ErrSessionNotFound", err)
		}
		if sessionOwner(t, s, "alice-3") != "" {
			t.Error("同一个游客会话只能换出一个新会话")
		}
	})

	t.Run("游客绑定 GitHub 就是注册", func(t *testing.T) {
		s := open(t)
		alice := newGuest(t, s, "alice", "游客-1a2b")
		bob := newGuest(t, s, "bob", "游客-3c4d")
		if err := s.UnbindGitHub(t.Context(), alice.ID); !errors.Is(err, port.ErrNoPassword) {
			t.Errorf("游客解绑 = %v，期望 ErrNoPassword", err)
		}
		if registeredByToken(t, s, "alice") {
			t.Error("绑定之前 MemberByToken 的 registered 应为 false")
		}
		if err := s.BindGitHub(t.Context(), alice.ID, 42); err != nil {
			t.Fatal(err)
		}
		if p, err := s.AccountProfile(t.Context(), alice.ID); err != nil || p != (port.AccountProfile{GitHub: true}) || !p.Registered() {
			t.Errorf("绑定之后的 AccountProfile = %#v, %v，期望只绑定了 GitHub 的已注册账号", p, err)
		}
		if !registeredByToken(t, s, "alice") {
			t.Error("只绑定了 GitHub 之后 MemberByToken 的 registered 应为 true")
		}
		want := port.Account{Member: alice, GitHubID: 42}
		if got, err := s.AccountByGitHub(t.Context(), 42); err != nil || got != want {
			t.Errorf("AccountByGitHub = %#v, %v，期望 %#v", got, err, want)
		}
		if got, err := s.AccountByID(t.Context(), alice.ID); err != nil || got != want {
			t.Errorf("AccountByID = %#v, %v，期望 %#v", got, err, want)
		}
		if n := favoritesCount(t, s, alice.ID); n != 1 {
			t.Errorf("游客绑定 GitHub 就是注册，应创建一个“我喜欢”，实际 %d 个", n)
		}

		if err := s.BindGitHub(t.Context(), bob.ID, 42); !errors.Is(err, port.ErrGitHubTaken) {
			t.Errorf("GitHub 账号已经绑定了别人 = %v，期望 ErrGitHubTaken", err)
		}
		if err := s.BindGitHub(t.Context(), alice.ID, 43); !errors.Is(err, port.ErrGitHubAlreadyBound) {
			t.Errorf("已经绑定过的成员再绑定 = %v，期望 ErrGitHubAlreadyBound", err)
		}
		if p, err := s.AccountProfile(t.Context(), bob.ID); err != nil || p.Registered() || favoritesCount(t, s, bob.ID) != 0 || registeredByToken(t, s, "bob") {
			t.Errorf("绑定失败之后 bob 的 AccountProfile = %#v, %v，应仍是游客", p, err)
		}
		if _, err := s.AccountByGitHub(t.Context(), 99); !errors.Is(err, port.ErrMemberNotFound) {
			t.Errorf("没有绑定的 GitHub 账号 = %v，期望 ErrMemberNotFound", err)
		}
		if _, err := s.AccountByID(t.Context(), bob.ID); !errors.Is(err, port.ErrMemberNotFound) {
			t.Errorf("游客的 AccountByID = %v，期望 ErrMemberNotFound", err)
		}

		if _, err := s.UpdateMember(t.Context(), alice.ID, nil, nil, ptr("alice@example.com")); err != nil {
			t.Fatal(err)
		}
		if p, err := s.AccountProfile(t.Context(), alice.ID); err != nil || p.Email != "alice@example.com" {
			t.Errorf("只绑定了 GitHub 的账号保存邮箱之后 AccountProfile = %#v, %v", p, err)
		}
	})

	t.Run("只绑定了 GitHub 的账号补设用户名和密码", func(t *testing.T) {
		s := open(t)
		alice := newGuest(t, s, "alice", "游客-1a2b")
		if err := s.BindGitHub(t.Context(), alice.ID, 42); err != nil {
			t.Fatal(err)
		}
		if err := s.UnbindGitHub(t.Context(), alice.ID); !errors.Is(err, port.ErrNoPassword) {
			t.Errorf("没有密码的账号解绑 = %v，期望 ErrNoPassword", err)
		}
		if err := s.Register(t.Context(), alice.ID, "alice", "hash-a"); !errors.Is(err, port.ErrAlreadyRegistered) {
			t.Errorf("只绑定了 GitHub 的账号用 Register 设置用户名 = %v，期望 ErrAlreadyRegistered", err)
		}
		guest := newGuest(t, s, "guest", "游客-5e6f")
		if _, err := s.AddCredentials(t.Context(), guest.ID, "guest", "hash-g", sessionHash("guest")); !errors.Is(err, port.ErrAlreadyRegistered) {
			t.Errorf("游客用 AddCredentials 设置用户名 = %v，期望 ErrAlreadyRegistered", err)
		}
		if err := s.Register(t.Context(), guest.ID, "taken", "hash-g"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.AddCredentials(t.Context(), alice.ID, "taken", "hash-a", sessionHash("alice")); !errors.Is(err, port.ErrUsernameTaken) {
			t.Errorf("用户名已被占用时 = %v，期望 ErrUsernameTaken", err)
		}
		newGuest(t, s, "other", "游客-7a8b")
		if err := s.ReplaceSession(t.Context(), sessionHash("other"), alice.ID, sessionHash("alice-2"), ""); err != nil {
			t.Fatal(err)
		}
		deleted, err := s.AddCredentials(t.Context(), alice.ID, "alice", "hash-a", sessionHash("alice"))
		if err != nil || !sameHashes(deleted, [][]byte{sessionHash("alice-2")}) {
			t.Fatalf("AddCredentials 返回 %x, %v，期望被删除的 alice-2", deleted, err)
		}
		if sessionOwner(t, s, "alice") != alice.ID || sessionOwner(t, s, "alice-2") != "" {
			t.Error("AddCredentials 应只留下发出请求的会话")
		}
		if _, err := s.AddCredentials(t.Context(), alice.ID, "alice2", "hash-c", sessionHash("alice")); !errors.Is(err, port.ErrAlreadyRegistered) {
			t.Errorf("再补设一次 = %v，期望 ErrAlreadyRegistered", err)
		}
		if n := favoritesCount(t, s, alice.ID); n != 1 {
			t.Errorf("只绑定了 GitHub 的账号补设用户名之后有 %d 个“我喜欢”，期望仍是 1 个", n)
		}
		want := port.Account{Member: alice, Username: "alice", PasswordHash: "hash-a", GitHubID: 42}
		if got, err := s.AccountByUsername(t.Context(), "alice"); err != nil || got != want {
			t.Errorf("AccountByUsername = %#v, %v，期望 %#v", got, err, want)
		}
		if err := s.UnbindGitHub(t.Context(), alice.ID); err != nil {
			t.Fatal(err)
		}
		if p, err := s.AccountProfile(t.Context(), alice.ID); err != nil || p != (port.AccountProfile{Username: "alice"}) {
			t.Errorf("解绑之后的 AccountProfile = %#v, %v", p, err)
		}
		if _, err := s.AccountByGitHub(t.Context(), 42); !errors.Is(err, port.ErrMemberNotFound) {
			t.Errorf("解绑之后按 GitHub 查找 = %v，期望 ErrMemberNotFound", err)
		}
		if err := s.UnbindGitHub(t.Context(), alice.ID); err != nil {
			t.Errorf("没有绑定时解绑 = %v，期望什么也不做", err)
		}

		bob := newGuest(t, s, "bob", "游客-3c4d")
		if err := s.Register(t.Context(), bob.ID, "bob", "hash-b"); err != nil {
			t.Fatal(err)
		}
		if err := s.BindGitHub(t.Context(), bob.ID, 42); err != nil {
			t.Fatal(err)
		}
		if n := favoritesCount(t, s, bob.ID); n != 1 {
			t.Errorf("已注册的账号绑定 GitHub 之后有 %d 个“我喜欢”，期望仍是 1 个", n)
		}
	})
}
