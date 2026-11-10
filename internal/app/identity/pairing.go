package identity

import (
	"context"
	"errors"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/member"
)

var ErrInvalidPairingCode = errors.New("配对码不对或已经过期")

const (
	pairingTTL = 5 * time.Minute
	// 生成配对码只占 Redis 的空间，额度用来防止一个账号在短时间内灌入大量配对码。
	pairingCreateBurst  = 5
	pairingCreateRefill = time.Minute
	// 配对码有 32^8 ≈ 1.1e12 种，5 分钟就过期，所以按 IP 的额度与登录相同就足以让猜中的可能性忽略不计。
	pairingRedeemBurst  = 10
	pairingRedeemRefill = time.Minute
	// 新配对码与有效的配对码撞上的可能性约为万亿分之一，重试几次足够。
	pairingAttempts = 3
)

type PairingCode struct {
	Code      string
	ExpiresAt time.Time
}

// token 是发出请求的会话，配对码属于这个会话。会话被删除（退出登录、修改或重置密码）时，配对码随之作废。
func (a *Accounts) CreatePairingCode(ctx context.Context, memberID, token string) (PairingCode, error) {
	if _, registered, err := a.Account(ctx, memberID); err != nil {
		return PairingCode{}, err
	} else if !registered {
		return PairingCode{}, ErrNotRegistered
	}
	if err := a.take(ctx, "pairing:member:"+memberID, pairingCreateBurst, pairingCreateRefill); err != nil {
		return PairingCode{}, err
	}
	expires := time.Now().Add(pairingTTL)
	p := port.Pairing{MemberID: memberID, SessionTokenHash: hashSecret(token)}
	for range pairingAttempts {
		code := newPairingCode()
		created, err := a.pairings.CreatePairing(ctx, hashSecret(code), p, pairingTTL)
		if err != nil {
			return PairingCode{}, err
		}
		if created {
			return PairingCode{Code: code, ExpiresAt: expires}, nil
		}
	}
	return PairingCode{}, errors.New("生成的配对码都与有效的配对码重复")
}

// 兑换配对码就是登录。与 Login 相同，只有游客能兑换，成功后游客的这个会话随即失效。code 已经按协议校验过格式。
func (a *Accounts) RedeemPairingCode(ctx context.Context, current member.Member, currentToken, clientIP, code string) (Session, Account, error) {
	if err := a.requireGuest(ctx, current.ID); err != nil {
		return Session{}, Account{}, err
	}
	if err := a.take(ctx, "pairing:ip:"+quotaAddr(clientIP), pairingRedeemBurst, pairingRedeemRefill); err != nil {
		return Session{}, Account{}, err
	}
	pairing, err := a.pairings.TakePairing(ctx, hashSecret(code))
	if errors.Is(err, port.ErrCodeNotFound) {
		return Session{}, Account{}, ErrInvalidPairingCode
	}
	if err != nil {
		return Session{}, Account{}, err
	}
	// 先读密码哈希，再确认生成配对码的会话还在。修改或重置密码要是在确认之前提交，那个会话已经被删除。要是在确认之后提交，
	// 签发会话会因为密码哈希变了而失败。
	account, err := a.members.AccountByID(ctx, pairing.MemberID)
	if err != nil {
		return Session{}, Account{}, err
	}
	if _, err := a.members.MemberByToken(ctx, pairing.SessionTokenHash); errors.Is(err, port.ErrMemberNotFound) {
		return Session{}, Account{}, ErrInvalidPairingCode
	} else if err != nil {
		return Session{}, Account{}, err
	}
	session, err := a.signIn(ctx, currentToken, account)
	if errors.Is(err, port.ErrAccountPasswordChanged) {
		return Session{}, Account{}, ErrInvalidPairingCode
	}
	if err != nil {
		return Session{}, Account{}, err
	}
	return session, accountOf(account), nil
}
