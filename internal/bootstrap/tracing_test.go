package bootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func TestLoadTracing(t *testing.T) {
	cases := []struct {
		name     string
		endpoint *string
		traces   *string
		want     bool
		wantErr  bool
	}{
		{name: "未设置时不导出"},
		{name: "OTLP/HTTP 的基地址", endpoint: ptr("http://otel-collector:4318"), want: true},
		{name: "基地址带路径前缀", endpoint: ptr("https://otlp.example/otel"), want: true},
		{name: "只设置完整的追踪地址", traces: ptr("http://otel-collector:4318/v1/traces"), want: true},
		{name: "完整的追踪地址可以不以 /v1/traces 结尾", traces: ptr("https://otlp.example/traces"), want: true},
		{name: "两个都设置", endpoint: ptr("http://otel-collector:4318"), traces: ptr("http://tempo:4318/v1/traces"), want: true},
		{name: "基地址带用户信息", endpoint: ptr("http://user:SECRET@otel-collector:4318"), wantErr: true},
		{name: "基地址已经以 /v1/traces 结尾", endpoint: ptr("http://otel-collector:4318/v1/traces"), wantErr: true},
		{name: "基地址以 /v1/traces/ 结尾", endpoint: ptr("http://otel-collector:4318/otel/v1/traces/"), wantErr: true},
		{name: "追踪地址带用户信息", traces: ptr("http://user:SECRET@otel-collector:4318/v1/traces"), wantErr: true},
		{name: "基地址有效而追踪地址无效", endpoint: ptr("http://otel-collector:4318"), traces: ptr("tempo:4318"), wantErr: true},
		{name: "追踪地址有效而基地址无效", endpoint: ptr("otel-collector:4318"), traces: ptr("http://tempo:4318/v1/traces"), wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for name, value := range map[string]*string{"OTEL_EXPORTER_OTLP_ENDPOINT": tc.endpoint, "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": tc.traces} {
				if value == nil {
					unsetenv(t, name)
				} else {
					t.Setenv(name, *value)
				}
			}
			got, err := loadTracing()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("期望报错，实际得到 %v", got)
				}
				for _, value := range []*string{tc.endpoint, tc.traces} {
					if value != nil && (strings.Contains(err.Error(), *value) || strings.Contains(err.Error(), "SECRET")) {
						t.Errorf("错误 %q 回显了地址", err)
					}
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("loadTracing() = %v, %v，期望 %v", got, err, tc.want)
			}
		})
	}
}

func TestTracesAreExportedOverOTLP(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	var bodies [][]byte
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		paths = append(paths, r.Method+" "+r.URL.Path+" "+r.Header.Get("Content-Type"))
		bodies = append(bodies, body)
		mu.Unlock()
	}))
	t.Cleanup(collector.Close)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", collector.URL+"/otel")

	tracer, flush, err := newTracerProvider(t.Context(), true, "alisten-serve", "实例甲")
	if err != nil {
		t.Fatal(err)
	}
	_, span := tracer.Tracer("test").Start(t.Context(), "导出的 span")
	span.End()
	if err := flush(t.Context()); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(paths) != 1 || paths[0] != "POST /otel/v1/traces application/x-protobuf" {
		t.Fatalf("收到的请求 = %q，期望一次 POST /otel/v1/traces", paths)
	}
	for _, want := range []string{"导出的 span", "service.name", "alisten-serve", "service.instance.id", "实例甲", "service.version", version} {
		if !bytes.Contains(bodies[0], []byte(want)) {
			t.Errorf("导出的内容中没有 %q", want)
		}
	}
}

