package porttest

import (
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
)

// 与 Redis 适配器的 TestPairingCodes、TestGitHubLogins 相同的语义。
func TestCodes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		codes := NewCodes()
		p := port.Pairing{MemberID: "m1", SessionTokenHash: []byte("session")}
		if created, err := codes.CreatePairing(t.Context(), []byte("code"), p, time.Minute); err != nil || !created {
			t.Fatalf("CreatePairing = %v, %v", created, err)
		}
		if created, _ := codes.CreatePairing(t.Context(), []byte("code"), port.Pairing{MemberID: "m2"}, time.Minute); created {
			t.Error("哈希已经有配对码时不应覆盖")
		}
		if got, err := codes.TakePairing(t.Context(), []byte("code")); err != nil || got.MemberID != "m1" {
			t.Errorf("TakePairing = %#v, %v", got, err)
		}
		if _, err := codes.TakePairing(t.Context(), []byte("code")); !errors.Is(err, port.ErrCodeNotFound) {
			t.Errorf("第二次取 = %v，期望 ErrCodeNotFound", err)
		}

		if _, err := codes.CreatePairing(t.Context(), []byte("late"), p, time.Minute); err != nil {
			t.Fatal(err)
		}
		r := port.GitHubRequest{Intent: port.GitHubLogin, MemberID: "m1", NonceHash: []byte("nonce")}
		if err := codes.SaveRequest(t.Context(), []byte("late"), r, 2*time.Minute); err != nil {
			t.Fatal(err)
		}
		if err := codes.SaveGrant(t.Context(), []byte("late"), port.GitHubGrant{Request: r, GitHubID: 42}, 3*time.Minute); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Minute)
		if _, err := codes.TakePairing(t.Context(), []byte("late")); !errors.Is(err, port.ErrCodeNotFound) {
			t.Errorf("过期之后取配对码 = %v，期望 ErrCodeNotFound", err)
		}
		if created, _ := codes.CreatePairing(t.Context(), []byte("late"), p, time.Minute); !created {
			t.Error("过期的配对码不应挡住新的")
		}
		if got, err := codes.TakeRequest(t.Context(), []byte("late")); err != nil || got.Intent != port.GitHubLogin {
			t.Errorf("同一个哈希的 state = %#v, %v，三种键互不相通", got, err)
		}
		time.Sleep(2 * time.Minute)
		if _, err := codes.TakeGrant(t.Context(), []byte("late")); !errors.Is(err, port.ErrCodeNotFound) {
			t.Errorf("过期之后取兑换码 = %v，期望 ErrCodeNotFound", err)
		}
	})
}

// 与 Redis 适配器的 TestGitHubVerifications 相同的语义。
func TestGitHubVerifications(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		codes := NewCodes()
		if err := codes.SaveVerification(t.Context(), []byte("session"), 10*time.Minute); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			if ok, err := codes.Verified(t.Context(), []byte("session")); err != nil || !ok {
				t.Errorf("Verified = %v, %v，期望读取不删除", ok, err)
			}
		}
		if err := codes.DeleteVerification(t.Context(), []byte("session")); err != nil {
			t.Fatal(err)
		}
		if ok, err := codes.Verified(t.Context(), []byte("session")); err != nil || ok {
			t.Errorf("删除之后 Verified = %v, %v，期望 false", ok, err)
		}
		if err := codes.DeleteVerification(t.Context(), []byte("session")); err != nil {
			t.Errorf("删除不存在的确认 = %v，期望什么也不做", err)
		}

		r := port.GitHubRequest{Intent: port.GitHubVerify, MemberID: "m1", NonceHash: []byte("nonce")}
		if err := codes.SaveGrant(t.Context(), []byte("other"), port.GitHubGrant{Request: r, GitHubID: 42}, time.Minute); err != nil {
			t.Fatal(err)
		}
		if ok, err := codes.Verified(t.Context(), []byte("other")); err != nil || ok {
			t.Errorf("同一个哈希的兑换码当作确认 = %v, %v，期望 false：两种键互不相通", ok, err)
		}

		if err := codes.SaveVerification(t.Context(), []byte("late"), 10*time.Minute); err != nil {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Minute)
		if ok, err := codes.Verified(t.Context(), []byte("late")); err != nil || ok {
			t.Errorf("过期之后 Verified = %v, %v，期望 false", ok, err)
		}
	})
}
