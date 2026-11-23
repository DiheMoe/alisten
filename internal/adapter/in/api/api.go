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
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/trace"

	"github.com/dihemoe/alisten/internal/app/admin"
	"github.com/dihemoe/alisten/internal/app/catalog"
	"github.com/dihemoe/alisten/internal/app/identity"
	"github.com/dihemoe/alisten/internal/app/listen"
	"github.com/dihemoe/alisten/internal/app/lobby"
	"github.com/dihemoe/alisten/internal/app/media"
	"github.com/dihemoe/alisten/internal/app/playlist"
	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/app/realtime"
	"github.com/dihemoe/alisten/internal/app/site"
	playlistorder "github.com/dihemoe/alisten/internal/domain/playlist"
	"github.com/dihemoe/alisten/internal/domain/room"
	"github.com/dihemoe/alisten/internal/lib/logctx"
	"github.com/dihemoe/alisten/internal/lib/logthrottle"
	"github.com/dihemoe/alisten/protocol"
)

const (
	maxBodyBytes = 64 << 10
	// 歌单的批量接口一次最多 1000 首，链接曲目的 ID 最长 2048 字节，每首再留 256 字节给来源、字段名和 JSON 的标点。
	maxBatchBodyBytes = 1000 * (2048 + 256)
	// 导入文件一次最多 1000 首，每首除了曲目还带标题、歌手、专辑和两个地址。nginx 中 /api/me/playlists/imports 的上限要与它保持一致。
	maxImportBodyBytes = 1 << 20
	requestDeadline    = 3 * time.Minute
)

// 两条长连接的路径。追踪要跳过它们，否则一条连接会被记成一条和连接一样长的 span。
const (
	WebSocketPath  = "/ws"
	RoomEventsPath = "/api/room/events"
)

type Services struct {
	Identity  *identity.Service
	Accounts  *identity.Accounts
	Playlists *playlist.Service
	Lobby     *lobby.Service
	Admin     *admin.Service
	Gateway   *realtime.Gateway
	Catalog   *catalog.Service
	Media     *media.Library
	Playback  *media.Playback
	Site      *site.Service
	Listens   *listen.Service
	GitHub    *identity.GitHub

	// 浏览器访问对象存储时使用的地址前缀，结尾没有 /。
	StorageURL string
	// 网页的 origin，结尾没有 /。
	PublicURL string
}

type server struct {
	Services
	tracer    trace.Tracer
	logger    *slog.Logger
	commands  *prometheus.CounterVec
	redirects *prometheus.CounterVec
	// 键是接口，HTTP 是路由加音乐平台，WebSocket 是请求类型。值是 *logthrottle.Warning。
	sourceWarned sync.Map
}

type call struct {
	r      *http.Request
	caller identity.Caller
	// 只有 AuthAccount 的接口才有值。
	owner  identity.AccountOwner
	roomID string
	ticket string
	// 票据是常驻房间的 API Key。
	bot bool
	// 发出请求的客户端地址，按来源限流时使用。
	clientIP string
}

type handlerFunc func(ctx context.Context, header http.Header, c call, body []byte) (any, error)

