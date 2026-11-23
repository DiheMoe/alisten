package cookiecache

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

func TestGetReloadsAfterTTL(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		loads := 0
		c := New(func(context.Context) (string, error) {
			loads++
			return "cookie=" + string(rune('0'+loads)), nil
		}, nil)
		for range 2 {
			if got, err := c.Get(t.Context()); err != nil || got != "cookie=1" {
				t.Fatalf("Get = %q, %v，期望 cookie=1", got, err)
			}
		}
		time.Sleep(TTL - time.Nanosecond)
		if got, err := c.Get(t.Context()); err != nil || got != "cookie=1" {
			t.Fatalf("未过期的 Get = %q, %v，期望命中缓存", got, err)
		}
		time.Sleep(time.Nanosecond)
		if got, err := c.Get(t.Context()); err != nil || got != "cookie=2" {
			t.Errorf("过期后的 Get = %q, %v，期望重新加载", got, err)
		}
	})
}

func TestReloadBypassesCache(t *testing.T) {
	loads := 0
	c := New(func(context.Context) (string, error) {
		loads++
		return "cookie=" + string(rune('0'+loads)), nil
	}, nil)
	if got, err := c.Get(t.Context()); err != nil || got != "cookie=1" {
		t.Fatalf("首次 Get = %q, %v", got, err)
	}
	if got, err := c.Reload(t.Context()); err != nil || got != "cookie=2" {
		t.Fatalf("Reload = %q, %v，期望立即读取最新凭据", got, err)
	}
	if got, err := c.Get(t.Context()); err != nil || got != "cookie=2" {
		t.Errorf("Reload 之后的 Get = %q, %v，期望返回更新后的缓存", got, err)
	}
}

func TestDeadlinesAreIndependentOfCallerTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := New(
			func(ctx context.Context) (string, error) {
				<-ctx.Done()
				return "", ctx.Err()
			},
			func(ctx context.Context, _ string) error {
				<-ctx.Done()
				return ctx.Err()
			},
		)
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		defer cancel()

		start := time.Now()
		if _, err := c.Get(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("读取卡住时的错误 = %v，期望 DeadlineExceeded", err)
		}
		if waited := time.Since(start); waited != LoadTimeout {
			t.Errorf("读取等待了 %v，期望 %v", waited, LoadTimeout)
		}

		start = time.Now()
		if err := c.Put(ctx, "cookie=1"); !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("保存卡住时的错误 = %v，期望 DeadlineExceeded", err)
		}
		if waited := time.Since(start); waited != LoadTimeout {
			t.Errorf("保存等待了 %v，期望 %v", waited, LoadTimeout)
		}
	})
}

func TestPutAndSwap(t *testing.T) {
	stored := "v1"
	c := NewWithSwap(
		func(context.Context) (string, error) { return stored, nil },
		func(_ context.Context, v string) error { stored = v; return nil },
		func(_ context.Context, expected, v string) (bool, error) {
			if stored != expected {
				return false, nil
			}
			stored = v
			return true, nil
		},
	)
	if got, err := c.Get(t.Context()); err != nil || got != "v1" {
		t.Fatalf("初始 Get = %q, %v", got, err)
	}
	if ok, err := c.Swap(t.Context(), "v1", "v2"); err != nil || !ok {
		t.Fatalf("匹配旧值的 Swap = %v, %v，期望成功", ok, err)
	}
	if cur, ok := c.Current(); !ok || cur != "v2" {
		t.Errorf("Swap 成功后的缓存 = %q, %v，期望 v2", cur, ok)
	}

	// 模拟其他实例改写了数据库中的凭据。
	stored = "v3"
	if ok, err := c.Swap(t.Context(), "v2", "stale"); err != nil || ok {
		t.Fatalf("旧值已变化时的 Swap = %v, %v，期望返回 false", ok, err)
	}
	if got, err := c.Get(t.Context()); err != nil || got != "v3" {
		t.Errorf("Swap 未命中后的 Get = %q, %v，期望重新加载 v3", got, err)
	}

	if err := c.Put(t.Context(), "v4"); err != nil {
		t.Fatal(err)
	}
	if cur, ok := c.Current(); !ok || cur != "v4" {
		t.Errorf("Put 后的缓存 = %q, %v，期望 v4", cur, ok)
	}
}
