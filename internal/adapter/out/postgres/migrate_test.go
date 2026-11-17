package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/dihemoe/alisten/db/migrations"
	"github.com/dihemoe/alisten/internal/app/port"
)

func migrate(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) error {
	return WithMigrationLock(ctx, pool, func(ctx context.Context) error {
		if err := MigrateSchema(ctx, pool, logger); err != nil {
			return err
		}
		return MigrateRiver(ctx, pool, logger)
	})
}

var discard = slog.New(slog.DiscardHandler)

func TestMigrationLockOutlivesSessionTimeouts(t *testing.T) {
	for _, setting := range []string{"idle_in_transaction_session_timeout = '100ms'", "transaction_timeout = '100ms'"} {
		t.Run(setting, func(t *testing.T) {
			cfg := newDatabase(t, "template1")
			if _, err := admin.Exec(t.Context(), fmt.Sprintf("ALTER DATABASE %s SET %s", cfg.ConnConfig.Database, setting)); err != nil {
				t.Fatal(err)
			}
			holder, observer := connect(t, cfg), connect(t, cfg)

			err := WithMigrationLock(t.Context(), holder, func(ctx context.Context) error {
				time.Sleep(500 * time.Millisecond)
				var held bool
				if err := observer.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_locks
					WHERE locktype = 'advisory' AND granted
					AND database = (SELECT oid FROM pg_database WHERE datname = current_database()))`).Scan(&held); err != nil {
					return err
				}
				if !held {
					return errors.New("超过数据库设置的超时之后，迁移锁失效了")
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMigrateWaitsForMigrationLock(t *testing.T) {
	for _, tc := range []struct{ name, setting string }{
		{name: "lock_timeout", setting: "lock_timeout = '200ms'"},
		{name: "statement_timeout", setting: "statement_timeout = '200ms'"},
		{name: "transaction_timeout", setting: "transaction_timeout = '200ms'"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := newDatabase(t, "template1")
			// 先完成迁移，这样等到锁之后的迁移只剩检查，不会被 statement_timeout 打断。
			if err := migrate(t.Context(), connect(t, cfg), discard); err != nil {
				t.Fatal(err)
			}
			if _, err := admin.Exec(t.Context(), fmt.Sprintf("ALTER DATABASE %s SET %s", cfg.ConnConfig.Database, tc.setting)); err != nil {
				t.Fatal(err)
			}
			holder, other := connect(t, cfg), connect(t, cfg)

			done := make(chan error, 1)
			err := WithMigrationLock(t.Context(), holder, func(ctx context.Context) error {
				go func() { done <- migrate(t.Context(), other, discard) }()
				for {
					var waiting int
					if err := holder.QueryRow(ctx, `SELECT count(*) FROM pg_locks
						WHERE locktype = 'advisory' AND NOT granted
						AND database = (SELECT oid FROM pg_database WHERE datname = current_database())`).Scan(&waiting); err != nil {
						return err
					}
					if waiting > 0 {
						break
					}
					select {
					case err := <-done:
						return fmt.Errorf("迁移没有等待锁就结束了：%v", err)
					case <-time.After(20 * time.Millisecond):
					}
				}
				// 持锁的时间超过设置的超时之后，等待中的迁移仍在等待。
				select {
				case err := <-done:
					return fmt.Errorf("迁移在锁释放之前就结束了：%v", err)
				case <-time.After(500 * time.Millisecond):
					return nil
				}
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMigrationLogsKeepFixedFields(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil)).With("version", "test")
	if err := migrate(t.Context(), connect(t, newDatabase(t, "template1")), logger); err != nil {
		t.Fatal(err)
	}

	// River 的日志自带 version，放在 river 分组里才不会盖掉固定字段，所以样本里一定要有 River 的日志。
	var goose, river int
	for line := range bytes.Lines(bytes.TrimSpace(buf.Bytes())) {
		var entry map[string]any
		if err := json.Unmarshal(line, &entry); err != nil {
			t.Fatalf("日志不是 JSON：%v\n%s", err, line)
		}
		if entry["version"] != "test" {
			t.Errorf("日志覆盖了固定字段 version：%s", line)
		}
		if _, ok := entry["river"]; ok {
			river++
		} else {
			goose++
		}
	}
	if goose == 0 || river == 0 {
		t.Errorf("goose 记了 %d 条、River 记了 %d 条，两边都应记录执行的迁移：\n%s", goose, river, buf.String())
	}
}

func TestMemberSessionsMigrationKeepsMemberTokens(t *testing.T) {
	ctx := t.Context()
	name := fmt.Sprintf("test_%d", databases.Add(1))
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	cfg := server.Copy()
	cfg.ConnConfig.Database = name
	pool := connect(t, cfg)
	db := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() { _ = db.Close() })
	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(ctx, 2); err != nil {
		t.Fatal(err)
	}
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	var guest, bot string
	if err := pool.QueryRow(ctx, "INSERT INTO members (token_hash, name, created_at) VALUES ($1, '游客-1a2b', $2) RETURNING id", tokenHash("guest"), created).Scan(&guest); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "INSERT INTO members (token_hash, name) VALUES ($1, 'bot') RETURNING id", tokenHash("bot")).Scan(&bot); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "INSERT INTO rooms (kind, name, description, bot_member_id) VALUES ('persistent', '大厅', '', $1)", bot); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatal(err)
	}

	store := New(pool)
	if m, _, err := store.MemberByToken(ctx, tokenHash("guest")); err != nil || m.ID != guest {
		t.Errorf("迁移之后按原来的 token 找到 %#v, %v，期望成员 %s", m, err, guest)
	}
	var sessionCreated time.Time
	if err := pool.QueryRow(ctx, "SELECT created_at FROM member_sessions WHERE member_id = $1", guest).Scan(&sessionCreated); err != nil || !sessionCreated.Equal(created) {
		t.Errorf("会话的创建时间 = %v, %v，期望沿用成员的创建时间 %v", sessionCreated, err, created)
	}
	if _, _, err := store.MemberByToken(ctx, tokenHash("bot")); !errors.Is(err, port.ErrMemberNotFound) {
		t.Errorf("bot 成员原来的 token 在迁移之后 = %v，期望不再有效", err)
	}
}

func TestTicketSessionsMigrationBindsTicketsToSessions(t *testing.T) {
	ctx := t.Context()
	name := fmt.Sprintf("test_%d", databases.Add(1))
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	cfg := server.Copy()
	cfg.ConnConfig.Database = name
	pool := connect(t, cfg)
	db := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() { _ = db.Close() })
	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(ctx, 2); err != nil {
		t.Fatal(err)
	}
	var guest, bot, roomID string
	if err := pool.QueryRow(ctx, "INSERT INTO members (token_hash, name) VALUES ($1, '游客-1a2b') RETURNING id", tokenHash("guest")).Scan(&guest); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "INSERT INTO members (token_hash, name) VALUES ($1, 'bot') RETURNING id", tokenHash("bot")).Scan(&bot); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "INSERT INTO rooms (kind, name, description, bot_member_id) VALUES ('persistent', '大厅', '', $1) RETURNING id", bot).Scan(&roomID); err != nil {
		t.Fatal(err)
	}
	for ticket, memberID := range map[string]string{"guest-ticket": guest, "api-key": bot} {
		if _, err := pool.Exec(ctx, "INSERT INTO room_tickets (token_hash, room_id, member_id) VALUES ($1, $2, $3)", tokenHash(ticket), roomID, memberID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatal(err)
	}

	store := New(pool)
	if holder, err := store.TicketHolder(ctx, tokenHash("guest-ticket")); err != nil || !bytes.Equal(holder.SessionTokenHash, tokenHash("guest")) {
		t.Errorf("游客的票据 = %#v, %v，期望归到游客的会话", holder, err)
	}
	if holder, err := store.TicketHolder(ctx, tokenHash("api-key")); err != nil || !holder.Bot || holder.SessionTokenHash != nil {
		t.Errorf("API Key 的票据 = %#v, %v，期望保留，没有会话", holder, err)
	}
}

func TestRoomCapacityMigrationSetsCapacityByKind(t *testing.T) {
	ctx := t.Context()
	name := fmt.Sprintf("test_%d", databases.Add(1))
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	cfg := server.Copy()
	cfg.ConnConfig.Database = name
	pool := connect(t, cfg)
	db := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() { _ = db.Close() })
	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(ctx, 8); err != nil {
		t.Fatal(err)
	}
	var hall, lobby string
	if err := pool.QueryRow(ctx, "INSERT INTO rooms (kind, name, description) VALUES ('persistent', '大厅', '') RETURNING id").Scan(&hall); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "INSERT INTO rooms (kind, name, description) VALUES ('ephemeral', '临时', '') RETURNING id").Scan(&lobby); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "INSERT INTO site (brand_name) VALUES ('周末听歌会')"); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatal(err)
	}

	store := New(pool)
	for id, want := range map[string]int{hall: 64, lobby: 16} {
		if r, err := store.FindRoom(ctx, id); err != nil || r.Info.Capacity != want {
			t.Errorf("迁移之后房间 %s = %#v, %v，期望人数上限 %d", id, r.Info, err, want)
		}
	}
	if settings, err := store.Settings(ctx); err != nil || settings != (port.SiteSettings{BrandName: "周末听歌会", RegistrationOpen: true}) {
		t.Errorf("迁移之后的站点设置 = %#v, %v，期望临时房间最多人数没有设置过", settings, err)
	}
	// 回滚到旧版本之后，旧代码新建房间时不写 capacity。
	var capacity int
	if err := pool.QueryRow(ctx, "INSERT INTO rooms (kind, name, description) VALUES ('ephemeral', '旧版本建的', '') RETURNING capacity").Scan(&capacity); err != nil || capacity != 64 {
		t.Errorf("不写 capacity 新建的房间 = %d, %v，期望取默认值 64", capacity, err)
	}
}

func TestSessionLastUsedMigrationStartsFromMigration(t *testing.T) {
	ctx := t.Context()
	name := fmt.Sprintf("test_%d", databases.Add(1))
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	cfg := server.Copy()
	cfg.ConnConfig.Database = name
	pool := connect(t, cfg)
	db := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() { _ = db.Close() })
	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(ctx, 9); err != nil {
		t.Fatal(err)
	}
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	var guest string
	if err := pool.QueryRow(ctx, "INSERT INTO members (name, created_at) VALUES ('游客-1a2b', $1) RETURNING id", created).Scan(&guest); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "INSERT INTO member_sessions (token_hash, member_id, created_at) VALUES ($1, $2, $3)", tokenHash("guest"), guest, created); err != nil {
		t.Fatal(err)
	}
	var before, after, used time.Time
	if err := pool.QueryRow(ctx, "SELECT now()").Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT now()").Scan(&after); err != nil {
		t.Fatal(err)
	}

	if err := pool.QueryRow(ctx, "SELECT last_used_at FROM member_sessions WHERE token_hash = $1", tokenHash("guest")).Scan(&used); err != nil {
		t.Fatal(err)
	}
	if used.Before(before) || used.After(after) {
		t.Errorf("迁移之后已有会话的 last_used_at = %v，期望是迁移时刻，在 %v 和 %v 之间", used, before, after)
	}
}
