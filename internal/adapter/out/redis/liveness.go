package redis

import (
	"context"
	"errors"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/dihemoe/alisten/internal/app/port"
)

var _ port.Liveness = (*Liveness)(nil)

type Liveness struct {
	client   *goredis.Client
	instance string
}

func NewLiveness(client *goredis.Client, instanceID string) *Liveness {
	return &Liveness{client: client, instance: instanceID}
}

func aliveKey(instanceID string) string { return "inst:" + instanceID + ":alive" }

func (l *Liveness) Heartbeat(ctx context.Context, ttl time.Duration) (bool, error) {
	err := l.client.SetArgs(ctx, aliveKey(l.instance), 1, goredis.SetArgs{TTL: ttl, Get: true}).Err()
	if errors.Is(err, goredis.Nil) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("刷新实例的存活键：%w", err)
	}
	return true, nil
}

func (l *Liveness) Expired(ctx context.Context, instanceIDs []string) ([]string, error) {
	values, err := mget(ctx, l.client, instanceIDs, aliveKey)
	if err != nil {
		return nil, fmt.Errorf("读取实例的存活键：%w", err)
	}
	var expired []string
	for i, v := range values {
		if v == nil {
			expired = append(expired, instanceIDs[i])
		}
	}
	return expired, nil
}
