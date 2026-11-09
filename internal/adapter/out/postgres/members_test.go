package postgres

import (
	"crypto/sha256"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/member"
)

func tokenHash(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

func createMember(t *testing.T, store *Store, token, name string) member.Member {
	t.Helper()
	m, err := store.CreateMember(t.Context(), tokenHash(token), name)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestCreateAndFindMember(t *testing.T) {
	store, _ := newStore(t)
	m := createMember(t, store, "alice", "游客-1a2b")
	if !isUUID(m.ID) || m.ID[14] != '7' {
		t.Errorf("成员 ID %q 应是数据库生成的 UUIDv7", m.ID)
	}
	if want := (member.Member{ID: m.ID, Name: "游客-1a2b"}); m != want {
		t.Errorf("新成员 = %#v，期望 %#v", m, want)
	}

	got, err := store.MemberByToken(t.Context(), tokenHash("alice"))
	if err != nil || got != m {
		t.Errorf("MemberByToken = %#v, %v，期望 %#v", got, err, m)
	}
	if _, err := store.MemberByToken(t.Context(), tokenHash("bob")); !errors.Is(err, port.ErrMemberNotFound) {
		t.Errorf("未知 token 的错误 = %v，期望 ErrMemberNotFound", err)
	}
	if _, err := store.CreateMember(t.Context(), tokenHash("alice"), "重复"); err == nil {
		t.Error("token 哈希重复时应失败")
	}
}

func TestMemberAvatarIsNullWhenUnset(t *testing.T) {
	store, pool := newStore(t)
	m := createMember(t, store, "alice", "游客-1a2b")
	avatar := func() string {
		t.Helper()
		var hash string
		if err := pool.QueryRow(t.Context(), "SELECT coalesce(avatar_hash, 'NULL') FROM members WHERE id = $1", m.ID).Scan(&hash); err != nil {
			t.Fatal(err)
		}
		return hash
	}
	if hash := avatar(); hash != "NULL" {
		t.Errorf("新成员的 avatar_hash = %q，期望 NULL", hash)
	}
	if _, err := store.UpdateMember(t.Context(), m.ID, nil, ptr("0bc83cb571cd1c50ba6f3e8a78ef1346"), nil); err != nil {
		t.Fatal(err)
	}
	if hash := avatar(); hash != "0bc83cb571cd1c50ba6f3e8a78ef1346" {
		t.Errorf("设置头像之后 avatar_hash = %q", hash)
	}
	got, err := store.UpdateMember(t.Context(), m.ID, nil, ptr(""), nil)
	if err != nil {
		t.Fatal(err)
	}
	if hash := avatar(); hash != "NULL" || got.AvatarHash != "" {
		t.Errorf("清除头像之后 avatar_hash = %q、成员的头像哈希 = %q，期望 NULL 和空串", hash, got.AvatarHash)
	}
}

func TestUpdateMemberKeepsOmittedFields(t *testing.T) {
	store, _ := newStore(t)
	m := createMember(t, store, "alice", "游客-1a2b")

	got, err := store.UpdateMember(t.Context(), m.ID, ptr("甲"), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := (member.Member{ID: m.ID, Name: "甲"}); got != want {
		t.Errorf("只改昵称 = %#v，期望 %#v", got, want)
	}
	got, err = store.UpdateMember(t.Context(), m.ID, nil, ptr("0bc83cb571cd1c50ba6f3e8a78ef1346"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := (member.Member{ID: m.ID, Name: "甲", AvatarHash: "0bc83cb571cd1c50ba6f3e8a78ef1346"}); got != want {
		t.Errorf("只改头像 = %#v，期望 %#v", got, want)
	}
	if found, _ := store.MemberByToken(t.Context(), tokenHash("alice")); found != got {
		t.Errorf("修改后按 token 查到 %#v，期望 %#v", found, got)
	}

	if _, err := store.UpdateMember(t.Context(), "01a15778-1911-7a28-8b2e-e08c2a25f388", ptr("乙"), nil, nil); !errors.Is(err, port.ErrMemberNotFound) {
		t.Errorf("修改不存在的成员，错误 = %v，期望 ErrMemberNotFound", err)
	}
}

func TestUpdateMemberEmail(t *testing.T) {
	store, pool := newStore(t)
	alice := createMember(t, store, "alice", "游客-1a2b")
	guest := createMember(t, store, "guest", "游客-3c4d")
	if err := store.Register(t.Context(), alice.ID, "alice", "hash-a"); err != nil {
		t.Fatal(err)
	}
	column := func(id string) string {
		t.Helper()
		var email string
		if err := pool.QueryRow(t.Context(), "SELECT coalesce(email, 'NULL') FROM members WHERE id = $1", id).Scan(&email); err != nil {
			t.Fatal(err)
		}
		return email
	}
	profile := func() port.AccountProfile {
		t.Helper()
		p, err := store.AccountProfile(t.Context(), alice.ID)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}

	if _, err := store.UpdateMember(t.Context(), alice.ID, nil, nil, ptr("Alice@Example.COM")); err != nil {
		t.Fatal(err)
	}
	if got := profile(); got != (port.AccountProfile{Username: "alice", Email: "Alice@Example.COM"}) {
		t.Errorf("保存邮箱之后 AccountProfile = %#v", got)
	}
	if account, err := store.AccountByUsername(t.Context(), "alice"); err != nil || account.Email != "Alice@Example.COM" {
		t.Errorf("AccountByUsername 的邮箱 = %q, %v", account.Email, err)
	}
	if _, err := store.UpdateMember(t.Context(), alice.ID, ptr("甲"), nil, nil); err != nil {
		t.Fatal(err)
	}
	if got := profile().Email; got != "Alice@Example.COM" {
		t.Errorf("邮箱参数为 nil 时应保持原值，得到 %q", got)
	}
	if _, err := store.UpdateMember(t.Context(), alice.ID, nil, nil, ptr("b@example.com")); err != nil {
		t.Fatal(err)
	}
	if got := profile().Email; got != "b@example.com" {
		t.Errorf("更新邮箱之后 = %q", got)
	}
	if _, err := store.UpdateMember(t.Context(), alice.ID, nil, nil, ptr("")); err != nil {
		t.Fatal(err)
	}
	if got := column(alice.ID); got != "NULL" {
		t.Errorf("清除邮箱之后 email 列 = %q，期望 NULL", got)
	}

	if _, err := store.UpdateMember(t.Context(), guest.ID, nil, nil, ptr("g@example.com")); err != nil {
		t.Fatal(err)
	}
	if got := column(guest.ID); got != "NULL" {
		t.Errorf("游客传入邮箱之后 email 列 = %q，期望 NULL", got)
	}
	if p, err := store.AccountProfile(t.Context(), guest.ID); err != nil || p != (port.AccountProfile{}) {
		t.Errorf("游客的 AccountProfile = %#v, %v", p, err)
	}
}

func ownerOf(t *testing.T, store *Store, token string) string {
	t.Helper()
	m, err := store.MemberByToken(t.Context(), tokenHash(token))
	if errors.Is(err, port.ErrMemberNotFound) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return m.ID
}

func TestRegisterMember(t *testing.T) {
	store, _ := newStore(t)
	alice := createMember(t, store, "alice", "游客-1a2b")
	bob := createMember(t, store, "bob", "游客-3c4d")
	if profile, err := store.AccountProfile(t.Context(), alice.ID); err != nil || profile != (port.AccountProfile{}) {
		t.Fatalf("游客的 AccountProfile = %#v, %v，期望两个字段都为空", profile, err)
	}
	if err := store.Register(t.Context(), alice.ID, "alice", "hash-a"); err != nil {
		t.Fatal(err)
	}
	if profile, err := store.AccountProfile(t.Context(), alice.ID); err != nil || profile != (port.AccountProfile{Username: "alice"}) {
		t.Errorf("注册之后的 AccountProfile = %#v, %v，期望只有用户名 alice", profile, err)
	}
	account, err := store.AccountByUsername(t.Context(), "alice")
	if want := (port.Account{Member: alice, Username: "alice", PasswordHash: "hash-a"}); err != nil || account != want {
		t.Errorf("AccountByUsername = %#v, %v，期望 %#v", account, err, want)
	}
	if err := store.Register(t.Context(), bob.ID, "alice", "hash-b"); !errors.Is(err, port.ErrUsernameTaken) {
		t.Errorf("用户名重复时 = %v，期望 ErrUsernameTaken", err)
	}
	if err := store.Register(t.Context(), alice.ID, "alice2", "hash-c"); !errors.Is(err, port.ErrAlreadyRegistered) {
		t.Errorf("已经注册过的成员再注册 = %v，期望 ErrAlreadyRegistered", err)
	}
	if _, err := store.AccountByUsername(t.Context(), "nobody"); !errors.Is(err, port.ErrMemberNotFound) {
		t.Errorf("不存在的用户名 = %v，期望 ErrMemberNotFound", err)
	}
	if _, err := store.AccountProfile(t.Context(), "00000000-0000-7000-8000-000000000000"); !errors.Is(err, port.ErrMemberNotFound) {
		t.Errorf("不存在的成员 = %v，期望 ErrMemberNotFound", err)
	}
}

func TestMemberSessions(t *testing.T) {
	store, _ := newStore(t)
	alice := createMember(t, store, "alice-1", "游客-1a2b")
	createMember(t, store, "guest-1", "游客-3c4d")
	createMember(t, store, "guest-2", "游客-5e6f")
	for _, s := range []struct{ guest, token string }{{"guest-1", "alice-2"}, {"guest-2", "alice-3"}} {
		if err := store.ReplaceSession(t.Context(), tokenHash(s.guest), alice.ID, tokenHash(s.token), ""); err != nil {
			t.Fatal(err)
		}
		if got := ownerOf(t, store, s.guest); got != "" {
			t.Errorf("换掉之后 %s 仍属于成员 %s", s.guest, got)
		}
		if got := ownerOf(t, store, s.token); got != alice.ID {
			t.Errorf("%s 属于成员 %q，期望 %s", s.token, got, alice.ID)
		}
	}
	if err := store.Register(t.Context(), alice.ID, "alice", "old"); err != nil {
		t.Fatal(err)
	}
	if err := store.ChangePassword(t.Context(), alice.ID, "old", "new", tokenHash("alice-1")); err != nil {
		t.Fatal(err)
	}
	if ownerOf(t, store, "alice-1") != alice.ID || ownerOf(t, store, "alice-2") != "" || ownerOf(t, store, "alice-3") != "" {
		t.Error("ChangePassword 应只留下发出请求的会话")
	}
	if err := store.DeleteSession(t.Context(), tokenHash("alice-1")); err != nil {
		t.Fatal(err)
	}
	if got := ownerOf(t, store, "alice-1"); got != "" {
		t.Errorf("删除之后 alice-1 仍属于成员 %s", got)
	}
}

func TestSetPassword(t *testing.T) {
	store, _ := newStore(t)
	guest := createMember(t, store, "guest", "游客-1a2b")
	if err := store.SetPassword(t.Context(), guest.ID, "hash"); !errors.Is(err, port.ErrMemberNotFound) {
		t.Errorf("给游客设置密码 = %v，期望 ErrMemberNotFound", err)
	}
	if err := store.ChangePassword(t.Context(), guest.ID, "", "hash", tokenHash("guest")); !errors.Is(err, port.ErrMemberNotFound) {
		t.Errorf("游客修改密码 = %v，期望 ErrMemberNotFound", err)
	}
	alice := createMember(t, store, "alice-1", "游客-3c4d")
	if err := store.Register(t.Context(), alice.ID, "alice", "old"); err != nil {
		t.Fatal(err)
	}
	createMember(t, store, "other", "游客-5e6f")
	if err := store.ReplaceSession(t.Context(), tokenHash("other"), alice.ID, tokenHash("alice-2"), "old"); err != nil {
		t.Fatal(err)
	}
	if err := store.ChangePassword(t.Context(), alice.ID, "old", "new", tokenHash("alice-1")); err != nil {
		t.Fatal(err)
	}
	if account, err := store.AccountByUsername(t.Context(), "alice"); err != nil || account.PasswordHash != "new" {
		t.Errorf("修改之后的密码哈希 = %q, %v，期望 new", account.PasswordHash, err)
	}
	if ownerOf(t, store, "alice-1") != alice.ID || ownerOf(t, store, "alice-2") != "" {
		t.Error("修改密码应只留下指定的会话")
	}
	if err := store.SetPassword(t.Context(), alice.ID, "newer"); err != nil {
		t.Fatal(err)
	}
	if got := ownerOf(t, store, "alice-1"); got != "" {
		t.Errorf("SetPassword 应删除全部会话，alice-1 仍属于 %s", got)
	}
}

func TestChangePasswordChecksSessionAndPassword(t *testing.T) {
	store, _ := newStore(t)
	alice := createMember(t, store, "alice-1", "游客-1a2b")
	if err := store.Register(t.Context(), alice.ID, "alice", "h1"); err != nil {
		t.Fatal(err)
	}
	createMember(t, store, "guest", "游客-3c4d")
	createMember(t, store, "bob", "游客-5e6f")
	if err := store.ReplaceSession(t.Context(), tokenHash("guest"), alice.ID, tokenHash("alice-2"), "h1"); err != nil {
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
		if err := store.ChangePassword(t.Context(), alice.ID, tc.current, "h2", tokenHash(tc.keep)); !errors.Is(err, tc.want) {
			t.Errorf("%s时 = %v，期望 %v", tc.name, err, tc.want)
		}
	}
	if account, err := store.AccountByUsername(t.Context(), "alice"); err != nil || account.PasswordHash != "h1" {
		t.Errorf("失败之后的密码哈希 = %q, %v，期望不变的 h1", account.PasswordHash, err)
	}
	if ownerOf(t, store, "alice-1") != alice.ID || ownerOf(t, store, "alice-2") != alice.ID {
		t.Error("失败时不应删除任何会话")
	}
}

// 模拟 SetPassword 或另一次 ChangePassword 的事务，先不提交：改密码哈希，删除 keep 以外的会话，keep 为 nil 时全部删除。
func changePasswordMeanwhile(t *testing.T, store *Store, pool *pgxpool.Pool, memberID string, keep []byte, want error) {
	t.Helper()
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	if _, err := tx.Exec(t.Context(), "UPDATE members SET password_hash = 'h2' WHERE id = $1", memberID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), "DELETE FROM member_sessions WHERE member_id = $1 AND token_hash IS DISTINCT FROM $2", memberID, keep); err != nil {
		t.Fatal(err)
	}

	result := make(chan error, 1)
	go func() {
		result <- store.ChangePassword(t.Context(), memberID, "h1", "h3", tokenHash("alice-1"))
	}()
	waitForLockWait(t, pool, 1)
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := <-result; !errors.Is(err, want) {
		t.Errorf("另一次修改提交之后，ChangePassword = %v，期望 %v", err, want)
	}
	if account, err := store.AccountByUsername(t.Context(), "alice"); err != nil || account.PasswordHash != "h2" {
		t.Errorf("密码哈希 = %q, %v，期望先提交的那次写入的 h2", account.PasswordHash, err)
	}
}

func TestChangePasswordWaitsForReset(t *testing.T) {
	store, pool := newStore(t)
	alice := createMember(t, store, "alice-1", "游客-1a2b")
	if err := store.Register(t.Context(), alice.ID, "alice", "h1"); err != nil {
		t.Fatal(err)
	}
	changePasswordMeanwhile(t, store, pool, alice.ID, nil, port.ErrSessionNotFound)
}

func TestChangePasswordWaitsForSameSessionChange(t *testing.T) {
	store, pool := newStore(t)
	alice := createMember(t, store, "alice-1", "游客-1a2b")
	if err := store.Register(t.Context(), alice.ID, "alice", "h1"); err != nil {
		t.Fatal(err)
	}
	changePasswordMeanwhile(t, store, pool, alice.ID, tokenHash("alice-1"), port.ErrAccountPasswordChanged)
	if ownerOf(t, store, "alice-1") != alice.ID {
		t.Error("同一个会话先改过密码，这个会话应仍然有效")
	}
}

func TestReplaceSessionChecksGuestSessionAndPassword(t *testing.T) {
	store, _ := newStore(t)
	alice := createMember(t, store, "alice-1", "游客-1a2b")
	if err := store.Register(t.Context(), alice.ID, "alice", "h1"); err != nil {
		t.Fatal(err)
	}
	createMember(t, store, "guest", "游客-3c4d")

	if err := store.ReplaceSession(t.Context(), tokenHash("gone"), alice.ID, tokenHash("alice-2"), "h1"); !errors.Is(err, port.ErrSessionNotFound) {
		t.Errorf("游客的会话已经删除时 = %v，期望 ErrSessionNotFound", err)
	}
	if err := store.ReplaceSession(t.Context(), tokenHash("guest"), alice.ID, tokenHash("alice-2"), "h0"); !errors.Is(err, port.ErrAccountPasswordChanged) {
		t.Errorf("密码哈希已经变了时 = %v，期望 ErrAccountPasswordChanged", err)
	}
	if ownerOf(t, store, "alice-2") != "" || ownerOf(t, store, "guest") == "" {
		t.Error("失败时不应创建新会话，也不应删除游客的会话")
	}
	if err := store.ReplaceSession(t.Context(), tokenHash("guest"), alice.ID, tokenHash("alice-2"), "h1"); err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceSession(t.Context(), tokenHash("guest"), alice.ID, tokenHash("alice-3"), "h1"); !errors.Is(err, port.ErrSessionNotFound) {
		t.Errorf("同一个游客会话再登录一次 = %v，期望 ErrSessionNotFound", err)
	}
	if ownerOf(t, store, "alice-3") != "" {
		t.Error("同一个游客会话只能换出一个新会话")
	}
}

func TestReplaceSessionWaitsForPasswordChange(t *testing.T) {
	store, pool := newStore(t)
	alice := createMember(t, store, "alice-1", "游客-1a2b")
	if err := store.Register(t.Context(), alice.ID, "alice", "h1"); err != nil {
		t.Fatal(err)
	}
	createMember(t, store, "guest", "游客-3c4d")

	// 模拟 SetPassword 的事务，先不提交。
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	if _, err := tx.Exec(t.Context(), "UPDATE members SET password_hash = 'h2' WHERE id = $1", alice.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), "DELETE FROM member_sessions WHERE member_id = $1", alice.ID); err != nil {
		t.Fatal(err)
	}

	result := make(chan error, 1)
	go func() {
		result <- store.ReplaceSession(t.Context(), tokenHash("guest"), alice.ID, tokenHash("alice-2"), "h1")
	}()
	waitForLockWait(t, pool, 1)
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}

	if err := <-result; !errors.Is(err, port.ErrAccountPasswordChanged) {
		t.Errorf("改密码提交之后，登录的错误 = %v，期望 ErrAccountPasswordChanged", err)
	}
	if got := ownerOf(t, store, "alice-2"); got != "" {
		t.Errorf("改密码之后还留下了按旧密码签发的会话，属于 %s", got)
	}
}

func TestOnlyAccountsHavePasswordOrEmail(t *testing.T) {
	store, pool := newStore(t)
	guest := createMember(t, store, "guest", "游客-1a2b")
	for _, c := range []struct{ set, constraint string }{
		{"password_hash = 'hash'", "members_password_check"},
		{"email = 'guest@example.com'", "members_email_check"},
	} {
		_, err := pool.Exec(t.Context(), "UPDATE members SET "+c.set+" WHERE id = $1", guest.ID)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23514" || pgErr.ConstraintName != c.constraint {
			t.Errorf("给没有用户名的游客设置 %s，错误 = %v，期望违反 %s", c.set, err, c.constraint)
		}
	}
}
