package postgres

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

const schemaTemplate = "schema_template"

var (
	server    *pgxpool.Config
	admin     *pgxpool.Pool
	databases atomic.Int64
)

func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

func runTests(m *testing.M) int {
	ctx := context.Background()
	container, err := tcpostgres.Run(ctx, "postgres:18-alpine", tcpostgres.BasicWaitStrategies())
	defer func() { _ = testcontainers.TerminateContainer(container) }()
	if err != nil {
		fmt.Fprintln(os.Stderr, "启动 PostgreSQL 容器：", err)
		return 1
	}

	url, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if server, err = pgxpool.ParseConfig(url); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if admin, err = pgxpool.NewWithConfig(ctx, server); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer admin.Close()
	if err := createSchemaTemplate(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "准备模板数据库：", err)
		return 1
	}
	return m.Run()
}

func createSchemaTemplate(ctx context.Context) error {
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+schemaTemplate); err != nil {
		return err
	}
	cfg := server.Copy()
	cfg.ConnConfig.Database = schemaTemplate
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return err
	}
	// 作为模板的数据库在复制时不能有连接。
	defer pool.Close()
	return MigrateSchema(ctx, pool, discard)
}

func newDatabase(t *testing.T, template string) *pgxpool.Config {
	t.Helper()
	name := fmt.Sprintf("test_%d", databases.Add(1))
	if _, err := admin.Exec(t.Context(), fmt.Sprintf("CREATE DATABASE %s TEMPLATE %s", name, template)); err != nil {
		t.Fatal(err)
	}
	cfg := server.Copy()
	cfg.ConnConfig.Database = name
	return cfg
}

func connect(t *testing.T, cfg *pgxpool.Config) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func newStore(t *testing.T) (*Store, *pgxpool.Pool) {
	t.Helper()
	pool := connect(t, newDatabase(t, schemaTemplate))
	return New(pool), pool
}
