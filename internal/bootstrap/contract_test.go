package bootstrap

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	goredis "github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	tcvalkey "github.com/testcontainers/testcontainers-go/modules/valkey"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"golang.org/x/tools/txtar"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/app/port/porttest"
	"github.com/dihemoe/alisten/internal/app/realtime"
	"github.com/dihemoe/alisten/internal/domain/track"
)

func TestContract(t *testing.T) {
	var files []string
	for _, dir := range []string{"http", "ws"} {
		matches, err := filepath.Glob(filepath.Join("testdata", dir, "*.txtar"))
		if err != nil {
			t.Fatal(err)
		}
		if len(matches) == 0 {
			t.Fatalf("testdata/%s 中没有用例", dir)
		}
		files = append(files, matches...)
	}
	newDatabase, newRedis := startPostgres(t), startValkey(t)
	for _, file := range files {
		name, _ := filepath.Rel("testdata", strings.TrimSuffix(file, ".txtar"))
		t.Run(filepath.ToSlash(name), func(t *testing.T) {
			archive, err := txtar.ParseFile(file)
			if err != nil {
				t.Fatal(err)
			}
			runContract(t, archive.Files, newDatabase(t), newRedis(t))
		})
	}
}

// 整个包共用一个 PostgreSQL 和一个 Valkey 容器：如果每个测试各起一组容器，一次 go test 要在宿主机上创建、删除几十块虚拟网卡，
// 宿主机上的浏览器每次都会当作网络变化而断开连接。
var shared struct {
	postgresOnce sync.Once
	postgres     *tcpostgres.PostgresContainer
	postgresURL  string
	postgresErr  error
	databases    int
	valkeyOnce   sync.Once
	valkey       *tcvalkey.ValkeyContainer
	valkeyURL    string
	valkeyErr    error
	valkeyDBs    int
}

func startPostgres(t *testing.T) func(*testing.T) string {
	t.Helper()
	shared.postgresOnce.Do(func() {
		ctx := context.Background()
		shared.postgres, shared.postgresErr = tcpostgres.Run(ctx, "postgres:18-alpine", tcpostgres.BasicWaitStrategies())
		if shared.postgresErr == nil {
			shared.postgresURL, shared.postgresErr = shared.postgres.ConnectionString(ctx, "sslmode=disable")
		}
	})
	if shared.postgresErr != nil {
		t.Fatalf("启动 PostgreSQL 容器：%v", shared.postgresErr)
	}
	return func(t *testing.T) string {
		conn, err := pgx.Connect(t.Context(), shared.postgresURL)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = conn.Close(context.Background()) }()
		shared.databases++
		name := fmt.Sprintf("test_%d", shared.databases)
		if _, err := conn.Exec(t.Context(), "CREATE DATABASE "+name); err != nil {
			t.Fatal(err)
		}
		u, err := url.Parse(shared.postgresURL)
		if err != nil {
			t.Fatal(err)
		}
		u.Path = "/" + name
		return u.String()
	}
}

func startValkey(t *testing.T) func(*testing.T) string {
	t.Helper()
	shared.valkeyOnce.Do(func() {
		ctx := context.Background()
		shared.valkey, shared.valkeyErr = tcvalkey.Run(ctx, "valkey/valkey:8-alpine", testcontainers.WithCmdArgs("--databases", "1024"))
		if shared.valkeyErr == nil {
			shared.valkeyURL, shared.valkeyErr = shared.valkey.ConnectionString(ctx)
		}
	})
	if shared.valkeyErr != nil {
		t.Fatalf("启动 Valkey 容器：%v", shared.valkeyErr)
	}
	return func(t *testing.T) string {
		shared.valkeyDBs++
		if shared.valkeyDBs >= 1024 {
			t.Fatal("共用的 Valkey 的 1024 个逻辑数据库已经用完")
		}
		return fmt.Sprintf("%s/%d", shared.valkeyURL, shared.valkeyDBs)
	}
}

