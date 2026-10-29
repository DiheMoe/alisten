package postgres

import (
	"errors"
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

	if err := store.CreateTicket(t.Context(), port.NewTicket{TokenHash: tokenHash("t1"), RoomID: id, MemberID: alice.ID}); err != nil {
		t.Fatal(err)
	}
	got, err := store.TicketHolder(t.Context(), tokenHash("t1"))
	if want := (port.TicketHolder{RoomID: id, Member: alice}); err != nil || got != want {
		t.Errorf("TicketHolder = %#v, %v，期望 %#v", got, err, want)
	}

	renamed, err := store.UpdateMember(t.Context(), alice.ID, ptr("新名字"), nil)
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
