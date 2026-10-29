package bootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// alice 不回应关闭握手，网关要等她最多 5 秒；
// 这段时间里 bob 已经收到 1012，如果会话先于房间关闭，房间此时还在处理命令。
func TestShutdownReleasesRoomsBeforeClosingSessions(t *testing.T) {
	contractEnv(t, startPostgres(t)(t), startValkey(t)(t))
	svc := startService(t)
	request := func(text string) map[string]string {
		t.Helper()
		line, status, _, body := send(t, svc.server, text)
		if status != http.StatusCreated {
			t.Fatalf("%s 的状态码 = %d：%s", line, status, body)
		}
		var v map[string]string
		if err := json.Unmarshal(body, &v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	roomID := request("POST /api/rooms\n\n{\"name\": \"临时\", \"description\": \"\"}")["id"]
	join := func() (*websocket.Conn, string) {
		t.Helper()
		var session struct{ MemberToken string }
		_, _, _, body := send(t, svc.server, "POST /api/members\n\n{}")
		if err := json.Unmarshal(body, &session); err != nil {
			t.Fatal(err)
		}
		ticket := request("POST /api/rooms/" + roomID + "/tickets\nAuthorization: Bearer " + session.MemberToken + "\n\n{}")["ticket"]
		conn := dial(t, svc.server, "")
		hello := `{"kind":"request","type":"hello","id":"h1","data":{"roomId":"` + roomID + `","ticket":"` + ticket + `","protocolVersion":"` + protocolVersion(t) + `"}}`
		if err := conn.Write(t.Context(), websocket.MessageText, []byte(hello)); err != nil {
			t.Fatal(err)
		}
		if _, frame, err := conn.Read(t.Context()); err != nil || !strings.Contains(string(frame), `"type":"hello"`) || strings.Contains(string(frame), `"error"`) {
			t.Fatalf("握手：%s, %v", frame, err)
		}
		return conn, ticket
	}
	_, _ = join()
	bob, bobTicket := join()
	// bob 加入时 alice 收到的在线列表留在她的连接中不读：她既不读取消息，也不回应关闭握手。

	var logs bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- svc.public.shutdown(context.Background(), svc.server.Config, newLogger(&logs, slog.LevelInfo))
	}()
	ctx, cancel := context.WithTimeout(t.Context(), messageWait)
	defer cancel()
	if _, frame, err := bob.Read(ctx); websocket.CloseStatus(err) != websocket.StatusServiceRestart {
		t.Fatalf("bob 应收到 1012，实际收到 %s, %v", frame, err)
	}
	line, status, _, body := send(t, svc.server, "POST /api/room/messages\nAuthorization: Bearer "+bobTicket+"\n\n{\"content\": \"还在吗\"}")
	if status != http.StatusServiceUnavailable {
		t.Errorf("会话关闭时 %s = %d %s，期望房间已释放、返回 503", line, status, body)
	}

	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var steps []string
	for line := range strings.Lines(strings.TrimSpace(logs.String())) {
		steps = append(steps, decodeLine(t, []byte(line))["msg"].(string))
	}
	if got := strings.Join(steps, "，"); got != "已释放本实例的全部房间，已关闭全部 WebSocket 会话，已停止执行准备任务" {
		t.Errorf("退出的日志依次为 %s", got)
	}
}

func TestShutdownStopsJobsWhenPublicPortCannotDrain(t *testing.T) {
	contractEnv(t, startPostgres(t)(t), startValkey(t)(t))
	svc := startService(t)

	entered, release := make(chan struct{}), make(chan struct{})
	stalled := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		close(entered)
		<-release
	}))
	t.Cleanup(stalled.Close)
	t.Cleanup(func() { close(release) })
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, stalled.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		if resp, err := stalled.Client().Do(req); err == nil {
			_ = resp.Body.Close()
		}
	}()
	<-entered

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	var logs bytes.Buffer
	err = svc.public.shutdown(ctx, stalled.Config, newLogger(&logs, slog.LevelInfo))
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "关闭公开端口") {
		t.Errorf("公开端口排不空时的错误 = %v，期望关闭公开端口超过期限", err)
	}
	select {
	case <-svc.public.jobs.Stopped():
	default:
		t.Error("公开端口排不空时 River client 没有停止")
	}
	if !strings.Contains(logs.String(), "已停止执行准备任务") {
		t.Errorf("共用的期限用完之后，停止 River client 仍应在自己的期限内完成：\n%s", logs.String())
	}
}

