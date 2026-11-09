package postgres

import (
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/room"
)

func TestTicketHolder(t *testing.T) {
	store, _ := newStore(t)
	alice := createMember(t, store, "alice", "甲")
	id := createRoom(t, store, port.NewRoom{Kind: room.Ephemeral, Name: "临时"})

	if err := store.CreateTicket(t.Context(), port.NewTicket{TokenHash: tokenHash("t1"), RoomID: id, MemberID: alice.ID, SessionTokenHash: tokenHash("alice")}); err != nil {
		t.Fatal(err)
	}
	got, err := store.TicketHolder(t.Context(), tokenHash("t1"))
	if want := (port.TicketHolder{RoomID: id, Member: alice, SessionTokenHash: tokenHash("alice")}); err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("TicketHolder = %#v, %v，期望 %#v", got, err, want)
	}

	renamed, err := store.UpdateMember(t.Context(), alice.ID, ptr("新名字"), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := store.TicketHolder(t.Context(), tokenHash("t1")); got.Member != renamed {
		t.Errorf("持有者 = %#v，期望成员的当前资料 %#v", got.Member, renamed)
	}

	if _, err := store.TicketHolder(t.Context(), tokenHash("t2")); !errors.Is(err, port.ErrTicketNotFound) {
		t.Errorf("未知票据的错误 = %v，期望 ErrTicketNotFound", err)
	}
}

func TestExistingTickets(t *testing.T) {
	store, _ := newStore(t)
	alice := createMember(t, store, "alice", "甲")
	id := createRoom(t, store, port.NewRoom{Kind: room.Persistent, Name: "大厅"})
	for _, ticket := range []string{"t1", "t2"} {
		if err := store.CreateTicket(t.Context(), port.NewTicket{TokenHash: tokenHash(ticket), RoomID: id, MemberID: alice.ID, SessionTokenHash: tokenHash("alice")}); err != nil {
			t.Fatal(err)
		}
	}
	resetAPIKey(t, store, id, "key")

	got, err := store.ExistingTickets(t.Context(), [][]byte{tokenHash("t1"), tokenHash("t2"), tokenHash("key"), tokenHash("t3"), tokenHash("t1")})
	if want := [][]byte{tokenHash("t1"), tokenHash("t2"), tokenHash("key")}; err != nil || !sameHashes(got, want) {
		t.Errorf("ExistingTickets = %x, %v，期望 t1、t2 和 API Key 各一次", got, err)
	}
	if err := store.DeleteSession(t.Context(), tokenHash("alice")); err != nil {
		t.Fatal(err)
	}
	got, err = store.ExistingTickets(t.Context(), [][]byte{tokenHash("t1"), tokenHash("t2"), tokenHash("key")})
	if want := [][]byte{tokenHash("key")}; err != nil || !sameHashes(got, want) {
		t.Errorf("会话删除之后 ExistingTickets = %x, %v，期望只剩 API Key", got, err)
	}
}

func TestCreateTicketRejectsDeletedSession(t *testing.T) {
	store, _ := newStore(t)
	alice := createMember(t, store, "alice", "甲")
	id := createRoom(t, store, port.NewRoom{Kind: room.Ephemeral, Name: "临时"})
	if err := store.DeleteSession(t.Context(), tokenHash("alice")); err != nil {
		t.Fatal(err)
	}

	err := store.CreateTicket(t.Context(), port.NewTicket{TokenHash: tokenHash("t1"), RoomID: id, MemberID: alice.ID, SessionTokenHash: tokenHash("alice")})
	if !errors.Is(err, port.ErrSessionNotFound) {
		t.Errorf("会话已经删除时 CreateTicket = %v，期望 ErrSessionNotFound", err)
	}
}

