package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/coder/websocket"
	"github.com/prometheus/client_golang/prometheus"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/dihemoe/alisten/internal/app/admin"
	"github.com/dihemoe/alisten/internal/app/identity"
	"github.com/dihemoe/alisten/internal/app/lobby"
	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/app/port/porttest"
	"github.com/dihemoe/alisten/internal/app/realtime"
	"github.com/dihemoe/alisten/internal/domain/member"
	"github.com/dihemoe/alisten/internal/domain/room"
	"github.com/dihemoe/alisten/internal/lib/logctx"
	"github.com/dihemoe/alisten/protocol"
)

// 内存中的监听器：synctest 的时钟只在 bubble 内的全部 goroutine 都持久阻塞时前进，阻塞在真实网络连接上不算持久阻塞。
type pipeListener struct {
	conns  chan net.Conn
	closed chan struct{}
	once   sync.Once
	// 在 dial 之前设置。
	onServerClose func()
}

type hookedConn struct {
	net.Conn
	once    sync.Once
	onClose func()
}

func (c *hookedConn) Close() error {
	c.once.Do(c.onClose)
	return c.Conn.Close()
}

func newPipeListener() *pipeListener {
	return &pipeListener{conns: make(chan net.Conn), closed: make(chan struct{})}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *pipeListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *pipeListener) Addr() net.Addr { return &net.TCPAddr{} }

func (l *pipeListener) dial(ctx context.Context, _, _ string) (net.Conn, error) {
	server, client := net.Pipe()
	conn := server
	if l.onServerClose != nil {
		conn = &hookedConn{Conn: server, onClose: l.onServerClose}
	}
	select {
	case l.conns <- conn:
		return client, nil
	case <-l.closed:
		return nil, net.ErrClosed
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type spanFields struct{ slog.Handler }

func (h spanFields) Handle(ctx context.Context, r slog.Record) error {
	if sc := trace.SpanContextFromContext(ctx); sc.IsSampled() {
		r.AddAttrs(slog.String("trace_id", sc.TraceID().String()), slog.String("span_id", sc.SpanID().String()))
	}
	return h.Handler.Handle(ctx, r)
}

func (h spanFields) WithAttrs(attrs []slog.Attr) slog.Handler {
	return spanFields{h.Handler.WithAttrs(attrs)}
}

func (h spanFields) WithGroup(name string) slog.Handler { return spanFields{h.Handler.WithGroup(name)} }

type wsFixture struct {
	identity *identity.Service
	lobby    *lobby.Service
	admin    *admin.Service
	gateway  *realtime.Gateway
	node     *porttest.Node
	preparer *porttest.Preparer
	client   *http.Client
	listener *pipeListener
	metrics  *prometheus.Registry
	spans    *tracetest.SpanRecorder
	// 这些场景都不应产生日志；需要日志的用例读取之后要清空它。
	logs *bytes.Buffer
}

// 只能在 synctest 的 bubble 中调用。
func newWSFixture(t *testing.T, store *porttest.Store, snapshots port.RoomSnapshots, directory port.RoomDirectory) *wsFixture {
	t.Helper()
	return newWSFixtureWithTickets(t, store, store, snapshots, directory)
}

// tickets 用来校验票据。只能在 synctest 的 bubble 中调用。
func newWSFixtureWithTickets(t *testing.T, store *porttest.Store, tickets port.Tickets, snapshots port.RoomSnapshots, directory port.RoomDirectory) *wsFixture {
	t.Helper()
	music := newCatalog(directory, 10)
	node := porttest.NewRedis().Node("a")
	preparer := porttest.NewPreparer()
	host, gateway := realtime.New(snapshots, store, music, preparer, realtime.Cluster{Instance: "a", Leases: node, Liveness: node, Bus: node}, slog.New(slog.DiscardHandler))
	ctx, stop := context.WithCancel(context.Background())
	wait, err := realtime.Start(ctx, host, gateway)
	if err != nil {
		t.Fatal(err)
	}
	l := lobby.New(directory, node)
	f := &wsFixture{
		identity: identity.New(store, directory, tickets, gateway),
		lobby:    l,
		admin:    admin.New("secret", l, store, store, store, porttest.NewNetEaseQRLogin(), porttest.NewQQQRLogin(), porttest.NewBilibiliQRLogin(), porttest.NewYouTubeCookies(), gateway),
		gateway:  gateway,
		node:     node,
		preparer: preparer,
		listener: newPipeListener(),
		metrics:  prometheus.NewRegistry(),
		spans:    tracetest.NewSpanRecorder(),
	}
	var logs bytes.Buffer
	f.logs = &logs
	services := Services{Identity: f.identity, Lobby: l, Admin: f.admin, Gateway: gateway, Catalog: music}
	tracer := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(f.spans))
	handler := SameOrigin([]string{allowedOrigin, "http://localhost:9876"}, New(services, f.metrics, tracer, slog.New(logctx.NewHandler(spanFields{slog.NewJSONHandler(&logs, nil)}))))
	listener := f.listener
	server := &http.Server{Handler: handler}
	go func() { _ = server.Serve(listener) }()
	transport := &http.Transport{DialContext: listener.dial}
	f.client = &http.Client{Transport: transport}
	t.Cleanup(func() {
		if err := host.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
		if err := gateway.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
		stop()
		wait()
		_ = server.Close()
		transport.CloseIdleConnections()
		if logs.Len() != 0 {
			t.Errorf("这些场景都不应产生日志：\n%s", logs.String())
		}
	})
	return f
}

func (f *wsFixture) enter(t *testing.T, roomID string) (string, member.Member, string) {
	t.Helper()
	session, err := f.identity.CreateMember(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if roomID == "" {
		if roomID, err = f.lobby.CreateRoom(t.Context(), lobby.NewRoom{Name: "临时"}); err != nil {
			t.Fatal(err)
		}
	}
	ticket, err := f.identity.IssueTicket(t.Context(), session.Member.ID, roomID, "")
	if err != nil {
		t.Fatal(err)
	}
	return roomID, session.Member, ticket
}

func (f *wsFixture) dial(t *testing.T, header http.Header) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.Dial(t.Context(), "ws://alisten.test/ws", &websocket.DialOptions{HTTPClient: f.client, HTTPHeader: header})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })
	return conn
}

