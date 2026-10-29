package bootstrap

import (
	"slices"
	"testing"
)

func TestLoadServeConfig(t *testing.T) {
	unsetenv(t, "ALISTEN_LISTEN")
	unsetenv(t, "ALISTEN_OPS_LISTEN")
	unsetenv(t, "ALISTEN_ADMIN_TOKEN")
	unsetenv(t, "ALISTEN_ALLOWED_ORIGINS")
	t.Setenv("ALISTEN_PUBLIC_URL", "https://alisten.example")
	t.Setenv("DATABASE_HOST", "db.internal")
	t.Setenv("DATABASE_USERNAME", "alisten")
	t.Setenv("DATABASE_NAME", "alisten")
	t.Setenv("REDIS_HOST", "cache.internal")
	t.Setenv("STORAGE_PUBLIC_URL", "https://alisten.example/storage/alisten/")
	t.Setenv("STORAGE_ENDPOINT_URL", "http://rustfs:9000")
	t.Setenv("STORAGE_ACCESS_KEY_ID", "alisten")
	t.Setenv("STORAGE_SECRET_ACCESS_KEY", "secret")
	unsetenv(t, "STORAGE_REGION")
	unsetenv(t, "STORAGE_BUCKET_NAME")

	cfg, err := loadServeConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.listen != ":8080" || cfg.opsListen != ":9090" || cfg.publicOrigin != "https://alisten.example" || cfg.database.ConnConfig.Host != "db.internal" || cfg.redis.Addr != "cache.internal:6379" {
		t.Errorf("配置 = %s、%s、%s、%s、%s", cfg.listen, cfg.opsListen, cfg.publicOrigin, cfg.database.ConnConfig.Host, cfg.redis.Addr)
	}
	if cfg.adminToken != "" {
		t.Error("未设置 ALISTEN_ADMIN_TOKEN 时管理接口不可用")
	}
	if cfg.storageURL != "https://alisten.example/storage/alisten" {
		t.Errorf("对象存储的地址前缀 = %q，期望去掉结尾的 /", cfg.storageURL)
	}
	if cfg.storage.Endpoint != "http://rustfs:9000" || cfg.storage.Bucket != "alisten" {
		t.Errorf("对象存储 = %+v", cfg.storage)
	}
	if !slices.Equal(cfg.allowedOrigins, []string{"https://alisten.example"}) {
		t.Errorf("默认允许来源 = %v，期望只有公开地址", cfg.allowedOrigins)
	}
	t.Setenv("ALISTEN_ALLOWED_ORIGINS", "https://a.example, http://localhost:9876,https://a.example")
	cfg, err = loadServeConfig()
	if err != nil || !slices.Equal(cfg.allowedOrigins, []string{"https://a.example", "http://localhost:9876"}) {
		t.Errorf("显式允许来源 = %v, %v，期望两个去重后的地址", cfg.allowedOrigins, err)
	}
	for _, value := range []string{"https://a.example,,https://b.example", "ftp://a.example", "not-a-url"} {
		t.Setenv("ALISTEN_ALLOWED_ORIGINS", value)
		if _, err := loadServeConfig(); err == nil {
			t.Errorf("ALISTEN_ALLOWED_ORIGINS=%q 时应报错", value)
		}
	}
	t.Setenv("ALISTEN_ALLOWED_ORIGINS", "")

	t.Setenv("ALISTEN_ADMIN_TOKEN", "secret")
	if cfg, err := loadServeConfig(); err != nil || cfg.adminToken != "secret" {
		t.Errorf("管理 token = %q, %v，期望来自 ALISTEN_ADMIN_TOKEN", cfg.adminToken, err)
	}
	for _, name := range []string{"DATABASE_HOST", "REDIS_HOST", "STORAGE_PUBLIC_URL", "STORAGE_ENDPOINT_URL"} {
		t.Run(name, func(t *testing.T) {
			unsetenv(t, name)
			if _, err := loadServeConfig(); err == nil {
				t.Errorf("缺少 %s 时应报错", name)
			}
		})
	}
}