func TestTicketsBelongToSessions(t *testing.T) {
	store, _ := newStore(t)
	alice := createMember(t, store, "alice-1", "甲")
	id := createRoom(t, store, port.NewRoom{Kind: room.Ephemeral, Name: "临时"})
	for _, guest := range []string{"guest-2", "guest-3", "guest-4"} {
		createMember(t, store, guest, "游客")
	}
	for _, s := range []struct{ guest, token string }{{"guest-2", "alice-2"}, {"guest-3", "alice-3"}} {
		if err := store.ReplaceSession(t.Context(), tokenHash(s.guest), alice.ID, tokenHash(s.token), ""); err != nil {
			t.Fatal(err)
		}
	}
	for _, session := range []string{"alice-1", "alice-2", "alice-3", "guest-4"} {
		memberID := alice.ID
		if session == "guest-4" {
			memberID = ownerOf(t, store, "guest-4")
		}
		if err := store.CreateTicket(t.Context(), port.NewTicket{TokenHash: tokenHash("ticket-" + session), RoomID: id, MemberID: memberID, SessionTokenHash: tokenHash(session)}); err != nil {
			t.Fatal(err)
		}
	}
	valid := func(session string) bool {
		t.Helper()
		_, err := store.TicketHolder(t.Context(), tokenHash("ticket-"+session))
		if err != nil && !errors.Is(err, port.ErrTicketNotFound) {
			t.Fatal(err)
		}
		return err == nil
	}

	if err := store.Register(t.Context(), alice.ID, "alice", "old"); err != nil {
		t.Fatal(err)
	}
	deleted, err := store.ChangePassword(t.Context(), alice.ID, "old", "new", tokenHash("alice-1"))
	if want := [][]byte{tokenHash("alice-2"), tokenHash("alice-3")}; err != nil || !sameHashes(deleted, want) {
		t.Errorf("ChangePassword 返回 %x, %v，期望被删除的 alice-2、alice-3", deleted, err)
	}
	if !valid("alice-1") || valid("alice-2") || valid("alice-3") {
		t.Error("删除会话之后，只有保留的会话的票据仍然有效")
	}
	if err := store.ReplaceSession(t.Context(), tokenHash("guest-4"), alice.ID, tokenHash("alice-4"), "new"); err != nil {
		t.Fatal(err)
	}
	if valid("guest-4") {
		t.Error("登录换掉游客的会话之后，游客的票据仍然有效")
	}
	if err := store.DeleteSession(t.Context(), tokenHash("alice-1")); err != nil {
		t.Fatal(err)
	}
	if valid("alice-1") {
		t.Error("退出登录之后，这个会话的票据仍然有效")
	}
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

func TestCreateTicketChecksPasswordHash(t *testing.T) {
	store, _ := newStore(t)
	alice := createMember(t, store, "alice", "甲")
	id := createRoom(t, store, port.NewRoom{Kind: room.Persistent, Name: "大厅", PasswordHash: "h1"})

	stale := port.NewTicket{TokenHash: tokenHash("t1"), RoomID: id, MemberID: alice.ID}
	if err := store.CreateTicket(t.Context(), stale); !errors.Is(err, port.ErrPasswordChanged) {
		t.Errorf("按没有密码签发，错误 = %v，期望 ErrPasswordChanged", err)
	}
	current := port.NewTicket{TokenHash: tokenHash("t1"), RoomID: id, MemberID: alice.ID, PasswordHash: "h1"}
	if err := store.CreateTicket(t.Context(), current); err != nil {
		t.Fatal(err)
	}

	missing := port.NewTicket{TokenHash: tokenHash("t2"), RoomID: "01a15778-1911-7a28-8b2e-e08c2a25f388", MemberID: alice.ID}
	if err := store.CreateTicket(t.Context(), missing); !errors.Is(err, port.ErrRoomNotFound) {
		t.Errorf("房间不存在时的错误 = %v，期望 ErrRoomNotFound", err)
	}
}

func TestCreateTicketWaitsForPasswordChange(t *testing.T) {
	store, pool := newStore(t)
	alice := createMember(t, store, "alice", "甲")
	id := createRoom(t, store, port.NewRoom{Kind: room.Persistent, Name: "大厅", PasswordHash: "h1"})

	// 模拟 UpdateRoomAccess 设置密码的事务，先不提交。
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	if _, err := tx.Exec(t.Context(), "UPDATE rooms SET password_hash = 'h2' WHERE id = $1", id); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), "DELETE FROM room_tickets WHERE room_id = $1", id); err != nil {
		t.Fatal(err)
	}

	result := make(chan error, 1)
	go func() {
		result <- store.CreateTicket(t.Context(), port.NewTicket{TokenHash: tokenHash("t1"), RoomID: id, MemberID: alice.ID, PasswordHash: "h1"})
	}()
	waitForLockWait(t, pool, 1)
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}

	if err := <-result; !errors.Is(err, port.ErrPasswordChanged) {
		t.Errorf("改密码提交后，签发的错误 = %v，期望 ErrPasswordChanged", err)
	}
	if n := ticketCount(t, pool, id); n != 0 {
		t.Errorf("改密码之后还剩 %d 张票据", n)
	}
}

// 等到至少 n 个查询在等锁。
func waitForLockWait(t *testing.T, pool *pgxpool.Pool, n int) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		var waiting int
		err := pool.QueryRow(t.Context(),
			"SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'",
		).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting >= n {
			return
		}
	}
	t.Fatalf("10 秒内没有 %d 个查询在等锁", n)
}