// B 站的视频：完整链接和 b23.tv 短链都识别成第 2 P；番剧的链接认得出但不能点。下载到的是平台的原始音频和一张封面。
// 付费视频的下载在 worker 的测试中设为被拒绝。
func contractBilibili() *porttest.Bilibili {
	b := porttest.NewBilibili()
	b.AddVideo("BV1xx411c7mD/2", track.Meta{Title: "晴天（钢琴版）", Artist: "某 UP 主", Duration: 2 * time.Second}, []byte("平台给的原始音频"), []byte("jpeg"), "jpg")
	b.AddLink("https://www.bilibili.com/video/BV1xx411c7mD?p=2", "BV1xx411c7mD/2")
	b.AddLink("https://b23.tv/qingtian", "BV1xx411c7mD/2")
	b.RejectLink("https://www.bilibili.com/bangumi/play/ep1", "哔哩哔哩的番剧不能点播")
	b.AddVideo("BV1GJ411x7h7", track.Meta{Title: "付费视频", Artist: "某 UP 主", Duration: time.Minute}, []byte("平台给的原始音频"), nil, "")
	b.AddLink("https://www.bilibili.com/video/BV1GJ411x7h7", "BV1GJ411x7h7")
	return b
}

// 准备时下载到的是一段静音，增益正好是 1，时长是静音的时长。
func contractSources() sources {
	netease := porttest.NewSource(track.NetEase)
	netease.Add("186016", track.Meta{Title: "晴天", Artist: "周杰伦", Album: "叶惠美", CoverURL: "https://p1.music.126.net/example.jpg", PageURL: "https://music.163.com/#/song?id=186016", Duration: 269 * time.Second},
		"[00:01.00]故事的小黄花", porttest.SilentWAV(269500*time.Millisecond))
	netease.Add("185811", track.Meta{Title: "七里香", Artist: "周杰伦", Album: "七里香", CoverURL: "https://p1.music.126.net/qilixiang.jpg", PageURL: "https://music.163.com/#/song?id=185811", Duration: 299 * time.Second},
		"", porttest.SilentWAV(299*time.Second))
	netease.Add("1100", track.Meta{Title: "片头", Artist: "无名", PageURL: "https://music.163.com/#/song?id=1100", Duration: 15 * time.Second},
		"", porttest.SilentWAV(15*time.Second))
	netease.Add("404", track.Meta{Title: "不能播放的歌", Artist: "无名", PageURL: "https://music.163.com/#/song?id=404", Duration: time.Minute}, "", nil)
	netease.Reject("404", "网易云没有提供这首歌的播放地址")
	netease.SetSimilar("186016", "185811")
	netease.AddPlaylist(port.Playlist{ID: "3778678", Name: "周杰伦精选", CoverURL: "https://p1.music.126.net/jay.jpg", Creator: "alice", Description: "", TrackCount: 2, PlayCount: 100}, "186016", "185811")
	bilibili := contractBilibili()
	return sources{
		netease:       netease,
		qq:            porttest.NewSource(track.QQ),
		bilibili:      bilibili,
		bilibiliLinks: bilibili,
		playback:      map[track.Source]port.PlayURLs{track.NetEase: netease},
		neteaseLogin:  porttest.NewNetEaseQRLogin(),
		qqLogin:       porttest.NewQQQRLogin(),
		bilibiliLogin: porttest.NewBilibiliQRLogin(),
	}
}

type service struct {
	server   *httptest.Server
	public   public
	instance string
	metrics  *prometheus.Registry
	logs     *logBuffer
	spans    *tracetest.SpanRecorder
	tracer   *sdktrace.TracerProvider
}

type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *logBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Len()
}

