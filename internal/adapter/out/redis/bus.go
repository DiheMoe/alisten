package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	goredis "github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/member"
)

var _ port.Bus = (*Bus)(nil)

const (
	membersChannel  = "members"
	preparedChannel = "tracks.prepared"
	roomPrefix      = "room:"
	// 订阅连接空闲这么久发一次 PING，再过这么久仍无回复就换一个新连接：
	// 半开的 TCP 连接读不到任何错误，只能这样发现。
	pingInterval   = 5 * time.Second
	reconnectDelay = time.Second
	// 订阅连接上的写入不使用调用方的期限：go-redis 把写超时当作连接已坏而重建共用的订阅连接，本实例的全部订阅随之中断；
	// 而调用方可能在 go-redis 内部的锁上一直等到自己的期限用完才开始写，例如 go-redis 正持锁重新连接时。
	// go-redis 从真正开始写的时刻按 WriteTimeout 计算写入的期限，这个上限只是兜底，要长于持锁重新连接的时间：
	// 默认的连接、读、写超时各 5 秒，持锁重新连接一次最长约 20–30 秒。
	sharedWriteTimeout = 30 * time.Second
)

var errBusClosed = errors.New("实例间消息的订阅已经关闭")

func instanceChannel(id string) string { return "inst:" + id }
func roomChannel(id string) string     { return roomPrefix + id }

type Bus struct {
	client   *goredis.Client
	instance string
	version  string
	tracer   trace.Tracer
	logger   *slog.Logger
	dropped  *prometheus.CounterVec
	done     chan struct{}

	// 持有 mu 时不调用可能等网络的 go-redis 方法：Receive 处理 PONG、报告中断都要 mu。
	mu       sync.Mutex
	closed   bool
	ps       *goredis.PubSub
	channels map[string]bool
	waiters  map[string]chan struct{}
	tokens   uint64
	// 已换成新的订阅对象，但还没有在上面订阅原来的频道。
	stale bool

	// 以下字段只由 Receive 访问。
	connected bool
	pinged    bool
}

func NewBus(client *goredis.Client, instanceID, version string, metrics prometheus.Registerer, tracer trace.TracerProvider, logger *slog.Logger) *Bus {
	dropped := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "alisten_bus_dropped_total",
		Help: "被丢弃的实例间消息，reason 为 version（构建版本不一致）或 malformed（无法解码）。",
	}, []string{"reason"})
	dropped.WithLabelValues("version")
	dropped.WithLabelValues("malformed")
	metrics.MustRegister(dropped)
	return &Bus{
		client:   client,
		instance: instanceID,
		version:  version,
		tracer:   tracer.Tracer("github.com/dihemoe/alisten/internal/adapter/out/redis"),
		logger:   logger,
		dropped:  dropped,
		done:     make(chan struct{}),
		ps:       client.Subscribe(context.Background()),
		channels: map[string]bool{},
		waiters:  map[string]chan struct{}{},
	}
}

func (b *Bus) SubscribeInstance(ctx context.Context) error {
	return b.subscribe(ctx, instanceChannel(b.instance), membersChannel, preparedChannel)
}

func (b *Bus) SubscribeRoom(ctx context.Context, roomID string) error {
	return b.subscribe(ctx, roomChannel(roomID))
}

// Redis 按顺序处理一个连接上的命令：订阅之后在同一个连接上发一次 PING，收到它的回复时订阅已经生效。
func (b *Bus) subscribe(ctx context.Context, channels ...string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("订阅 %s：%w", strings.Join(channels, "、"), err)
	}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return errBusClosed
	}
	for _, ch := range channels {
		b.channels[ch] = true
	}
	b.tokens++
	token := strconv.FormatUint(b.tokens, 10)
	confirmed := make(chan struct{})
	b.waiters[token] = confirmed
	ps := b.ps
	b.mu.Unlock()
	err := writeShared(ctx, func(w context.Context) error { return ps.Subscribe(w, channels...) })
	if err == nil {
		err = writeShared(ctx, func(w context.Context) error { return ps.Ping(w, token) })
	}
	if err == nil {
		select {
		case <-confirmed:
			return nil
		case <-b.done:
			err = errBusClosed
		case <-ctx.Done():
			err = ctx.Err()
		}
	}
	b.mu.Lock()
	delete(b.waiters, token)
	b.mu.Unlock()
	return fmt.Errorf("订阅 %s：%w", strings.Join(channels, "、"), err)
}

