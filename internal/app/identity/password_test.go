package identity

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func hashPassword(t *testing.T, password string) string {
	t.Helper()
	hash, err := HashPassword(t.Context(), password)
	if err != nil {
		t.Fatal(err)
	}
	return hash
}

func TestPasswordHash(t *testing.T) {
	if got := hashPassword(t, ""); got != "" {
		t.Errorf("空密码的哈希 = %q，期望空字符串", got)
	}

	hash := hashPassword(t, "芝麻开门")
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Errorf("哈希 %q 应是 argon2id 的 PHC 字符串", hash)
	}
	if hashPassword(t, "芝麻开门") == hash {
		t.Error("每次哈希都应使用新的盐")
	}
	for password, want := range map[string]bool{"芝麻开门": true, "芝麻关门": false, "": false} {
		if ok, err := checkPassword(t.Context(), hash, password); err != nil || ok != want {
			t.Errorf("checkPassword(%q) = %v, %v，期望 %v", password, ok, err, want)
		}
	}
}

func TestCheckPasswordRejectsMalformedHash(t *testing.T) {
	for _, malformed := range []string{
		"芝麻开门",
	} {
		if _, err := checkPassword(t.Context(), malformed, "芝麻开门"); !errors.Is(err, errMalformedPasswordHash) {
			t.Errorf("checkPassword(%q) 的错误 = %v，期望 errMalformedPasswordHash", malformed, err)
		}
	}
}

func occupyArgonSlots(t *testing.T) {
	t.Helper()
	for range cap(argonSlots) {
		argonSlots <- struct{}{}
	}
	// 归还名额时不阻塞：实现在 ctx 结束之后仍去计算时，会在结束时替这里归还一个名额；阻塞地归还会让测试一直挂到包超时，
	// 而不是报出错误。
	t.Cleanup(func() {
		for range cap(argonSlots) {
			select {
			case <-argonSlots:
			default:
			}
		}
	})
}

func TestQueuedPasswordWorkGivesUpWithContext(t *testing.T) {
	hash := hashPassword(t, "芝麻开门")
	occupyArgonSlots(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := HashPassword(ctx, "芝麻开门"); !errors.Is(err, context.Canceled) {
		t.Errorf("名额占满时 HashPassword 的错误 = %v，期望 context.Canceled", err)
	}
	if _, err := checkPassword(ctx, hash, "芝麻开门"); !errors.Is(err, context.Canceled) {
		t.Errorf("名额占满时 checkPassword 的错误 = %v，期望 context.Canceled", err)
	}
}

func TestPasswordWorkReturnsItsSlot(t *testing.T) {
	// 名额没有归还时，第 cap+1 次计算会一直等到期限。
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	for range cap(argonSlots) + 1 {
		if _, err := HashPassword(ctx, "芝麻开门"); err != nil {
			t.Fatal(err)
		}
	}
}
