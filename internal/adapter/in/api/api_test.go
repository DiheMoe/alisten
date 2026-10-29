package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/dihemoe/alisten/internal/app/admin"
	"github.com/dihemoe/alisten/internal/app/identity"
	"github.com/dihemoe/alisten/internal/app/lobby"
	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/app/port/porttest"
	"github.com/dihemoe/alisten/internal/app/realtime"
	"github.com/dihemoe/alisten/internal/app/site"
	"github.com/dihemoe/alisten/internal/domain/member"
	"github.com/dihemoe/alisten/internal/domain/room"
	"github.com/dihemoe/alisten/internal/lib/logctx"
	"github.com/dihemoe/alisten/protocol"
)

const allowedOrigin = "https://allowed.example"

// 没有调用 realtime.Start：这些测试不运行房间。
func services(store *porttest.Store, rooms port.RoomDirectory, adminToken string) Services {
	music := newCatalog(rooms, 10)
	node := porttest.NewRedis().Node("a")
	_, gateway := realtime.New(store, rooms, music, porttest.NewPreparer(), realtime.Cluster{Instance: "a", Leases: node, Liveness: node, Bus: node}, slog.New(slog.DiscardHandler))
	l := lobby.New(rooms, node)
	return Services{
		Identity: identity.New(store, rooms, store, gateway),
		Lobby:    l,
		Admin:    admin.New(adminToken, l, rooms, store, store, porttest.NewNetEaseQRLogin(), porttest.NewQQQRLogin(), porttest.NewBilibiliQRLogin(), porttest.NewYouTubeCookies(), gateway),
		Gateway:  gateway,
		Catalog:  music,
		Site:     site.New(store),
	}
}

func start(t *testing.T, s Services) (*httptest.Server, *bytes.Buffer) {
	t.Helper()
	var logs bytes.Buffer
	server := httptest.NewServer(SameOrigin([]string{allowedOrigin, "http://localhost:9876"}, New(s, prometheus.NewRegistry(), noop.NewTracerProvider(), slog.New(logctx.NewHandler(slog.NewJSONHandler(&logs, nil))))))
	t.Cleanup(server.Close)
	return server, &logs
}

type response struct {
	status int
	body   string
}

func send(t *testing.T, server *httptest.Server, method, path, body string, header ...string) response {
	t.Helper()
	resp, _ := exchange(t, server, method, path, body, header...)
	return resp
}

func exchange(t *testing.T, server *httptest.Server, method, path, body string, header ...string) (response, http.Header) {
	t.Helper()
	return roundTrip(t, server.Client(), method, server.URL+path, body, header...)
}

func roundTrip(t *testing.T, client *http.Client, method, url, body string, header ...string) (response, http.Header) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response{status: resp.StatusCode, body: strings.TrimSpace(string(b))}, resp.Header
}

func checkCreated(t *testing.T, what string, resp response, header http.Header, location string) {
	t.Helper()
	if resp.status != http.StatusCreated || header.Get("Location") != location {
		t.Errorf("%s = %#v、Location %q，期望 201、Location %q", what, resp, header.Get("Location"), location)
	}
}

func checkNoContent(t *testing.T, what string, resp response, header http.Header) {
	t.Helper()
	if resp != (response{status: http.StatusNoContent}) || header.Get("Content-Type") != "" {
		t.Errorf("%s = %#v、Content-Type %q，期望 204，没有响应体和 Content-Type", what, resp, header.Get("Content-Type"))
	}
}

func failure(code protocol.ErrorCode, message string) response {
	b, _ := json.Marshal(protocol.ErrorResponse{Error: protocol.ErrorInfo{Code: code, Message: message}})
	return response{status: code.HTTPStatus(), body: string(b)}
}

func memberToken(t *testing.T, server *httptest.Server) string {
	t.Helper()
	var created protocol.CreateMemberResponse
	if err := json.Unmarshal([]byte(send(t, server, http.MethodPost, "/api/members", "{}").body), &created); err != nil {
		t.Fatal(err)
	}
	return created.MemberToken
}

