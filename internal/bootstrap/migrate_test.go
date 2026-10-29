package bootstrap

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dihemoe/alisten/internal/adapter/out/postgres"
	"github.com/dihemoe/alisten/internal/adapter/out/storage"
)

func TestMigrateIsRepeatable(t *testing.T) {
	url := startPostgres(t)(t)
	unsetenv(t, "ALISTEN_LOG_LEVEL")
	connectionEnv(t, url)
	bucket := storageEnv(t)
	pool, err := pgxpool.New(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	done := make(chan int, 1)
	err = postgres.WithMigrationLock(t.Context(), pool, func(ctx context.Context) error {
		go func() { done <- Main([]string{"migrate"}) }()
		for {
			var waiting int
			if err := pool.QueryRow(ctx, "SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND NOT granted").Scan(&waiting); err != nil {
				return err
			}
			if waiting > 0 {
				return nil
			}
			select {
			case code := <-done:
				return fmt.Errorf("迁移没有等待锁就结束了：%d", code)
			case <-time.After(20 * time.Millisecond):
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := <-done; got != 0 {
		t.Fatalf("Main(migrate) = %d，期望 0", got)
	}

	if got := Main([]string{"migrate"}); got != 0 {
		t.Fatalf("第二次 Main(migrate) = %d，期望 0", got)
	}
	for _, table := range []string{"rooms", "river_job"} {
		var exists bool
		if err := pool.QueryRow(t.Context(), "SELECT to_regclass($1) IS NOT NULL", table).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Errorf("迁移后缺少表 %s", table)
		}
	}
	if created, err := storage.New(bucket).EnsureBucket(t.Context()); err != nil || created {
		t.Errorf("迁移之后检查 bucket = %v, %v，期望迁移已经创建了它", created, err)
	}
}
