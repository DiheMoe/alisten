package redis

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/dihemoe/alisten/internal/app/port"
)

var (
	_ port.PairingCodes        = (*Codes)(nil)
	_ port.GitHubLogins        = (*Codes)(nil)
	_ port.GitHubVerifications = (*Codes)(nil)
)

const (
	pairingPrefix        = "pairing:"
	githubRequestPrefix  = "github:request:"
	githubGrantPrefix    = "github:grant:"
	githubVerifiedPrefix = "github:verified:"
)

// 配对码、GitHub 登录的 state 和兑换码、用 GitHub 确认身份的记录，键中只有它们的哈希。丢失时用户重新生成、重新登录或重新确认即可。
type Codes struct {
	client *goredis.Client
}

func NewCodes(client *goredis.Client) *Codes {
	return &Codes{client: client}
}

func codeKey(prefix string, hash []byte) string {
	return prefix + hex.EncodeToString(hash)
}

type pairingRecord struct {
	MemberID string `json:"memberId"`
	Session  []byte `json:"session"`
}

type githubRequestRecord struct {
	Intent   string `json:"intent"`
	Client   string `json:"client"`
	MemberID string `json:"memberId"`
	Nonce    []byte `json:"nonce"`
	Verifier string `json:"verifier"`
}

type githubGrantRecord struct {
	githubRequestRecord
	GitHubID int64 `json:"githubId"`
}

var (
	intentNames = map[port.GitHubIntent]string{port.GitHubLogin: "login", port.GitHubBind: "bind", port.GitHubVerify: "verify"}
	clientNames = map[port.GitHubClient]string{port.GitHubWeb: "web", port.GitHubApp: "app"}
)

func requestRecord(r port.GitHubRequest) githubRequestRecord {
	return githubRequestRecord{Intent: intentNames[r.Intent], Client: clientNames[r.Client], MemberID: r.MemberID, Nonce: r.NonceHash, Verifier: r.CodeVerifier}
}

func (r githubRequestRecord) request() (port.GitHubRequest, error) {
	intent, ok := keyOf(intentNames, r.Intent)
	if !ok {
		return port.GitHubRequest{}, fmt.Errorf("不认识的意图 %q", r.Intent)
	}
	client, ok := keyOf(clientNames, r.Client)
	if !ok {
		return port.GitHubRequest{}, fmt.Errorf("不认识的客户端 %q", r.Client)
	}
	return port.GitHubRequest{Intent: intent, Client: client, MemberID: r.MemberID, NonceHash: r.Nonce, CodeVerifier: r.Verifier}, nil
}

func keyOf[K comparable](names map[K]string, name string) (K, bool) {
	for k, n := range names {
		if n == name {
			return k, true
		}
	}
	var zero K
	return zero, false
}

func (c *Codes) CreatePairing(ctx context.Context, codeHash []byte, p port.Pairing, ttl time.Duration) (bool, error) {
	value, err := json.Marshal(pairingRecord{MemberID: p.MemberID, Session: p.SessionTokenHash})
	if err != nil {
		return false, err
	}
	created, err := c.client.SetNX(ctx, codeKey(pairingPrefix, codeHash), value, ttl).Result()
	if err != nil {
		return false, fmt.Errorf("保存配对码：%w", err)
	}
	return created, nil
}

func (c *Codes) TakePairing(ctx context.Context, codeHash []byte) (port.Pairing, error) {
	var record pairingRecord
	if err := c.take(ctx, codeKey(pairingPrefix, codeHash), &record); err != nil {
		return port.Pairing{}, fmt.Errorf("取出配对码：%w", err)
	}
	return port.Pairing{MemberID: record.MemberID, SessionTokenHash: record.Session}, nil
}

func (c *Codes) SaveRequest(ctx context.Context, stateHash []byte, r port.GitHubRequest, ttl time.Duration) error {
	if err := c.save(ctx, codeKey(githubRequestPrefix, stateHash), requestRecord(r), ttl); err != nil {
		return fmt.Errorf("保存 GitHub 授权请求：%w", err)
	}
	return nil
}

func (c *Codes) TakeRequest(ctx context.Context, stateHash []byte) (port.GitHubRequest, error) {
	var record githubRequestRecord
	if err := c.take(ctx, codeKey(githubRequestPrefix, stateHash), &record); err != nil {
		return port.GitHubRequest{}, fmt.Errorf("取出 GitHub 授权请求：%w", err)
	}
	r, err := record.request()
	if err != nil {
		return port.GitHubRequest{}, fmt.Errorf("取出 GitHub 授权请求：%w", err)
	}
	return r, nil
}

func (c *Codes) SaveGrant(ctx context.Context, codeHash []byte, g port.GitHubGrant, ttl time.Duration) error {
	record := githubGrantRecord{githubRequestRecord: requestRecord(g.Request), GitHubID: g.GitHubID}
	if err := c.save(ctx, codeKey(githubGrantPrefix, codeHash), record, ttl); err != nil {
		return fmt.Errorf("保存 GitHub 登录的兑换码：%w", err)
	}
	return nil
}

func (c *Codes) TakeGrant(ctx context.Context, codeHash []byte) (port.GitHubGrant, error) {
	var record githubGrantRecord
	if err := c.take(ctx, codeKey(githubGrantPrefix, codeHash), &record); err != nil {
		return port.GitHubGrant{}, fmt.Errorf("取出 GitHub 登录的兑换码：%w", err)
	}
	r, err := record.request()
	if err != nil {
		return port.GitHubGrant{}, fmt.Errorf("取出 GitHub 登录的兑换码：%w", err)
	}
	return port.GitHubGrant{Request: r, GitHubID: record.GitHubID}, nil
}

func (c *Codes) SaveVerification(ctx context.Context, tokenHash []byte, ttl time.Duration) error {
	if err := c.client.Set(ctx, codeKey(githubVerifiedPrefix, tokenHash), "", ttl).Err(); err != nil {
		return fmt.Errorf("保存用 GitHub 确认身份的记录：%w", err)
	}
	return nil
}

func (c *Codes) Verified(ctx context.Context, tokenHash []byte) (bool, error) {
	n, err := c.client.Exists(ctx, codeKey(githubVerifiedPrefix, tokenHash)).Result()
	if err != nil {
		return false, fmt.Errorf("读取用 GitHub 确认身份的记录：%w", err)
	}
	return n == 1, nil
}

func (c *Codes) DeleteVerification(ctx context.Context, tokenHash []byte) error {
	if err := c.client.Del(ctx, codeKey(githubVerifiedPrefix, tokenHash)).Err(); err != nil {
		return fmt.Errorf("删除用 GitHub 确认身份的记录：%w", err)
	}
	return nil
}

func (c *Codes) save(ctx context.Context, key string, record any, ttl time.Duration) error {
	value, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return c.client.Set(ctx, key, value, ttl).Err()
}

// GETDEL 让取出和删除成为一个原子操作，所以并发的两次兑换只有一次能拿到。
func (c *Codes) take(ctx context.Context, key string, record any) error {
	value, err := c.client.GetDel(ctx, key).Bytes()
	if errors.Is(err, goredis.Nil) {
		return port.ErrCodeNotFound
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(value, record); err != nil {
		return fmt.Errorf("解码存储的值：%w", err)
	}
	return nil
}
