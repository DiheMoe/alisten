package api

import (
	"context"
	"net/http"

	"github.com/dihemoe/alisten/internal/app/admin"
	"github.com/dihemoe/alisten/protocol"
)

// bot 没有会话，用房间票据通过 HTTP 发送房间内请求，不出现在在线列表中。
func (s *server) roomRequest(route protocol.RoomRoute) handlerFunc {
	return func(ctx context.Context, header http.Header, c call, body []byte) (_ any, err error) {
		data, err := route.Decode(c.r.PathValue, c.r.URL.RawQuery, body)
		if err != nil {
			return nil, err
		}
		// 与 WebSocket 一致，客户端已断开的请求不计数。panic 由 serve 恢复并回复 500，这里按 INTERNAL 计数后原样抛出。
		defer func() {
			if value := recover(); value != nil {
				s.count(route.Type, errPanicked)
				panic(value)
			}
			if c.r.Context().Err() == nil {
				s.count(route.Type, err)
			}
		}()
		if resp, handled, err := s.answer(ctx, c.roomID, data); handled {
			return resp, err
		}
		a, by, err := s.action(ctx, c.roomID, c.caller.Member, c.bot, data)
		if err != nil {
			return nil, err
		}
		res, err := s.Gateway.Do(ctx, c.roomID, by, a)
		if err != nil {
			return nil, err
		}
		resp := s.result(res)
		if added, ok := resp.(protocol.QueueAddResponse); ok {
			header.Set("Location", "/api/room/queue/"+added.Entry.ID)
		}
		return resp, nil
	}
}

func (s *server) adminUpdateRoom(ctx context.Context, c call, req protocol.AdminUpdateRoomRequest) error {
	change := admin.RoomChange{Name: req.Name, Description: req.Description, Slug: req.Slug, Password: req.Password}
	if req.Capacity != nil {
		capacity := int(*req.Capacity)
		change.Capacity = &capacity
	}
	return s.Admin.UpdateRoom(ctx, c.r.PathValue("id"), change)
}

func (s *server) adminGetRoomAPIKey(ctx context.Context, c call) (protocol.AdminGetRoomApiKeyResponse, error) {
	status, err := s.Admin.RoomAPIKeyStatus(ctx, c.r.PathValue("id"))
	if err != nil {
		return protocol.AdminGetRoomApiKeyResponse{}, err
	}
	resp := protocol.AdminGetRoomApiKeyResponse{Set: status.Set}
	// 零值的 UnixMilli 是负数，协议约定没有 Key 时为 0。
	if !status.CreatedAt.IsZero() {
		resp.CreatedAtMs = status.CreatedAt.UnixMilli()
	}
	return resp, nil
}

func (s *server) adminResetRoomAPIKey(ctx context.Context, c call) (protocol.APIKey, string, error) {
	id := c.r.PathValue("id")
	key, err := s.Admin.ResetRoomAPIKey(ctx, id)
	if err != nil {
		return protocol.APIKey{}, "", err
	}
	return protocol.APIKey{Key: key}, "/api/admin/rooms/" + id + "/api-key", nil
}

func (s *server) adminDeleteRoom(ctx context.Context, c call) error {
	return s.Admin.DeleteRoom(ctx, c.r.PathValue("id"))
}
