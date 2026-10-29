package bootstrap

import (
	"bytes"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func TestLoadRedisConfig(t *testing.T) {
	t.Setenv("REDIS_HOST", "cache.internal")
	unsetenv(t, "REDIS_PORT")
	unsetenv(t, "REDIS_PASSWORD")
	unsetenv(t, "REDIS_DB")
	opts, err := loadRedisConfig()
	if err != nil {
		t.Fatal(err)
	}
	if opts.Addr != "cache.internal:6379" || opts.Password != "" || opts.DB != 0 || !opts.ContextTimeoutEnabled {
		t.Errorf("地址 %s、密码 %q、库 %d、遵守 ctx 的期限 %v，期望默认端口 6379、不认证、0 号库，并遵守 ctx 的期限", opts.Addr, opts.Password, opts.DB, opts.ContextTimeoutEnabled)
	}

	t.Setenv("REDIS_HOST", "::1")
	t.Setenv("REDIS_PORT", "6380")
	t.Setenv("REDIS_PASSWORD", "Zq9x@K7mP")
	t.Setenv("REDIS_DB", "2")
	opts, err = loadRedisConfig()
	if err != nil {
		t.Fatal(err)
	}
	if opts.Addr != "[::1]:6380" || opts.Password != "Zq9x@K7mP" || opts.DB != 2 {
		t.Errorf("地址 %s、密码 %q、库 %d，期望 [::1]:6380、环境变量中的密码、2 号库", opts.Addr, opts.Password, opts.DB)
	}

	for _, tc := range []struct{ name, value string }{
		{"REDIS_HOST", ""},
		{"REDIS_PORT", "0"},
		{"REDIS_DB", "-1"},
		{"REDIS_DB", "db"},
	} {
		t.Run(tc.name+"="+tc.value, func(t *testing.T) {
			t.Setenv(tc.name, tc.value)
			_, err := loadRedisConfig()
			if err == nil || !strings.Contains(err.Error(), tc.name) {
				t.Fatalf("错误 = %v，期望报错并写明 %s", err, tc.name)
			}
			if strings.Contains(err.Error(), "Zq9x") || strings.Contains(err.Error(), "K7mP") {
				t.Errorf("错误信息中不能出现密码：%v", err)
			}
		})
	}
}

func TestRedisLoggerSkipsRepeatsWithinAMinute(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var logs bytes.Buffer
		l := newRedisLogger(newLogger(&logs, slog.LevelInfo))
		const dial = "redis: connection pool: failed to dial after %d attempts: %v"
		for range 10 {
			l.Printf(t.Context(), dial, 5, "connection refused")
			l.Printf(t.Context(), "redis: discarding bad PubSub connection: %v", "EOF")
			time.Sleep(time.Second)
		}
		time.Sleep(redisLogRepeat)
		l.Printf(t.Context(), dial, 5, "connection refused")

		var got []string
		for line := range strings.Lines(strings.TrimSpace(logs.String())) {
			entry := decodeLine(t, []byte(line))
			if entry["level"] != "WARN" {
				t.Errorf("go-redis 的日志级别 = %v，期望 WARN", entry["level"])
			}
			got = append(got, entry["msg"].(string))
		}
		want := []string{
			"redis: connection pool: failed to dial after 5 attempts: connection refused",
			"redis: discarding bad PubSub connection: EOF",
			"redis: connection pool: failed to dial after 5 attempts: connection refused",
		}
		if !slices.Equal(got, want) {
			t.Errorf("记下的日志 = %q，期望同样的内容一分钟内只记一次", got)
		}
	})
}
