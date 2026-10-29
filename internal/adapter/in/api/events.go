package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/dihemoe/alisten/internal/app/realtime"
	"github.com/dihemoe/alisten/protocol"
)

const (
	// 小于入口的读超时：nginx 默认 60 秒收不到数据就断开。
	streamHeartbeat = 25 * time.Second
	// 客户端不再读取时，写入在这个期限之后失败，事件流随之结束：否则关闭事件流（包括退出时）要一直等阻塞的写入。
	streamWriteTimeout = 10 * time.Second
)

var streamCloses = map[realtime.CloseReason]streamClose{
	realtime.CloseRestart:  {Code: protocol.CloseServiceRestart, Message: "服务端实例退出或订阅中断，请重新连接"},
	realtime.CloseRevoked:  {Code: protocol.CloseUnauthorized, Message: "凭证已失效：房间设置或修改了密码，或者 API Key 已重置"},
	realtime.CloseRoomGone: {Code: protocol.CloseNotFound, Message: "房间已关闭或被删除"},
	realtime.CloseOverflow: {Code: protocol.CloseSendQueueFull, Message: "积压的事件过多，请重新连接"},
	// 不是 1012：共用一把 Key 的两个 bot 看到它就该停下，而不是重连之后互相顶掉。
	realtime.CloseReplaced: {Code: 4409, Message: "同一凭证打开了新的事件流"},
}

type streamClose struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// 房间的事件流：先发 snapshot，data 与 room.sync 的响应相同；之后是房间事件，data 与 WebSocket 上的报文相同；
// 结束之前发 close，code 沿用 WebSocket 的关闭码，让给同一凭证的新事件流时是 4409。每次连接都从新快照开始，
// 不按 Last-Event-ID 补发断开期间的事件。
func (s *server) roomEvents(w http.ResponseWriter, r *http.Request) {
	// 打开事件流的期限与 WebSocket 的握手相同，打开之后的推送不设期限。
	setup, cancel := context.WithTimeout(r.Context(), helloTimeout)
	ctx, opened, err := s.serve(setup, w, r, protocol.AuthTicket, s.observe)
	cancel()
	if err != nil {
		s.fail(ctx, w, r, err)
		return
	}
	o := opened.(*realtime.Observer)
	defer s.Gateway.Unobserve(o)
	header := w.Header()
	header.Set("Content-Type", "text/event-stream")
	header.Set("Cache-Control", "no-cache")
	// nginx 默认缓冲 /api/ 的响应。
	header.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	stream := eventStream{w: w, rc: http.NewResponseController(w), logger: s.logger}
	for {
		wait, cancel := context.WithTimeout(r.Context(), streamHeartbeat)
		m, reason, err := o.Next(wait)
		cancel()
		switch {
		case r.Context().Err() != nil:
			return
		case err != nil:
			if !stream.write([]byte(": ping\n\n")) {
				return
			}
		case reason != 0:
			stream.event(ctx, "close", "", streamCloses[reason])
			return
		default:
			name, id, data := s.streamMessage(m)
			if !stream.event(ctx, name, id, data) {
				return
			}
		}
	}
}

// 确认票据失败或 panic 时都要退订，否则事件流的名额和房间频道一直被占着。
func (s *server) observe(ctx context.Context, c call) (any, error) {
	// 内存中只留凭证的哈希。
	credential := sha256.Sum256([]byte(c.ticket))
	o, err := s.Gateway.Observe(ctx, c.roomID, c.member, c.bot, string(credential[:]))
	if err != nil {
		return nil, err
	}
	confirmed := false
	defer func() {
		if !confirmed {
			s.Gateway.Unobserve(o)
		}
	}()
	// 作废时先删除票据、再通知断开：事件流在本实例生效之前发出的通知它收不到，所以生效之后再确认一次票据。
	if _, err := s.Identity.VerifyTicket(ctx, c.ticket); err != nil {
		return nil, err
	}
	confirmed = true
	return o, nil
}

func (s *server) streamMessage(m realtime.Message) (name, id string, data any) {
	switch m := m.(type) {
	case realtime.Snapshot:
		return "snapshot", "", protocol.RoomSyncResponse{Room: s.snapshot(m.View)}
	case realtime.Event:
		msg := s.message(m, "")
		return msg.Type, strconv.FormatInt(msg.Seq, 10), msg
	}
	panic(fmt.Sprintf("未知的事件流消息 %T", m))
}

type eventStream struct {
	w      http.ResponseWriter
	rc     *http.ResponseController
	logger *slog.Logger
}

// data 只占一行：json.Marshal 的输出中没有换行。
func (st eventStream) event(ctx context.Context, name, id string, data any) bool {
	payload, err := json.Marshal(data)
	if err != nil {
		st.logger.ErrorContext(ctx, "编码事件流的消息失败", "event", name, "error", err)
		return false
	}
	frame := "event: " + name + "\n"
	if id != "" {
		frame += "id: " + id + "\n"
	}
	return st.write([]byte(frame + "data: " + string(payload) + "\n\n"))
}

func (st eventStream) write(frame []byte) bool {
	_ = st.rc.SetWriteDeadline(time.Now().Add(streamWriteTimeout))
	if _, err := st.w.Write(frame); err != nil {
		return false
	}
	return st.rc.Flush() == nil
}
