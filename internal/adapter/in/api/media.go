package api

import (
	"context"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/dihemoe/alisten/internal/app/media"
	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/track"
	"github.com/dihemoe/alisten/protocol"
)

func (s *server) redirectMedia(w http.ResponseWriter, r *http.Request) {
	source := track.Source(r.PathValue("source"))
	if source != track.NetEase && source != track.QQ {
		writeError(w, protocol.ErrorInfo{Code: protocol.ErrorCodeNotFound, Message: "接口不存在"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), requestDeadline)
	defer cancel()
	target, err := s.redirectTarget(ctx, r, track.Track{Source: source, ID: r.PathValue("id")})
	// 客户端已断开不是服务端的错误，不计入跳转的结果。
	if r.Context().Err() != nil {
		return
	}
	s.redirects.WithLabelValues(string(source), resultLabel(err)).Inc()
	if err != nil {
		s.fail(ctx, w, r, err)
		return
	}
	// 播放地址有时效，不能让浏览器缓存这次跳转。
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, target, http.StatusFound)
}

func (s *server) redirectTarget(ctx context.Context, r *http.Request, t track.Track) (_ string, err error) {
	defer func() {
		if value := recover(); value != nil {
			err = s.panicked(ctx, r, value)
		}
	}()
	// 路径里的 ID 不经过协议的解码，可以是任意字节。不是合法 UTF-8 或带 U+0000 的 ID 不属于任何曲目，
	// 交给存储查询还会让数据库报错，所以在这里就按不在任何房间中处理。
	if !utf8.ValidString(t.ID) || strings.ContainsRune(t.ID, 0) {
		return "", media.ErrNotInRoom
	}
	return s.Playback.URL(ctx, t, time.Now())
}

var mediaSorts = map[protocol.MediaSort]port.MediaSort{
	protocol.MediaSortStored: port.MediaByStored,
	protocol.MediaSortIdle:   port.MediaByIdle,
	protocol.MediaSortSize:   port.MediaBySize,
}

func (s *server) adminListMedia(ctx context.Context, _ call, req protocol.AdminListMediaRequest) (protocol.AdminListMediaResponse, error) {
	q := port.MediaQuery{Page: 1, Sort: port.MediaByStored}
	if req.Page != nil {
		q.Page = int(*req.Page)
	}
	if req.Sort != nil {
		q.Sort = mediaSorts[*req.Sort]
	}
	if req.Q != nil {
		q.Title = *req.Q
	}
	page, err := s.Media.List(ctx, q)
	if err != nil {
		return protocol.AdminListMediaResponse{}, err
	}
	items := make([]protocol.StoredMedia, len(page.Items))
	for i, m := range page.Items {
		link := m.Link
		// 后台把 link 当作链接打开，所以 B 站的歌给出视频页面。
		if m.Source == track.Bilibili {
			link = track.BilibiliPageURL(m.Link)
		}
		items[i] = protocol.StoredMedia{
			Key:        m.Key,
			Source:     protocol.Source(m.Source),
			Link:       link,
			Title:      m.Title,
			Artist:     m.Artist,
			CoverURL:   s.coverURL(m.CoverURL, m.CoverKey),
			Size:       m.Size,
			StoredAtMs: m.StoredAt.UnixMilli(),
		}
		if m.LastPlayedAt != nil {
			played := m.LastPlayedAt.UnixMilli()
			items[i].LastPlayedAtMs = &played
		}
	}
	return protocol.AdminListMediaResponse{Items: items, Total: int64(page.Total), TotalBytes: page.TotalBytes, Page: int64(q.Page), PageSize: port.MediaPageSize}, nil
}

func (s *server) adminRemoveMedia(ctx context.Context, _ call, req protocol.AdminRemoveMediaRequest) (protocol.AdminRemoveMediaResponse, error) {
	keys := make([]string, len(req.Media))
	for i, m := range req.Media {
		keys[i] = m.Key
	}
	deleted, inUse, err := s.Media.DeleteMany(ctx, keys)
	if err != nil {
		return protocol.AdminRemoveMediaResponse{}, err
	}
	return protocol.AdminRemoveMediaResponse{Deleted: deleted, InUse: inUse}, nil
}
