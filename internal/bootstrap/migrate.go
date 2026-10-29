package bootstrap

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dihemoe/alisten/internal/adapter/out/postgres"
	"github.com/dihemoe/alisten/internal/adapter/out/storage"
)

const migrateTimeout = 5 * time.Minute

func runMigrate(base *slog.Logger) error {
	cfg, err := loadDatabaseConfig()
	if err != nil {
		return err
	}
	storageCfg, err := loadStorageConfig()
	if err != nil {
		return err
	}
	logger := base.With("component", "migrate")

	signals, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stopSignals()
	ctx, cancel := context.WithTimeout(signals, migrateTimeout)
	defer cancel()

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return fmt.Errorf("创建 PostgreSQL 连接池：%w", err)
	}
	defer pool.Close()

	err = postgres.WithMigrationLock(ctx, pool, func(ctx context.Context) error {
		if err := postgres.MigrateSchema(ctx, pool, logger); err != nil {
			return err
		}
		if err := postgres.MigrateRiver(ctx, pool, logger); err != nil {
			return err
		}
		created, err := storage.New(storageCfg).EnsureBucket(ctx)
		if err != nil {
			return err
		}
		if created {
			logger.InfoContext(ctx, "已创建对象存储的 bucket", "bucket", storageCfg.Bucket)
		}
		return nil
	})
	if err != nil {
		return err
	}
	logger.InfoContext(ctx, "迁移完成")
	return nil
}
