package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/dihemoe/alisten/internal/app/port"
)

var _ port.LoginDevices = (*Devices)(nil)

const devicePrefix = "device:"

// 登录设备凭证，键中只有它的哈希，值是所属成员的 ID 和签发时的密码指纹。
type Devices struct {
	client *goredis.Client
}

func NewDevices(client *goredis.Client) *Devices {
	return &Devices{client: client}
}

type deviceRecord struct {
	MemberID      string `json:"memberId"`
	PasswordStamp []byte `json:"passwordStamp"`
}

func (d *Devices) SaveDevice(ctx context.Context, deviceHash []byte, memberID string, passwordStamp []byte, ttl time.Duration) error {
	value, err := json.Marshal(deviceRecord{MemberID: memberID, PasswordStamp: passwordStamp})
	if err != nil {
		return err
	}
	if err := d.client.Set(ctx, codeKey(devicePrefix, deviceHash), value, ttl).Err(); err != nil {
		return fmt.Errorf("保存登录设备凭证：%w", err)
	}
	return nil
}

func (d *Devices) DeviceMember(ctx context.Context, deviceHash []byte) (string, []byte, error) {
	value, err := d.client.Get(ctx, codeKey(devicePrefix, deviceHash)).Bytes()
	if errors.Is(err, goredis.Nil) {
		return "", nil, port.ErrDeviceNotFound
	}
	if err != nil {
		return "", nil, fmt.Errorf("读取登录设备凭证：%w", err)
	}
	var record deviceRecord
	if err := json.Unmarshal(value, &record); err != nil {
		return "", nil, fmt.Errorf("读取登录设备凭证：解码存储的值：%w", err)
	}
	return record.MemberID, record.PasswordStamp, nil
}