func (f *wsFixture) join(t *testing.T, roomID, ticket string) *websocket.Conn {
	t.Helper()
	conn := f.dial(t, nil)
	writeFrame(t, conn, hello(roomID, ticket))
	if m := readMessage(t, conn); m["error"] != nil {
		t.Fatalf("握手失败：%v", m)
	}
	return conn
}

func writeFrame(t *testing.T, conn *websocket.Conn, frame string) {
	t.Helper()
	if err := conn.Write(t.Context(), websocket.MessageText, []byte(frame)); err != nil {
		t.Fatal(err)
	}
}

func hello(roomID, ticket string) string {
	return `{"kind":"request","type":"hello","id":"h1","data":{"roomId":"` + roomID + `","ticket":"` + ticket + `","protocolVersion":"` + protocol.Version + `"}}`
}

func readMessage(t *testing.T, conn *websocket.Conn) map[string]any {
	t.Helper()
	_, frame, err := conn.Read(t.Context())
	if err != nil {
		t.Fatalf("读消息：%v", err)
	}
	return decodeMessage(t, frame)
}

// 服务端生成的时间戳换成 1，便于与期望的报文比较。
func decodeMessage(t *testing.T, frame []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(frame, &m); err != nil {
		t.Fatal(err)
	}
	return normalize(m).(map[string]any)
}

func normalize(v any) any {
	switch v := v.(type) {
	case map[string]any:
		for k, x := range v {
			if n, ok := x.(float64); ok && n > 0 && (strings.HasSuffix(k, "AtMs") || k == "serverTimeMs") {
				v[k] = float64(1)
				continue
			}
			v[k] = normalize(x)
		}
	case []any:
		for i := range v {
			v[i] = normalize(v[i])
		}
	}
	return v
}

