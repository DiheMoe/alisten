package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"runtime/debug"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/app/realtime"
	"github.com/dihemoe/alisten/internal/lib/logctx"
	"github.com/dihemoe/alisten/protocol"
)

const (
	helloTimeout = 10 * time.Second
	idleTimeout  = 30 * time.Second
)

var errClientOutdated = errors.New("前端与服务端的协议版本不一致，请刷新页面")

var handshakeCloseCodes = map[protocol.ErrorCode]websocket.StatusCode{
	protocol.ErrorCodeInvalidArgument: protocol.CloseInvalidArgument,
	protocol.ErrorCodeInternal:        protocol.CloseInternal,
	protocol.ErrorCodeUnavailable:     protocol.CloseUnavailable,
	protocol.ErrorCodeUnauthorized:    protocol.CloseUnauthorized,
	protocol.ErrorCodeNotFound:        protocol.CloseNotFound,
	protocol.ErrorCodeClientOutdated:  protocol.CloseClientOutdated,
}

var sessionCloseCodes = map[realtime.CloseReason]websocket.StatusCode{
	realtime.CloseRestart:  protocol.CloseServiceRestart,
	realtime.CloseRevoked:  protocol.CloseUnauthorized,
	realtime.CloseRoomGone: protocol.CloseNotFound,
	realtime.CloseOverflow: protocol.CloseSendQueueFull,
}

type requestTag struct {
	typ string
	id  string
}

func (s *server) websocket(w http.ResponseWriter, r *http.Request) {
	var lastFrame atomic.Int64
	touch := func() { lastFrame.Store(time.Now().UnixNano()) }
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// Accept 默认要求 Origin 与 Host 头一致，而经过入口转发时 Host 不一定与浏览器页面的 Origin 相同。
		InsecureSkipVerify: true,
		OnPingReceived: func(context.Context, []byte) bool {
			touch()
			return true
		},
		OnPongReceived: func(context.Context, []byte) { touch() },
	})
	if err != nil {
		return
	}
	defer func() { _ = conn.CloseNow() }()
	conn.SetReadLimit(maxBodyBytes)
	// 连接被 Hijack 之后，请求的 context 不再反映连接的状态。
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 超时时先以 4408 关闭连接，再取消 ctx：如果先取消，正在读取第一帧的 Read 会直接断开连接，
	// 客户端收不到 4408。
	timeout := time.AfterFunc(helloTimeout, func() {
		_ = conn.Close(protocol.CloseHandshakeTimeout, "")
		cancel()
	})
	sess, helloID := s.handshake(ctx, conn)
	if !timeout.Stop() {
		if sess != nil {
			s.Gateway.Leave(sess)
		}
		return
	}
	if sess == nil {
		return
	}
	defer s.Gateway.Leave(sess)
	ctx = logctx.WithMember(logctx.WithRoom(ctx, sess.Room()), sess.Member().ID)

	touch()
	go watchIdle(ctx, conn, &lastFrame)
	go s.write(ctx, conn, sess, helloID)
	for {
		typ, frame, err := conn.Read(ctx)
		if err != nil {
			return
		}
		touch()
		s.dispatch(ctx, conn, sess, typ, frame)
	}
}

func (s *server) handshake(ctx context.Context, conn *websocket.Conn) (*realtime.Session, string) {
	typ, frame, err := conn.Read(ctx)
	if err != nil {
		return nil, ""
	}
	// span 在读到第一帧之后才开始，等待客户端发送的时间不计入。
	ctx, span := s.tracer.Start(ctx, "WS hello", trace.WithSpanKind(trace.SpanKindServer))
	defer span.End()
	req, err := decodeFrame(typ, frame)
	if err == nil {
		var sess *realtime.Session
		if sess, err = s.hello(ctx, req); err == nil {
			span.SetAttributes(attribute.String("alisten.room_id", sess.Room()))
			return sess, req.ID
		}
	}
	info := s.requestError(ctx, req.Type, err)
	s.reject(ctx, conn, req, info)
	_ = conn.Close(handshakeCloseCodes[info.Code], "")
	return nil, ""
}