func TestNoSpansWithoutExport(t *testing.T) {
	tracer, flush, err := newTracerProvider(t.Context(), false, "alisten-serve", "实例甲")
	if err != nil {
		t.Fatal(err)
	}
	ctx, span := tracer.Tracer("test").Start(t.Context(), "不导出的 span")
	span.End()
	if span.IsRecording() || trace.SpanContextFromContext(ctx).IsValid() {
		t.Error("不导出时 span 不应记录，也不应有 trace ID")
	}
	if err := flush(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestOpenTelemetryLogsAreJSON(t *testing.T) {
	var buf bytes.Buffer
	routeOpenTelemetryLogs(newLogger(&buf, slog.LevelInfo).With("component", "otel"))
	t.Cleanup(func() { routeOpenTelemetryLogs(newLogger(io.Discard, slog.LevelInfo)) })

	otel.Handle(errors.New("导出失败"))
	// 格式错误的环境变量由导出器自己记一条错误日志。
	t.Setenv("OTEL_EXPORTER_OTLP_TIMEOUT", "很久")
	if _, err := otlptracehttp.New(t.Context()); err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("期望两行日志，实际：\n%s", buf.String())
	}
	handled, logged := decodeLine(t, []byte(lines[0])), decodeLine(t, []byte(lines[1]))
	if handled["level"] != "WARN" || handled["error"] != "导出失败" || handled["component"] != "otel" {
		t.Errorf("报告的错误 = %v，期望 WARN 级别、带着错误", handled)
	}
	fields, _ := logged["otel"].(map[string]any)
	if logged["level"] != "ERROR" || logged["component"] != "otel" || fields["input"] != redacted || fields["err"] == nil {
		t.Errorf("导出器的日志 = %v，期望 ERROR 级别，otel 分组中有错误、变量的值已脱敏", logged)
	}
}

// 逐个读取 token：解码成 map 时，后一个值会静默覆盖前一个，看不出重复。
func duplicateKeys(t *testing.T, line string) []string {
	t.Helper()
	type object struct {
		keys    map[string]bool
		wantKey bool
	}
	var stack []*object
	var duplicates []string
	valueDone := func() {
		if n := len(stack); n > 0 && stack[n-1].keys != nil {
			stack[n-1].wantKey = true
		}
	}
	decoder := json.NewDecoder(strings.NewReader(line))
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return duplicates
		}
		if err != nil {
			t.Fatalf("日志不是 JSON：%v\n%s", err, line)
		}
		if delim, ok := token.(json.Delim); ok {
			switch delim {
			case '{':
				stack = append(stack, &object{keys: map[string]bool{}, wantKey: true})
			case '[':
				stack = append(stack, &object{})
			default:
				stack = stack[:len(stack)-1]
				valueDone()
			}
			continue
		}
		if n := len(stack); n > 0 && stack[n-1].wantKey {
			key := token.(string)
			if stack[n-1].keys[key] {
				duplicates = append(duplicates, key)
			}
			stack[n-1].keys[key] = true
			stack[n-1].wantKey = false
			continue
		}
		valueDone()
	}
}

// debug 级别下 OpenTelemetry 在创建 tracer 时记录的日志带有自己的 version 字段，不能与日志的固定字段 version 重名。
func TestOpenTelemetryDebugLogsHaveNoDuplicateKeys(t *testing.T) {
	var buf bytes.Buffer
	routeOpenTelemetryLogs(newLogger(&buf, slog.LevelDebug).With("component", "otel"))
	t.Cleanup(func() { routeOpenTelemetryLogs(newLogger(io.Discard, slog.LevelInfo)) })
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://otel-collector:4318")

	tracer, flush, err := newTracerProvider(t.Context(), true, "alisten-serve", "实例甲")
	if err != nil {
		t.Fatal(err)
	}
	tracer.Tracer("test", trace.WithInstrumentationVersion("9.9.9"))
	if err := flush(t.Context()); err != nil {
		t.Fatal(err)
	}

	var created bool
	for line := range strings.Lines(strings.TrimSpace(buf.String())) {
		if duplicates := duplicateKeys(t, line); len(duplicates) > 0 {
			t.Errorf("日志中重复的键 %q：%s", duplicates, line)
		}
		entry := decodeLine(t, []byte(line))
		if entry["version"] != version {
			t.Errorf("version = %v，期望固定字段 %q：%s", entry["version"], version, line)
		}
		fields, _ := entry["otel"].(map[string]any)
		if _, ok := fields["version"]; entry["msg"] == "Tracer created" && ok {
			created = true
		}
	}
	if !created {
		t.Errorf("日志中没有创建 tracer 的记录，其 version 应在 otel 分组中：\n%s", buf.String())
	}
}

