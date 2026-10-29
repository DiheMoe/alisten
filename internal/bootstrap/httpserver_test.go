package bootstrap

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/codes"
)

func TestRecoverPanicsLogsStackInOneField(t *testing.T) {
	var buf bytes.Buffer
	logger := newLogger(&buf, slog.LevelInfo).With("component", "test")
	handler := recoverPanics(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}), logger)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("状态码 = %d，期望 500", rec.Code)
	}
	entry := decodeLine(t, buf.Bytes())
	if entry["panic"] != "boom" {
		t.Errorf("panic = %v，期望 boom", entry["panic"])
	}
	if stack, _ := entry["stack"].(string); !strings.Contains(stack, "goroutine") {
		t.Errorf("stack 字段中没有调用栈：%v", entry["stack"])
	}
}

func TestRecoverPanicsLeavesAbortHandlerToNetHTTP(t *testing.T) {
	var buf bytes.Buffer
	logger := newLogger(&buf, slog.LevelInfo).With("component", "test")
	handler := recoverPanics(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	}), logger)

	defer func() {
		value := recover()
		if err, ok := value.(error); !ok || !errors.Is(err, http.ErrAbortHandler) {
			t.Errorf("recover() = %v，期望 http.ErrAbortHandler", value)
		}
		if buf.Len() != 0 {
			t.Errorf("不应记录日志：\n%s", buf.String())
		}
	}()
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil))
}

func TestPanickingRequestIsAnErrorSpan(t *testing.T) {
	tracer, spans := recordingTracer(t)
	var buf bytes.Buffer
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/boom", func(http.ResponseWriter, *http.Request) { panic("boom") })
	handler := publicHandler(mux, true, tracer, newLogger(&buf, slog.LevelInfo).With("component", "http"))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/boom", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("状态码 = %d，期望 500", rec.Code)
	}
	ended := spans.Ended()
	if len(ended) != 1 {
		t.Fatalf("记了 %d 个 span，期望 1 个", len(ended))
	}
	span := ended[0]
	if got := attributes(span)["http.response.status_code"]; span.Name() != "GET /api/boom" || span.Status().Code != codes.Error || got != "500" {
		t.Errorf("span = %s、状态 = %v、状态码 = %s，期望以路由命名、出错、500", span.Name(), span.Status(), got)
	}
	entry := decodeLine(t, buf.Bytes())
	if entry["panic"] != "boom" || entry["trace_id"] != span.SpanContext().TraceID().String() || entry["span_id"] != span.SpanContext().SpanID().String() {
		t.Errorf("panic 的日志 = %v，期望带着 span %v", entry, span.SpanContext())
	}
}

func TestPublicHandlerWithoutTracing(t *testing.T) {
	tracer, spans := recordingTracer(t)
	var buf bytes.Buffer
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/boom", func(http.ResponseWriter, *http.Request) { panic("boom") })
	handler := publicHandler(mux, false, tracer, newLogger(&buf, slog.LevelInfo).With("component", "http"))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/boom", nil))

	if n := len(spans.Started()); n != 0 {
		t.Errorf("不导出追踪时开始了 %d 个 span", n)
	}
	if entry := decodeLine(t, buf.Bytes()); rec.Code != http.StatusInternalServerError || entry["panic"] != "boom" {
		t.Errorf("状态码 = %d、日志 = %v，期望 panic 照样恢复", rec.Code, entry)
	}
}

func TestHTTPServerLimitsHeaderSize(t *testing.T) {
	srv := newHTTPServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), newLogger(io.Discard, slog.LevelInfo))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	client := &http.Client{Timeout: 5 * time.Second}
	for _, tt := range []struct {
		size int
		want int
	}{
		{1 << 10, http.StatusOK},
		{80 << 10, http.StatusRequestHeaderFieldsTooLarge},
	} {
		resp, err := client.Get("http://" + ln.Addr().String() + "/?q=" + strings.Repeat("a", tt.size))
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != tt.want {
			t.Errorf("%d 字节的查询串 = %d，期望 %d", tt.size, resp.StatusCode, tt.want)
		}
	}
}