func (s *server) hello(ctx context.Context, req protocol.Request) (*realtime.Session, error) {
	if req.Type != protocol.RequestHello {
		return nil, &protocol.ArgumentError{Reason: "第一帧必须是 hello"}
	}
	hello := req.Data.(protocol.HelloRequest)
	if hello.ProtocolVersion != protocol.Version {
		return nil, errClientOutdated
	}
	// 不是 UUID 的值会被当作别名查找。
	if !uuidPattern.MatchString(hello.RoomID) {
		return nil, port.ErrRoomNotFound
	}
	return s.Identity.EnterRoom(ctx, hello.RoomID, hello.Ticket)
}

func decodeFrame(typ websocket.MessageType, frame []byte) (protocol.Request, error) {
	if typ != websocket.MessageText || !utf8.Valid(frame) {
		return protocol.Request{}, &protocol.ArgumentError{Reason: "只接受 UTF-8 编码的 JSON 文本帧"}
	}
	return protocol.DecodeRequest(frame)
}

func (s *server) dispatch(ctx context.Context, conn *websocket.Conn, sess *realtime.Session, typ websocket.MessageType, frame []byte) {
	req, err := decodeFrame(typ, frame)
	if err == nil && req.Type == protocol.RequestHello {
		err = &protocol.ArgumentError{Reason: "已经完成握手"}
	}
	if err != nil {
		s.reject(sess.Context(), conn, req, s.requestError(ctx, req.Type, err))
		return
	}
	if req.Type == protocol.RequestPing {
		ping := req.Data.(protocol.PingRequest)
		s.writeMessage(sess.Context(), conn, protocol.Response(req.Type, req.ID, protocol.PingResponse{ClientTimeMs: ping.ClientTimeMs, ServerTimeMs: time.Now().UnixMilli()}))
		return
	}
	if !sess.Begin() {
		s.commands.WithLabelValues(req.Type, string(protocol.ErrorCodeRateLimited)).Inc()
		s.reject(sess.Context(), conn, req, protocol.ErrorInfo{Code: protocol.ErrorCodeRateLimited, Message: "操作太快了，请稍后再试。"})
		return
	}
	go s.command(ctx, sess, req)
}

func (s *server) command(ctx context.Context, sess *realtime.Session, req protocol.Request) {
	ctx, cancel := context.WithTimeout(ctx, requestDeadline)
	defer cancel()
	ctx, span := s.tracer.Start(ctx, "WS "+req.Type, trace.WithSpanKind(trace.SpanKindServer), trace.WithAttributes(attribute.String("alisten.room_id", sess.Room())))
	defer span.End()
	tag := requestTag{typ: req.Type, id: req.ID}
	// 这个 goroutine 不在 net/http 的恢复范围之内，panic 会让进程退出，本实例上的房间随之全部停止。
	defer func() {
		if value := recover(); value != nil {
			s.logger.ErrorContext(ctx, "处理 WebSocket 请求时发生 panic", "panic", fmt.Sprint(value), "type", req.Type, "stack", string(debug.Stack()))
			s.requestError(ctx, req.Type, errPanicked)
			sess.Fail(tag, errPanicked)
		}
	}()
	if err := s.perform(ctx, sess, tag, req.Data); err != nil {
		s.requestError(ctx, req.Type, err)
	}
}

func (s *server) perform(ctx context.Context, sess *realtime.Session, tag requestTag, data any) error {
	if resp, handled, err := s.answer(ctx, sess.Room(), data); handled {
		if err != nil {
			sess.Fail(tag, err)
		} else {
			sess.Answer(tag, resp)
		}
		return err
	}
	// 握手拒绝了 API Key，会话的成员不是 bot，房间命令总以会话的成员发出。
	a, _, err := s.action(ctx, sess.Member(), false, data)
	if err != nil {
		sess.Fail(tag, err)
		return err
	}
	return s.Gateway.Command(ctx, sess, tag, a)
}

