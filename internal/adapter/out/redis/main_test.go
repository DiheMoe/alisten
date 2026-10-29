package redis

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"testing"

	goredis "github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	tcvalkey "github.com/testcontainers/testcontainers-go/modules/valkey"
)

var (
	server    *goredis.Options
	databases atomic.Int64
)

func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

// 每个测试用一个单独的逻辑数据库，所以容器开启了足够多的数据库。
func runTests(m *testing.M) int {
	ctx := context.Background()
	container, err := tcvalkey.Run(ctx, "valkey/valkey:8-alpine", testcontainers.WithCmdArgs("--databases", "1024"))
	defer func() { _ = testcontainers.TerminateContainer(container) }()
	if err != nil {
		fmt.Fprintln(os.Stderr, "启动 Valkey 容器：", err)
		return 1
	}
	url, err := container.ConnectionString(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if server, err = goredis.ParseURL(url); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return m.Run()
}

func newClient(t *testing.T) *goredis.Client {
	t.Helper()
	opts := *server
	opts.DB = int(databases.Add(1))
	client := goredis.NewClient(&opts)
	t.Cleanup(func() { _ = client.Close() })
	if err := client.Ping(t.Context()).Err(); err != nil {
		t.Fatalf("连接第 %d 个数据库：%v", opts.DB, err)
	}
	return client
}