// Helm chart 和 compose 设置的退出宽限期都是 30 秒，与 Kubernetes 的默认值相同。
func TestShutdownFitsGracePeriod(t *testing.T) {
	if total := drainDelay + shutdownTimeout + jobsStopTimeout; total > 30*time.Second {
		t.Errorf("退出最长要 %v，超过了 30 秒的退出宽限期", total)
	}
}

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) messages(t *testing.T) []string {
	t.Helper()
	b.mu.Lock()
	text := b.buf.String()
	b.mu.Unlock()
	var msgs []string
	for line := range strings.Lines(strings.TrimSpace(text)) {
		msgs = append(msgs, decodeLine(t, []byte(line))["msg"].(string))
	}
	return msgs
}

// 接受连接但从不应答，span 要到退出时才导出：这样导出剩下的 span 会超时，而不是很快失败。
func unresponsiveCollector(t *testing.T) {
	t.Helper()
	release := make(chan struct{})
	collector := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	// Cleanup 按注册的逆序执行：先放行，Close 才不会等待没有返回的请求。
	t.Cleanup(collector.Close)
	t.Cleanup(func() { close(release) })
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", collector.URL)
	t.Setenv("OTEL_BSP_SCHEDULE_DELAY", "3600000")
}

// 监听地址的端口不能写 0，所以先让系统挑一个空闲的端口。
func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().String()
}

