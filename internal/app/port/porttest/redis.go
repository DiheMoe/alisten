package porttest

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/member"
)

var ErrRedisDown = errors.New("Redis 不可达")

// 时间取自 time.Now，可以在 synctest 中使用。
// 同一个频道上的消息按发布的顺序送达每个订阅方，包括发布方自己。
type Redis struct {
	mu     sync.Mutex
	leases map[string]lease
	alive  map[string]time.Time
	subs   map[string]map[*Node]struct{}
}

type lease struct {
	owner   string
	online  int
	expires time.Time
}

func NewRedis() *Redis {
	return &Redis{leases: map[string]lease{}, alive: map[string]time.Time{}, subs: map[string]map[*Node]struct{}{}}
}

// Node 是一个实例所见的 Redis。
type Node struct {
	redis    *Redis
	instance string
	wake     chan struct{}

	// 以下字段由 redis.mu 保护。
	down  bool
	queue []port.Delivery
}

var (
	_ port.Leases       = (*Node)(nil)
	_ port.OnlineCounts = (*Node)(nil)
	_ port.Liveness     = (*Node)(nil)
	_ port.Bus          = (*Node)(nil)
)

func (r *Redis) Node(instanceID string) *Node {
	return &Node{redis: r, instance: instanceID, wake: make(chan struct{}, 1)}
}

// Flush 模拟 Redis 被清空：租约、在线人数和存活键都丢失，订阅不受影响。
func (r *Redis) Flush() {
	r.mu.Lock()
	defer r.mu.Unlock()
	clear(r.leases)
	clear(r.alive)
}

// SetDown 模拟本实例连不上 Redis：之后的操作都返回 ErrRedisDown；订阅连接随之断开，下一条收到的消息是
// SubscriptionLost。恢复之后仍然订阅着原来的频道，断开期间发布的消息不会补回。
func (n *Node) SetDown(down bool) {
	r := n.redis
	r.mu.Lock()
	defer r.mu.Unlock()
	if down && !n.down {
		n.lose()
	}
	n.down = down
}

// Disconnect 模拟订阅连接短暂断开：还没有收到的消息丢失，下一条收到的消息是 SubscriptionLost。
func (n *Node) Disconnect() {
	n.redis.mu.Lock()
	defer n.redis.mu.Unlock()
	n.lose()
}

// 调用方持有 redis.mu。
func (n *Node) lose() {
	n.queue = []port.Delivery{port.SubscriptionLost{}}
	n.signal()
}

func (n *Node) signal() {
	select {
	case n.wake <- struct{}{}:
	default:
	}
}

// 调用方持有 redis.mu。
func (n *Node) check() error {
	if n.down {
		return ErrRedisDown
	}
	return nil
}

func valid(l lease) bool {
	return l.owner != "" && time.Now().Before(l.expires)
}

func (n *Node) Acquire(_ context.Context, roomID string, ttl time.Duration) (bool, error) {
	r := n.redis
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := n.check(); err != nil {
		return false, err
	}
	if l := r.leases[roomID]; valid(l) && l.owner != n.instance {
		return false, nil
	}
	r.leases[roomID] = lease{owner: n.instance, expires: time.Now().Add(ttl)}
	return true, nil
}

func (n *Node) Renew(_ context.Context, roomID string, ttl time.Duration) (bool, error) {
	r := n.redis
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := n.check(); err != nil {
		return false, err
	}
	l := r.leases[roomID]
	if !valid(l) || l.owner != n.instance {
		return false, nil
	}
	l.expires = time.Now().Add(ttl)
	r.leases[roomID] = l
	return true, nil
}

func (n *Node) SetOnline(_ context.Context, roomID string, online int) (bool, error) {
	r := n.redis
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := n.check(); err != nil {
		return false, err
	}
	l := r.leases[roomID]
	if !valid(l) || l.owner != n.instance {
		return false, nil
	}
	l.online = online
	r.leases[roomID] = l
	return true, nil
}

func (n *Node) Release(_ context.Context, roomID string) error {
	r := n.redis
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := n.check(); err != nil {
		return err
	}
	if l := r.leases[roomID]; valid(l) && l.owner == n.instance {
		delete(r.leases, roomID)
	}
	return nil
}

