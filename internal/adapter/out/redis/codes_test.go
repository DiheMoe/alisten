package redis

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
)

func hashOf(s string) []byte {
	sum := sha256.Sum256([]byte(s))
	return sum[:]
}

func TestPairingCodes(t *testing.T) {
	client := newClient(t)
	codes := NewCodes(client)
	p := port.Pairing{MemberID: "00000000-0000-7000-8000-000000000001", SessionTokenHash: hashOf("session")}
	created, err := codes.CreatePairing(t.Context(), hashOf("ABCD2345"), p, 5*time.Minute)
	if err != nil || !created {
		t.Fatalf("CreatePairing = %v, %v", created, err)
	}
	key := "pairing:" + hex.EncodeToString(hashOf("ABCD2345"))
	if ttl := client.PTTL(t.Context(), key).Val(); ttl < 4*time.Minute || ttl > 5*time.Minute {
		t.Errorf("配对码的有效期 = %v，期望 5 分钟", ttl)
	}
	other := port.Pairing{MemberID: "00000000-0000-7000-8000-000000000002", SessionTokenHash: hashOf("other")}
	if created, err := codes.CreatePairing(t.Context(), hashOf("ABCD2345"), other, time.Minute); err != nil || created {
		t.Errorf("哈希已经有配对码时 CreatePairing = %v, %v，期望不覆盖", created, err)
	}
	got, err := codes.TakePairing(t.Context(), hashOf("ABCD2345"))
	if err != nil || got.MemberID != p.MemberID || !bytes.Equal(got.SessionTokenHash, p.SessionTokenHash) {
		t.Errorf("TakePairing = %#v, %v，期望 %#v", got, err, p)
	}
	if _, err := codes.TakePairing(t.Context(), hashOf("ABCD2345")); !errors.Is(err, port.ErrCodeNotFound) {
		t.Errorf("第二次取 = %v，期望 ErrCodeNotFound", err)
	}
	if _, err := codes.TakePairing(t.Context(), hashOf("ZZZZ2345")); !errors.Is(err, port.ErrCodeNotFound) {
		t.Errorf("不存在的配对码 = %v，期望 ErrCodeNotFound", err)
	}

	if err := client.Set(t.Context(), "pairing:"+hex.EncodeToString(hashOf("BROKEN22")), "not json", time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := codes.TakePairing(t.Context(), hashOf("BROKEN22")); err == nil || errors.Is(err, port.ErrCodeNotFound) {
		t.Errorf("存储中的值无法解码时 = %v，期望报错", err)
	}
}

func TestGitHubLogins(t *testing.T) {
	client := newClient(t)
	codes := NewCodes(client)
	r := port.GitHubRequest{Intent: port.GitHubBind, Client: port.GitHubApp, MemberID: "00000000-0000-7000-8000-000000000001", NonceHash: hashOf("nonce"), CodeVerifier: "verifier-1"}
	if err := codes.SaveRequest(t.Context(), hashOf("state"), r, 10*time.Minute); err != nil {
		t.Fatal(err)
	}
	if ttl := client.PTTL(t.Context(), "github:request:"+hex.EncodeToString(hashOf("state"))).Val(); ttl < 9*time.Minute || ttl > 10*time.Minute {
		t.Errorf("state 的有效期 = %v，期望 10 分钟", ttl)
	}
	if _, err := codes.TakeGrant(t.Context(), hashOf("state")); !errors.Is(err, port.ErrCodeNotFound) {
		t.Errorf("用 state 取兑换码 = %v，期望 ErrCodeNotFound：两种键互不相通", err)
	}
	got, err := codes.TakeRequest(t.Context(), hashOf("state"))
	if err != nil || got.Intent != r.Intent || got.Client != r.Client || got.MemberID != r.MemberID || !bytes.Equal(got.NonceHash, r.NonceHash) || got.CodeVerifier != r.CodeVerifier {
		t.Errorf("TakeRequest = %#v, %v，期望 %#v", got, err, r)
	}
	if _, err := codes.TakeRequest(t.Context(), hashOf("state")); !errors.Is(err, port.ErrCodeNotFound) {
		t.Errorf("第二次取 state = %v，期望 ErrCodeNotFound", err)
	}

	g := port.GitHubGrant{Request: port.GitHubRequest{Intent: port.GitHubLogin, Client: port.GitHubWeb, MemberID: r.MemberID, NonceHash: r.NonceHash}, GitHubID: 9007199254740993}
	if err := codes.SaveGrant(t.Context(), hashOf("code"), g, time.Minute); err != nil {
		t.Fatal(err)
	}
	if ttl := client.PTTL(t.Context(), "github:grant:"+hex.EncodeToString(hashOf("code"))).Val(); ttl < 50*time.Second || ttl > time.Minute {
		t.Errorf("兑换码的有效期 = %v，期望 1 分钟", ttl)
	}
	gotGrant, err := codes.TakeGrant(t.Context(), hashOf("code"))
	if err != nil || gotGrant.GitHubID != g.GitHubID || gotGrant.Request.Intent != port.GitHubLogin || gotGrant.Request.Client != port.GitHubWeb || gotGrant.Request.MemberID != r.MemberID || !bytes.Equal(gotGrant.Request.NonceHash, r.NonceHash) {
		t.Errorf("TakeGrant = %#v, %v，期望 %#v", gotGrant, err, g)
	}
	if _, err := codes.TakeGrant(t.Context(), hashOf("code")); !errors.Is(err, port.ErrCodeNotFound) {
		t.Errorf("第二次取兑换码 = %v，期望 ErrCodeNotFound", err)
	}

	for name, record := range map[string]string{
		"意图":  `{"intent":"steal","client":"web","memberId":"x","nonce":""}`,
		"客户端": `{"intent":"login","client":"evil","memberId":"x","nonce":""}`,
	} {
		if err := client.Set(t.Context(), "github:request:"+hex.EncodeToString(hashOf("odd")), record, time.Minute).Err(); err != nil {
			t.Fatal(err)
		}
		if _, err := codes.TakeRequest(t.Context(), hashOf("odd")); err == nil || errors.Is(err, port.ErrCodeNotFound) {
			t.Errorf("存储中的%s不认识时 = %v，期望报错", name, err)
		}
	}
}

func TestGitHubVerifications(t *testing.T) {
	client := newClient(t)
	codes := NewCodes(client)
	if err := codes.SaveVerification(t.Context(), hashOf("session"), 10*time.Minute); err != nil {
		t.Fatal(err)
	}
	if ttl := client.PTTL(t.Context(), "github:verified:"+hex.EncodeToString(hashOf("session"))).Val(); ttl < 9*time.Minute || ttl > 10*time.Minute {
		t.Errorf("确认的有效期 = %v，期望 10 分钟", ttl)
	}
	for range 2 {
		if ok, err := codes.Verified(t.Context(), hashOf("session")); err != nil || !ok {
			t.Errorf("Verified = %v, %v，期望读取不删除", ok, err)
		}
	}
	if err := codes.DeleteVerification(t.Context(), hashOf("session")); err != nil {
		t.Fatal(err)
	}
	if ok, err := codes.Verified(t.Context(), hashOf("session")); err != nil || ok {
		t.Errorf("删除之后 Verified = %v, %v，期望 false", ok, err)
	}
	if err := codes.DeleteVerification(t.Context(), hashOf("session")); err != nil {
		t.Errorf("删除不存在的确认 = %v，期望什么也不做", err)
	}

	g := port.GitHubGrant{Request: port.GitHubRequest{Intent: port.GitHubVerify, Client: port.GitHubWeb, MemberID: "00000000-0000-7000-8000-000000000001", NonceHash: hashOf("nonce")}, GitHubID: 42}
	if err := codes.SaveGrant(t.Context(), hashOf("other"), g, time.Minute); err != nil {
		t.Fatal(err)
	}
	if ok, err := codes.Verified(t.Context(), hashOf("other")); err != nil || ok {
		t.Errorf("同一个哈希的兑换码当作确认 = %v, %v，期望 false：两种键互不相通", ok, err)
	}
	if got, err := codes.TakeGrant(t.Context(), hashOf("other")); err != nil || got.Request.Intent != port.GitHubVerify {
		t.Errorf("TakeGrant = %#v, %v，期望意图 verify 原样取回", got, err)
	}
}