func (b *logBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// 各场景共用一个 Valkey，而 Pub/Sub 不按逻辑库隔离：实例频道靠随机的实例 ID 区分；
// members 和 tracks.prepared 两个频道靠场景依次运行、每个场景结束时停止服务来隔开。
func startService(t *testing.T) service {
	t.Helper()
	if err := runMigrate(newLogger(io.Discard, slog.LevelInfo)); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadServeConfig()
	if err != nil {
		t.Fatal(err)
	}
	spans := tracetest.NewSpanRecorder()
	tracer := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans))
	cfg.tracing = true
	traceDatabase(cfg.database, tracer)
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg.database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	rdb := goredis.NewClient(cfg.redis)
	t.Cleanup(func() { _ = rdb.Close() })
	traceRedis(rdb, tracer)
	logs := &logBuffer{}
	instance := rand.Text()
	metrics := prometheus.NewRegistry()
	p, err := newPublic(cfg, pool, rdb, instance, contractSources(), metrics, func(track.Source, string, time.Duration) {}, tracer, newLogger(logs, slog.LevelInfo))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.start(); err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithCancel(context.Background())
	wait, err := realtime.Start(ctx, p.host, p.gateway)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(p.handler)
	t.Cleanup(func() {
		if err := p.host.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
		if err := p.gateway.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
		server.Close()
		if err := p.jobs.StopAndCancel(context.Background()); err != nil {
			t.Error(err)
		}
		stop()
		wait()
	})
	return service{server: server, public: p, instance: instance, metrics: metrics, logs: logs, spans: spans, tracer: tracer}
}

// 场景依次由以下几节组成：
//   - 可选的 env 放在最前面，每行写成 名称=值；
//   - request 和 response 成对出现，是一次 HTTP 请求和完整的响应：response 依次是状态码、若干行 名称: 值 的响应头和响应体，204 没有响应体；
//   - connect 名称：打开一个 WebSocket 连接，内容是请求头；紧跟 response 时，期望握手被拒绝；
//   - send 名称：发送一帧；receive 名称：读下一条消息，与期望的报文比较；
//   - closed 名称：连接应以给出的关闭码断开；disconnect 名称：客户端断开连接；
//   - stream 名称：打开房间的事件流，内容是请求头；event 名称：读下一个事件，与期望的事件比较，close 之后事件流应结束。
func runContract(t *testing.T, sections []txtar.File, databaseURL, redisURL string) {
	contractEnv(t, databaseURL, redisURL)
	if len(sections) > 0 && sections[0].Name == "env" {
		for line := range strings.Lines(strings.TrimSpace(string(sections[0].Data))) {
			name, value, ok := strings.Cut(strings.TrimSpace(line), "=")
			if !ok {
				t.Fatalf("env 中的 %q 应写成 名称=值", line)
			}
			t.Setenv(name, value)
		}
		sections = sections[1:]
	}
	svc := startService(t)

	vars := map[string]string{"protocolVersion": protocolVersion(t)}
	conns := map[string]*websocket.Conn{}
	streams := map[string]*eventStream{}
	for i := 0; i < len(sections); i++ {
		kind, name, _ := strings.Cut(sections[i].Name, " ")
		data := string(sections[i].Data)
		switch {
		case kind == "request":
			if i+1 == len(sections) || sections[i+1].Name != "response" {
				t.Fatalf("第 %d 节的 request 之后应是 response", i+1)
			}
			i++
			line, status, header, body := send(t, svc.server, expand(t, data, vars))
			checkResponse(t, line, status, header, body, string(sections[i].Data), vars)
		case kind == "connect" && i+1 < len(sections) && sections[i+1].Name == "response":
			i++
			status, header, body := dialRejected(t, svc.server, expand(t, data, vars))
			checkResponse(t, "GET /ws", status, header, body, string(sections[i].Data), vars)
		case kind == "connect":
			conns[name] = dial(t, svc.server, expand(t, data, vars))
		case kind == "send":
			frame := []byte(strings.TrimSpace(expand(t, data, vars)))
			if err := connection(t, conns, name).Write(t.Context(), websocket.MessageText, frame); err != nil {
				t.Fatalf("%s 发送：%v", name, err)
			}
		case kind == "receive":
			receive(t, connection(t, conns, name), name, data, vars)
		case kind == "closed":
			checkClosed(t, connection(t, conns, name), name, data)
		case kind == "disconnect":
			_ = connection(t, conns, name).CloseNow()
			delete(conns, name)
		case kind == "stream":
			streams[name] = openStream(t, svc.server, expand(t, data, vars))
		case kind == "event":
			stream, ok := streams[name]
			if !ok {
				t.Fatalf("事件流 %s 还没有打开", name)
			}
			checkEvent(t, stream, name, data, vars)
		default:
			t.Fatalf("第 %d 节 %q 不是认识的格式", i+1, sections[i].Name)
		}
	}
	if svc.logs.Len() != 0 {
		t.Errorf("这些场景都不应产生日志：\n%s", svc.logs.String())
	}
}