func checkMessage(t *testing.T, conn *websocket.Conn, want string) {
	t.Helper()
	var w any
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatal(err)
	}
	if got, want := jsonOf(t, readMessage(t, conn)), jsonOf(t, w); got != want {
		t.Errorf("收到\n%s\n期望\n%s", got, want)
	}
}

func checkClosed(t *testing.T, conn *websocket.Conn, code websocket.StatusCode) {
	t.Helper()
	_, frame, err := conn.Read(t.Context())
	if got := websocket.CloseStatus(err); got != code {
		t.Errorf("关闭码 = %d（%v %s），期望 %d", got, err, frame, code)
	}
}

func TestHandshakeTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		f := newWSFixture(t, store, store, store)
		conn := f.dial(t, nil)
		start := time.Now()
		checkClosed(t, conn, protocol.CloseHandshakeTimeout)
		if elapsed := time.Since(start); elapsed != helloTimeout {
			t.Errorf("%v 后断开，期望 %v", elapsed, helloTimeout)
		}
	})
}

type stallingDirectory struct {
	*porttest.Store
	returned chan struct{}
	lookup   atomic.Pointer[context.Context]
}

func (d *stallingDirectory) FindRoom(ctx context.Context, _ string) (port.RoomRecord, error) {
	d.lookup.Store(&ctx)
	<-ctx.Done()
	close(d.returned)
	return port.RoomRecord{}, ctx.Err()
}

func TestHandshakeTimeoutCancelsLookups(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		directory := &stallingDirectory{Store: store, returned: make(chan struct{})}
		f := newWSFixture(t, store, store, directory)
		// 关闭连接时同步检查查询是否已被取消，结果不取决于 goroutine 的调度。
		var cancelledFirst atomic.Bool
		f.listener.onServerClose = func() {
			if ctx := directory.lookup.Load(); ctx != nil && (*ctx).Err() != nil {
				cancelledFirst.Store(true)
			}
		}
		conn := f.dial(t, nil)
		start := time.Now()
		writeFrame(t, conn, hello("01a15778-1911-7a28-8b2e-e08c2a25f388", "不存在"))
		checkClosed(t, conn, protocol.CloseHandshakeTimeout)
		if elapsed := time.Since(start); elapsed != helloTimeout {
			t.Errorf("%v 后断开，期望 %v", elapsed, helloTimeout)
		}
		synctest.Wait()
		select {
		case <-directory.returned:
		default:
			t.Fatal("以 4408 断开之后，握手中的查询仍在等待")
		}
		if cancelledFirst.Load() {
			t.Error("连接以 4408 断开之前，握手中的查询就已经取消了")
		}
	})
}

// 握手要能查到房间，所以 armed 之后才 panic。
type panickingDirectory struct {
	*porttest.Store
	armed atomic.Bool
}

func (d *panickingDirectory) FindRoom(ctx context.Context, ref string) (port.RoomRecord, error) {
	if d.armed.Load() {
		panic("房间目录坏了")
	}
	return d.Store.FindRoom(ctx, ref)
}

