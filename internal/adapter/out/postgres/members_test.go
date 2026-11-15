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

func TestMemberIDIsUUIDv7(t *testing.T) {
	store, _ := newStore(t)
	m := createMember(t, store, "alice", "游客-1a2b")
	if !isUUID(m.ID) || m.ID[14] != '7' {
		t.Errorf("成员 ID %q 应是数据库生成的 UUIDv7", m.ID)
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
	if _, err := store.UpdateMember(t.Context(), m.ID, nil, ptr(""), nil); err != nil {
		t.Fatal(err)
	}
	if hash := avatar(); hash != "NULL" {
		t.Errorf("清除头像之后 avatar_hash = %q，期望 NULL", hash)
	}
}

func TestMemberEmailIsNullWhenClearedOrGuest(t *testing.T) {
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

	if _, err := store.UpdateMember(t.Context(), alice.ID, nil, nil, ptr("b@example.com")); err != nil {
		t.Fatal(err)
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
}

func ownerOf(t *testing.T, store *Store, token string) string {
	t.Helper()
	m, _, err := store.MemberByToken(t.Context(), tokenHash(token))
	if errors.Is(err, port.ErrMemberNotFound) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return m.ID
}

// 模拟 SetPassword 或另一次 ChangePassword 的事务，先不提交。事务里改密码哈希，删除 keep 以外的会话，keep 为 nil 时全部删除。
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
		_, err := store.ChangePassword(t.Context(), memberID, "h1", "h3", tokenHash("alice-1"))
		result <- err
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

// 补设用户名和密码时，用 GitHub 登录的另一台设备恰好在签发会话。补设要等那台设备的事务提交，再把它签发的会话一并删除。
func TestAddCredentialsWaitsForGitHubLogin(t *testing.T) {
	store, pool := newStore(t)
	alice := createMember(t, store, "alice", "游客-1a2b")
	if err := store.BindGitHub(t.Context(), alice.ID, 42); err != nil {
		t.Fatal(err)
	}
	createMember(t, store, "guest", "游客-3c4d")

	// 模拟 ReplaceSession 的事务，先不提交。
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	if _, err := tx.Exec(t.Context(), "SELECT 1 FROM members WHERE id = $1 FOR NO KEY UPDATE", alice.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), "DELETE FROM member_sessions WHERE token_hash = $1", tokenHash("guest")); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), "INSERT INTO member_sessions (token_hash, member_id) VALUES ($1, $2)", tokenHash("alice-2"), alice.ID); err != nil {
		t.Fatal(err)
	}

	type result struct {
		deleted [][]byte
		err     error
	}
	done := make(chan result, 1)
	go func() {
		deleted, err := store.AddCredentials(t.Context(), alice.ID, "alice", "hash-a", tokenHash("alice"))
		done <- result{deleted, err}
	}()
	waitForLockWait(t, pool, 1)
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if r := <-done; r.err != nil || !sameHashes(r.deleted, [][]byte{tokenHash("alice-2")}) {
		t.Errorf("登录提交之后，AddCredentials 返回 %x, %v，期望删除登录签发的 alice-2", r.deleted, r.err)
	}
	if ownerOf(t, store, "alice-2") != "" || ownerOf(t, store, "alice") != alice.ID {
		t.Error("补设之后应只留下发出请求的会话")
	}
}
