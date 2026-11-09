package port

import (
	"context"
	"errors"
	"time"
)

var ErrDeviceNotFound = errors.New("登录设备凭证不存在或已经过期")

// 登录设备凭证只以 SHA-256 哈希的形式出现在端口中，所有实例共用。凭证丢失只会让设备退回按用户名计数的登录额度，所以可以存在会丢数据的存储中。
// passwordStamp 是签发时账号密码哈希的指纹，端口原样保存。
type LoginDevices interface {
	// 已经有这个凭证时覆盖它，有效期从现在重新计算。
	SaveDevice(ctx context.Context, deviceHash []byte, memberID string, passwordStamp []byte, ttl time.Duration) error
	// 返回凭证所属的成员和签发时的指纹。不存在或已过期时返回 ErrDeviceNotFound。
	DeviceMember(ctx context.Context, deviceHash []byte) (memberID string, passwordStamp []byte, err error)
}
