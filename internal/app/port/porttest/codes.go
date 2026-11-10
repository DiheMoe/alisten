package porttest

import (
	"context"
	"sync"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
)

var (
	_ port.PairingCodes        = (*Codes)(nil)
	_ port.GitHubLogins        = (*Codes)(nil)
	_ port.GitHubVerifications = (*Codes)(nil)
)

// 时间取自 time.Now，可以在 synctest 中使用。
type Codes struct {
	mu      sync.Mutex
	entries map[string]codeEntry
}

type codeEntry struct {
	value   any
	expires time.Time
}

func NewCodes() *Codes {
	return &Codes{entries: map[string]codeEntry{}}
}

func (c *Codes) put(key string, value any, ttl time.Duration, overwrite bool) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.entries[key]; ok && !overwrite && time.Now().Before(e.expires) {
		return false
	}
	c.entries[key] = codeEntry{value: value, expires: time.Now().Add(ttl)}
	return true
}

func take[T any](c *Codes, key string) (T, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	delete(c.entries, key)
	if !ok || !time.Now().Before(e.expires) {
		var zero T
		return zero, port.ErrCodeNotFound
	}
	return e.value.(T), nil
}

func (c *Codes) CreatePairing(_ context.Context, codeHash []byte, p port.Pairing, ttl time.Duration) (bool, error) {
	return c.put("pairing:"+string(codeHash), p, ttl, false), nil
}

func (c *Codes) TakePairing(_ context.Context, codeHash []byte) (port.Pairing, error) {
	return take[port.Pairing](c, "pairing:"+string(codeHash))
}

func (c *Codes) SaveRequest(_ context.Context, stateHash []byte, r port.GitHubRequest, ttl time.Duration) error {
	c.put("github:request:"+string(stateHash), r, ttl, true)
	return nil
}

func (c *Codes) TakeRequest(_ context.Context, stateHash []byte) (port.GitHubRequest, error) {
	return take[port.GitHubRequest](c, "github:request:"+string(stateHash))
}

func (c *Codes) SaveGrant(_ context.Context, codeHash []byte, g port.GitHubGrant, ttl time.Duration) error {
	c.put("github:grant:"+string(codeHash), g, ttl, true)
	return nil
}

func (c *Codes) TakeGrant(_ context.Context, codeHash []byte) (port.GitHubGrant, error) {
	return take[port.GitHubGrant](c, "github:grant:"+string(codeHash))
}

func (c *Codes) SaveVerification(_ context.Context, tokenHash []byte, ttl time.Duration) error {
	c.put("github:verified:"+string(tokenHash), struct{}{}, ttl, true)
	return nil
}

func (c *Codes) Verified(_ context.Context, tokenHash []byte) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries["github:verified:"+string(tokenHash)]
	return ok && time.Now().Before(e.expires), nil
}

func (c *Codes) DeleteVerification(_ context.Context, tokenHash []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, "github:verified:"+string(tokenHash))
	return nil
}
