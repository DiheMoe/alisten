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
			// 先完成迁移：等到锁之后的迁移只剩检查，不会被 statement_timeout 打断。
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

	lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))
	if len(lines) < 2 {
		t.Fatalf("goose 和 River 都应记录执行的迁移：\n%s", buf.String())
	}
	for _, line := range lines {
		var entry map[string]any
		if err := json.Unmarshal(line, &entry); err != nil {
			t.Fatalf("日志不是 JSON：%v\n%s", err, line)
		}
		if entry["version"] != "test" {
			t.Errorf("日志覆盖了固定字段 version：%s", line)
		}
	}
}
