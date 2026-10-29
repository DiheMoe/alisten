package bootstrap

import (
	"strings"
	"testing"
)

func TestLoadDatabaseConfig(t *testing.T) {
	t.Setenv("DATABASE_HOST", "db.internal")
	t.Setenv("DATABASE_USERNAME", "alisten")
	t.Setenv("DATABASE_NAME", "alisten")
	unsetenv(t, "DATABASE_PORT")
	unsetenv(t, "DATABASE_PASSWORD")
	unsetenv(t, "DATABASE_SSLMODE")
	cfg, err := loadDatabaseConfig()
	if err != nil {
		t.Fatal(err)
	}
	c := cfg.ConnConfig
	if c.Host != "db.internal" || c.Port != 5432 || c.User != "alisten" || c.Database != "alisten" || c.Password != "" {
		t.Errorf("连接配置 = %s:%d、用户 %q、数据库 %q、密码 %q，期望默认端口 5432、没有密码", c.Host, c.Port, c.User, c.Database, c.Password)
	}
	if c.TLSConfig == nil || len(c.Fallbacks) != 1 || c.Fallbacks[0].TLSConfig != nil {
		t.Error("默认的 sslmode 应为 prefer：先用 TLS 连接，失败时改用明文")
	}
	if cfg.MaxConns < 2 {
		t.Errorf("连接池最多 %d 个连接，迁移锁要一直占用一个，至少要有两个", cfg.MaxConns)
	}

	t.Setenv("DATABASE_HOST", "/var/run/postgresql")
	t.Setenv("DATABASE_PORT", "6432")
	t.Setenv("DATABASE_USERNAME", `o'brien \x`)
	t.Setenv("DATABASE_PASSWORD", `Zq9x K7mP' @/\`)
	t.Setenv("DATABASE_NAME", "alisten db")
	t.Setenv("DATABASE_SSLMODE", "disable")
	cfg, err = loadDatabaseConfig()
	if err != nil {
		t.Fatal(err)
	}
	c = cfg.ConnConfig
	if c.Host != "/var/run/postgresql" || c.Port != 6432 || c.User != `o'brien \x` || c.Database != "alisten db" || c.Password != `Zq9x K7mP' @/\` {
		t.Errorf("连接配置 = %s:%d、用户 %q、数据库 %q、密码 %q，期望与环境变量逐字相同", c.Host, c.Port, c.User, c.Database, c.Password)
	}
	if strings.Contains(cfg.ConnString(), "Zq9x") {
		t.Error("密码不能出现在连接串中")
	}
	t.Setenv("DATABASE_HOST", "db.internal")
	cfg, err = loadDatabaseConfig()
	if err != nil {
		t.Fatal(err)
	}
	if c := cfg.ConnConfig; c.TLSConfig != nil || len(c.Fallbacks) != 0 {
		t.Error("sslmode=disable 时不应使用 TLS")
	}

	for _, tc := range []struct{ name, value string }{
		{"DATABASE_HOST", ""},
		{"DATABASE_PORT", "0"},
		{"DATABASE_PORT", "65536"},
		{"DATABASE_SSLMODE", "on"},
	} {
		t.Run(tc.name+"="+tc.value, func(t *testing.T) {
			t.Setenv(tc.name, tc.value)
			_, err := loadDatabaseConfig()
			if err == nil || !strings.Contains(err.Error(), tc.name) {
				t.Fatalf("错误 = %v，期望报错并写明 %s", err, tc.name)
			}
			if strings.Contains(err.Error(), "Zq9x") || strings.Contains(err.Error(), "K7mP") {
				t.Errorf("错误信息中不能出现密码：%v", err)
			}
		})
	}
}