func (b *Bus) UnsubscribeRoom(ctx context.Context, roomID string) error {
	ch := roomChannel(roomID)
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return errBusClosed
	}
	delete(b.channels, ch)
	ps := b.ps
	b.mu.Unlock()
	if err := writeShared(ctx, func(w context.Context) error { return ps.Unsubscribe(w, ch) }); err != nil {
		return fmt.Errorf("退订 %s：%w", ch, err)
	}
	return nil
}

func writeShared(ctx context.Context, write func(context.Context) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	w, cancel := context.WithTimeout(context.WithoutCancel(ctx), sharedWriteTimeout)
	defer cancel()
	// 调用方结束时取消 w：go-redis 只按 ctx 的期限设置读写超时而不理会取消，所以已有连接上的写入不受影响；取消只打断建立连接，
	// 以免调用方持着 go-redis 的锁等到连接超时，拖住排在后面的调用方。
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	return write(w)
}

func (b *Bus) PublishRoom(ctx context.Context, roomID string, m port.RoomMessage) error {
	kind, data, err := encodeRoomMessage(m)
	if err != nil {
		return fmt.Errorf("编码房间 %s 的消息：%w", roomID, err)
	}
	_, err = b.publish(ctx, roomChannel(roomID), kind, data, nil)
	return err
}

func (b *Bus) SendInput(ctx context.Context, instanceID string, in port.RoomInput) (bool, error) {
	data, err := encodeInput(in)
	if err != nil {
		return false, fmt.Errorf("编码房间 %s 的输入：%w", in.Room, err)
	}
	carrier := propagation.MapCarrier{}
	propagation.TraceContext{}.Inject(ctx, carrier)
	receivers, err := b.publish(ctx, instanceChannel(instanceID), kindInput, data, carrier)
	return receivers > 0, err
}

func (b *Bus) PublishMember(ctx context.Context, m member.Member) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	_, err = b.publish(ctx, membersChannel, kindMember, data, nil)
	return err
}

func (b *Bus) PublishPrepared(ctx context.Context, o port.PrepareOutcome) error {
	data, err := json.Marshal(o)
	if err != nil {
		return err
	}
	_, err = b.publish(ctx, preparedChannel, kindPrepared, data, nil)
	return err
}

func (b *Bus) publish(ctx context.Context, channel, kind string, data json.RawMessage, carrier map[string]string) (int64, error) {
	payload, err := json.Marshal(envelope{Version: b.version, From: b.instance, Kind: kind, Trace: carrier, Data: data})
	if err != nil {
		return 0, err
	}
	receivers, err := b.client.Publish(ctx, channel, payload).Result()
	if err != nil {
		return 0, fmt.Errorf("发布到 %s：%w", channel, err)
	}
	return receivers, nil
}

// 连接出错时 go-redis 会自行重新连接并重新订阅全部频道，但断开期间的消息已经丢失；连接只是停止响应时 go-redis 无法察觉，
// 由这里换一个新连接。两种情况都在恢复订阅之前先报告 SubscriptionLost，让调用方尽早释放房间、断开会话。
func (b *Bus) Receive(ctx context.Context) (port.Delivery, error) {
	stop := context.AfterFunc(ctx, b.close)
	defer stop()
	for {
		b.mu.Lock()
		ps, closed := b.ps, b.closed
		var channels []string
		if b.stale {
			channels, b.stale = slices.Collect(maps.Keys(b.channels)), false
		}
		b.mu.Unlock()
		if closed {
			return nil, errors.Join(errBusClosed, ctx.Err())
		}
		if len(channels) > 0 {
			// 即使连不上，go-redis 也会记下这些频道，在连接建立时一并订阅。
			_ = ps.Subscribe(ctx, channels...)
		}
		msg, err := ps.ReceiveTimeout(ctx, pingInterval)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if timeout(err) && !b.pinged {
				b.pinged = true
				_ = ps.Ping(ctx)
				continue
			}
			if timeout(err) {
				b.replace(ps)
			}
			lost := b.connected
			b.connected, b.pinged = false, false
			if lost {
				return port.SubscriptionLost{}, nil
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(reconnectDelay):
			}
			continue
		}
		b.pinged = false
		if !b.connected {
			b.connected = true
			b.repingWaiters(ctx, ps)
		}
		switch msg := msg.(type) {
		case *goredis.Message:
			if d, ok := b.decode(ctx, msg); ok {
				return d, nil
			}
		case *goredis.Pong:
			b.confirm(msg.Payload)
		}
	}
}

func timeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// 这里不做 I/O：先报告 SubscriptionLost，下一次接收时再连接并订阅原来的频道，停止响应的服务端不会拖延报告。
func (b *Bus) replace(old *goredis.PubSub) {
	b.mu.Lock()
	if b.closed || b.ps != old {
		b.mu.Unlock()
		return
	}
	b.ps = b.client.Subscribe(context.Background())
	b.stale = true
	b.mu.Unlock()
	// Close 要等 old 内部的锁，而持有这把锁的写入可能正在等网络。
	go func() { _ = old.Close() }()
}

// 新连接建立时已经重新订阅了全部频道，之前发出的 PING 都随旧连接丢失了。
func (b *Bus) repingWaiters(ctx context.Context, ps *goredis.PubSub) {
	b.mu.Lock()
	tokens := slices.Collect(maps.Keys(b.waiters))
	b.mu.Unlock()
	for _, token := range tokens {
		_ = ps.Ping(ctx, token)
	}
}

func (b *Bus) confirm(token string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if confirmed, ok := b.waiters[token]; ok {
		close(confirmed)
		delete(b.waiters, token)
	}
}

func (b *Bus) close() {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.closed = true
	close(b.done)
	ps := b.ps
	b.mu.Unlock()
	_ = ps.Close()
}

func (b *Bus) decode(ctx context.Context, msg *goredis.Message) (port.Delivery, bool) {
	var e envelope
	if err := json.Unmarshal([]byte(msg.Payload), &e); err != nil {
		b.malformed(ctx, msg.Channel, err)
		return nil, false
	}
	if e.Version != b.version {
		b.dropped.WithLabelValues("version").Inc()
		b.logger.ErrorContext(ctx, "丢弃构建版本不同的实例间消息", "channel", msg.Channel, "from", e.From, "remote_version", e.Version)
		return nil, false
	}
	d, err := b.delivery(msg.Channel, e)
	if err != nil {
		b.malformed(ctx, msg.Channel, err)
		return nil, false
	}
	return d, true
}

func (b *Bus) delivery(channel string, e envelope) (port.Delivery, error) {
	switch {
	case channel == instanceChannel(b.instance) && e.Kind == kindInput:
		in, err := decodeInput(e.Data)
		if err != nil {
			return nil, err
		}
		return port.InputDelivery{Context: b.received(e.Trace, in.Room), Input: in}, nil
	case channel == membersChannel && e.Kind == kindMember:
		var m member.Member
		err := json.Unmarshal(e.Data, &m)
		return port.MemberDelivery{Member: m}, err
	case channel == preparedChannel && e.Kind == kindPrepared:
		var o port.PrepareOutcome
		err := json.Unmarshal(e.Data, &o)
		return port.PreparedDelivery{Outcome: o}, err
	case strings.HasPrefix(channel, roomPrefix):
		m, err := decodeRoomMessage(e.Kind, e.Data)
		return port.RoomDelivery{Room: strings.TrimPrefix(channel, roomPrefix), Message: m}, err
	}
	return nil, fmt.Errorf("频道 %s 上不应有 %s 消息", channel, e.Kind)
}

// 带有追踪上下文的输入延续网关的 trace：记录一个 consumer span，owner 处理这条输入时的调用都在它之下。
// 这个 span 只标记收到输入的时刻，处理的耗时由它之下的调用体现。
func (b *Bus) received(carrier map[string]string, roomID string) context.Context {
	ctx := propagation.TraceContext{}.Extract(context.Background(), propagation.MapCarrier(carrier))
	if !trace.SpanContextFromContext(ctx).IsValid() {
		return ctx
	}
	ctx, span := b.tracer.Start(ctx, "receive room input", trace.WithSpanKind(trace.SpanKindConsumer), trace.WithAttributes(
		semconv.MessagingSystemKey.String("redis"),
		semconv.MessagingOperationTypeReceive,
		attribute.String("alisten.room_id", roomID),
	))
	span.End()
	return ctx
}

func (b *Bus) malformed(ctx context.Context, channel string, err error) {
	b.dropped.WithLabelValues("malformed").Inc()
	b.logger.ErrorContext(ctx, "丢弃无法解码的实例间消息", "channel", channel, "error", err)
}
