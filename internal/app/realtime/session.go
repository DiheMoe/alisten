package realtime

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/dihemoe/alisten/internal/domain/member"
	"github.com/dihemoe/alisten/internal/domain/room"
)

const (
	sendQueueLimit = 256
	requestLimit   = 16
)

type CloseReason uint8

const (
	CloseRestart CloseReason = iota + 1
	CloseRevoked
	CloseRoomGone
	CloseOverflow
	// 只用于事件流：名额用完时让给同一凭证新打开的事件流。
	CloseReplaced
)

type Message interface{ message() }

type (
	// 握手的结果，总是会话收到的第一条消息。
	Hello struct {
		Member member.Member
		View   room.View
	}

	// 事件流收到的房间快照：总是第一条消息，房间开始新的一次运行之后再收到一份。
	Snapshot struct {
		View room.View
	}

	Event struct {
		Emit room.Emit
	}

	// Tag 是发出请求时交给网关的值，原样带回，用来对应请求。
	Reply struct {
		Tag    any
		Result room.Result
		Err    error
	}

	Answer struct {
		Tag  any
		Data any
	}
)

func (Hello) message()    {}
func (Snapshot) message() {}
func (Event) message()    {}
func (Reply) message()    {}
func (Answer) message()   {}

type sendQueue struct {
	ctx    context.Context
	cancel context.CancelFunc
	wake   chan struct{}

	mu         sync.Mutex
	subscribed bool
	queue      []Message
	reason     CloseReason
}

func newSendQueue() *sendQueue {
	ctx, cancel := context.WithCancel(context.Background())
	return &sendQueue{ctx: ctx, cancel: cancel, wake: make(chan struct{}, 1)}
}

type Session struct {
	*sendQueue
	id   room.SessionID
	room string
	link *roomLink
	// 在发出 Leave 之前设置。重新加入的 Join 可能排在 Leave 之后，网关据此补发 Leave。
	left atomic.Bool

	// 由 sendQueue 的 mu 保护。
	member      member.Member
	outstanding int
}

func newSession(id room.SessionID, roomID string, m member.Member) *Session {
	return &Session{sendQueue: newSendQueue(), id: id, room: roomID, member: m}
}

func (s *Session) ended() bool {
	return s.left.Load() || s.ctx.Err() != nil
}

func (s *Session) Member() member.Member {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.member
}

func (s *Session) Room() string {
	return s.room
}

// Context 在关闭时取消。会话向客户端写消息时以它为期限：客户端不再读取时，关闭会话也要能中止阻塞的写入。
func (q *sendQueue) Context() context.Context {
	return q.ctx
}

// 关闭之后返回关闭的原因，队列中剩下的消息不再发送。
func (q *sendQueue) Next(ctx context.Context) (Message, CloseReason, error) {
	for {
		q.mu.Lock()
		if q.reason != 0 {
			reason := q.reason
			q.mu.Unlock()
			return nil, reason, nil
		}
		if len(q.queue) > 0 {
			m := q.queue[0]
			q.queue = q.queue[1:]
			q.mu.Unlock()
			return m, 0, nil
		}
		q.mu.Unlock()
		select {
		case <-q.wake:
		case <-ctx.Done():
			return nil, 0, ctx.Err()
		}
	}
}

// 请求的回复放进发送队列时就归还名额，所以客户端收到回复之后总能立即发出下一个请求。
func (s *Session) Begin() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.outstanding == requestLimit {
		return false
	}
	s.outstanding++
	return true
}

func (s *Session) Fail(tag any, err error) {
	s.reply(tag, nil, err)
}

func (s *Session) Answer(tag, data any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.outstanding--
	s.enqueue(Answer{Tag: tag, Data: data})
}

func (s *Session) reply(tag any, res room.Result, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.outstanding--
	s.enqueue(Reply{Tag: tag, Result: res, Err: err})
}

// 在接收消息的 goroutine 上按房间频道的顺序调用：此后房间的事件才发给这个会话，快照之前的事件都已包含在快照中。
func (s *Session) welcome(v room.View) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.subscribed = true
	s.enqueue(Hello{Member: s.member, View: v})
}

func (s *Session) update(m member.Member) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.member.ID != m.ID {
		return false
	}
	s.member = m
	return true
}

func (q *sendQueue) event(e room.Emit) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.subscribed {
		q.enqueue(Event{Emit: e})
	}
}

func (q *sendQueue) close(reason CloseReason) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closeLocked(reason)
}

// 调用方持有 q.mu。
func (q *sendQueue) enqueue(m Message) {
	if q.reason != 0 {
		return
	}
	if len(q.queue) == sendQueueLimit {
		q.closeLocked(CloseOverflow)
		return
	}
	q.queue = append(q.queue, m)
	q.signal()
}

func (q *sendQueue) closeLocked(reason CloseReason) {
	if q.reason != 0 {
		return
	}
	q.reason = reason
	q.queue = nil
	q.cancel()
	q.signal()
}

func (q *sendQueue) signal() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}
