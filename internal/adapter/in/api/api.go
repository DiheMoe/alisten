package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"runtime/debug"
	"slices"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/trace"

	"github.com/dihemoe/alisten/internal/app/admin"
	"github.com/dihemoe/alisten/internal/app/catalog"
	"github.com/dihemoe/alisten/internal/app/identity"
	"github.com/dihemoe/alisten/internal/app/lobby"
	"github.com/dihemoe/alisten/internal/app/media"
	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/app/realtime"
	"github.com/dihemoe/alisten/internal/app/site"
	"github.com/dihemoe/alisten/internal/domain/member"
	"github.com/dihemoe/alisten/internal/domain/room"
	"github.com/dihemoe/alisten/internal/lib/logctx"
	"github.com/dihemoe/alisten/protocol"
)

const (
	maxBodyBytes    = 64 << 10
	requestDeadline = 3 * time.Minute
)

type Services struct {
	Identity *identity.Service
	Lobby    *lobby.Service
	Admin    *admin.Service
	Gateway  *realtime.Gateway
	Catalog  *catalog.Service
	Media    *media.Library
	Playback *media.Playback
	Site     *site.Service

	// 浏览器访问对象存储时使用的地址前缀，结尾没有 /。
	StorageURL string
}

type server struct {
	Services
	tracer    trace.Tracer
	logger    *slog.Logger
	commands  *prometheus.CounterVec
	redirects *prometheus.CounterVec
}

type call struct {
	r      *http.Request
	body   []byte
	member member.Member
	roomID string
	ticket string
	// 票据是常驻房间的 API Key。
	bot bool
}

type handlerFunc func(ctx context.Context, c call) (any, error)

// 带 Location 的接口，处理函数返回 located。
type located struct {
	location string
	body     any
}

func New(s Services, metrics prometheus.Registerer, tracer trace.TracerProvider, logger *slog.Logger) http.Handler {
	commands := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "alisten_room_commands_total",
		Help: "房间内请求的数量，按请求类型和结果计数，结果为 ok 或错误码。",
	}, []string{"type", "result"})
	redirects := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "alisten_media_redirects_total",
		Help: "/media 跳转的数量，source 为 netease 或 qq，结果为 ok 或错误码。",
	}, []string{"source", "result"})
	metrics.MustRegister(commands, redirects)
	srv := &server{
		Services:  s,
		tracer:    tracer.Tracer("github.com/dihemoe/alisten/internal/adapter/in/api"),
		logger:    logger,
		commands:  commands,
		redirects: redirects,
	}
	mux := http.NewServeMux()
	srv.handle(mux, protocol.RouteCreateMember, srv.createMember)
	srv.handle(mux, protocol.RouteGetMe, srv.getMe)
	srv.handle(mux, protocol.RouteUpdateMe, srv.updateMe)
	srv.handle(mux, protocol.RouteListRooms, srv.listRooms)
	srv.handle(mux, protocol.RouteGetRoom, srv.getRoom)
	srv.handle(mux, protocol.RouteCreateRoom, srv.createRoom)
	srv.handle(mux, protocol.RouteCreateTicket, srv.createTicket)
	srv.handle(mux, protocol.RouteGetSite, srv.getSite)
	for _, route := range protocol.RoomRoutes {
		srv.handle(mux, route.Route, srv.roomRequest(route))
	}
	srv.handle(mux, protocol.RouteAdminListRooms, srv.adminListRooms)
	srv.handle(mux, protocol.RouteAdminCreateRoom, srv.adminCreateRoom)
	srv.handle(mux, protocol.RouteAdminUpdateRoom, srv.adminUpdateRoom)
	srv.handle(mux, protocol.RouteAdminDeleteRoom, srv.adminDeleteRoom)
	srv.handle(mux, protocol.RouteAdminGetRoomApiKey, srv.adminGetRoomAPIKey)
	srv.handle(mux, protocol.RouteAdminResetRoomApiKey, srv.adminResetRoomAPIKey)
	srv.handle(mux, protocol.RouteAdminGetNeteaseCredential, srv.adminGetNeteaseCredential)
	srv.handle(mux, protocol.RouteAdminGetQQCredential, srv.adminGetQQCredential)
	srv.handle(mux, protocol.RouteAdminStartNeteaseLogin, srv.adminStartNeteaseLogin)
	srv.handle(mux, protocol.RouteAdminCheckNeteaseLogin, srv.adminCheckNeteaseLogin)
	srv.handle(mux, protocol.RouteAdminStartQQLogin, srv.adminStartQQLogin)
	srv.handle(mux, protocol.RouteAdminCheckQQLogin, srv.adminCheckQQLogin)
	srv.handle(mux, protocol.RouteAdminGetBilibiliCredential, srv.adminGetBilibiliCredential)
	srv.handle(mux, protocol.RouteAdminStartBilibiliLogin, srv.adminStartBilibiliLogin)
	srv.handle(mux, protocol.RouteAdminCheckBilibiliLogin, srv.adminCheckBilibiliLogin)
	srv.handle(mux, protocol.RouteAdminGetYoutubeCredential, srv.adminGetYoutubeCredential)
	srv.handle(mux, protocol.RouteAdminSetYoutubeCredential, srv.adminSetYoutubeCredential)
	srv.handle(mux, protocol.RouteAdminDeleteYoutubeCredential, srv.adminDeleteYoutubeCredential)
	srv.handle(mux, protocol.RouteAdminListMedia, srv.adminListMedia)
	srv.handle(mux, protocol.RouteAdminDeleteMedia, srv.adminDeleteMedia)
	srv.handle(mux, protocol.RouteAdminUpdateSite, srv.adminUpdateSite)
	mux.HandleFunc("GET /ws", srv.websocket)
	mux.HandleFunc("GET /api/room/events", srv.roomEvents)
	mux.HandleFunc("GET /media/{source}/{id}", srv.redirectMedia)
	// HTTP 的错误响应体是 JSON，而 ServeMux 自带的 404、405 响应是纯文本。不带方法的兜底路由也会匹配方法不符的请求，
	// 因此它们得到 404：错误码中没有与 405 对应的。
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, protocol.ErrorInfo{Code: protocol.ErrorCodeNotFound, Message: "接口不存在"})
	})
	return mux
}