func (n *Node) Owners(_ context.Context, roomIDs []string) ([]string, error) {
	r := n.redis
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := n.check(); err != nil {
		return nil, err
	}
	owners := make([]string, len(roomIDs))
	for i, id := range roomIDs {
		if l := r.leases[id]; valid(l) {
			owners[i] = l.owner
		}
	}
	return owners, nil
}

func (n *Node) OnlineCounts(_ context.Context, roomIDs []string) ([]int, error) {
	r := n.redis
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := n.check(); err != nil {
		return nil, err
	}
	counts := make([]int, len(roomIDs))
	for i, id := range roomIDs {
		if l := r.leases[id]; valid(l) {
			counts[i] = l.online
		}
	}
	return counts, nil
}

func (n *Node) Heartbeat(_ context.Context, ttl time.Duration) (bool, error) {
	r := n.redis
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := n.check(); err != nil {
		return false, err
	}
	existed := time.Now().Before(r.alive[n.instance])
	r.alive[n.instance] = time.Now().Add(ttl)
	return existed, nil
}

func (n *Node) Expired(_ context.Context, instanceIDs []string) ([]string, error) {
	r := n.redis
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := n.check(); err != nil {
		return nil, err
	}
	var expired []string
	for _, id := range instanceIDs {
		if !time.Now().Before(r.alive[id]) {
			expired = append(expired, id)
		}
	}
	return expired, nil
}

func (n *Node) SubscribeInstance(context.Context) error {
	return n.subscribe("inst:"+n.instance, "members", "tracks.prepared")
}

func (n *Node) SubscribeRoom(_ context.Context, roomID string) error {
	return n.subscribe("room:" + roomID)
}

func (n *Node) subscribe(channels ...string) error {
	r := n.redis
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := n.check(); err != nil {
		return err
	}
	for _, ch := range channels {
		if r.subs[ch] == nil {
			r.subs[ch] = map[*Node]struct{}{}
		}
		r.subs[ch][n] = struct{}{}
	}
	return nil
}

func (n *Node) UnsubscribeRoom(_ context.Context, roomID string) error {
	r := n.redis
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := n.check(); err != nil {
		return err
	}
	delete(r.subs["room:"+roomID], n)
	return nil
}

func (n *Node) Subscribed(roomID string) bool {
	r := n.redis
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.subs["room:"+roomID][n]
	return ok
}

func (n *Node) PublishRoom(_ context.Context, roomID string, m port.RoomMessage) error {
	_, err := n.publish("room:"+roomID, port.RoomDelivery{Room: roomID, Message: m})
	return err
}

func (n *Node) SendInput(_ context.Context, instanceID string, in port.RoomInput) (bool, error) {
	receivers, err := n.publish("inst:"+instanceID, port.InputDelivery{Context: context.Background(), Input: in})
	return receivers > 0, err
}

func (n *Node) PublishMember(_ context.Context, m member.Member) error {
	_, err := n.publish("members", port.MemberDelivery{Member: m})
	return err
}

func (n *Node) PublishPrepared(_ context.Context, o port.PrepareOutcome) error {
	_, err := n.publish("tracks.prepared", port.PreparedDelivery{Outcome: o})
	return err
}

// 连不上 Redis 的实例没有订阅连接，收不到消息。
func (n *Node) publish(channel string, d port.Delivery) (int, error) {
	r := n.redis
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := n.check(); err != nil {
		return 0, err
	}
	receivers := 0
	for s := range r.subs[channel] {
		if !s.down {
			s.queue = append(s.queue, d)
			s.signal()
			receivers++
		}
	}
	return receivers, nil
}

func (n *Node) Receive(ctx context.Context) (port.Delivery, error) {
	r := n.redis
	for {
		r.mu.Lock()
		if len(n.queue) > 0 {
			d := n.queue[0]
			n.queue = n.queue[1:]
			r.mu.Unlock()
			return d, nil
		}
		r.mu.Unlock()
		select {
		case <-n.wake:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}
