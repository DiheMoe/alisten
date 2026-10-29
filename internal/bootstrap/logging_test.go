package bootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"go.opentelemetry.io/otel/trace"

	"github.com/dihemoe/alisten/internal/lib/logctx"
)

func TestLoggerWritesFixedFields(t *testing.T) {
	var buf bytes.Buffer
	newLogger(&buf, slog.LevelInfo).With("component", "test").Info("消息")

	entry := decodeLine(t, buf.Bytes())
	want := map[string]any{"msg": "消息", "level": "INFO", "service": "alisten", "component": "test", "version": version}
	for key, value := range want {
		if entry[key] != value {
			t.Errorf("%s = %v，期望 %v", key, entry[key], value)
		}
	}
	if _, ok := entry["time"]; !ok {
		t.Error("缺少 time 字段")
	}
}

func TestLoggerRedactsCredentials(t *testing.T) {
	var buf bytes.Buffer
	newLogger(&buf, slog.LevelInfo).With("component", "test").Info("消息",
		"member_token", "t",
		"Cookie", "c",
		"roomPassword", "p",
		"tickets", []string{"a"},
		"S3_SECRET_ACCESS_KEY", "s",
		"Authorization", "Bearer x",
		slog.Group("request", "ticket", "k"),
		"room_id", "r1",
	)

	entry := decodeLine(t, buf.Bytes())
	for _, key := range []string{"member_token", "Cookie", "roomPassword", "tickets", "S3_SECRET_ACCESS_KEY", "Authorization"} {
		if entry[key] != redacted {
			t.Errorf("%s = %v，期望已脱敏", key, entry[key])
		}
	}
	if group, _ := entry["request"].(map[string]any); group["ticket"] != redacted {
		t.Errorf("request.ticket = %v，期望已脱敏", group["ticket"])
	}
	if entry["room_id"] != "r1" {
		t.Errorf("room_id = %v，不应脱敏", entry["room_id"])
	}
}

func exampleSpan(t *testing.T, flags trace.TraceFlags) trace.SpanContext {
	t.Helper()
	traceID, err := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	if err != nil {
		t.Fatal(err)
	}
	spanID, err := trace.SpanIDFromHex("00f067aa0ba902b7")
	if err != nil {
		t.Fatal(err)
	}
	return trace.NewSpanContext(trace.SpanContextConfig{TraceID: traceID, SpanID: spanID, TraceFlags: flags})
}

// 打开了 group 的 logger（River 的）也要把关联字段写在顶层。
func TestLoggerAddsCorrelationFields(t *testing.T) {
	sc := exampleSpan(t, trace.FlagsSampled)
	ctx := trace.ContextWithSpanContext(context.Background(), sc)
	ctx = logctx.WithMember(logctx.WithRoom(logctx.WithRequest(ctx, "a/r1"), "room-1"), "member-1")
	want := map[string]any{
		"trace_id":   "4bf92f3577b34da6a3ce929d0e0e4736",
		"span_id":    "00f067aa0ba902b7",
		"request_id": "a/r1",
		"room_id":    "room-1",
		"member_id":  "member-1",
		"component":  "test",
	}
	for _, group := range []string{"", "river"} {
		var buf bytes.Buffer
		newLogger(&buf, slog.LevelInfo).With("component", "test").WithGroup(group).InfoContext(ctx, "消息", "attempt", 1)

		entry := decodeLine(t, buf.Bytes())
		for key, value := range want {
			if entry[key] != value {
				t.Errorf("group %q：%s = %v，期望 %v", group, key, entry[key], value)
			}
		}
	}
}

func TestLoggerSkipsUnsampledSpans(t *testing.T) {
	ctx := trace.ContextWithSpanContext(context.Background(), exampleSpan(t, 0))
	var buf bytes.Buffer
	newLogger(&buf, slog.LevelInfo).InfoContext(ctx, "消息")

	entry := decodeLine(t, buf.Bytes())
	for _, key := range []string{"trace_id", "span_id"} {
		if value, ok := entry[key]; ok {
			t.Errorf("%s = %v，期望没有这个字段", key, value)
		}
	}
}

func decodeLine(t *testing.T, out []byte) map[string]any {
	t.Helper()
	lines := bytes.Split(bytes.TrimSuffix(out, []byte("\n")), []byte("\n"))
	if len(lines) != 1 {
		t.Fatalf("期望一行日志，实际 %d 行：\n%s", len(lines), out)
	}
	var entry map[string]any
	if err := json.Unmarshal(lines[0], &entry); err != nil {
		t.Fatalf("日志不是 JSON：%v\n%s", err, out)
	}
	return entry
}