func TestPanickingRequestFailsOnlyThatRequest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		directory := &panickingDirectory{Store: store}
		f := newWSFixture(t, store, store, directory)
		roomID, m, ticket := f.enter(t, "")
		conn := f.join(t, roomID, ticket)

		directory.armed.Store(true)
		writeFrame(t, conn, `{"kind":"request","type":"search.tracks","id":"s1","data":{"source":"netease","keyword":"晴天","page":1,"pageSize":10}}`)
		checkMessage(t, conn, `{"kind":"response","type":"search.tracks","id":"s1","data":{},"error":{"code":"INTERNAL","message":"服务器出错了，请稍后再试。"}}`)

		var entry map[string]any
		if err := json.Unmarshal(f.logs.Bytes(), &entry); err != nil {
			t.Fatalf("日志应只有一行 JSON：%v\n%s", err, f.logs)
		}
		stack, _ := entry["stack"].(string)
		if entry["level"] != "ERROR" || entry["panic"] != "房间目录坏了" || entry["type"] != "search.tracks" || !strings.Contains(stack, "panickingDirectory") {
			t.Errorf("日志 = %v，期望记录 panic 的值和请求类型，stack 字段是 panic 处的调用栈", entry)
		}
		if entry["room_id"] != roomID || entry["member_id"] != m.ID {
			t.Errorf("日志 = %v，期望带着会话的房间 %s 和成员 %s", entry, roomID, m.ID)
		}
		f.logs.Reset()

		writeFrame(t, conn, `{"kind":"request","type":"chat.send","id":"c1","data":{"content":"还在"}}`)
		self := `{"avatarHash":"","id":"` + m.ID + `","name":"` + m.Name + `"}`
		checkMessage(t, conn, `{"kind":"event","type":"chat.message","term":1,"seq":2,"data":{"member":`+self+`,"content":"还在","sentAtMs":1}}`)
		checkMessage(t, conn, `{"kind":"response","type":"chat.send","id":"c1","data":{}}`)
	})
}

type brokenDirectory struct {
	*porttest.Store
	armed atomic.Bool
}

func (d *brokenDirectory) FindRoom(ctx context.Context, ref string) (port.RoomRecord, error) {
	if d.armed.Load() {
		return port.RoomRecord{}, errors.New("房间目录坏了")
	}
	return d.Store.FindRoom(ctx, ref)
}

func TestUnexpectedRequestErrorIsLoggedOnceInTheRequestSpan(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		directory := &brokenDirectory{Store: store}
		f := newWSFixture(t, store, store, directory)
		roomID, m, ticket := f.enter(t, "")
		conn := f.join(t, roomID, ticket)

		directory.armed.Store(true)
		writeFrame(t, conn, `{"kind":"request","type":"search.tracks","id":"s1","data":{"source":"netease","keyword":"晴天","page":1,"pageSize":10}}`)
		checkMessage(t, conn, `{"kind":"response","type":"search.tracks","id":"s1","data":{},"error":{"code":"INTERNAL","message":"服务器出错了，请稍后再试。"}}`)
		synctest.Wait()

		var request sdktrace.ReadOnlySpan
		for _, span := range f.spans.Ended() {
			if span.Name() == "WS search.tracks" {
				request = span
			}
		}
		if request == nil {
			t.Fatal("没有这个请求的 span")
		}
		lines := slices.Collect(strings.Lines(strings.TrimSpace(f.logs.String())))
		if len(lines) != 1 {
			t.Fatalf("日志有 %d 行，期望恰好 1 行：\n%s", len(lines), f.logs.String())
		}
		var entry map[string]any
		if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
			t.Fatal(err)
		}
		want := map[string]any{
			"type": "search.tracks", "error": "房间目录坏了", "room_id": roomID, "member_id": m.ID,
			"trace_id": request.SpanContext().TraceID().String(), "span_id": request.SpanContext().SpanID().String(),
		}
		for key, value := range want {
			if entry[key] != value {
				t.Errorf("日志的 %s = %v，期望 %v：%v", key, entry[key], value, entry)
			}
		}
		f.logs.Reset()
	})
}

