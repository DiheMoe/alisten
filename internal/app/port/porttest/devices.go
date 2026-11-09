package porttest

import (
	"context"
	"sync"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
)

var _ port.LoginDevices = (*Devices)(nil)

// 时间取自 time.Now，可以在 synctest 中使用。
type Devices struct {
	mu      sync.Mutex
	entries map[string]device
}

type device struct {
	memberID      string
	passwordStamp string
	expires       time.Time
}

func NewDevices() *Devices {
	return &Devices{entries: map[string]device{}}
}

func (d *Devices) SaveDevice(_ context.Context, deviceHash []byte, memberID string, passwordStamp []byte, ttl time.Duration) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.entries[string(deviceHash)] = device{memberID: memberID, passwordStamp: string(passwordStamp), expires: time.Now().Add(ttl)}
	return nil
}

func (d *Devices) DeviceMember(_ context.Context, deviceHash []byte) (string, []byte, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	e, ok := d.entries[string(deviceHash)]
	if !ok || !time.Now().Before(e.expires) {
		return "", nil, port.ErrDeviceNotFound
	}
	return e.memberID, []byte(e.passwordStamp), nil
}
