package api

import (
	"context"

	"github.com/dihemoe/alisten/internal/app/admin"
	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/protocol"
)

// bot 没有会话，用房间票据通过 HTTP 发送房间内请求，不出现在在线列表中。
func (s *server) roomRequest(route protocol.RoomRoute) handlerFunc {
	return func(ctx context.Context, c call) (_ any, err error) {
		data, err := route.Decode(c.r.PathValue, c.r.URL.RawQuery, c.body)
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
		a, by, err := s.action(ctx, c.member, c.bot, data)
		if err != nil {
			return nil, err
		}
		res, err := s.Gateway.Do(ctx, c.roomID, by, a)
		if err != nil {
			return nil, err
		}
		resp := s.result(res)
		if added, ok := resp.(protocol.QueueAddResponse); ok {
			return located{location: "/api/room/queue/" + added.Entry.ID, body: added}, nil
		}
		return resp, nil
	}
}

func (s *server) adminUpdateRoom(ctx context.Context, c call) (any, error) {
	id := c.r.PathValue("id")
	if !uuidPattern.MatchString(id) {
		return nil, port.ErrRoomNotFound
	}
	req, err := protocol.DecodeAdminUpdateRoomRequest(c.body)
	if err != nil {
		return nil, err
	}
	change := admin.RoomChange{Name: req.Name, Description: req.Description, Slug: req.Slug, Password: req.Password}
	return nil, s.Admin.UpdateRoom(ctx, id, change)
}

func (s *server) adminGetRoomAPIKey(ctx context.Context, c call) (any, error) {
	id := c.r.PathValue("id")
	if !uuidPattern.MatchString(id) {
		return nil, port.ErrRoomNotFound
	}
	status, err := s.Admin.RoomAPIKeyStatus(ctx, id)
	if err != nil {
		return nil, err
	}
	resp := protocol.AdminGetRoomApiKeyResponse{Set: status.Set}
	// 零值的 UnixMilli 是负数，协议约定没有 Key 时为 0。
	if !status.CreatedAt.IsZero() {
		resp.CreatedAtMs = status.CreatedAt.UnixMilli()
	}
	return resp, nil
}

func (s *server) adminResetRoomAPIKey(ctx context.Context, c call) (any, error) {
	id := c.r.PathValue("id")
	if !uuidPattern.MatchString(id) {
		return nil, port.ErrRoomNotFound
	}
	if _, err := protocol.DecodeAdminResetRoomApiKeyRequest(c.body); err != nil {
		return nil, err
	}
	key, err := s.Admin.ResetRoomAPIKey(ctx, id)
	if err != nil {
		return nil, err
	}
	return located{location: "/api/admin/rooms/" + id + "/api-key", body: protocol.APIKey{Key: key}}, nil
}

func (s *server) adminDeleteRoom(ctx context.Context, c call) (any, error) {
	id := c.r.PathValue("id")
	if !uuidPattern.MatchString(id) {
		return nil, port.ErrRoomNotFound
	}
	return nil, s.Admin.DeleteRoom(ctx, id)
}