func (s *server) write(ctx context.Context, conn *websocket.Conn, sess *realtime.Session, helloID string) {
	for {
		m, reason, err := sess.Next(ctx)
		if err != nil {
			return
		}
		if reason != 0 {
			_ = conn.Close(sessionCloseCodes[reason], "")
			return
		}
		// 写入以会话的 ctx 为期限：客户端不再读取、发送队列溢出时，阻塞的写入随会话关闭而中止。
		if !s.writeMessage(sess.Context(), conn, s.message(m, helloID)) {
			return
		}
	}
}

func (s *server) message(m realtime.Message, helloID string) protocol.Message {
	switch m := m.(type) {
	case realtime.Hello:
		return protocol.Response(protocol.RequestHello, helloID, protocol.HelloResponse{Member: memberDTO(m.Member), ServerTimeMs: time.Now().UnixMilli(), Room: s.snapshot(m.View)})
	case realtime.Event:
		typ, data := s.event(m.Emit.Event)
		return protocol.RoomEvent(typ, int64(m.Emit.Term), int64(m.Emit.Seq), data)
	case realtime.Reply:
		// 按写给客户端的回复计数，会话关闭时没有写出的回复不计。意外的错误已在处理请求时记录。
		tag := m.Tag.(requestTag)
		s.count(tag.typ, m.Err)
		if m.Err != nil {
			info, _ := errorInfo(m.Err)
			return protocol.Failure(tag.typ, tag.id, info)
		}
		return protocol.Response(tag.typ, tag.id, s.result(m.Result))
	case realtime.Answer:
		tag := m.Tag.(requestTag)
		s.count(tag.typ, nil)
		return protocol.Response(tag.typ, tag.id, m.Data)
	}
	panic(fmt.Sprintf("未知的会话消息 %T", m))
}

// 错误码对应 5xx 时把请求的 span 标为出错，与 HTTP 请求的 span 一致。
// 会话结束时被取消的请求不记录日志，也不标为出错。
func (s *server) requestError(ctx context.Context, typ string, err error) protocol.ErrorInfo {
	info, expected := errorInfo(err)
	if errors.Is(err, context.Canceled) {
		return info
	}
	if !expected {
		s.logger.ErrorContext(withRequestID(ctx, err), "处理 WebSocket 请求失败", "type", typ, "error", err)
	}
	if info.Code.HTTPStatus() >= http.StatusInternalServerError {
		trace.SpanFromContext(ctx).SetStatus(codes.Error, string(info.Code))
	}
	return info
}

func (s *server) reject(ctx context.Context, conn *websocket.Conn, req protocol.Request, info protocol.ErrorInfo) {
	if req.ID == "" {
		s.writeMessage(ctx, conn, protocol.ErrorEvent(info))
		return
	}
	s.writeMessage(ctx, conn, protocol.Failure(req.Type, req.ID, info))
}

func (s *server) writeMessage(ctx context.Context, conn *websocket.Conn, m protocol.Message) bool {
	frame, err := json.Marshal(m)
	if err != nil {
		s.logger.ErrorContext(ctx, "编码 WebSocket 消息失败", "type", m.Type, "error", err)
		return false
	}
	return conn.Write(ctx, websocket.MessageText, frame) == nil
}

func watchIdle(ctx context.Context, conn *websocket.Conn, lastFrame *atomic.Int64) {
	timer := time.NewTimer(idleTimeout)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			idle := time.Since(time.Unix(0, lastFrame.Load()))
			if idle >= idleTimeout {
				_ = conn.CloseNow()
				return
			}
			timer.Reset(idleTimeout - idle)
		}
	}
}
