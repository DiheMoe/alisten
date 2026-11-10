package port

import (
	"context"
	"errors"
	"time"
)

// 配对码、GitHub 登录的 state 和兑换码都只能用一次：取出即删除，到期自动失效。
var ErrCodeNotFound = errors.New("不存在或已经过期")

// 已注册成员生成的配对码：新设备兑换之后登录这个账号。
type Pairing struct {
	MemberID string
	// 生成配对码的会话：它被删除之后（退出登录、修改或重置密码），配对码随之作废。
	SessionTokenHash []byte
}

// 配对码只以 SHA-256 哈希的形式出现在端口中，所有实例共用。
type PairingCodes interface {
	// 这个哈希已经有配对码时返回 false，不覆盖它。
	CreatePairing(ctx context.Context, codeHash []byte, p Pairing, ttl time.Duration) (bool, error)
	// 取出并删除，同一个配对码只能取出一次。不存在或已过期时返回 ErrCodeNotFound。
	TakePairing(ctx context.Context, codeHash []byte) (Pairing, error)
}
