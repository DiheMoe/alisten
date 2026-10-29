package api

import (
	"context"
	"net/http"
	"regexp"
	"time"

	"github.com/dihemoe/alisten/internal/app/media"
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
	return s.Playback.URL(ctx, t, time.Now())
}

var mediaKeyPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func (s *server) adminListMedia(ctx context.Context, _ call) (any, error) {
	list, err := s.Media.List(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]protocol.StoredMedia, len(list))
	for i, m := range list {
		link := m.Link
		// 后台把 link 当作链接打开，B 站的歌给出视频页面。
		if m.Source == track.Bilibili {
			link = track.BilibiliPageURL(m.Link)
		}
		items[i] = protocol.StoredMedia{Key: m.Key, Source: protocol.Source(m.Source), Link: link, Title: m.Title, Size: m.Size, StoredAtMs: m.StoredAt.UnixMilli()}
		if m.LastPlayedAt != nil {
			played := m.LastPlayedAt.UnixMilli()
			items[i].LastPlayedAtMs = &played
		}
	}
	return protocol.AdminListMediaResponse{Items: items}, nil
}

func (s *server) adminDeleteMedia(ctx context.Context, c call) (any, error) {
	key := c.r.PathValue("key")
	if !mediaKeyPattern.MatchString(key) {
		return nil, media.ErrNotFound
	}
	return nil, s.Media.Delete(ctx, key)
}
