package postgres

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"

	"github.com/dihemoe/alisten/db/migrations"
)

const migrationLock int64 = 0x616c697374656e

// 锁所在的事务一直占用一个连接，fn 中的迁移要使用连接池中的其他连接。
func WithMigrationLock(ctx context.Context, pool *pgxpool.Pool, fn func(context.Context) error) error {
	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		// 这个事务在整个迁移期间都处于空闲状态，数据库设置的空闲事务超时或事务超时会断开它，锁随之失效。
		// 后到的迁移要一直等待锁，而数据库设置的 lock_timeout、statement_timeout 和 transaction_timeout 会让它报错而不是等待。
		if _, err := tx.Exec(ctx, `SELECT set_config('idle_in_transaction_session_timeout', '0', true),
			set_config('lock_timeout', '0', true), set_config('statement_timeout', '0', true),
			set_config('transaction_timeout', '0', true)`); err != nil {
			return fmt.Errorf("关闭迁移锁所在事务的超时：%w", err)
		}
		if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", migrationLock); err != nil {
			return fmt.Errorf("获取迁移锁：%w", err)
		}
		return fn(ctx)
	})
}

func MigrateSchema(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) error {
	db := stdlib.OpenDBFromPool(pool)
	defer func() { _ = db.Close() }()
	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS)
	if err != nil {
		return fmt.Errorf("加载数据库迁移：%w", err)
	}
	results, err := provider.Up(ctx)
	if err != nil {
		return fmt.Errorf("执行数据库迁移：%w", err)
	}
	for _, r := range results {
		logger.InfoContext(ctx, "已执行数据库迁移", "migration", r.Source.Version, "file", r.Source.Path, "duration", r.Duration)
	}
	return nil
}

func MigrateRiver(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) error {
	// rivermigrate 的日志带有 version 字段，而 slog 会把重名的字段写两次：放进 river 分组，它才不会与日志的固定字段 version 重名。
	migrator, err := rivermigrate.New(riverpgxv5.New(pool), &rivermigrate.Config{Logger: logger.WithGroup("river")})
	if err != nil {
		return fmt.Errorf("创建 River 迁移器：%w", err)
	}
	if _, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		return fmt.Errorf("执行 River 迁移：%w", err)
	}
	return nil
}