// 不带 Origin 的请求不检查：bot 从服务端发起的调用没有 Origin。
func SameOrigin(allowedOrigins []string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" && !slices.Contains(allowedOrigins, origin) {
			writeError(w, protocol.ErrorInfo{Code: protocol.ErrorCodeForbidden, Message: "服务器不接受从这个网址发出的操作，请用网站的正式地址打开。"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *server) handle(mux *http.ServeMux, route protocol.Route, fn handlerFunc) {
	mux.HandleFunc(route.Pattern, func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), requestDeadline)
		defer cancel()
		ctx, resp, err := s.serve(ctx, w, r, route.Auth, func(ctx context.Context, c call) (any, error) {
			resp, err := fn(ctx, c)
			if err != nil || !route.Location {
				return resp, err
			}
			l := resp.(located)
			w.Header().Set("Location", l.location)
			return l.body, nil
		})
		if err != nil {
			s.fail(ctx, w, r, err)
			return
		}
		if route.Status == http.StatusNoContent {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeJSON(w, route.Status, resp)
	})
}

// 先鉴权再读请求体：后台没有启用时，无论请求是否合法都返回 FORBIDDEN。返回的 ctx 带有凭证所属的成员和房间，
// 出错时的日志也要带上它们。
func (s *server) serve(ctx context.Context, w http.ResponseWriter, r *http.Request, auth protocol.Auth, fn handlerFunc) (logCtx context.Context, _ any, err error) {
	defer func() {
		if value := recover(); value != nil {
			logCtx, err = ctx, s.panicked(ctx, r, value)
		}
	}()
	c := call{r: r}
	switch auth {
	case protocol.AuthNone:
	case protocol.AuthMember:
		m, err := s.Identity.Authenticate(ctx, bearer(r))
		if err != nil {
			return ctx, nil, err
		}
		c.member = m
		ctx = logctx.WithMember(ctx, m.ID)
	case protocol.AuthTicket:
		c.ticket = bearer(r)
		holder, err := s.Identity.VerifyTicket(ctx, c.ticket)
		if err != nil {
			return ctx, nil, err
		}
		c.roomID, c.member, c.bot = holder.RoomID, holder.Member, holder.Bot
		ctx = logctx.WithMember(logctx.WithRoom(ctx, holder.RoomID), holder.Member.ID)
	case protocol.AuthAdmin:
		if err := s.Admin.Authorize(bearer(r)); err != nil {
			return ctx, nil, err
		}
	default:
		// 未知的凭证类型按失败处理，不能当作不需要凭证。
		return ctx, nil, fmt.Errorf("不支持的凭证类型 %d", auth)
	}
	if r.Method != http.MethodGet && r.Method != http.MethodDelete {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
		if err != nil {
			return ctx, nil, bodyError(r, err)
		}
		c.body = body
	}
	resp, err := fn(ctx, c)
	return ctx, resp, err
}

// HTTP 的错误响应体是 JSON，所以 panic 要在写出响应之前恢复，交给 fail 回复 INTERNAL。
func (s *server) panicked(ctx context.Context, r *http.Request, value any) error {
	// ErrAbortHandler 表示有意中止响应，net/http 处理它时不记录日志。
	if err, ok := value.(error); ok && errors.Is(err, http.ErrAbortHandler) {
		panic(value)
	}
	s.logger.ErrorContext(ctx, "处理 HTTP 请求时发生 panic",
		"panic", fmt.Sprint(value),
		"method", r.Method,
		"path", r.URL.Path,
		"stack", string(debug.Stack()),
	)
	return errPanicked
}

var errUnreadableBody = errors.New("无法读取请求体")

// 恢复 panic 的地方已经记录了日志，回复时不再重复记录。
var errPanicked = errors.New("服务器出错了，请稍后再试。")