func TestSameOrigin(t *testing.T) {
	store := porttest.New()
	server, _ := start(t, services(store, store, ""))

	for _, header := range [][]string{{"Origin", "https://allowed.example"}, {"Origin", "http://localhost:9876"}, nil} {
		if got := send(t, server, http.MethodPost, "/api/members", "{}", header...); got.status != http.StatusCreated {
			t.Errorf("请求头 %q 时 = %#v，期望成功", header, got)
		}
	}
	want := failure(protocol.ErrorCodeForbidden, "服务器不接受从这个网址发出的操作，请用网站的正式地址打开。")
	if got := send(t, server, http.MethodPost, "/api/members", "{}", "Origin", "https://evil.example"); got != want {
		t.Errorf("列表外来源的请求 = %#v，期望 %#v", got, want)
	}
}

func TestRequestBodyLimit(t *testing.T) {
	store := porttest.New()
	server, _ := start(t, services(store, store, ""))
	body := `{"name":"房间","description":""}`
	fits := body + strings.Repeat(" ", 64<<10-len(body))

	if got := send(t, server, http.MethodPost, "/api/rooms", fits); got.status != http.StatusCreated {
		t.Errorf("64 KiB 的请求体 = %#v，期望成功", got)
	}
	want := failure(protocol.ErrorCodeInvalidArgument, "请求体不能超过 64 KiB")
	if got := send(t, server, http.MethodPost, "/api/rooms", fits+" "); got != want {
		t.Errorf("超过 64 KiB 的请求体 = %#v，期望 %#v", got, want)
	}
}

func TestMalformedPathParametersAreNotFound(t *testing.T) {
	store := porttest.New()
	server, logs := start(t, services(store, store, ""))
	if _, err := store.CreateRoom(t.Context(), port.NewRoom{Kind: room.Persistent, Name: "大厅", Slug: "hall"}); err != nil {
		t.Fatal(err)
	}

	if got := send(t, server, http.MethodGet, "/api/rooms/hall", ""); got.status != http.StatusOK {
		t.Fatalf("按别名查找 = %#v，期望成功", got)
	}
	want := failure(protocol.ErrorCodeNotFound, "房间不存在")
	for _, path := range []string{"/api/rooms/%FF"} {
		if got := send(t, server, http.MethodGet, path, ""); got != want {
			t.Errorf("GET %s = %#v，期望 %#v", path, got, want)
		}
	}
	if logs.Len() != 0 {
		t.Errorf("不存在的房间不是内部错误，不应记录日志：\n%s", logs)
	}
}

func TestUnknownEndpointsAreJSONNotFound(t *testing.T) {
	store := porttest.New()
	server, _ := start(t, services(store, store, ""))

	want := failure(protocol.ErrorCodeNotFound, "接口不存在")
	for _, tt := range []struct{ method, path string }{
		{http.MethodGet, "/api/nope"},
		{http.MethodGet, "/api/members"},
	} {
		if got := send(t, server, tt.method, tt.path, ""); got != want {
			t.Errorf("%s %s = %#v，期望 %#v", tt.method, tt.path, got, want)
		}
	}
	if got := send(t, server, http.MethodGet, "/api/rooms", ""); got.status != http.StatusOK {
		t.Errorf("GET /api/rooms = %#v，期望成功", got)
	}
}

func TestCredentialsAreCheckedFirst(t *testing.T) {
	store := porttest.New()
	disabled, _ := start(t, services(store, store, ""))
	enabled, _ := start(t, services(store, store, "secret"))

	tests := []struct {
		name   string
		server *httptest.Server
		method string
		path   string
		header []string
		want   response
	}{
		{"后台没有启用", disabled, http.MethodPost, "/api/admin/rooms", nil, failure(protocol.ErrorCodeForbidden, "后台没有启用：服务端没有配置管理 token。")},
		{"后台没有启用时带着 token", disabled, http.MethodPost, "/api/admin/rooms", []string{"Authorization", "Bearer secret"}, failure(protocol.ErrorCodeForbidden, "后台没有启用：服务端没有配置管理 token。")},
		{"管理 token 错误", enabled, http.MethodPost, "/api/admin/rooms", []string{"Authorization", "Bearer wrong"}, failure(protocol.ErrorCodeUnauthorized, "管理 token 无效")},
		{"缺少成员 token", enabled, http.MethodPatch, "/api/me", nil, failure(protocol.ErrorCodeUnauthorized, "成员 token 无效")},
		{"成员 token 错误", enabled, http.MethodPost, "/api/rooms/hall/tickets", []string{"Authorization", "Bearer wrong"}, failure(protocol.ErrorCodeUnauthorized, "成员 token 无效")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := send(t, tt.server, tt.method, tt.path, "不是 JSON", tt.header...); got != tt.want {
				t.Errorf("%s %s = %#v，期望 %#v", tt.method, tt.path, got, tt.want)
			}
		})
	}
}

