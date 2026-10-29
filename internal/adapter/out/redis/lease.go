package redis

import (
	"context"
	"fmt"
	"strconv"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/dihemoe/alisten/internal/app/port"
)

var (
	_ port.Leases       = (*Leases)(nil)
	_ port.OnlineCounts = (*Leases)(nil)
)

type Leases struct {
	client   *goredis.Client
	instance string
}

func NewLeases(client *goredis.Client, instanceID string) *Leases {
	return &Leases{client: client, instance: instanceID}
}

func ownerKey(roomID string) string  { return "room:" + roomID + ":owner" }
func onlineKey(roomID string) string { return "room:" + roomID + ":online" }

// 比较租约的值和写入在同一个脚本中完成，其间不会插入其他实例的命令。
var (
	acquireScript = goredis.NewScript(`
local owner = redis.call('GET', KEYS[1])
if owner and owner ~= ARGV[1] then
	return 0
end
redis.call('SET', KEYS[1], ARGV[1], 'PX', ARGV[2])
redis.call('SET', KEYS[2], 0, 'PX', ARGV[2])
return 1`)

	renewScript = goredis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then
	return 0
end
redis.call('PEXPIRE', KEYS[1], ARGV[2])
redis.call('PEXPIRE', KEYS[2], ARGV[2])
return 1`)

	setOnlineScript = goredis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then
	return 0
end
redis.call('SET', KEYS[2], ARGV[2], 'PX', redis.call('PTTL', KEYS[1]))
return 1`)

	releaseScript = goredis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
	redis.call('DEL', KEYS[1], KEYS[2])
end
return 0`)
)

func (l *Leases) Acquire(ctx context.Context, roomID string, ttl time.Duration) (bool, error) {
	n, err := acquireScript.Run(ctx, l.client, []string{ownerKey(roomID), onlineKey(roomID)}, l.instance, ttl.Milliseconds()).Int()
	if err != nil {
		return false, fmt.Errorf("取得房间 %s 的租约：%w", roomID, err)
	}
	return n == 1, nil
}

func (l *Leases) Renew(ctx context.Context, roomID string, ttl time.Duration) (bool, error) {
	n, err := renewScript.Run(ctx, l.client, []string{ownerKey(roomID), onlineKey(roomID)}, l.instance, ttl.Milliseconds()).Int()
	if err != nil {
		return false, fmt.Errorf("续期房间 %s 的租约：%w", roomID, err)
	}
	return n == 1, nil
}

func (l *Leases) SetOnline(ctx context.Context, roomID string, online int) (bool, error) {
	n, err := setOnlineScript.Run(ctx, l.client, []string{ownerKey(roomID), onlineKey(roomID)}, l.instance, online).Int()
	if err != nil {
		return false, fmt.Errorf("写入房间 %s 的在线人数：%w", roomID, err)
	}
	return n == 1, nil
}

func (l *Leases) Release(ctx context.Context, roomID string) error {
	if err := releaseScript.Run(ctx, l.client, []string{ownerKey(roomID), onlineKey(roomID)}, l.instance).Err(); err != nil {
		return fmt.Errorf("释放房间 %s 的租约：%w", roomID, err)
	}
	return nil
}

func (l *Leases) Owners(ctx context.Context, roomIDs []string) ([]string, error) {
	values, err := mget(ctx, l.client, roomIDs, ownerKey)
	if err != nil {
		return nil, fmt.Errorf("读取房间的 owner：%w", err)
	}
	owners := make([]string, len(values))
	for i, v := range values {
		owners[i], _ = v.(string)
	}
	return owners, nil
}

func (l *Leases) OnlineCounts(ctx context.Context, roomIDs []string) ([]int, error) {
	values, err := mget(ctx, l.client, roomIDs, onlineKey)
	if err != nil {
		return nil, fmt.Errorf("读取房间的在线人数：%w", err)
	}
	counts := make([]int, len(values))
	for i, v := range values {
		s, ok := v.(string)
		if !ok {
			continue
		}
		if counts[i], err = strconv.Atoi(s); err != nil {
			return nil, fmt.Errorf("房间 %s 的在线人数 %q 不是整数", roomIDs[i], s)
		}
	}
	return counts, nil
}

// MGET 对不存在的键返回 nil，并且不接受零个键。
func mget(ctx context.Context, client *goredis.Client, ids []string, key func(string) string) ([]any, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	keys := make([]string, len(ids))
	for i, id := range ids {
		keys[i] = key(id)
	}
	return client.MGet(ctx, keys...).Result()
}