func TestHandshakeFailures(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		f := newWSFixture(t, store, store, store)
		roomID, _, ticket := f.enter(t, "")
		other, _, _ := f.enter(t, "")
		failure := func(code, message string) string {
			return `{"kind":"response","type":"hello","id":"h1","data":{},"error":{"code":"` + code + `","message":"` + message + `"}}`
		}
		tests := []struct {
			name  string
			frame string
			want  string
			code  websocket.StatusCode
		}{
			{"第一帧不是 hello", `{"kind":"request","type":"room.sync","id":"r1","data":{}}`,
				`{"kind":"response","type":"room.sync","id":"r1","data":{},"error":{"code":"INVALID_ARGUMENT","message":"第一帧必须是 hello"}}`, protocol.CloseInvalidArgument},
			{"不是 JSON", `hello`,
				`{"kind":"event","type":"error","data":{"code":"INVALID_ARGUMENT","message":"消息必须是 JSON 对象"}}`, protocol.CloseInvalidArgument},
			{"缺少字段", `{"kind":"request","type":"hello","id":"h1","data":{"roomId":"` + roomID + `"}}`,
				failure("INVALID_ARGUMENT", "data.protocolVersion：缺少必填字段"), protocol.CloseInvalidArgument},
			{"协议版本不一致", `{"kind":"request","type":"hello","id":"h1","data":{"roomId":"` + roomID + `","ticket":"` + ticket + `","protocolVersion":"0000000000000000"}}`,
				failure("CLIENT_OUTDATED", "前端与服务端的协议版本不一致，请刷新页面"), protocol.CloseClientOutdated},
			{"房间 ID 不是 UUID", hello("hall", ticket), failure("NOT_FOUND", "房间不存在"), protocol.CloseNotFound},
			{"房间不存在", hello("01a15778-1911-7a28-8b2e-e08c2a25f388", "不存在"), failure("NOT_FOUND", "房间不存在"), protocol.CloseNotFound},
			{"票据无效", hello(roomID, "不存在"), failure("UNAUTHORIZED", "票据无效"), protocol.CloseUnauthorized},
			{"票据不属于这个房间", hello(other, ticket), failure("UNAUTHORIZED", "票据无效"), protocol.CloseUnauthorized},
		}
		for _, tt := range tests {
			conn := f.dial(t, nil)
			writeFrame(t, conn, tt.frame)
			checkMessage(t, conn, tt.want)
			checkClosed(t, conn, tt.code)
			if t.Failed() {
				t.Fatalf("场景：%s", tt.name)
			}
		}
	})
}

func TestHandshakeWhileRedisIsDown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		f := newWSFixture(t, store, store, store)
		roomID, _, ticket := f.enter(t, "")
		f.node.SetDown(true)
		conn := f.dial(t, nil)
		writeFrame(t, conn, hello(roomID, ticket))
		checkMessage(t, conn, `{"kind":"response","type":"hello","id":"h1","data":{},"error":{"code":"UNAVAILABLE","message":"房间暂时不可用，请稍后再试。"}}`)
		checkClosed(t, conn, protocol.CloseUnavailable)
		f.node.SetDown(false)
	})
}