func TestBearerScheme(t *testing.T) {
	store := porttest.New()
	server, _ := start(t, services(store, store, ""))
	token := memberToken(t, server)

	for _, authorization := range []string{"Bearer " + token, "bearer " + token, "Bearer   " + token} {
		if got := send(t, server, http.MethodPatch, "/api/me", "{}", "Authorization", authorization); got.status != http.StatusOK {
			t.Errorf("Authorization: %s = %#v，期望成功", authorization, got)
		}
	}
	for _, authorization := range []string{token, "Basic " + token, "Bearer" + token, "Bearer\t" + token} {
		if got := send(t, server, http.MethodPatch, "/api/me", "{}", "Authorization", authorization); got.status != http.StatusUnauthorized {
			t.Errorf("Authorization: %s = %#v，期望 401", authorization, got)
		}
	}
}

func TestErrorInfo(t *testing.T) {
	tests := []struct {
		err      error
		want     protocol.ErrorInfo
		expected bool
	}{
		{fmt.Errorf("查询：%w", context.DeadlineExceeded), protocol.ErrorInfo{Code: protocol.ErrorCodeUnavailable, Message: "服务器没有及时响应，请稍后再试。"}, true},
	}
	for _, tt := range tests {
		if got, expected := errorInfo(tt.err); got != tt.want || expected != tt.expected {
			t.Errorf("errorInfo(%v) = %#v, %v，期望 %#v, %v", tt.err, got, expected, tt.want, tt.expected)
		}
	}
}

type brokenRooms struct{ *porttest.Store }

func (brokenRooms) ListRooms(context.Context) ([]port.RoomRecord, error) {
	return nil, errors.New("连接中断")
}

func TestUnexpectedErrorIsLogged(t *testing.T) {
	store := porttest.New()
	server, logs := start(t, services(store, brokenRooms{store}, ""))

	if got, want := send(t, server, http.MethodGet, "/api/rooms", ""), failure(protocol.ErrorCodeInternal, "服务器出错了，请稍后再试。"); got != want {
		t.Errorf("GET /api/rooms = %#v，期望 %#v", got, want)
	}
	var entry map[string]any
	if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
		t.Fatalf("应记录一行 JSON 日志：%v\n%s", err, logs)
	}
	if entry["error"] != "连接中断" || entry["path"] != "/api/rooms" {
		t.Errorf("日志 = %v，期望带有错误和路径", entry)
	}
}

type abortingRooms struct{ *porttest.Store }

func (abortingRooms) ListRooms(context.Context) ([]port.RoomRecord, error) {
	panic(http.ErrAbortHandler)
}

func TestAbortHandlerIsLeftToNetHTTP(t *testing.T) {
	store := porttest.New()
	var logs bytes.Buffer
	handler := New(services(store, abortingRooms{store}, ""), prometheus.NewRegistry(), noop.NewTracerProvider(), slog.New(slog.NewJSONHandler(&logs, nil)))

	defer func() {
		if value := recover(); value != http.ErrAbortHandler {
			t.Errorf("recover() = %v，期望 http.ErrAbortHandler", value)
		}
		if logs.Len() != 0 {
			t.Errorf("不应记录日志：\n%s", logs.String())
		}
	}()
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/rooms", nil))
}

type brokenProfiles struct{ *porttest.Store }

func (brokenProfiles) UpdateMember(context.Context, string, *string, *string) (member.Member, error) {
	return member.Member{}, errors.New("连接中断")
}