// 无法读取请求体是客户端的问题，不是内部错误。连接读取失败时，net/http 在返回错误之前已经取消了
// r.Context()，此时按客户端断开处理；连接仍在时，是请求体本身不合法，比如分块编码有误。
func bodyError(r *http.Request, err error) error {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return err
	}
	if r.Context().Err() != nil {
		return r.Context().Err()
	}
	return errUnreadableBody
}

// 认证方案的名称不区分大小写，和凭证之间可以有多个空格（RFC 9110）。
func bearer(r *http.Request) string {
	scheme, credential, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return ""
	}
	return strings.TrimLeft(credential, " ")
}

var knownErrors = []struct {
	err  error
	code protocol.ErrorCode
}{
	{errUnreadableBody, protocol.ErrorCodeInvalidArgument},
	{port.ErrRoomNotFound, protocol.ErrorCodeNotFound},
	{port.ErrSlugTaken, protocol.ErrorCodeConflict},
	{media.ErrNotFound, protocol.ErrorCodeNotFound},
	{media.ErrNotInRoom, protocol.ErrorCodeNotFound},
	{media.ErrInUse, protocol.ErrorCodeConflict},
	{identity.ErrInvalidMemberToken, protocol.ErrorCodeUnauthorized},
	{identity.ErrWrongPassword, protocol.ErrorCodeUnauthorized},
	{admin.ErrInvalidToken, protocol.ErrorCodeUnauthorized},
	{admin.ErrDisabled, protocol.ErrorCodeForbidden},
	{admin.ErrUnrecognizedCookies, protocol.ErrorCodeInvalidArgument},
	{identity.ErrInvalidTicket, protocol.ErrorCodeUnauthorized},
	{identity.ErrBotSession, protocol.ErrorCodeUnauthorized},
	{realtime.ErrRequesterNotAllowed, protocol.ErrorCodeInvalidArgument},
	{site.ErrInvalidBrandName, protocol.ErrorCodeInvalidArgument},
	{realtime.ErrUnavailable, protocol.ErrorCodeUnavailable},
	{realtime.ErrTooManyObservers, protocol.ErrorCodeRateLimited},
	{port.ErrProviderFailed, protocol.ErrorCodeProviderError},
	{media.ErrNoPlayback, protocol.ErrorCodeProviderError},
	{catalog.ErrRateLimited, protocol.ErrorCodeRateLimited},
	{errClientOutdated, protocol.ErrorCodeClientOutdated},
	{errPanicked, protocol.ErrorCodeInternal},
}

func errorInfo(err error) (info protocol.ErrorInfo, expected bool) {
	var argument *protocol.ArgumentError
	if errors.As(err, &argument) {
		return protocol.ErrorInfo{Code: protocol.ErrorCodeInvalidArgument, Message: argument.Error()}, true
	}
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return protocol.ErrorInfo{Code: protocol.ErrorCodeInvalidArgument, Message: "请求体不能超过 64 KiB"}, true
	}
	var roomErr *room.Error
	if errors.As(err, &roomErr) {
		return protocol.ErrorInfo{Code: roomErrorCodes[roomErr.Kind], Message: roomErr.Message}, true
	}
	var rejectedLink *catalog.RejectedLinkError
	if errors.As(err, &rejectedLink) {
		return protocol.ErrorInfo{Code: protocol.ErrorCodeInvalidArgument, Message: rejectedLink.Error()}, true
	}
	for _, known := range knownErrors {
		if errors.Is(err, known.err) {
			return protocol.ErrorInfo{Code: known.code, Message: known.err.Error()}, true
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return protocol.ErrorInfo{Code: protocol.ErrorCodeUnavailable, Message: "服务器没有及时响应，请稍后再试。"}, true
	}
	return protocol.ErrorInfo{Code: protocol.ErrorCodeInternal, Message: "服务器出错了，请稍后再试。"}, false
}

// 请求类型取自解码之后的请求，取值有限，可以作为标签。
func (s *server) count(typ string, err error) {
	s.commands.WithLabelValues(typ, resultLabel(err)).Inc()
}

func resultLabel(err error) string {
	if err == nil {
		return "ok"
	}
	info, _ := errorInfo(err)
	return string(info.Code)
}

func (s *server) fail(ctx context.Context, w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, context.Canceled) && r.Context().Err() != nil {
		return
	}
	info, expected := errorInfo(err)
	if !expected {
		s.logger.ErrorContext(withRequestID(ctx, err), "处理 HTTP 请求失败", "method", r.Method, "path", r.URL.Path, "error", err)
	}
	writeError(w, info)
}

func withRequestID(ctx context.Context, err error) context.Context {
	var failed *realtime.RequestError
	if errors.As(err, &failed) {
		return logctx.WithRequest(ctx, string(failed.ID))
	}
	return ctx
}

func writeError(w http.ResponseWriter, info protocol.ErrorInfo) {
	writeJSON(w, info.Code.HTTPStatus(), protocol.ErrorResponse{Error: info})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