func TestSessionRequests(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		f := newWSFixture(t, store, store, store)
		roomID, m, ticket := f.enter(t, "")
		conn := f.dial(t, nil)
		writeFrame(t, conn, hello(roomID, ticket))
		self := `{"avatarHash":"","id":"` + m.ID + `","name":"` + m.Name + `"}`
		snapshot := func(seq int) string {
			return fmt.Sprintf(`{"term":1,"seq":%d,"room":{"id":"%s","name":"临时","description":"","kind":"ephemeral"},"playMode":"sequential","nowPlaying":null,"pending":null,"items":[],"members":{"sessions":[%s],"onlineCount":1}}`, seq, roomID, self)
		}
		// 加入时的在线列表事件已经包含在快照中，不再单独发送。
		checkMessage(t, conn, `{"kind":"response","type":"hello","id":"h1","data":{"member":`+self+`,"serverTimeMs":1,"room":`+snapshot(1)+`}}`)

		writeFrame(t, conn, `{"kind":"request","type":"ping","id":"p1","data":{"clientTimeMs":1234}}`)
		checkMessage(t, conn, `{"kind":"response","type":"ping","id":"p1","data":{"clientTimeMs":1234,"serverTimeMs":1}}`)

		writeFrame(t, conn, `{"kind":"request","type":"chat.send","id":"c1","data":{"content":"你好"}}`)
		checkMessage(t, conn, `{"kind":"event","type":"chat.message","term":1,"seq":2,"data":{"member":`+self+`,"content":"你好","sentAtMs":1}}`)
		checkMessage(t, conn, `{"kind":"response","type":"chat.send","id":"c1","data":{}}`)

		writeFrame(t, conn, `[]`)
		checkMessage(t, conn, `{"kind":"event","type":"error","data":{"code":"INVALID_ARGUMENT","message":"消息必须是 JSON 对象"}}`)
		if err := conn.Write(t.Context(), websocket.MessageBinary, []byte(`{}`)); err != nil {
			t.Fatal(err)
		}
		checkMessage(t, conn, `{"kind":"event","type":"error","data":{"code":"INVALID_ARGUMENT","message":"只接受 UTF-8 编码的 JSON 文本帧"}}`)
		writeFrame(t, conn, hello(roomID, ticket))
		checkMessage(t, conn, `{"kind":"response","type":"hello","id":"h1","data":{},"error":{"code":"INVALID_ARGUMENT","message":"已经完成握手"}}`)
		writeFrame(t, conn, `{"kind":"request","type":"queue.clear","id":"x1","data":{}}`)
		checkMessage(t, conn, `{"kind":"response","type":"queue.clear","id":"x1","data":{},"error":{"code":"INVALID_ARGUMENT","message":"type：未知的请求类型"}}`)
		writeFrame(t, conn, `{"kind":"request","type":"recommend.list","id":"x2","data":{}}`)
		checkMessage(t, conn, `{"kind":"response","type":"recommend.list","id":"x2","data":{"items":[{"meta":{"album":"七里香","artist":"周杰伦","coverUrl":"","durationMs":299000,"pageUrl":"https://music.163.com/#/song?id=185811","title":"七里香"},"track":{"id":"185811","source":"netease"}}]}}`)

		writeFrame(t, conn, `{"kind":"request","type":"room.sync","id":"s1","data":{}}`)
		checkMessage(t, conn, `{"kind":"response","type":"room.sync","id":"s1","data":{"room":`+snapshot(2)+`}}`)
	})
}

func TestFrameSizeLimit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		f := newWSFixture(t, store, store, store)
		roomID, _, ticket := f.enter(t, "")
		conn := f.join(t, roomID, ticket)

		ping := `{"kind":"request","type":"ping","id":"p1","data":{"clientTimeMs":1}}`
		writeFrame(t, conn, ping+strings.Repeat(" ", maxBodyBytes-len(ping)))
		checkMessage(t, conn, `{"kind":"response","type":"ping","id":"p1","data":{"clientTimeMs":1,"serverTimeMs":1}}`)
		writeFrame(t, conn, ping+strings.Repeat(" ", maxBodyBytes-len(ping)+1))
		checkClosed(t, conn, websocket.StatusMessageTooBig)
	})
}

func TestIdleSessionIsDisconnected(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		f := newWSFixture(t, store, store, store)
		roomID, _, ticket := f.enter(t, "")
		conn := f.join(t, roomID, ticket)
		// 客户端要一直读取，Ping 才能收到 pong。
		frames := make(chan []byte)
		closed := make(chan error, 1)
		go func() {
			for {
				_, frame, err := conn.Read(context.Background())
				if err != nil {
					closed <- err
					return
				}
				frames <- frame
			}
		}()
		ping := `{"kind":"request","type":"ping","id":"p1","data":{"clientTimeMs":1}}`

		time.Sleep(idleTimeout - time.Second)
		writeFrame(t, conn, ping)
		<-frames
		time.Sleep(idleTimeout - time.Second)
		if err := conn.Ping(t.Context()); err != nil {
			t.Fatalf("ping 控制帧：%v", err)
		}
		time.Sleep(idleTimeout - time.Second)
		writeFrame(t, conn, ping)
		<-frames

		time.Sleep(idleTimeout - time.Nanosecond)
		synctest.Wait()
		select {
		case err := <-closed:
			t.Fatalf("不到 30 秒就断开了：%v", err)
		default:
		}
		time.Sleep(time.Nanosecond)
		synctest.Wait()
		select {
		case err := <-closed:
			if websocket.CloseStatus(err) != -1 {
				t.Errorf("空闲的连接应直接断开，实际收到关闭帧：%v", err)
			}
		default:
			t.Error("30 秒没有收到任何帧之后连接应已断开")
		}
	})
}

