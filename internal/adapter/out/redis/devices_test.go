package redis

import (
	"bytes"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
)

func TestLoginDevices(t *testing.T) {
	client := newClient(t)
	devices := NewDevices(client)
	const alice, bob = "00000000-0000-7000-8000-000000000001", "00000000-0000-7000-8000-000000000002"
	aliceStamp, bobStamp := hashOf("alice password")[:16], hashOf("bob password")[:16]
	if _, _, err := devices.DeviceMember(t.Context(), hashOf("alice-device")); !errors.Is(err, port.ErrDeviceNotFound) {
		t.Errorf("没有保存过的凭证 = %v，期望 ErrDeviceNotFound", err)
	}
	if err := devices.SaveDevice(t.Context(), hashOf("alice-device"), alice, aliceStamp, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := devices.SaveDevice(t.Context(), hashOf("bob-device"), bob, bobStamp, time.Minute); err != nil {
		t.Fatal(err)
	}
	for device, want := range map[string]struct {
		member string
		stamp  []byte
	}{"alice-device": {alice, aliceStamp}, "bob-device": {bob, bobStamp}} {
		if member, stamp, err := devices.DeviceMember(t.Context(), hashOf(device)); err != nil || member != want.member || !bytes.Equal(stamp, want.stamp) {
			t.Errorf("%s = %q, %x, %v，期望 %q, %x", device, member, stamp, err, want.member, want.stamp)
		}
	}

	changed := hashOf("alice new password")[:16]
	if err := devices.SaveDevice(t.Context(), hashOf("alice-device"), alice, changed, 180*24*time.Hour); err != nil {
		t.Fatal(err)
	}
	if member, stamp, err := devices.DeviceMember(t.Context(), hashOf("alice-device")); err != nil || member != alice || !bytes.Equal(stamp, changed) {
		t.Errorf("再次保存之后 = %q, %x, %v，期望 %q, %x", member, stamp, err, alice, changed)
	}
	ttl := client.PTTL(t.Context(), "device:"+hex.EncodeToString(hashOf("alice-device"))).Val()
	if ttl < 180*24*time.Hour-time.Minute || ttl > 180*24*time.Hour {
		t.Errorf("再次保存之后的有效期 = %v，期望从现在重新计为 180 天", ttl)
	}
	if ttl := client.PTTL(t.Context(), "device:"+hex.EncodeToString(hashOf("bob-device"))).Val(); ttl < 50*time.Second || ttl > time.Minute {
		t.Errorf("另一个凭证的有效期 = %v，期望不受影响，仍是 1 分钟", ttl)
	}

	if err := client.Set(t.Context(), "device:"+hex.EncodeToString(hashOf("broken-device")), alice, time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := devices.DeviceMember(t.Context(), hashOf("broken-device")); err == nil || errors.Is(err, port.ErrDeviceNotFound) {
		t.Errorf("存储中的值无法解码时 = %v，期望报错", err)
	}
}
