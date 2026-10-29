package identity

import (
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
)

// 成员 token 和票据都是 256 位随机值，存储中只有它们的 SHA-256。
func newSecret() (string, []byte) {
	b := make([]byte, 32)
	// crypto/rand.Read 不会返回错误。
	rand.Read(b)
	secret := base64.RawURLEncoding.EncodeToString(b)
	return secret, hashSecret(secret)
}

func hashSecret(secret string) []byte {
	sum := sha256.Sum256([]byte(secret))
	return sum[:]
}

func guestName() string {
	b := make([]byte, 2)
	rand.Read(b)
	return "游客-" + hex.EncodeToString(b)
}

// Gravatar 用去掉首尾空白、转为小写的邮箱计算哈希。
func avatarHash(email string) string {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return ""
	}
	sum := md5.Sum([]byte(email))
	return hex.EncodeToString(sum[:])
}