// 导出器把写错的环境变量的原值记进自己的日志。把请求头写成 "Authorization: Bearer …" 这种常见的错误格式时，
// 追踪后端的凭证会以 ERROR 级别进入日志。
func TestOpenTelemetryLogsDoNotEchoMalformedHeaders(t *testing.T) {
	var buf bytes.Buffer
	routeOpenTelemetryLogs(newLogger(&buf, slog.LevelInfo).With("component", "otel"))
	t.Cleanup(func() { routeOpenTelemetryLogs(newLogger(io.Discard, slog.LevelInfo)) })
	t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "Authorization Bearer SUPERSECRETTOKEN,x-api-key=abc%zzSECRET2")

	if _, err := otlptracehttp.New(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "parse headers") || !strings.Contains(buf.String(), "escape header value") {
		t.Fatalf("导出器应记录两条错误日志，实际：\n%s", buf.String())
	}
	for _, secret := range []string{"SUPERSECRETTOKEN", "SECRET2"} {
		if strings.Contains(buf.String(), secret) {
			t.Errorf("日志中有请求头的原值 %s：\n%s", secret, buf.String())
		}
	}
}

func TestExitLogFollowsTraceFlush(t *testing.T) {
	failed := errors.New("子命令运行失败")
	cases := []struct {
		name   string
		flush  error
		err    error
		expect []string
	}{
		{name: "正常退出", expect: []string{"服务已退出"}},
		{name: "导出失败的告警排在退出日志前面", flush: context.DeadlineExceeded, expect: []string{"导出剩下的追踪失败", "服务已退出"}},
		{name: "出错退出没有退出日志", flush: context.DeadlineExceeded, err: failed, expect: []string{"导出剩下的追踪失败"}},
		{name: "出错退出而导出成功", err: failed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			exitAfterFlush(tc.err, func(context.Context) error { return tc.flush }, newLogger(&buf, slog.LevelInfo), "服务已退出")
			var got []string
			for line := range strings.Lines(strings.TrimSpace(buf.String())) {
				got = append(got, decodeLine(t, []byte(line))["msg"].(string))
			}
			if !slices.Equal(got, tc.expect) {
				t.Errorf("日志依次为 %q，期望 %q", got, tc.expect)
			}
		})
	}
}

// HTTP 和 WebSocket 请求的 span 在回复发出之后才结束，所以要等。
func awaitSpan(t *testing.T, spans *tracetest.SpanRecorder, name string, traceID trace.TraceID) sdktrace.ReadOnlySpan {
	t.Helper()
	for deadline := time.Now().Add(messageWait); ; time.Sleep(10 * time.Millisecond) {
		for _, span := range spans.Ended() {
			if span.Name() == name && (!traceID.IsValid() || span.SpanContext().TraceID() == traceID) {
				return span
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("没有名为 %q 的 span", name)
		}
	}
}

func TestHTTPRequestsStartTraces(t *testing.T) {
	contractEnv(t, startPostgres(t)(t), startValkey(t)(t))
	svc := startService(t)
	hall := createPersistentRoom(t, svc, "大厅")
	_, ticket := enterRoom(t, svc, hall)
	connect(t, svc, hall, ticket)
	openStream(t, svc.server, "Authorization: Bearer "+ticket)
	const clientTrace = "4bf92f3577b34da6a3ce929d0e0e4736"
	if line, status, _, body := send(t, svc.server, "GET /api/rooms\ntraceparent: 00-"+clientTrace+"-00f067aa0ba902b7-01"); status != http.StatusOK {
		t.Fatalf("%s = %d %s", line, status, body)
	}

	traces := map[trace.TraceID]string{}
	for _, name := range []string{"POST /api/admin/rooms", "POST /api/members", "POST /api/rooms/{id}/tickets", "WS hello", "GET /api/rooms"} {
		span := awaitSpan(t, svc.spans, name, trace.TraceID{})
		if span.SpanKind() != trace.SpanKindServer || span.Parent().IsValid() {
			t.Errorf("%s 的类型 = %v、父 span = %v，期望没有父 span 的 server span", name, span.SpanKind(), span.Parent())
		}
		if other, ok := traces[span.SpanContext().TraceID()]; ok {
			t.Errorf("%s 与 %s 在同一条 trace 中", name, other)
		}
		traces[span.SpanContext().TraceID()] = name
	}
	if got := awaitSpan(t, svc.spans, "GET /api/rooms", trace.TraceID{}).SpanContext().TraceID().String(); got == clientTrace {
		t.Error("GET /api/rooms 沿用了客户端带来的 trace")
	}
	// 升级请求和事件流的 span 要到连接断开才结束，所以看开始的 span，而不是结束的。
	for _, span := range svc.spans.Started() {
		if path := attributes(span)["url.path"]; path == "/ws" || path == "/api/room/events" {
			t.Errorf("%s 的请求开始了 span %s", path, span.Name())
		}
	}
}
