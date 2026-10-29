package bootstrap

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/app/port/porttest"
)

func recordingTracer(t *testing.T) (*sdktrace.TracerProvider, *tracetest.SpanRecorder) {
	t.Helper()
	spans := tracetest.NewSpanRecorder()
	tracer := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans))
	t.Cleanup(func() { _ = tracer.Shutdown(context.Background()) })
	return tracer, spans
}

func childSpans(t *testing.T, spans *tracetest.SpanRecorder, parent trace.Span) []string {
	t.Helper()
	var got []string
	for _, span := range spans.Ended() {
		if span.SpanContext().SpanID() == parent.SpanContext().SpanID() {
			continue
		}
		if span.Parent().SpanID() != parent.SpanContext().SpanID() || span.SpanKind() != trace.SpanKindClient {
			t.Errorf("%s 的父 span = %v、类型 = %v，期望是请求的 span 之下的 client span", span.Name(), span.Parent(), span.SpanKind())
		}
		got = append(got, span.Name()+" "+span.Status().Code.String())
	}
	return got
}

func attributes(span sdktrace.ReadOnlySpan) map[string]string {
	attrs := map[string]string{}
	for _, kv := range span.Attributes() {
		attrs[string(kv.Key)] = kv.Value.String()
	}
	return attrs
}

func TestOutboundHTTPSpans(t *testing.T) {
	var mu sync.Mutex
	var traceparents []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		traceparents = append(traceparents, r.Header.Get("Traceparent"))
		mu.Unlock()
		if r.URL.Path == "/missing" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, "音频")
	}))
	t.Cleanup(upstream.Close)
	tracer, spans := recordingTracer(t)
	client := &http.Client{Transport: traceHTTP(http.DefaultTransport, tracer)}
	get := func(ctx context.Context, path string) *http.Response {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, upstream.URL+path+"?vkey=secret", nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	_ = get(t.Context(), "/audio").Body.Close()
	if n := len(spans.Ended()); n != 0 {
		t.Fatalf("后台的请求记了 %d 个 span", n)
	}
	ctx, parent := tracer.Tracer("test").Start(t.Context(), "请求")
	resp := get(ctx, "/audio")
	if n := len(spans.Ended()); n != 0 {
		t.Error("响应体读完之前 span 就结束了")
	}
	if _, err := io.ReadAll(resp.Body); err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	_ = get(ctx, "/missing").Body.Close()
	parent.End()

	if got, want := childSpans(t, spans, parent), []string{"HTTP GET Unset", "HTTP GET Error"}; !slices.Equal(got, want) {
		t.Errorf("span = %q，期望 %q", got, want)
	}
	for i, span := range spans.Ended()[:2] {
		attrs := attributes(span)
		if attrs["http.request.method"] != "GET" || attrs["server.address"] != "127.0.0.1" || attrs["http.response.status_code"] != []string{"200", "404"}[i] {
			t.Errorf("%s 的属性 = %v", span.Name(), attrs)
		}
		for key, value := range attrs {
			if strings.Contains(value, "vkey") || strings.Contains(value, "/audio") {
				t.Errorf("属性 %s = %q 带出了请求的地址", key, value)
			}
		}
	}
	mu.Lock()
	defer mu.Unlock()
	for _, header := range traceparents {
		if header != "" {
			t.Errorf("音源收到了 traceparent：%q", header)
		}
	}
}

func TestObjectStoreSpans(t *testing.T) {
	tracer, spans := recordingTracer(t)
	store := porttest.NewMediaStore()
	objects := traceObjects(store, tracer)
	if _, err := objects.Exists(t.Context(), "link/a/audio.mp3"); err != nil {
		t.Fatal(err)
	}
	if n := len(spans.Ended()); n != 0 {
		t.Fatalf("后台的调用记了 %d 个 span", n)
	}

	ctx, parent := tracer.Tracer("test").Start(t.Context(), "任务")
	if err := objects.Put(ctx, "link/a/audio.mp3", strings.NewReader("mp3"), "audio/mpeg"); err != nil {
		t.Fatal(err)
	}
	if ok, err := objects.Exists(ctx, "link/a/audio.mp3"); !ok || err != nil {
		t.Fatalf("Exists = %v, %v", ok, err)
	}
	parent.End()
	store.Fail(errors.New("连接中断"))
	if err := objects.Delete(ctx, "link/a/audio.mp3"); err == nil {
		t.Fatal("对象存储出错时 Delete 应返回错误")
	}

	if got, want := childSpans(t, spans, parent), []string{"S3 PutObject Unset", "S3 HeadObject Unset", "S3 DeleteObject Error"}; !slices.Equal(got, want) {
		t.Errorf("span = %q，期望 %q", got, want)
	}
}