func contractEnv(t *testing.T, databaseURL, redisURL string) {
	unsetenv(t, "ALISTEN_LISTEN")
	unsetenv(t, "ALISTEN_OPS_LISTEN")
	storageEnv(t)
	connectionEnv(t, databaseURL)
	connectionEnv(t, redisURL)
	t.Setenv("ALISTEN_PUBLIC_URL", "https://alisten.example")
	t.Setenv("ALISTEN_ALLOWED_ORIGINS", "https://alisten.example,http://localhost:9876")
	t.Setenv("ALISTEN_ADMIN_TOKEN", "admin-secret")
	t.Setenv("STORAGE_PUBLIC_URL", "https://alisten.example/storage/alisten")
}

func connectionEnv(t *testing.T, connString string) {
	t.Helper()
	u, err := url.Parse(connString)
	if err != nil {
		t.Fatal(err)
	}
	password, _ := u.User.Password()
	name := strings.TrimPrefix(u.Path, "/")
	switch u.Scheme {
	case "postgres":
		t.Setenv("DATABASE_HOST", u.Hostname())
		t.Setenv("DATABASE_PORT", u.Port())
		t.Setenv("DATABASE_USERNAME", u.User.Username())
		t.Setenv("DATABASE_PASSWORD", password)
		t.Setenv("DATABASE_NAME", name)
		t.Setenv("DATABASE_SSLMODE", u.Query().Get("sslmode"))
	case "redis":
		t.Setenv("REDIS_HOST", u.Hostname())
		t.Setenv("REDIS_PORT", u.Port())
		t.Setenv("REDIS_PASSWORD", password)
		t.Setenv("REDIS_DB", name)
	default:
		t.Fatalf("%q 不是 PostgreSQL 或 Redis 的连接串", connString)
	}
}

// bootstrap 不能 import protocol，所以 hello 中的协议版本从生成的 Go 代码中读出。
func protocolVersion(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "protocol", "types.gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`const Version = "([0-9a-f]{16})"`).FindSubmatch(b)
	if m == nil {
		t.Fatal("protocol/types.gen.go 中没有协议版本")
	}
	return string(m[1])
}

func connection(t *testing.T, conns map[string]*websocket.Conn, name string) *websocket.Conn {
	t.Helper()
	conn, ok := conns[name]
	if !ok {
		t.Fatalf("连接 %s 还没有打开", name)
	}
	return conn
}

func wsURL(server *httptest.Server) string {
	return "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"
}

func headers(t *testing.T, text string) http.Header {
	t.Helper()
	header := http.Header{}
	for line := range strings.Lines(strings.TrimSpace(text)) {
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			t.Fatalf("请求头 %q 应写成 名称: 值", line)
		}
		header.Set(strings.TrimSpace(name), strings.TrimSpace(value))
	}
	return header
}