func eventually(t *testing.T, what string, finished <-chan struct{}, cond func() bool) {
	t.Helper()
	deadline := time.After(60 * time.Second)
	for !cond() {
		select {
		case <-finished:
			if !cond() {
				t.Fatalf("等待%s时 runServe 已经返回", what)
			}
		case <-deadline:
			t.Fatalf("60 秒内没有等到%s", what)
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func readyStatus(opsAddr string) int {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://" + opsAddr + "/readyz")
	if err != nil {
		return 0
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

func TestServeExitsGracefullyOnSIGTERM(t *testing.T) {
	contractEnv(t, startPostgres(t)(t), startValkey(t)(t))
	listen, opsListen := freeAddr(t), freeAddr(t)
	t.Setenv("ALISTEN_LISTEN", listen)
	t.Setenv("ALISTEN_OPS_LISTEN", opsListen)
	unresponsiveCollector(t)
	if err := runMigrate(newLogger(io.Discard, slog.LevelInfo)); err != nil {
		t.Fatal(err)
	}

	var logs lockedBuffer
	var runErr error
	var returned time.Time
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		runErr = runServe(newLogger(&logs, slog.LevelInfo))
		returned = time.Now()
	}()

	eventually(t, "/readyz 返回 200", finished, func() bool { return readyStatus(opsListen) == http.StatusOK })
	// 这个 HTTP 请求记下一个 span，它到退出时才导出。
	if resp, err := (&http.Client{Timeout: 10 * time.Second}).Get("http://" + listen + "/api/rooms"); err != nil {
		t.Fatal(err)
	} else {
		_ = resp.Body.Close()
	}
	signalled := time.Now()
	if err := syscall.Kill(syscall.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	// 退出开始的日志写出时，就绪标志已经清除；入口摘除之前（5 秒）运维端口仍然可以访问。
	eventually(t, "退出开始的日志", finished, func() bool {
		return slices.Contains(logs.messages(t), "收到退出信号，等待入口摘除本实例")
	})
	if status := readyStatus(opsListen); status == http.StatusOK {
		t.Error("收到退出信号之后 /readyz 仍然返回 200")
	}

	select {
	case <-finished:
	case <-time.After(60 * time.Second):
		t.Fatal("收到 SIGTERM 60 秒之后 runServe 仍没有返回")
	}
	if runErr != nil {
		t.Errorf("收到 SIGTERM 之后 runServe 返回 %v，期望 nil", runErr)
	}
	if elapsed := returned.Sub(signalled); elapsed < drainDelay {
		t.Errorf("收到 SIGTERM %v 之后就退出了，期望先等 %v 让入口摘除本实例", elapsed, drainDelay)
	}
	want := []string{
		"服务已启动",
		"收到退出信号，等待入口摘除本实例",
		"已释放本实例的全部房间",
		"已关闭全部 WebSocket 会话",
		"已停止执行准备任务",
		"导出剩下的追踪失败",
		"服务已退出",
	}
	var got []string
	for _, msg := range logs.messages(t) {
		if slices.Contains(want, msg) {
			got = append(got, msg)
		}
	}
	if !slices.Equal(got, want) {
		t.Errorf("退出的日志依次为\n%s\n期望\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

type tcpProxy struct {
	listener net.Listener
	target   string

	mu    sync.Mutex
	cut   bool
	conns []net.Conn
}

func startProxy(t *testing.T, target string) *tcpProxy {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &tcpProxy{listener: listener, target: target}
	t.Cleanup(p.disconnect)
	go func() {
		for {
			client, err := listener.Accept()
			if err != nil {
				return
			}
			go p.forward(client)
		}
	}()
	return p
}

func (p *tcpProxy) forward(client net.Conn) {
	server, err := net.Dial("tcp", p.target)
	if err != nil {
		_ = client.Close()
		return
	}
	p.mu.Lock()
	if p.cut {
		p.mu.Unlock()
		_ = client.Close()
		_ = server.Close()
		return
	}
	p.conns = append(p.conns, client, server)
	p.mu.Unlock()
	go func() {
		_, _ = io.Copy(server, client)
		_ = server.Close()
	}()
	_, _ = io.Copy(client, server)
	_ = client.Close()
}

func (p *tcpProxy) disconnect() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cut = true
	_ = p.listener.Close()
	for _, c := range p.conns {
		_ = c.Close()
	}
}

// 会话不回应关闭握手，关闭它要等最多 5 秒：PostgreSQL 断开之后、依赖检查停止之前，至少完成一次检查。
func TestServeLogsNoReadinessDuringExit(t *testing.T) {
	database := startPostgres(t)(t)
	contractEnv(t, database, startValkey(t)(t))
	if err := runMigrate(newLogger(io.Discard, slog.LevelInfo)); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(database)
	if err != nil {
		t.Fatal(err)
	}
	proxy := startProxy(t, u.Host)
	u.Host = proxy.listener.Addr().String()
	connectionEnv(t, u.String())
	listen, opsListen := freeAddr(t), freeAddr(t)
	t.Setenv("ALISTEN_LISTEN", listen)
	t.Setenv("ALISTEN_OPS_LISTEN", opsListen)

	var logs lockedBuffer
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		_ = runServe(newLogger(&logs, slog.LevelInfo))
	}()
	eventually(t, "/readyz 返回 200", finished, func() bool { return readyStatus(opsListen) == http.StatusOK })

	call := func(method, path, token, body string) map[string]any {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), method, "http://"+listen+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		var v map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&v); err != nil || resp.StatusCode != http.StatusCreated {
			t.Fatalf("%s %s = %d, %v", method, path, resp.StatusCode, err)
		}
		return v
	}
	token := call(http.MethodPost, "/api/members", "", "{}")["memberToken"].(string)
	roomID := call(http.MethodPost, "/api/rooms", "", `{"name": "临时", "description": ""}`)["id"].(string)
	ticket := call(http.MethodPost, "/api/rooms/"+roomID+"/tickets", token, "{}")["ticket"].(string)
	conn, _, err := websocket.Dial(t.Context(), "ws://"+listen+"/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })
	hello := `{"kind":"request","type":"hello","id":"h1","data":{"roomId":"` + roomID + `","ticket":"` + ticket + `","protocolVersion":"` + protocolVersion(t) + `"}}`
	if err := conn.Write(t.Context(), websocket.MessageText, []byte(hello)); err != nil {
		t.Fatal(err)
	}
	if _, frame, err := conn.Read(t.Context()); err != nil || strings.Contains(string(frame), `"error"`) {
		t.Fatalf("握手：%s, %v", frame, err)
	}
	// 之后不再读取这个连接，也就不会回应关闭握手。

	if err := syscall.Kill(syscall.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	const exiting = "收到退出信号，等待入口摘除本实例"
	eventually(t, "退出开始的日志", finished, func() bool { return slices.Contains(logs.messages(t), exiting) })
	proxy.disconnect()
	select {
	case <-finished:
	case <-time.After(60 * time.Second):
		t.Fatal("收到 SIGTERM 60 秒之后 runServe 仍没有返回")
	}
	msgs := logs.messages(t)
	for _, msg := range msgs[slices.Index(msgs, exiting):] {
		if msg == "依赖检查未通过，实例未就绪" || msg == "依赖检查通过，实例已就绪" {
			t.Errorf("退出开始之后记了就绪检查的日志 %q：\n%s", msg, strings.Join(msgs, "\n"))
		}
	}
}
