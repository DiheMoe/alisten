package postgres

import (
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/room"
)

func newAPIKey(key string) port.NewAPIKey {
	return port.NewAPIKey{TokenHash: tokenHash(key), BotTokenHash: tokenHash("bot " + key)}
}

func resetAPIKey(t *testing.T, store *Store, roomID, key string) {
	t.Helper()
	if err := store.ResetRoomAPIKey(t.Context(), roomID, newAPIKey(key)); err != nil {
		t.Fatal(err)
	}
}

func memberTickets(t *testing.T, pool *pgxpool.Pool, memberID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM room_tickets WHERE member_id = $1", memberID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestResetRoomAPIKey(t *testing.T) {
	store, pool := newStore(t)
	id := createRoom(t, store, port.NewRoom{Kind: room.Persistent, Name: "大厅"})
	if status, err := store.RoomAPIKeyStatus(t.Context(), id); err != nil || status.Set || !status.CreatedAt.IsZero() {
		t.Fatalf("还没有 Key 时的状态 = %+v, %v，期望未生成", status, err)
	}

	resetAPIKey(t, store, id, "k1")
	bot, err := store.TicketHolder(t.Context(), tokenHash("k1"))
	if err != nil || bot.RoomID != id || !bot.Bot || bot.Member.Name != "bot" || bot.Member.AvatarHash != "" {
		t.Fatalf("Key 的持有者 = %#v, %v，期望这个房间的 bot 成员，昵称 bot，没有头像", bot, err)
	}
	if _, err := store.MemberByToken(t.Context(), tokenHash("k1")); !errors.Is(err, port.ErrMemberNotFound) {
		t.Errorf("用 Key 查成员的错误 = %v，期望 ErrMemberNotFound：bot 的成员 token 不能是 Key", err)
	}
	var createdAt time.Time
	if err := pool.QueryRow(t.Context(), "SELECT created_at FROM room_tickets WHERE token_hash = $1", tokenHash("k1")).Scan(&createdAt); err != nil {
		t.Fatal(err)
	}
	if status, err := store.RoomAPIKeyStatus(t.Context(), id); err != nil || !status.Set || !status.CreatedAt.Equal(createdAt) {
		t.Errorf("生成之后的状态 = %+v, %v，期望已生成，时间是 bot 票据的创建时间 %v", status, err, createdAt)
	}

	resetAPIKey(t, store, id, "k2")
	if _, err := store.TicketHolder(t.Context(), tokenHash("k1")); !errors.Is(err, port.ErrTicketNotFound) {
		t.Errorf("重置之后旧 Key 的错误 = %v，期望 ErrTicketNotFound", err)
	}
	if holder, err := store.TicketHolder(t.Context(), tokenHash("k2")); err != nil || holder != bot {
		t.Errorf("重置之后新 Key 的持有者 = %#v, %v，期望 bot 成员不变", holder, err)
	}
	if n := memberTickets(t, pool, bot.Member.ID); n != 1 {
		t.Errorf("重置之后 bot 有 %d 张票据，期望只有新 Key 一张", n)
	}

	missing := "01a15778-1911-7a28-8b2e-e08c2a25f388"
	if err := store.ResetRoomAPIKey(t.Context(), missing, newAPIKey("k3")); !errors.Is(err, port.ErrRoomNotFound) {
		t.Errorf("重置不存在的房间的错误 = %v，期望 ErrRoomNotFound", err)
	}
}

func TestSettingPasswordKeepsAPIKey(t *testing.T) {
	store, pool := newStore(t)
	id := createRoom(t, store, port.NewRoom{Kind: room.Persistent, Name: "大厅"})
	resetAPIKey(t, store, id, "k1")
	bot, err := store.TicketHolder(t.Context(), tokenHash("k1"))
	if err != nil {
		t.Fatal(err)
	}
	addTicket(t, pool, id)

	if err := store.UpdateRoomAccess(t.Context(), id, port.AccessChange{PasswordHash: ptr("h1")}); err != nil {
		t.Fatal(err)
	}
	if holder, err := store.TicketHolder(t.Context(), tokenHash("k1")); err != nil || holder != bot {
		t.Errorf("设置密码之后 Key 的持有者 = %#v, %v，期望 Key 仍然有效", holder, err)
	}
	if n := ticketCount(t, pool, id); n != 1 {
		t.Errorf("设置密码之后房间有 %d 张票据，期望成员的票据删除，只剩 Key", n)
	}
}

func TestDeletingRoomDeletesAPIKey(t *testing.T) {
	store, pool := newStore(t)
	id := createRoom(t, store, port.NewRoom{Kind: room.Persistent, Name: "大厅"})
	lounge := createRoom(t, store, port.NewRoom{Kind: room.Persistent, Name: "二厅"})
	resetAPIKey(t, store, id, "k1")
	resetAPIKey(t, store, lounge, "k2")
	bot, err := store.TicketHolder(t.Context(), tokenHash("k1"))
	if err != nil {
		t.Fatal(err)
	}

	term, _ := takeRoom(t, store, id)
	if err := store.DeleteRoom(t.Context(), id, term); err != nil {
		t.Fatal(err)
	}
	if n := memberTickets(t, pool, bot.Member.ID); n != 0 {
		t.Errorf("删除房间之后 bot 还有 %d 张票据", n)
	}
	if status, err := store.RoomAPIKeyStatus(t.Context(), lounge); err != nil || !status.Set {
		t.Errorf("删除大厅之后二厅的状态 = %+v, %v，期望不受影响", status, err)
	}
}

func TestConcurrentFirstResetsCreateOneBot(t *testing.T) {
	store, pool := newStore(t)
	id := createRoom(t, store, port.NewRoom{Kind: room.Persistent, Name: "大厅"})

	// 先锁住房间行，两个首次生成都在等它，放开之后同时进行。
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	if _, err := tx.Exec(t.Context(), "SELECT 1 FROM rooms WHERE id = $1 FOR NO KEY UPDATE", id); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 2)
	for _, key := range []string{"k1", "k2"} {
		go func() { done <- store.ResetRoomAPIKey(t.Context(), id, newAPIKey(key)) }()
	}
	waitForLockWait(t, pool, 2)
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}

	var bots int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM members WHERE name = 'bot'").Scan(&bots); err != nil {
		t.Fatal(err)
	}
	if n := ticketCount(t, pool, id); n != 1 || bots != 1 {
		t.Errorf("有 %d 把 Key、%d 个 bot 成员，期望各一个", n, bots)
	}
}