func dial(t *testing.T, server *httptest.Server, header string) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.Dial(t.Context(), wsURL(server), &websocket.DialOptions{HTTPHeader: headers(t, header)})
	if err != nil {
		t.Fatalf("WebSocket 握手：%v", err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })
	return conn
}

func dialRejected(t *testing.T, server *httptest.Server, header string) (int, http.Header, []byte) {
	t.Helper()
	conn, resp, err := websocket.Dial(t.Context(), wsURL(server), &websocket.DialOptions{HTTPHeader: headers(t, header)})
	if err == nil {
		_ = conn.CloseNow()
		t.Fatal("WebSocket 握手应被拒绝")
	}
	if resp == nil {
		t.Fatalf("握手被拒绝时应有 HTTP 响应：%v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, resp.Header, body
}

// 消息总会到达，期限只是为了出错时不一直等下去：最长的一次要等《片头》放完，15 秒。
const messageWait = 25 * time.Second

func receive(t *testing.T, conn *websocket.Conn, name, want string, vars map[string]string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), messageWait)
	defer cancel()
	_, frame, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("%s 读消息：%v", name, err)
	}
	var wantJSON, gotJSON any
	if err := json.Unmarshal([]byte(want), &wantJSON); err != nil {
		t.Fatalf("期望的消息不是 JSON：%v\n%s", err, want)
	}
	if err := json.Unmarshal(frame, &gotJSON); err != nil {
		t.Fatalf("%s 收到的消息不是 JSON：%v\n%s", name, err, frame)
	}
	if err := match(wantJSON, timestamps(gotJSON), vars, "消息"); err != nil {
		t.Fatalf("%s：%v\n期望：%s\n实际：%s", name, err, strings.TrimSpace(want), frame)
	}
}

func checkClosed(t *testing.T, conn *websocket.Conn, name, want string) {
	t.Helper()
	code, err := strconv.Atoi(strings.TrimSpace(want))
	if err != nil {
		t.Fatalf("closed 的内容应是关闭码：%q", want)
	}
	ctx, cancel := context.WithTimeout(t.Context(), messageWait)
	defer cancel()
	_, frame, err := conn.Read(ctx)
	if got := websocket.CloseStatus(err); got != websocket.StatusCode(code) {
		t.Fatalf("%s 的关闭码 = %d（%v %s），期望 %d", name, got, err, frame, code)
	}
}

type eventStream struct {
	body   io.Closer
	reader *bufio.Reader
}

func openStream(t *testing.T, server *httptest.Server, header string) *eventStream {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/api/room/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header = headers(t, header)
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/event-stream" {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("打开事件流 = %d %s：%s", resp.StatusCode, resp.Header.Get("Content-Type"), body)
	}
	return &eventStream{body: resp.Body, reader: bufio.NewReader(resp.Body)}
}

// 返回下一个事件的各行，跳过心跳之类只有注释的事件；事件流结束时返回 nil。
func (s *eventStream) next(t *testing.T, name string) []string {
	t.Helper()
	timeout := time.AfterFunc(messageWait, func() { _ = s.body.Close() })
	defer timeout.Stop()
	var lines []string
	for {
		line, err := s.reader.ReadString('\n')
		if errors.Is(err, io.EOF) && line == "" && len(lines) == 0 {
			return nil
		}
		if err != nil {
			t.Fatalf("%s 读事件：%v", name, err)
		}
		switch line = strings.TrimSuffix(line, "\n"); {
		case line == "" && len(lines) > 0:
			return lines
		case line != "" && !strings.HasPrefix(line, ":"):
			lines = append(lines, line)
		}
	}
}