func TestDatabaseAndRedisSpans(t *testing.T) {
	contractEnv(t, startPostgres(t)(t), startValkey(t)(t))
	tracer, spans := recordingTracer(t)
	cfg, err := loadDatabaseConfig()
	if err != nil {
		t.Fatal(err)
	}
	traceDatabase(cfg, tracer)
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	opts, err := loadRedisConfig()
	if err != nil {
		t.Fatal(err)
	}
	rdb := goredis.NewClient(opts)
	t.Cleanup(func() { _ = rdb.Close() })
	traceRedis(rdb, tracer)

	if _, err := pool.Exec(t.Context(), "SELECT 1"); err != nil {
		t.Fatal(err)
	}
	if err := rdb.Ping(t.Context()).Err(); err != nil {
		t.Fatal(err)
	}
	if n := len(spans.Ended()); n != 0 {
		t.Fatalf("后台的调用记了 %d 个 span", n)
	}

	ctx, parent := tracer.Tracer("test").Start(t.Context(), "请求")
	if _, err := pool.Exec(ctx, "-- name: Probe :exec\nSELECT 1"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "SELECT 2"); err != nil {
		t.Fatal(err)
	}
	if err := rdb.Get(ctx, "不存在的键").Err(); !errors.Is(err, goredis.Nil) {
		t.Fatalf("GET = %v，期望 redis.Nil", err)
	}
	parent.End()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	// Script.Run 先发 EVALSHA，脚本不在 Redis 的缓存中时收到 NOSCRIPT，再改用 EVAL。
	if err := goredis.NewScript("return 1").Run(ctx, rdb, nil).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "SELECT * FROM 不存在的表"); err == nil {
		t.Fatal("查询不存在的表应报错")
	}
	if err := rdb.Do(ctx, "不存在的命令").Err(); err == nil {
		t.Fatal("不存在的命令应报错")
	}

	if got, want := childSpans(t, spans, parent), []string{"Probe Unset", "SELECT Unset", "get Unset", "ping Unset", "evalsha Unset", "eval Unset", "SELECT Error", "不存在的命令 Error"}; !slices.Equal(got, want) {
		t.Errorf("span = %q，期望 %q", got, want)
	}
	for _, span := range spans.Ended() {
		if description := span.Status().Description; description != "" {
			t.Errorf("%s 的状态描述 = %q，期望不带错误的文本", span.Name(), description)
		}
	}
}

func TestQueryName(t *testing.T) {
	cases := map[string]string{
		"-- name: JobGetAvailable :many\nWITH locked_jobs AS (SELECT 1)": "JobGetAvailable",
		"select 1":        "SELECT",
		"\n\t begin":      "BEGIN",
		"commit\nignored": "COMMIT",
		"":                "QUERY",
		" \n":             "QUERY",
		"-- name: ":       "QUERY",
		"-- name:  \n\t":  "QUERY",
	}
	for sql, want := range cases {
		if got := queryName(sql); got != want {
			t.Errorf("queryName(%q) = %q，期望 %q", sql, got, want)
		}
	}
	long := "-- name: JobGetAvailable :many\n" + strings.Repeat("SELECT id FROM river_job WHERE state = 'available';\n", 40)
	if n := testing.AllocsPerRun(100, func() { _ = queryName(long) }); n != 0 {
		t.Errorf("queryName 处理 %d 字节的语句分配了 %v 次内存，期望 0 次", len(long), n)
	}
}

// 使用记录 span 的 provider：这样没有记录 span 是因为 ctx 中没有已采样的 span，而不是因为 provider 什么都不做。
func TestCallsOutsideSampledTracesAllocateNothing(t *testing.T) {
	tracer, _ := recordingTracer(t)
	ctx := t.Context()
	queries := databaseSpans{tracer: tracer.Tracer("test")}
	query := pgx.TraceQueryStartData{SQL: "-- name: JobGetAvailable :many\nSELECT 1"}
	command := redisSpans{tracer: tracer.Tracer("test")}.ProcessHook(func(context.Context, goredis.Cmder) error { return nil })
	cmd := goredis.NewCmd(ctx, "get", "key")
	for name, call := range map[string]func(){
		"数据库查询":    func() { queries.TraceQueryStart(ctx, nil, query) },
		"Redis 命令": func() { _ = command(ctx, cmd) },
	} {
		if n := testing.AllocsPerRun(100, call); n != 0 {
			t.Errorf("%s分配了 %v 次内存，期望 0 次", name, n)
		}
	}
}

type failedDownloads struct{ err error }

func (d failedDownloads) Download(context.Context, string) (port.LinkDownload, func(), error) {
	return port.LinkDownload{}, nil, d.err
}

type failedTransport struct{ err error }

func (t failedTransport) RoundTrip(*http.Request) (*http.Response, error) { return nil, t.err }

func TestSpanStatusDoesNotCarryErrorText(t *testing.T) {
	tracer, spans := recordingTracer(t)
	ctx, parent := tracer.Tracer("test").Start(t.Context(), "任务")
	defer parent.End()
	failure := errors.New("下载 https://music.example/audio?vkey=SECRET 失败")

	store := porttest.NewMediaStore()
	store.Fail(failure)
	objects := traceObjects(store, tracer)
	_, _ = objects.Exists(ctx, "link/a/audio.mp3")
	_ = objects.Delete(ctx, "link/a/audio.mp3")
	_, _, _ = traceDownloads(failedDownloads{failure}, tracer).Download(ctx, "https://example.com/a")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://music.example/audio", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = (&http.Client{Transport: traceHTTP(failedTransport{failure}, tracer)}).Do(req)

	failed := 0
	for _, span := range spans.Ended() {
		if span.Status().Code != codes.Error {
			continue
		}
		failed++
		if description := span.Status().Description; description != "" {
			t.Errorf("%s 的状态描述 = %q，期望不带错误的文本", span.Name(), description)
		}
	}
	if failed != 4 {
		t.Errorf("出错的 span 有 %d 个，期望 4 个", failed)
	}
}