func TestLoadServeConfigPublicURLDefaults(t *testing.T) {
	t.Setenv("ALISTEN_ALLOWED_ORIGINS", "")
	t.Setenv("DATABASE_HOST", "db.internal")
	t.Setenv("DATABASE_USERNAME", "alisten")
	t.Setenv("DATABASE_NAME", "alisten")
	t.Setenv("REDIS_HOST", "cache.internal")
	t.Setenv("STORAGE_PUBLIC_URL", "")
	t.Setenv("STORAGE_ENDPOINT_URL", "http://rustfs:9000")
	t.Setenv("STORAGE_REGION", "us-east-1")
	t.Setenv("STORAGE_ACCESS_KEY_ID", "alisten")
	t.Setenv("STORAGE_SECRET_ACCESS_KEY", "secret")
	unsetenv(t, "STORAGE_BUCKET_NAME")

	for _, tt := range []struct {
		name        string
		publicURL   string
		wantOrigin  string
		wantStorage string
	}{
		{name: "公开地址为空", publicURL: "", wantOrigin: "http://localhost:9876", wantStorage: "http://localhost:9876/storage/alisten"},
		{name: "空域名占位", publicURL: "https://", wantOrigin: "http://localhost:9876", wantStorage: "http://localhost:9876/storage/alisten"},
		{name: "局域网 HTTP", publicURL: "http://192.168.1.20:9876", wantOrigin: "http://192.168.1.20:9876", wantStorage: "http://192.168.1.20:9876/storage/alisten"},
		{name: "公开地址带路径", publicURL: "http://localhost:9876/r/hall", wantOrigin: "http://localhost:9876", wantStorage: "http://localhost:9876/storage/alisten"},
		{name: "公开地址带查询和片段", publicURL: "http://localhost:9876/?x=1#y", wantOrigin: "http://localhost:9876", wantStorage: "http://localhost:9876/storage/alisten"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("ALISTEN_PUBLIC_URL", tt.publicURL)
			cfg, err := loadServeConfig()
			if err != nil {
				t.Fatal(err)
			}
			if cfg.publicOrigin != tt.wantOrigin || cfg.storageURL != tt.wantStorage {
				t.Errorf("公开地址 = %q，对象存储地址 = %q，期望 %q、%q", cfg.publicOrigin, cfg.storageURL, tt.wantOrigin, tt.wantStorage)
			}
		})
	}
}

func TestPublicOrigin(t *testing.T) {
	valid := map[string]string{
		"https://alisten.example":             "https://alisten.example",
		"https://Alisten.Example/":            "https://alisten.example",
		"HTTPS://alisten.example:443/r/hall":  "https://alisten.example",
		"http://localhost:80":                 "http://localhost",
		"http://localhost:8080":               "http://localhost:8080",
		"https://[::1]:443":                   "https://[::1]",
		"https://[2001:db8::1]:8443":          "https://[2001:db8::1]:8443",
		"https://[2001:DB8::1]":               "https://[2001:db8::1]",
		"http://127.0.0.1:8080":               "http://127.0.0.1:8080",
		"https://alisten.example.":            "https://alisten.example.",
		"https://alisten.example:8443/?x=1#y": "https://alisten.example:8443",
		"https://xn--fsqu00a.example":         "https://xn--fsqu00a.example",
	}
	for value, want := range valid {
		if got, err := publicOrigin(value); err != nil || got != want {
			t.Errorf("publicOrigin(%q) = %q, %v，期望 %q", value, got, err, want)
		}
	}
	invalid := []string{
		"", "alisten.example", "ftp://alisten.example", "https://", "https://:8080", "http://:80/r/hall", "/r/hall", "https://alisten.example:port",
		"https://alisten.example:", "https://alisten.example:0443", "https://alisten.example:0", "https://alisten.example:65536",
		"https://例子.example",
		"https://[2001:db8:0:0:0:0:0:1]", "https://[2001:0db8::1]", "https://[::ffff:192.0.2.1]", "https://[fe80::1%25eth0]",
		"http://127.1:8080", "http://0x7f000001", "http://010.0.0.1", "http://127.0.0.1.:8080", "http://example.123",
	}
	for _, value := range invalid {
		if got, err := publicOrigin(value); err == nil {
			t.Errorf("publicOrigin(%q) = %q，期望报错", value, got)
		}
	}
}

func TestParseStorageURL(t *testing.T) {
	valid := map[string]string{
		"https://alisten.example/storage/alisten":  "https://alisten.example/storage/alisten",
		"https://alisten.example/storage/alisten/": "https://alisten.example/storage/alisten",
		"http://localhost:9000/alisten":            "http://localhost:9000/alisten",
		"https://cdn.example":                      "https://cdn.example",
	}
	for value, want := range valid {
		if got, err := parseStorageURL(value); err != nil || got != want {
			t.Errorf("parseStorageURL(%q) = %q, %v，期望 %q", value, got, err, want)
		}
	}
	for _, value := range []string{"", "cdn.example", "ftp://cdn.example", "https://", "https://:9000/alisten", "/storage", "https://cdn.example/a?x=1", "https://cdn.example/a#b", "https://cdn.example/a?", "https://cdn.example/a#", "https://key:secret@cdn.example/a"} {
		if got, err := parseStorageURL(value); err == nil {
			t.Errorf("parseStorageURL(%q) = %q，期望报错", value, got)
		}
	}
}
