package porttest

import (
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
)

// 与 Redis 适配器的 TestLoginDevices 相同的语义。
func TestDevices(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		devices := NewDevices()
		if _, _, err := devices.DeviceMember(t.Context(), []byte("device")); !errors.Is(err, port.ErrDeviceNotFound) {
			t.Errorf("没有保存过的凭证 = %v，期望 ErrDeviceNotFound", err)
		}
		if err := devices.SaveDevice(t.Context(), []byte("device"), "m1", []byte("stamp 1"), 2*time.Minute); err != nil {
			t.Fatal(err)
		}
		if member, stamp, err := devices.DeviceMember(t.Context(), []byte("device")); err != nil || member != "m1" || string(stamp) != "stamp 1" {
			t.Errorf("DeviceMember = %q, %q, %v，期望 m1, stamp 1", member, stamp, err)
		}
		time.Sleep(time.Minute)
		if err := devices.SaveDevice(t.Context(), []byte("device"), "m1", []byte("stamp 2"), 2*time.Minute); err != nil {
			t.Fatal(err)
		}
		time.Sleep(90 * time.Second)
		if member, stamp, err := devices.DeviceMember(t.Context(), []byte("device")); err != nil || member != "m1" || string(stamp) != "stamp 2" {
			t.Errorf("续期之后过了原来的有效期 = %q, %q, %v，期望仍然有效，指纹是 stamp 2", member, stamp, err)
		}
		time.Sleep(time.Minute)
		if _, _, err := devices.DeviceMember(t.Context(), []byte("device")); !errors.Is(err, port.ErrDeviceNotFound) {
			t.Errorf("过期之后 = %v，期望 ErrDeviceNotFound", err)
		}
	})
}
