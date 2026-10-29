package postgres

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/database"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"

	"github.com/dihemoe/alisten/db/migrations"
)

// 只读取版本表：goose 的 HasPending 在缺少版本表时会建表，就绪检查不能修改数据库。
func CheckMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	db := stdlib.OpenDBFromPool(pool)
	defer func() { _ = db.Close() }()
	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS)
	if err != nil {
		return fmt.Errorf("加载数据库迁移：%w", err)
	}
	sources := provider.ListSources()
	required := sources[len(sources)-1].Version
	store, err := database.NewStore(database.DialectPostgres, goose.DefaultTablename)
	if err != nil {
		return fmt.Errorf("创建 goose 版本表的访问：%w", err)
	}
	current, err := store.GetLatestVersion(ctx, db)
	if err != nil {
		return fmt.Errorf("读取数据库迁移的版本：%w", err)
	}
	if current < required {
		return fmt.Errorf("数据库迁移的版本为 %d，低于要求的 %d", current, required)
	}

	// rivermigrate 每次校验不通过都会记一条 info 日志，而就绪检查每 5 秒执行一次。
	migrator, err := rivermigrate.New(riverpgxv5.New(pool), &rivermigrate.Config{Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		return fmt.Errorf("创建 River 迁移器：%w", err)
	}
	result, err := migrator.Validate(ctx, nil)
	if err != nil {
		return fmt.Errorf("检查 River 迁移：%w", err)
	}
	if !result.OK {
		return fmt.Errorf("尚未完成 River 迁移：%s", strings.Join(result.Messages, "；"))
	}
	return nil
}