// 用 routes 登记的接口，请求体不能超过 maxBody 字节。
type routes struct {
	s       *server
	mux     *http.ServeMux
	maxBody int64
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
	rt := routes{s: srv, mux: mux, maxBody: maxBodyBytes}
	batch := routes{s: srv, mux: mux, maxBody: maxBatchBodyBytes}
	imports := routes{s: srv, mux: mux, maxBody: maxImportBodyBytes}
	handleNoRequest(rt, protocol.RouteCreateMember, srv.createMember)
	handleNoRequest(rt, protocol.RouteGetMe, srv.getMe)
	handle(rt, protocol.RouteUpdateMe, srv.updateMe)
	handle(rt, protocol.RouteRegister, srv.register)
	handleNoContent(rt, protocol.RouteChangePassword, srv.changePassword)
	handle(rt, protocol.RouteLogin, srv.login)
	handleNoContentNoRequest(rt, protocol.RouteLogout, srv.logout)
	handleNoRequest(rt, protocol.RouteListMyPlaylists, srv.listMyPlaylists)
	handle(rt, protocol.RouteCreateMyPlaylist, srv.createMyPlaylist)
	handleNoContent(rt, protocol.RouteRenameMyPlaylist, srv.renameMyPlaylist)
	handleNoContentNoRequest(rt, protocol.RouteDeleteMyPlaylist, srv.deleteMyPlaylist)
	handleNoRequest(rt, protocol.RouteListMyPlaylistItems, srv.listMyPlaylistItems)
	handle(rt, protocol.RouteAddMyPlaylistItem, srv.addMyPlaylistItem)
	handleNoContent(batch, protocol.RouteRemoveMyPlaylistItems, srv.removeMyPlaylistItems)
	handle(rt, protocol.RouteListMyTrackPlaylists, srv.listMyTrackPlaylists)
	handle(batch, protocol.RouteCopyMyPlaylistItems, srv.copyMyPlaylistItems)
	handleNoContent(batch, protocol.RouteMoveMyPlaylistItems, srv.moveMyPlaylistItems)
	handle(imports, protocol.RouteImportMyPlaylist, srv.importMyPlaylist)
	handle(rt, protocol.RouteResolveMyPlaylistLink, srv.resolveMyPlaylistLink)
	handle(rt, protocol.RouteImportMyPlatformPlaylist, srv.importMyPlatformPlaylist)
	handleNoRequest(rt, protocol.RouteListRooms, srv.listRooms)
	handleNoRequest(rt, protocol.RouteGetRoom, srv.getRoom)
	handleLocated(rt, protocol.RouteCreateRoom, srv.createRoom)
	handle(rt, protocol.RouteCreateTicket, srv.createTicket)
	handleNoRequest(rt, protocol.RouteGetSite, srv.getSite)
	for _, route := range protocol.RoomRoutes {
		rt.register(route.Route, nil, srv.roomRequest(route))
	}
	handleNoRequest(rt, protocol.RouteAdminListRooms, srv.adminListRooms)
	handleLocated(rt, protocol.RouteAdminCreateRoom, srv.adminCreateRoom)
	handleNoContent(rt, protocol.RouteAdminUpdateRoom, srv.adminUpdateRoom)
	handleNoContentNoRequest(rt, protocol.RouteAdminDeleteRoom, srv.adminDeleteRoom)
	handleNoRequest(rt, protocol.RouteAdminGetRoomApiKey, srv.adminGetRoomAPIKey)
	handleLocatedNoRequest(rt, protocol.RouteAdminResetRoomApiKey, srv.adminResetRoomAPIKey)
	handleNoRequest(rt, protocol.RouteAdminGetNetEaseCredential, srv.adminGetNetEaseCredential)
	handleNoRequest(rt, protocol.RouteAdminGetQQCredential, srv.adminGetQQCredential)
	handleNoRequest(rt, protocol.RouteAdminStartNetEaseLogin, srv.adminStartNetEaseLogin)
	handle(rt, protocol.RouteAdminCheckNetEaseLogin, srv.adminCheckNetEaseLogin)
	handle(rt, protocol.RouteAdminStartQQLogin, srv.adminStartQQLogin)
	handle(rt, protocol.RouteAdminCheckQQLogin, srv.adminCheckQQLogin)
	handleNoRequest(rt, protocol.RouteAdminGetBilibiliCredential, srv.adminGetBilibiliCredential)
	handleNoRequest(rt, protocol.RouteAdminStartBilibiliLogin, srv.adminStartBilibiliLogin)
	handle(rt, protocol.RouteAdminCheckBilibiliLogin, srv.adminCheckBilibiliLogin)
	handleNoRequest(rt, protocol.RouteAdminGetYouTubeCredential, srv.adminGetYouTubeCredential)
	handleNoContent(rt, protocol.RouteAdminSetYouTubeCredential, srv.adminSetYouTubeCredential)
	handleNoContentNoRequest(rt, protocol.RouteAdminDeleteYouTubeCredential, srv.adminDeleteYouTubeCredential)
	handleNoRequest(rt, protocol.RouteAdminGetGitHubCredential, srv.adminGetGitHubCredential)
	handleNoContent(rt, protocol.RouteAdminSetGitHubCredential, srv.adminSetGitHubCredential)
	handleNoContentNoRequest(rt, protocol.RouteAdminDeleteGitHubCredential, srv.adminDeleteGitHubCredential)
	handle(rt, protocol.RouteAdminListMedia, srv.adminListMedia)
	handle(rt, protocol.RouteAdminRemoveMedia, srv.adminRemoveMedia)
	handle(rt, protocol.RouteAdminUpdateSite, srv.adminUpdateSite)
	handleNoContentNoRequest(rt, protocol.RouteAdminOpenRegistration, srv.adminOpenRegistration)
	handleNoContentNoRequest(rt, protocol.RouteAdminCloseRegistration, srv.adminCloseRegistration)
	handleNoContent(rt, protocol.RouteAdminSetEphemeralRoomCapacity, srv.adminSetEphemeralRoomCapacity)
	handleNoRequest(rt, protocol.RouteAdminGetAccount, srv.adminGetAccount)
	handleNoRequest(rt, protocol.RouteAdminResetAccountPassword, srv.adminResetAccountPassword)
	handleNoRequest(rt, protocol.RouteListMyListens, srv.listMyListens)
	handleNoContent(rt, protocol.RouteRemoveMyListen, srv.removeMyListen)
	handleNoContentNoRequest(rt, protocol.RouteClearMyListens, srv.clearMyListens)
	handleNoContentNoRequest(rt, protocol.RouteEnableListenRecording, srv.enableListenRecording)
	handleNoContentNoRequest(rt, protocol.RouteDisableListenRecording, srv.disableListenRecording)
	handleNoRequest(rt, protocol.RouteShareMyPlaylist, srv.shareMyPlaylist)
	handleNoContentNoRequest(rt, protocol.RouteUnshareMyPlaylist, srv.unshareMyPlaylist)
	handleNoRequest(rt, protocol.RouteGetSharedPlaylist, srv.getSharedPlaylist)
	handle(rt, protocol.RouteCopySharedPlaylist, srv.copySharedPlaylist)
	handleNoRequest(rt, protocol.RouteCreatePairingCode, srv.createPairingCode)
	handle(rt, protocol.RouteRedeemPairingCode, srv.redeemPairingCode)
	handle(rt, protocol.RouteStartGitHubLogin, srv.startGitHubLogin)
	handle(rt, protocol.RouteFinishGitHubLogin, srv.finishGitHubLogin)
	handleNoContent(rt, protocol.RouteUnbindGitHub, srv.unbindGitHub)
	mux.HandleFunc("GET "+GitHubCallbackPath, srv.githubCallback)
	mux.HandleFunc("GET "+WebSocketPath, srv.websocket)
	mux.HandleFunc("GET "+RoomEventsPath, srv.roomEvents)
	mux.HandleFunc("GET /media/{source}/{id}", srv.redirectMedia)
	// HTTP 的错误响应体是 JSON，而 ServeMux 自带的 404、405 响应是纯文本。不带方法的兜底路由也会匹配方法不符的请求，
	// 所以这些请求得到的也是 404，因为错误码里没有和 405 对应的。
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, protocol.ErrorInfo{Code: protocol.ErrorCodeNotFound, Message: "接口不存在"})
	})
	return mux
}

