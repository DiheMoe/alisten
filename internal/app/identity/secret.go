package identity

import (
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
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

// 配对码要能手动输入，所以去掉了容易认错的 0、O、1、I。剩下 32 个字符，每个字符正好对应 5 位随机值。
var pairingEncoding = base32.NewEncoding("23456789ABCDEFGHJKLMNPQRSTUVWXYZ").WithPadding(base32.NoPadding)

// 40 位随机值，8 个字符。
func newPairingCode() string {
	b := make([]byte, 5)
	rand.Read(b)
	return pairingEncoding.EncodeToString(b)
}

// 后台重置密码时生成。新密码是 96 位随机值的 base64url 编码，共 16 个字符，方便转告用户。
func newPassword() string {
	b := make([]byte, 12)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
