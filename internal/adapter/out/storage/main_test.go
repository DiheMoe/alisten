package storage

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	rustfsImage = "rustfs/rustfs:1.0.3"
	accessKey   = "alisten-test"
	secretKey   = "alisten-test-secret"
)

var (
	endpoint string
	buckets  atomic.Int64
)

func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

func runTests(m *testing.M) int {
	ctx := context.Background()
	container, err := testcontainers.Run(ctx, rustfsImage,
		testcontainers.WithExposedPorts("9000/tcp"),
		testcontainers.WithEnv(map[string]string{"RUSTFS_ACCESS_KEY": accessKey, "RUSTFS_SECRET_KEY": secretKey}),
		testcontainers.WithWaitStrategy(wait.ForHTTP("/health/ready").WithPort("9000/tcp")),
	)
	defer func() { _ = testcontainers.TerminateContainer(container) }()
	if err != nil {
		fmt.Fprintln(os.Stderr, "启动 RustFS 容器：", err)
		return 1
	}
	if endpoint, err = container.PortEndpoint(ctx, "9000/tcp", "http"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return m.Run()
}

func newConfig() Config {
	return Config{
		Endpoint:        endpoint,
		Region:          "us-east-1",
		Bucket:          fmt.Sprintf("test-%d", buckets.Add(1)),
		AccessKeyID:     accessKey,
		SecretAccessKey: secretKey,
	}
}
