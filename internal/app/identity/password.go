package identity

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"runtime"
	"strings"

	"golang.org/x/crypto/argon2"
)

// 采用 OWASP 密码存储建议中 argon2id 的最低配置：任何成员都能请求换取票据，而每次校验都要分配这么多内存。
const (
	argonTime    = 2
	argonMemory  = 19 * 1024 // KiB
	argonThreads = 1
	argonKeyLen  = 32
	argonSaltLen = 16
)

var passwordPrefix = fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$", argon2.Version, argonMemory, argonTime, argonThreads)

var errMalformedPasswordHash = errors.New("房间密码哈希的格式不正确")

// 每次计算都要分配 argonMemory 的内存，而创建临时房间和换取票据任何人都能请求：不限制并发时，几十个请求就能耗尽内存。argon2 是纯计算，并发超过 GOMAXPROCS 也不会更快。
var argonSlots = make(chan struct{}, runtime.GOMAXPROCS(0))

func idKey(ctx context.Context, password string, salt []byte) ([]byte, error) {
	select {
	case argonSlots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-argonSlots }()
	return argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen), nil
}

func HashPassword(ctx context.Context, password string) (string, error) {
	if password == "" {
		return "", nil
	}
	salt := make([]byte, argonSaltLen)
	rand.Read(salt)
	key, err := idKey(ctx, password, salt)
	if err != nil {
		return "", err
	}
	return passwordPrefix + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(key), nil
}

// 只接受按当前参数生成的哈希：存储中的密码哈希都由 HashPassword 生成。
func checkPassword(ctx context.Context, hash, password string) (bool, error) {
	encoded, ok := strings.CutPrefix(hash, passwordPrefix)
	if !ok {
		return false, errMalformedPasswordHash
	}
	saltText, keyText, ok := strings.Cut(encoded, "$")
	if !ok {
		return false, errMalformedPasswordHash
	}
	salt, err := base64.RawStdEncoding.DecodeString(saltText)
	if err != nil {
		return false, errMalformedPasswordHash
	}
	key, err := base64.RawStdEncoding.DecodeString(keyText)
	if err != nil || len(key) != argonKeyLen {
		return false, errMalformedPasswordHash
	}
	got, err := idKey(ctx, password, salt)
	if err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare(got, key) == 1, nil
}