// data 之前的各行原样比较，data 按 JSON 比较。
func checkEvent(t *testing.T, s *eventStream, name, want string, vars map[string]string) {
	t.Helper()
	wantHead, wantData, ok := strings.Cut(strings.TrimSpace(want), "\ndata: ")
	if !ok {
		t.Fatalf("期望的事件应以 data 结尾：%q", want)
	}
	got := s.next(t, name)
	if got == nil {
		t.Fatalf("%s 的事件流已经结束，期望：%s", name, strings.TrimSpace(want))
	}
	gotHead, gotData := strings.Join(got[:len(got)-1], "\n"), strings.TrimPrefix(got[len(got)-1], "data: ")
	if gotHead != wantHead {
		t.Fatalf("%s 收到\n%s\n期望\n%s", name, gotHead, wantHead)
	}
	var wantJSON, gotJSON any
	if err := json.Unmarshal([]byte(wantData), &wantJSON); err != nil {
		t.Fatalf("期望的 data 不是 JSON：%v\n%s", err, wantData)
	}
	if err := json.Unmarshal([]byte(gotData), &gotJSON); err != nil {
		t.Fatalf("%s 收到的 data 不是 JSON：%v\n%s", name, err, gotData)
	}
	if err := match(wantJSON, timestamps(gotJSON), vars, "事件"); err != nil {
		t.Fatalf("%s：%v\n期望：%s\n实际：%s", name, err, wantData, gotData)
	}
	if gotHead == "event: close" {
		if after := s.next(t, name); after != nil {
			t.Fatalf("%s 的事件流在 close 之后又收到 %q", name, after)
		}
	}
}

// 服务端生成的正数时间戳在期望中统一写为 1：字段名以 AtMs 结尾，或者是 serverTimeMs。
func timestamps(v any) any {
	switch v := v.(type) {
	case map[string]any:
		for key, value := range v {
			if n, ok := value.(float64); ok && n > 0 && (strings.HasSuffix(key, "AtMs") || key == "serverTimeMs") {
				v[key] = float64(1)
			} else {
				v[key] = timestamps(value)
			}
		}
	case []any:
		for i := range v {
			v[i] = timestamps(v[i])
		}
	}
	return v
}

// $ 加小写字母开头的名字是变量：在响应中第一次出现时记下实际的值，之后出现时必须相同；在请求中替换为记下的值。
var variable = regexp.MustCompile(`\$[a-z][A-Za-z0-9]*`)

func expand(t *testing.T, text string, vars map[string]string) string {
	t.Helper()
	return variable.ReplaceAllStringFunc(text, func(v string) string {
		value, ok := vars[v[1:]]
		if !ok {
			t.Fatalf("变量 %s 在使用之前还没有出现在响应中", v)
		}
		return value
	})
}

func send(t *testing.T, server *httptest.Server, request string) (string, int, http.Header, []byte) {
	t.Helper()
	head, body, _ := strings.Cut(strings.TrimSpace(request), "\n\n")
	lines := strings.Split(head, "\n")
	method, path, ok := strings.Cut(lines[0], " ")
	if !ok {
		t.Fatalf("请求的第一行 %q 应为方法和路径", lines[0])
	}
	req, err := http.NewRequestWithContext(t.Context(), method, server.URL+path, strings.NewReader(strings.TrimSpace(body)))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range lines[1:] {
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			t.Fatalf("请求头 %q 应写成 名称: 值", line)
		}
		req.Header.Set(strings.TrimSpace(name), strings.TrimSpace(value))
	}
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return lines[0], resp.StatusCode, resp.Header, got
}

