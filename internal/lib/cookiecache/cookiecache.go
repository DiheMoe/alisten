// Package cookiecache 在进程内短暂缓存数据库里的平台凭据，并为数据库读写设置独立超时，避免占满上游请求的期限。
package cookiecache

import (
	"context"
	"sync"
	"time"
)

const (
	TTL         = 30 * time.Second
	LoadTimeout = 10 * time.Second
)

type Cache struct {
	load  func(context.Context) (string, error)
	store func(context.Context, string) error
	swap  func(context.Context, string, string) (bool, error)

	mu      sync.Mutex
	value   string
	expires time.Time
}

func New(load func(context.Context) (string, error), store func(context.Context, string) error) *Cache {
	return &Cache{load: load, store: store}
}

func NewWithSwap(
	load func(context.Context) (string, error),
	store func(context.Context, string) error,
	swap func(context.Context, string, string) (bool, error),
) *Cache {
	return &Cache{load: load, store: store, swap: swap}
}

func (c *Cache) CanWrite() bool {
	return c.store != nil
}

func (c *Cache) Get(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Now().Before(c.expires) {
		return c.value, nil
	}
	return c.loadLocked(ctx)
}

func (c *Cache) Reload(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.loadLocked(ctx)
}

func (c *Cache) Current() (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Now().Before(c.expires) {
		return c.value, true
	}
	return "", false
}

func (c *Cache) Put(ctx context.Context, value string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, LoadTimeout)
	defer cancel()
	if err := c.store(ctx, value); err != nil {
		return err
	}
	c.value, c.expires = value, time.Now().Add(TTL)
	return nil
}

// Swap 在存储里的凭据仍等于 expected 时原子替换为 value。若其他实例或管理员扫码在期间已经改写了凭据，
// 存储保持不变并返回 false，同时使本地仍等于 expected 的旧缓存立即过期。
func (c *Cache) Swap(ctx context.Context, expected, value string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, LoadTimeout)
	defer cancel()
	ok, err := c.swap(ctx, expected, value)
	if err != nil {
		return false, err
	}
	if ok {
		c.value, c.expires = value, time.Now().Add(TTL)
		return true, nil
	}
	if c.value == expected {
		c.expires = time.Time{}
	}
	return false, nil
}

func (c *Cache) loadLocked(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, LoadTimeout)
	defer cancel()
	value, err := c.load(ctx)
	if err != nil {
		return "", err
	}
	c.value, c.expires = value, time.Now().Add(TTL)
	return value, nil
}
