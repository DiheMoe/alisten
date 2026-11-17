package postgres

import (
	"errors"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/room"
)

func createRoom(t *testing.T, store *Store, r port.NewRoom) string {
	t.Helper()
	id, err := store.CreateRoom(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func addTicket(t *testing.T, pool *pgxpool.Pool, roomID string) {
	t.Helper()
	_, err := pool.Exec(t.Context(), `
		WITH m AS (
			INSERT INTO members (name) VALUES ('甲')
			RETURNING id
		)
		INSERT INTO room_tickets (token_hash, room_id, member_id) SELECT sha256(gen_random_uuid()::text::bytea), $1, id FROM m`,
		roomID)
	if err != nil {
		t.Fatal(err)
	}
}

func ticketCount(t *testing.T, pool *pgxpool.Pool, roomID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM room_tickets WHERE room_id = $1", roomID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func ptr(s string) *string { return &s }

func TestCreateListAndFindRooms(t *testing.T) {
	store, _ := newStore(t)
	lobby := createRoom(t, store, port.NewRoom{Kind: room.Ephemeral, Name: "临时", Description: "随便听听", Capacity: 16})
	hall := createRoom(t, store, port.NewRoom{Kind: room.Persistent, Name: "大厅", Slug: "hall", PasswordHash: "hash", Capacity: 64})

	for _, id := range []string{lobby, hall} {
		if !isUUID(id) || id[14] != '7' {
			t.Errorf("房间 ID %q 应是数据库生成的 UUIDv7", id)
		}
	}
	want := []port.RoomRecord{
		{Info: room.Info{ID: lobby, Name: "临时", Description: "随便听听", Kind: room.Ephemeral, Capacity: 16}},
		{Info: room.Info{ID: hall, Name: "大厅", Kind: room.Persistent, Capacity: 64}, Slug: "hall", PasswordHash: "hash"},
	}
	got, err := store.ListRooms(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("房间列表 = %#v，期望按创建顺序 %#v", got, want)
	}

	for ref, want := range map[string]port.RoomRecord{lobby: want[0], hall: want[1], "hall": want[1]} {
		got, err := store.FindRoom(t.Context(), ref)
		if err != nil || got != want {
			t.Errorf("FindRoom(%q) = %#v, %v，期望 %#v", ref, got, err, want)
		}
	}
	for _, ref := range []string{"lobby", "01a15778-1911-7a28-8b2e-e08c2a25f388", "not-a-uuid-but-36-characters-long!!!"} {
		if _, err := store.FindRoom(t.Context(), ref); !errors.Is(err, port.ErrRoomNotFound) {
			t.Errorf("FindRoom(%q) 的错误 = %v，期望 ErrRoomNotFound", ref, err)
		}
	}
}

func TestSlugIsUniqueAndOnlyForPersistentRooms(t *testing.T) {
	store, _ := newStore(t)
	createRoom(t, store, port.NewRoom{Kind: room.Persistent, Name: "大厅", Slug: "hall", Capacity: 64})
	other := createRoom(t, store, port.NewRoom{Kind: room.Persistent, Name: "二厅", Capacity: 64})

	if _, err := store.CreateRoom(t.Context(), port.NewRoom{Kind: room.Persistent, Name: "重名", Slug: "hall", Capacity: 64}); !errors.Is(err, port.ErrSlugTaken) {
		t.Errorf("新建房间使用已占用的别名，错误 = %v，期望 ErrSlugTaken", err)
	}
	if err := store.SetRoomSlug(t.Context(), other, "hall"); !errors.Is(err, port.ErrSlugTaken) {
		t.Errorf("改用已占用的别名，错误 = %v，期望 ErrSlugTaken", err)
	}
	_, err := store.CreateRoom(t.Context(), port.NewRoom{Kind: room.Ephemeral, Name: "临时", Slug: "temp", Capacity: 16})
	var pgErr *pgconn.PgError
	// 这条约束同时引用 slug 和 kind，PostgreSQL 给它起的名字是 rooms_check。
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" || pgErr.ConstraintName != "rooms_check" {
		t.Errorf("临时房间设别名的错误 = %v，期望违反只许常驻房间有别名的约束", err)
	}
}

func TestSetRoomSlug(t *testing.T) {
	store, pool := newStore(t)
	id := createRoom(t, store, port.NewRoom{Kind: room.Persistent, Name: "大厅", Slug: "hall", Capacity: 64})
	addTicket(t, pool, id)

	if err := store.SetRoomSlug(t.Context(), id, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.FindRoom(t.Context(), "hall"); !errors.Is(err, port.ErrRoomNotFound) {
		t.Errorf("取消别名后仍能按别名找到房间：%v", err)
	}
	if err := store.SetRoomSlug(t.Context(), id, "main"); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.FindRoom(t.Context(), "main"); got.Info.ID != id {
		t.Errorf("设置别名后按别名找到的房间 = %#v", got)
	}
	if ticketCount(t, pool, id) != 1 {
		t.Error("改别名不影响票据")
	}

	missing := "01a15778-1911-7a28-8b2e-e08c2a25f388"
	if err := store.SetRoomSlug(t.Context(), missing, "x"); !errors.Is(err, port.ErrRoomNotFound) {
		t.Errorf("SetRoomSlug(%q) 的错误 = %v，期望 ErrRoomNotFound", missing, err)
	}
}

func TestSetRoomPassword(t *testing.T) {
	store, pool := newStore(t)
	id := createRoom(t, store, port.NewRoom{Kind: room.Persistent, Name: "大厅", Slug: "hall", Capacity: 64})
	addTicket(t, pool, id)

	if err := store.SetRoomPassword(t.Context(), id, "hash"); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.FindRoom(t.Context(), id); got.PasswordHash != "hash" || got.Slug != "hall" {
		t.Errorf("设置密码后 = %#v", got)
	}
	if ticketCount(t, pool, id) != 0 {
		t.Error("设置密码时应删除全部票据")
	}

	addTicket(t, pool, id)
	if err := store.SetRoomPassword(t.Context(), id, ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.FindRoom(t.Context(), id); got.PasswordHash != "" || got.Slug != "hall" {
		t.Errorf("取消密码后 = %#v", got)
	}
	if ticketCount(t, pool, id) != 1 {
		t.Error("取消密码时票据保持有效")
	}

	missing := "01a15778-1911-7a28-8b2e-e08c2a25f388"
	if err := store.SetRoomPassword(t.Context(), missing, "x"); !errors.Is(err, port.ErrRoomNotFound) {
		t.Errorf("SetRoomPassword(%q) 的错误 = %v，期望 ErrRoomNotFound", missing, err)
	}
}