// 房间忙于落盘时，请求都在等待房间回复。
type slowSnapshots struct {
	*porttest.Store
	release chan struct{}
}

func (s slowSnapshots) SaveRoom(ctx context.Context, term uint32, snap room.Snapshot, plays []room.RecordPlay) error {
	<-s.release
	return s.Store.SaveRoom(ctx, term, snap, plays)
}

func TestOutstandingRequestLimit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		snapshots := slowSnapshots{Store: store, release: make(chan struct{})}
		f := newWSFixture(t, store, snapshots, store)
		roomID, _, ticket := f.enter(t, "")
		conn := f.join(t, roomID, ticket)

		for i := range 16 {
			writeFrame(t, conn, fmt.Sprintf(`{"kind":"request","type":"settings.setPlayMode","id":"m%d","data":{"playMode":"random"}}`, i))
		}
		writeFrame(t, conn, `{"kind":"request","type":"settings.setPlayMode","id":"m16","data":{"playMode":"random"}}`)
		checkMessage(t, conn, `{"kind":"response","type":"settings.setPlayMode","id":"m16","data":{},"error":{"code":"RATE_LIMITED","message":"操作太快了，请稍后再试。"}}`)
		writeFrame(t, conn, `{"kind":"request","type":"ping","id":"p1","data":{"clientTimeMs":1}}`)
		checkMessage(t, conn, `{"kind":"response","type":"ping","id":"p1","data":{"clientTimeMs":1,"serverTimeMs":1}}`)

		close(snapshots.release)
		responses := 0
		for responses < 16 {
			if m := readMessage(t, conn); m["kind"] == "response" {
				if m["error"] != nil {
					t.Errorf("请求失败：%v", m)
				}
				responses++
			}
		}
		want := map[string]float64{"settings.setPlayMode/ok": 16, "settings.setPlayMode/RATE_LIMITED": 1}
		if got := commandCounts(t, f.metrics); !maps.Equal(got, want) {
			t.Errorf("alisten_room_commands_total = %v，期望 %v", got, want)
		}
	})
}

func TestSessionCloseCodes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := porttest.New()
		f := newWSFixture(t, store, store, store)
		locked, err := f.admin.CreateRoom(t.Context(), admin.NewRoom{Name: "上锁"})
		if err != nil {
			t.Fatal(err)
		}
		deleted, err := f.admin.CreateRoom(t.Context(), admin.NewRoom{Name: "删除"})
		if err != nil {
			t.Fatal(err)
		}
		_, _, lockedTicket := f.enter(t, locked)
		_, _, deletedTicket := f.enter(t, deleted)
		ephemeral, _, ephemeralTicket := f.enter(t, "")
		revoked := f.join(t, locked, lockedTicket)
		gone := f.join(t, deleted, deletedTicket)
		restarted := f.join(t, ephemeral, ephemeralTicket)

		password := "芝麻开门"
		if err := f.admin.UpdateRoom(t.Context(), locked, admin.RoomChange{Password: &password}); err != nil {
			t.Fatal(err)
		}
		checkClosed(t, revoked, protocol.CloseUnauthorized)
		if err := f.admin.DeleteRoom(t.Context(), deleted); err != nil {
			t.Fatal(err)
		}
		checkClosed(t, gone, protocol.CloseNotFound)
		// Shutdown 会等待会话断开，而内存连接上的写入要等对方读取，所以一边关闭一边读取。
		shutdown := make(chan error, 1)
		go func() { shutdown <- f.gateway.Shutdown(t.Context()) }()
		checkClosed(t, restarted, protocol.CloseServiceRestart)
		if err := <-shutdown; err != nil {
			t.Fatal(err)
		}
	})
}