var headerLine = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9-]*): (.*)$`)

// 响应头在响应体之后比较，可以使用响应体中第一次出现的变量。没有列出 Location 时，响应也不能带 Location。
func checkResponse(t *testing.T, line string, status int, header http.Header, body []byte, want string, vars map[string]string) {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(want), "\n")
	wantStatus, err := strconv.Atoi(strings.TrimSpace(lines[0]))
	if err != nil {
		t.Fatalf("期望的响应应以状态码开头：%q", lines[0])
	}
	if status != wantStatus {
		t.Fatalf("%s 的状态码 = %d，期望 %d：\n%s", line, status, wantStatus, body)
	}
	wantHeader := http.Header{}
	rest := lines[1:]
	for ; len(rest) > 0; rest = rest[1:] {
		m := headerLine.FindStringSubmatch(rest[0])
		if m == nil {
			break
		}
		wantHeader.Set(m[1], m[2])
	}
	for name := range header {
		if strings.HasPrefix(name, "Access-Control-") {
			t.Errorf("%s 返回了 CORS 头 %s", line, name)
		}
	}
	wantBody := strings.Join(rest, "\n")
	if status == http.StatusNoContent {
		if wantBody != "" || len(body) > 0 {
			t.Fatalf("%s：204 没有响应体，期望：%q，实际：%q", line, wantBody, body)
		}
		if got := header.Get("Content-Type"); got != "" {
			t.Errorf("%s 的 Content-Type = %q，204 不带 Content-Type", line, got)
		}
	} else {
		if got := header.Get("Content-Type"); got != "application/json" {
			t.Errorf("%s 的 Content-Type = %q", line, got)
		}
		var wantJSON, gotJSON any
		if err := json.Unmarshal([]byte(wantBody), &wantJSON); err != nil {
			t.Fatalf("期望的响应体不是 JSON：%v\n%s", err, wantBody)
		}
		if err := json.Unmarshal(body, &gotJSON); err != nil {
			t.Fatalf("%s 的响应体不是 JSON：%v\n%s", line, err, body)
		}
		if err := match(wantJSON, timestamps(gotJSON), vars, "响应"); err != nil {
			t.Fatalf("%s：%v\n期望：%s\n实际：%s", line, err, wantBody, body)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(wantHeader)) {
		if got, want := header.Get(name), expand(t, wantHeader.Get(name), vars); got != want {
			t.Fatalf("%s 的 %s = %q，期望 %q", line, name, got, want)
		}
	}
	if got := header.Get("Location"); wantHeader.Get("Location") == "" && got != "" {
		t.Fatalf("%s 带了没有列出的 Location：%q", line, got)
	}
}

func match(want, got any, vars map[string]string, path string) error {
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			return fmt.Errorf("%s 应为对象，实际为 %v", path, got)
		}
		for _, key := range slices.Sorted(maps.Keys(g)) {
			if _, ok := w[key]; !ok {
				return fmt.Errorf("%s 多出字段 %s", path, key)
			}
		}
		for _, key := range slices.Sorted(maps.Keys(w)) {
			value, ok := g[key]
			if !ok {
				return fmt.Errorf("%s 缺少字段 %s", path, key)
			}
			if err := match(w[key], value, vars, path+"."+key); err != nil {
				return err
			}
		}
		return nil
	case []any:
		g, ok := got.([]any)
		if !ok || len(g) != len(w) {
			return fmt.Errorf("%s 应为 %d 项的数组，实际为 %v", path, len(w), got)
		}
		for i := range w {
			if err := match(w[i], g[i], vars, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
		return nil
	case string:
		if strings.HasPrefix(w, "$") && variable.FindString(w) == w {
			return bind(w[1:], got, vars, path)
		}
	}
	if !reflect.DeepEqual(want, got) {
		return fmt.Errorf("%s = %v，期望 %v", path, got, want)
	}
	return nil
}

// 不同的变量必须取不同的值：两次签发的 token 或两个房间的 ID 相同时，测试会失败。
func bind(name string, got any, vars map[string]string, path string) error {
	value, ok := got.(string)
	if !ok || value == "" {
		return fmt.Errorf("%s 应为非空字符串，实际为 %v", path, got)
	}
	if bound, ok := vars[name]; ok {
		if value != bound {
			return fmt.Errorf("%s = %q，期望与 $%s 相同：%q", path, value, name, bound)
		}
		return nil
	}
	for other, bound := range vars {
		if bound == value {
			return fmt.Errorf("%s 与 $%s 的值相同：%q", path, other, value)
		}
	}
	vars[name] = value
	return nil
}
