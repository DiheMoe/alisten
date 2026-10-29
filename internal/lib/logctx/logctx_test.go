package logctx

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"
)

func record(t *testing.T, ctx context.Context) map[string]any {
	t.Helper()
	var buf bytes.Buffer
	slog.New(NewHandler(slog.NewJSONHandler(&buf, nil))).With("component", "test").InfoContext(ctx, "消息", "error", "出错")
	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("日志不是一行 JSON：%v\n%s", err, buf.Bytes())
	}
	return entry
}

func TestHandlerWritesFieldsFromContext(t *testing.T) {
	ctx := WithJob(WithMember(WithRoom(WithRequest(context.Background(), "a/r1"), "room-1"), "member-1"), 42)
	entry := record(t, ctx)
	want := map[string]any{"request_id": "a/r1", "room_id": "room-1", "member_id": "member-1", "job_id": float64(42), "component": "test", "error": "出错"}
	for key, value := range want {
		if entry[key] != value {
			t.Errorf("%s = %v，期望 %v", key, entry[key], value)
		}
	}
}

func TestInnerContextOverridesOuter(t *testing.T) {
	outer := WithRoom(WithRequest(context.Background(), "a/r1"), "room-1")
	inner := WithRequest(outer, "b/r2")
	if got := record(t, inner); got["request_id"] != "b/r2" || got["room_id"] != "room-1" {
		t.Errorf("内层的日志 = %v，期望 request_id 为 b/r2、room_id 仍为 room-1", got)
	}
	if got := record(t, outer); got["request_id"] != "a/r1" {
		t.Errorf("外层的日志 = %v，期望 request_id 仍为 a/r1", got)
	}
}

func withoutTime(groups []string, a slog.Attr) slog.Attr {
	if len(groups) == 0 && a.Key == slog.TimeKey {
		return slog.Attr{}
	}
	return a
}

func TestFieldsStayAtTopLevelInGroups(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(NewHandler(slog.NewJSONHandler(&buf, &slog.HandlerOptions{ReplaceAttr: withoutTime})))
	logger = logger.With("component", "test").WithGroup("river").With("client", "c1").WithGroup("job")
	logger.InfoContext(WithJob(WithRoom(context.Background(), "room-1"), 42), "消息", "attempt", 1)
	want := `{"level":"INFO","msg":"消息","component":"test","room_id":"room-1","job_id":42,"river":{"client":"c1","job":{"attempt":1}}}` + "\n"
	if got := buf.String(); got != want {
		t.Errorf("日志 = %s期望 %s", got, want)
	}
}

func TestDerivedLoggersAreIndependent(t *testing.T) {
	var buf bytes.Buffer
	// 逐个打开 group、逐个添加字段，记录它们的切片才会有多余的容量。
	groups := slog.New(NewHandler(slog.NewJSONHandler(&buf, &slog.HandlerOptions{ReplaceAttr: withoutTime}))).WithGroup("a").WithGroup("b").WithGroup("c")
	attrs := groups.With("p", 1).With("q", 2).With("r", 3)
	for _, logger := range []*slog.Logger{groups.WithGroup("x"), groups.WithGroup("y"), attrs.With("s", "first"), attrs.With("s", "second"), attrs} {
		logger.Info("消息", "n", 1)
	}
	want := `{"level":"INFO","msg":"消息","a":{"b":{"c":{"x":{"n":1}}}}}
{"level":"INFO","msg":"消息","a":{"b":{"c":{"y":{"n":1}}}}}
{"level":"INFO","msg":"消息","a":{"b":{"c":{"p":1,"q":2,"r":3,"s":"first","n":1}}}}
{"level":"INFO","msg":"消息","a":{"b":{"c":{"p":1,"q":2,"r":3,"s":"second","n":1}}}}
{"level":"INFO","msg":"消息","a":{"b":{"c":{"p":1,"q":2,"r":3,"n":1}}}}
`
	if got := buf.String(); got != want {
		t.Errorf("日志 =\n%s期望\n%s", got, want)
	}
}
