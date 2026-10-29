package bootstrap

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/dihemoe/alisten/internal/adapter/out/storage"
)

const (
	rustfsImage     = "rustfs/rustfs:1.0.3"
	rustfsAccessKey = "alisten-test"
	rustfsSecretKey = "alisten-test-secret"
)

var rustfs struct {
	once      sync.Once
	container *testcontainers.DockerContainer
	endpoint  string
	err       error
	buckets   int
}

func TestMain(m *testing.M) {
	code := m.Run()
	_ = testcontainers.TerminateContainer(rustfs.container)
	_ = testcontainers.TerminateContainer(shared.postgres)
	_ = testcontainers.TerminateContainer(shared.valkey)
	os.Exit(code)
}

// 每次都是一个还不存在的 bucket，由迁移创建。
func storageEnv(t *testing.T) storage.Config {
	t.Helper()
	rustfs.once.Do(func() {
		ctx := context.Background()
		rustfs.container, rustfs.err = testcontainers.Run(ctx, rustfsImage,
			testcontainers.WithExposedPorts("9000/tcp"),
			testcontainers.WithEnv(map[string]string{"RUSTFS_ACCESS_KEY": rustfsAccessKey, "RUSTFS_SECRET_KEY": rustfsSecretKey}),
			testcontainers.WithWaitStrategy(wait.ForHTTP("/health/ready").WithPort("9000/tcp")),
		)
		if rustfs.err == nil {
			rustfs.endpoint, rustfs.err = rustfs.container.PortEndpoint(ctx, "9000/tcp", "http")
		}
	})
	if rustfs.err != nil {
		t.Fatalf("启动 RustFS 容器：%v", rustfs.err)
	}
	rustfs.buckets++
	cfg := storage.Config{
		Endpoint:        rustfs.endpoint,
		Region:          "us-east-1",
		Bucket:          fmt.Sprintf("test-%d", rustfs.buckets),
		AccessKeyID:     rustfsAccessKey,
		SecretAccessKey: rustfsSecretKey,
	}
	t.Setenv("STORAGE_ENDPOINT_URL", cfg.Endpoint)
	t.Setenv("STORAGE_REGION", cfg.Region)
	t.Setenv("STORAGE_BUCKET_NAME", cfg.Bucket)
	t.Setenv("STORAGE_ACCESS_KEY_ID", cfg.AccessKeyID)
	t.Setenv("STORAGE_SECRET_ACCESS_KEY", cfg.SecretAccessKey)
	return cfg
}

func TestLoadStorageConfig(t *testing.T) {
	t.Setenv("STORAGE_ENDPOINT_URL", "http://rustfs:9000")
	t.Setenv("STORAGE_ACCESS_KEY_ID", "alisten")
	t.Setenv("STORAGE_SECRET_ACCESS_KEY", "secret")
	unsetenv(t, "STORAGE_REGION")
	unsetenv(t, "STORAGE_BUCKET_NAME")
	cfg, err := loadStorageConfig()
	want := storage.Config{Endpoint: "http://rustfs:9000", Region: "us-east-1", Bucket: "alisten", AccessKeyID: "alisten", SecretAccessKey: "secret"}
	if err != nil || cfg != want {
		t.Fatalf("loadStorageConfig() = %+v, %v，期望 %+v", cfg, err, want)
	}

	invalid := map[string]string{
		"STORAGE_ENDPOINT_URL":      "https://key:secret@s3.example",
		"STORAGE_REGION":            "",
		"STORAGE_BUCKET_NAME":       `a"b`,
		"STORAGE_SECRET_ACCESS_KEY": "",
	}
	for name, value := range invalid {
		t.Run(name, func(t *testing.T) {
			t.Setenv(name, value)
			_, err := loadStorageConfig()
			if err == nil || !strings.Contains(err.Error(), name) {
				t.Fatalf("%s=%q 时应报错并写明变量名，实际为 %v", name, value, err)
			}
			if strings.Contains(err.Error(), "secret") {
				t.Errorf("错误 %q 中不应出现凭证", err)
			}
		})
	}
	for _, bucket := range []string{"ab", "Alisten", "-alisten", "alisten-", "a/b", strings.Repeat("a", 64)} {
		t.Setenv("STORAGE_BUCKET_NAME", bucket)
		if _, err := loadStorageConfig(); err == nil {
			t.Errorf("STORAGE_BUCKET_NAME=%q 时应报错", bucket)
		}
	}
}