// 不带 Origin 的请求不检查，因为 bot 从服务端发起的调用没有 Origin。
func SameOrigin(allowedOrigins []string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" && !slices.Contains(allowedOrigins, origin) {
			writeError(w, protocol.ErrorInfo{Code: protocol.ErrorCodeForbidden, Message: "服务器不接受从这个网址发出的操作，请用网站的正式地址打开。"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func handle[Req, Resp any](rt routes, e protocol.Endpoint[Req, Resp], fn func(context.Context, call, Req) (Resp, error)) {
	rt.register(e.Route, e.Params, func(ctx context.Context, _ http.Header, c call, body []byte) (any, error) {
		req, err := e.Decode(c.r.PathValue, body)
		if err != nil {
			return nil, err
		}
		return fn(ctx, c, req)
	})
}

func handleLocated[Req, Resp any](rt routes, e protocol.LocatedEndpoint[Req, Resp], fn func(context.Context, call, Req) (Resp, string, error)) {
	rt.register(e.Route, e.Params, func(ctx context.Context, header http.Header, c call, body []byte) (any, error) {
		req, err := e.Decode(c.r.PathValue, body)
		if err != nil {
			return nil, err
		}
		resp, location, err := fn(ctx, c, req)
		if err != nil {
			return nil, err
		}
		header.Set("Location", location)
		return resp, nil
	})
}

func handleNoContent[Req any](rt routes, e protocol.NoContentEndpoint[Req], fn func(context.Context, call, Req) error) {
	rt.register(e.Route, e.Params, func(ctx context.Context, _ http.Header, c call, body []byte) (any, error) {
		req, err := e.Decode(c.r.PathValue, body)
		if err != nil {
			return nil, err
		}
		return nil, fn(ctx, c, req)
	})
}

func handleNoRequest[Resp any](rt routes, e protocol.Endpoint[protocol.NoRequest, Resp], fn func(context.Context, call) (Resp, error)) {
	handle(rt, e, func(ctx context.Context, c call, _ protocol.NoRequest) (Resp, error) {
		return fn(ctx, c)
	})
}

func handleLocatedNoRequest[Resp any](rt routes, e protocol.LocatedEndpoint[protocol.NoRequest, Resp], fn func(context.Context, call) (Resp, string, error)) {
	handleLocated(rt, e, func(ctx context.Context, c call, _ protocol.NoRequest) (Resp, string, error) {
		return fn(ctx, c)
	})
}

func handleNoContentNoRequest(rt routes, e protocol.NoContentEndpoint[protocol.NoRequest], fn func(context.Context, call) error) {
	handleNoContent(rt, e, func(ctx context.Context, c call, _ protocol.NoRequest) error {
		return fn(ctx, c)
	})
}

// 先鉴权再读请求体，这样后台没有启用时，不管请求是否合法都返回 FORBIDDEN。
func (rt routes) register(route protocol.Route, params []protocol.PathParam, fn handlerFunc) {
	for _, p := range params {
		if paramNotFound[p.Type] == nil {
			panic(fmt.Sprintf("%s 的路径参数 %s 是 %s 类型，paramNotFound 中没有这个类型", route.Pattern, p.Name, p.Type))
		}
	}
	rt.mux.HandleFunc(route.Pattern, func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ctx, cancel := context.WithTimeout(r.Context(), requestDeadline)
		defer cancel()
		ctx, resp, err := rt.s.serve(ctx, r, route.Auth, func(ctx context.Context, c call) (any, error) {
			var body []byte
			if r.Method != http.MethodGet && r.Method != http.MethodDelete {
				var err error
				if body, err = io.ReadAll(http.MaxBytesReader(w, r.Body, rt.maxBody)); err != nil {
					return nil, bodyError(r, err)
				}
			}
			return fn(ctx, w.Header(), c, body)
		})
		if err != nil {
			rt.s.fail(ctx, w, r, start, err)
			return
		}
		if route.Status == http.StatusNoContent {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeJSON(w, route.Status, resp)
	})
}

// 返回的 ctx 带着凭证所属的成员和房间，因为出错时的日志也要带上它们。
func (s *server) serve(ctx context.Context, r *http.Request, auth protocol.Auth, fn func(context.Context, call) (any, error)) (logCtx context.Context, _ any, err error) {
	defer func() {
		if value := recover(); value != nil {
			logCtx, err = ctx, s.panicked(ctx, r, value)
		}
	}()
	c := call{r: r, clientIP: clientIP(r)}
	switch auth {
	case protocol.AuthNone:
	case protocol.AuthMember, protocol.AuthAccount:
		caller, err := s.Identity.Authenticate(ctx, bearer(r))
		if err != nil {
			return ctx, nil, err
		}
		c.caller = caller
		ctx = logctx.WithMember(ctx, caller.Member.ID)
		if auth == protocol.AuthAccount {
			if c.owner, err = caller.AccountOwner(); err != nil {
				return ctx, nil, err
			}
		}
	case protocol.AuthTicket:
		c.ticket = bearer(r)
		holder, err := s.Identity.VerifyTicket(ctx, c.ticket)
		if err != nil {
			return ctx, nil, err
		}
		c.roomID, c.caller, c.bot = holder.RoomID, identity.Caller{Member: holder.Member, TokenHash: holder.SessionTokenHash}, holder.Bot
		ctx = logctx.WithMember(logctx.WithRoom(ctx, holder.RoomID), holder.Member.ID)
	case protocol.AuthAdmin:
		if err := s.Admin.Authorize(bearer(r)); err != nil {
			return ctx, nil, err
		}
	default:
		// 未知的凭证类型按失败处理，不能当作不需要凭证。
		return ctx, nil, fmt.Errorf("不支持的凭证类型 %d", auth)
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

// 无法读取请求体是客户端的问题，不是内部错误。如果是连接读取失败，net/http 在返回错误之前已经取消了
// r.Context()，这时按客户端断开处理。如果连接还在，就是请求体本身不合法，比如分块编码有误。
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
	{media.ErrNotInRoom, protocol.ErrorCodeNotFound},
	{identity.ErrInvalidMemberToken, protocol.ErrorCodeUnauthorized},
	{identity.ErrWrongPassword, protocol.ErrorCodeUnauthorized},
	{admin.ErrInvalidToken, protocol.ErrorCodeUnauthorized},
	{admin.ErrDisabled, protocol.ErrorCodeForbidden},
	{admin.ErrUnrecognizedCookies, protocol.ErrorCodeInvalidArgument},
	{identity.ErrInvalidTicket, protocol.ErrorCodeUnauthorized},
	{identity.ErrBotSession, protocol.ErrorCodeUnauthorized},
	{identity.ErrInvalidEmail, protocol.ErrorCodeInvalidArgument},
	{identity.ErrInvalidCredentials, protocol.ErrorCodeInvalidCredentials},
	{identity.ErrWrongCurrentPassword, protocol.ErrorCodeInvalidCredentials},
	{identity.ErrAlreadyLoggedIn, protocol.ErrorCodeConflict},
	{identity.ErrRegistrationClosed, protocol.ErrorCodeForbidden},
	{identity.ErrNotRegistered, protocol.ErrorCodeForbidden},
	{identity.ErrTooManyAttempts, protocol.ErrorCodeRateLimited},
	{identity.ErrTooManyGuests, protocol.ErrorCodeRateLimited},
	{identity.ErrTooManyRoomGuesses, protocol.ErrorCodeRateLimited},
	{identity.ErrAccountNotFound, protocol.ErrorCodeNotFound},
	{lobby.ErrTooManyRooms, protocol.ErrorCodeRateLimited},
	{port.ErrUsernameTaken, protocol.ErrorCodeConflict},
	{port.ErrAlreadyRegistered, protocol.ErrorCodeConflict},
	{port.ErrPlaylistNotFound, protocol.ErrorCodeNotFound},
	{port.ErrFavoritesFixed, protocol.ErrorCodeConflict},
	{playlist.ErrInvalidName, protocol.ErrorCodeInvalidArgument},
	{playlist.ErrTooManyPlaylists, protocol.ErrorCodeConflict},
	{playlist.ErrPlaylistFull, protocol.ErrorCodeConflict},
	{playlist.ErrTrackNotReady, protocol.ErrorCodeConflict},
	{playlist.ErrTooFast, protocol.ErrorCodeRateLimited},
	{playlist.ErrInvalidPlaylistLink, protocol.ErrorCodeInvalidArgument},
	{playlist.ErrUnsupportedPlaylist, protocol.ErrorCodeInvalidArgument},
	{playlist.ErrShortLinkFailed, protocol.ErrorCodeSourceError},
	{playlistorder.ErrBeforeMoved, protocol.ErrorCodeInvalidArgument},
	{playlistorder.ErrBeforeMissing, protocol.ErrorCodeConflict},
	{realtime.ErrRequesterNotAllowed, protocol.ErrorCodeInvalidArgument},
	{site.ErrInvalidBrandName, protocol.ErrorCodeInvalidArgument},
	{realtime.ErrUnavailable, protocol.ErrorCodeUnavailable},
	{realtime.ErrTooManyObservers, protocol.ErrorCodeRateLimited},
	// 排在 ErrSourceFailed 前面，因为音乐平台适配器会把它和 ErrSourceFailed 包在同一个错误里，回复要说明是登录失效。
	{port.ErrSourceSignedOut, protocol.ErrorCodeSourceError},
	{port.ErrSourceFailed, protocol.ErrorCodeSourceError},
	{media.ErrNoPlayback, protocol.ErrorCodeSourceError},
	{catalog.ErrRateLimited, protocol.ErrorCodeRateLimited},
	{catalog.ErrNoMatch, protocol.ErrorCodeNotFound},
	{catalog.ErrUnsupported, protocol.ErrorCodeInvalidArgument},
	{errClientOutdated, protocol.ErrorCodeClientOutdated},
	{errPanicked, protocol.ErrorCodeInternal},
	{port.ErrShareNotFound, protocol.ErrorCodeNotFound},
	{identity.ErrInvalidPairingCode, protocol.ErrorCodeInvalidCredentials},
	{port.ErrNoPassword, protocol.ErrorCodeConflict},
	{identity.ErrGitHubLoginExpired, protocol.ErrorCodeInvalidCredentials},
	{identity.ErrGitHubMismatch, protocol.ErrorCodeInvalidCredentials},
	{identity.ErrGitHubUnverified, protocol.ErrorCodeForbidden},
	{identity.ErrGitHubDisabled, protocol.ErrorCodeNotFound},
	{port.ErrGitHubTaken, protocol.ErrorCodeConflict},
	{port.ErrGitHubAlreadyBound, protocol.ErrorCodeConflict},
}

// 格式不对的路径参数指向的资源一定不存在，所以按参数的类型回复对应的资源不存在。
var paramNotFound = map[string]error{
	"RoomRef":      port.ErrRoomNotFound,
	"RoomID":       port.ErrRoomNotFound,
	"MyPlaylistID": port.ErrPlaylistNotFound,
	"ShareID":      port.ErrShareNotFound,
	"Username":     identity.ErrAccountNotFound,
}

func errorInfo(err error) (info protocol.ErrorInfo, expected bool) {
	var param *protocol.PathParamError
	if errors.As(err, &param) {
		err = paramNotFound[param.Type]
	}
	var argument *protocol.ArgumentError
	if errors.As(err, &argument) {
		return protocol.ErrorInfo{Code: protocol.ErrorCodeInvalidArgument, Message: argument.Error()}, true
	}
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return protocol.ErrorInfo{Code: protocol.ErrorCodeInvalidArgument, Message: fmt.Sprintf("请求体不能超过 %d KiB", tooLarge.Limit>>10)}, true
	}
	var roomErr *room.Error
	if errors.As(err, &roomErr) {
		// 没有映射的 Kind 往下按意外的错误处理。否则回复里的错误码是空的，在 HTTP 上状态码是 0，写响应头会 panic。
		if code, ok := roomErrorCodes[roomErr.Kind]; ok {
			return protocol.ErrorInfo{Code: code, Message: roomErr.Message}, true
		}
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

func (s *server) fail(ctx context.Context, w http.ResponseWriter, r *http.Request, start time.Time, err error) {
	if errors.Is(err, context.Canceled) && r.Context().Err() != nil {
		return
	}
	info, expected := errorInfo(err)
	switch {
	case !expected:
		s.logger.ErrorContext(withRequestID(ctx, err), "处理 HTTP 请求失败", "method", r.Method, "path", r.URL.Path, "error", err)
	// 超时按预期的错误回复，但还是要记下来，因为数据库或下游变慢时，不开追踪就只有这条日志。
	case errors.Is(err, context.DeadlineExceeded):
		s.logger.WarnContext(withRequestID(ctx, err), "HTTP 请求超时", "method", r.Method, "route", r.Pattern, "path", r.URL.Path, "elapsed", time.Since(start), "error", err)
	case info.Code == protocol.ErrorCodeSourceError:
		// /media 的路径里带着音乐平台，按音乐平台分开节流，这样一个平台持续出错时，另一个平台刚开始的错误不会被压成 DEBUG。
		s.warnSource(withRequestID(ctx, err), r.Pattern+" "+r.PathValue("source"), "音乐平台出错", "method", r.Method, "route", r.Pattern, "path", r.URL.Path, "reason", sourceReason(err))
	}
	writeError(w, info)
}

// 音乐平台出错是预期内的错误，但不记下来就查不到原因，比如登录已失效。平台故障期间同一个接口会被反复调用，
// 房间里每个播放器都会重试 /media，所以按接口节流。成功时不调用 Recovered，距上一条 WARN 超过 logthrottle.Interval 之后再出错照样记 WARN。
func (s *server) warnSource(ctx context.Context, key, msg string, args ...any) {
	warned, _ := s.sourceWarned.LoadOrStore(key, &logthrottle.Warning{})
	s.logger.Log(ctx, warned.(*logthrottle.Warning).Level(), msg, args...)
}

// 只记能确定的原因，不记音乐平台适配器给的错误文本，因为那段文本可能带着凭据，比如扫码登录的 key。
func sourceReason(err error) string {
	var rejected *port.RejectedError
	switch {
	case errors.Is(err, port.ErrSourceSignedOut):
		return "登录已失效"
	case errors.Is(err, context.DeadlineExceeded):
		return "超时"
	case errors.As(err, &rejected):
		return "平台拒绝"
	default:
		return "平台出错"
	}
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