func TestUnexpectedErrorLogCarriesMember(t *testing.T) {
	store := porttest.New()
	s := services(store, store, "")
	s.Identity = identity.New(brokenProfiles{store}, store, store, s.Gateway)
	server, logs := start(t, s)
	var created protocol.CreateMemberResponse
	if err := json.Unmarshal([]byte(send(t, server, http.MethodPost, "/api/members", "{}").body), &created); err != nil {
		t.Fatal(err)
	}

	if got, want := send(t, server, http.MethodPatch, "/api/me", `{"name":"新名字"}`, "Authorization", "Bearer "+created.MemberToken), failure(protocol.ErrorCodeInternal, "服务器出错了，请稍后再试。"); got != want {
		t.Errorf("PATCH /api/me = %#v，期望 %#v", got, want)
	}
	var entry map[string]any
	if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
		t.Fatalf("应记录一行 JSON 日志：%v\n%s", err, logs)
	}
	if entry["member_id"] != created.Member.ID || entry["path"] != "/api/me" {
		t.Errorf("日志 = %v，期望带着成员 %s 和路径", entry, created.Member.ID)
	}
}

func TestFailureLogsCarryTheRequestID(t *testing.T) {
	var logs bytes.Buffer
	s := &server{logger: slog.New(logctx.NewHandler(slog.NewJSONHandler(&logs, nil)))}
	broken := errors.New("连接中断")
	request := httptest.NewRequest(http.MethodPost, "/api/room/messages", nil)

	s.fail(t.Context(), httptest.NewRecorder(), request, &realtime.RequestError{ID: "a/r7", Err: broken})
	s.requestError(t.Context(), "chat.send", &realtime.RequestError{ID: "a/r8", Err: broken})
	s.fail(t.Context(), httptest.NewRecorder(), request, broken)
	s.requestError(t.Context(), "search.tracks", broken)

	var got []any
	for line := range strings.Lines(strings.TrimSpace(logs.String())) {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("日志不是 JSON：%v\n%s", err, line)
		}
		got = append(got, entry["request_id"])
	}
	if want := []any{"a/r7", "a/r8", nil, nil}; !slices.Equal(got, want) {
		t.Errorf("日志中的 request_id = %v，期望 %v\n%s", got, want, logs.String())
	}
}

type writeRecorder struct {
	http.ResponseWriter
	wrote bool
}

func (w *writeRecorder) WriteHeader(status int) {
	w.wrote = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *writeRecorder) Write(b []byte) (int, error) {
	w.wrote = true
	return w.ResponseWriter.Write(b)
}

func TestClientGoneWhileUploadingIsSilent(t *testing.T) {
	store := porttest.New()
	var logs bytes.Buffer
	handler := New(services(store, store, ""), prometheus.NewRegistry(), noop.NewTracerProvider(), slog.New(slog.NewJSONHandler(&logs, nil)))
	wrote := make(chan bool, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder := &writeRecorder{ResponseWriter: w}
		handler.ServeHTTP(recorder, r)
		wrote <- recorder.wrote
	}))
	t.Cleanup(server.Close)

	conn, err := net.Dial("tcp", server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(conn, "POST /api/members HTTP/1.1\r\nHost: alisten.example\r\nContent-Length: 100\r\n\r\n{"); err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}

	if <-wrote {
		t.Error("客户端已经断开，不应再写响应")
	}
	if logs.Len() != 0 {
		t.Errorf("客户端断开不是内部错误，不应记录日志：\n%s", logs.String())
	}
}

func TestUnreadableBodyIsInvalidArgument(t *testing.T) {
	store := porttest.New()
	server, logs := start(t, services(store, store, ""))

	conn, err := net.Dial("tcp", server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := io.WriteString(conn, "POST /api/members HTTP/1.1\r\nHost: alisten.example\r\nTransfer-Encoding: chunked\r\n\r\nzz\r\n{}\r\n0\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}

	want := failure(protocol.ErrorCodeInvalidArgument, "无法读取请求体")
	if got := (response{status: resp.StatusCode, body: strings.TrimSpace(string(b))}); got != want {
		t.Errorf("分块编码不合法的请求 = %#v，期望 %#v", got, want)
	}
	if logs.Len() != 0 {
		t.Errorf("请求体不合法不是内部错误，不应记录日志：\n%s", logs)
	}
}
