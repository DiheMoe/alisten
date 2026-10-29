package bootstrap

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/dihemoe/alisten/internal/adapter/out/queue"
	"github.com/dihemoe/alisten/internal/domain/track"
)

// 模拟房间已由另一个实例接管：本实例下一次落盘会因任期过期而失败。
func bumpTerm(t *testing.T, databaseURL, roomID string) {
	t.Helper()
	conn, err := pgx.Connect(t.Context(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	if _, err := conn.Exec(t.Context(), "UPDATE rooms SET term = term + 1 WHERE id = $1", roomID); err != nil {
		t.Fatal(err)
	}
}

func logEntries(t *testing.T, logs *logBuffer, msg string) []map[string]any {
	t.Helper()
	var found []map[string]any
	for line := range strings.Lines(strings.TrimSpace(logs.String())) {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("日志不是 JSON：%v\n%s", err, line)
		}
		if entry["msg"] == msg {
			found = append(found, entry)
		}
	}
	return found
}

func TestRoomCommandIsOneTraceAcrossInstances(t *testing.T) {
	databaseURL, redisURL := startPostgres(t)(t), startValkey(t)(t)
	contractEnv(t, databaseURL, redisURL)
	services := []service{startService(t), startService(t)}
	observer := newObserver(t, redisURL)
	hall := createPersistentRoom(t, services[0], "大厅")
	_, botTicket := enterRoom(t, services[0], hall)
	roomRequest(t, services[0], botTicket, "GET /api/room", "")
	var owning, gateway service
	for _, svc := range services {
		if svc.instance == owner(t, observer, hall) {
			owning = svc
		} else {
			gateway = svc
		}
	}
	_, ticket := enterRoom(t, gateway, hall)
	client, hello := connect(t, gateway, hall, ticket)
	memberID := hello["member"].(map[string]any)["id"].(string)

	bumpTerm(t, databaseURL, hall)
	client.write(t, `{"kind":"request","type":"queue.add","id":"a1","data":{"track":{"source":"netease","id":"186016"}}}`)
	if reply := client.readUntil(t, func(m map[string]any) bool { return m["type"] == "queue.add" }); reply["error"].(map[string]any)["code"] != "UNAVAILABLE" {
		t.Fatalf("点歌的回复 = %v，期望 owner 落盘失败之后的 UNAVAILABLE", reply)
	}

	command := awaitSpan(t, gateway.spans, "WS queue.add", trace.TraceID{})
	traceID := command.SpanContext().TraceID()
	if command.Status().Code != codes.Error {
		t.Errorf("命令的 span 的状态 = %v，期望 UNAVAILABLE 标为出错", command.Status())
	}
	received := awaitSpan(t, owning.spans, "receive room input", traceID)
	if received.Parent().SpanID() != command.SpanContext().SpanID() || !received.Parent().IsRemote() {
		t.Errorf("owner 收到输入的 span 的父 span = %v，期望是网关的请求 %v", received.Parent(), command.SpanContext())
	}
	saved := awaitSpan(t, owning.spans, "SaveRoomState", traceID)
	if saved.Parent().SpanID() != received.SpanContext().SpanID() {
		t.Errorf("落盘的查询的父 span = %v，期望是 owner 收到输入的 span", saved.Parent())
	}

	entries := logEntries(t, owning.logs, "落盘失败，放弃房间")
	if len(entries) != 1 {
		t.Fatalf("owner 落盘失败的日志有 %d 条，期望 1 条：\n%s", len(entries), owning.logs.String())
	}
	entry := entries[0]
	requestID, _ := entry["request_id"].(string)
	if entry["trace_id"] != traceID.String() || entry["span_id"] != received.SpanContext().SpanID().String() ||
		entry["room_id"] != hall || entry["member_id"] != memberID || !strings.HasPrefix(requestID, gateway.instance+"/r") {
		t.Errorf("日志 = %v，期望 trace %s、span %s、房间 %s、成员 %s、网关 %s 发出的请求", entry, traceID, received.SpanContext().SpanID(), hall, memberID, gateway.instance)
	}
}

func TestPrepareJobLinksToTheRoomCommand(t *testing.T) {
	contractEnv(t, startPostgres(t)(t), startValkey(t)(t))
	objects, err := loadStorageConfig()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("STORAGE_PUBLIC_URL", objects.Endpoint+"/"+objects.Bucket)
	svc := startService(t)
	w := startWorker(t, &links{})
	w.waitReady(t)
	hall := createPersistentRoom(t, svc, "大厅")
	_, ticket := enterRoom(t, svc, hall)
	client, _ := connect(t, svc, hall, ticket)
	client.write(t, `{"kind":"request","type":"queue.add","id":"a1","data":{"track":{"source":"link","id":"https://example.com/qingtian.mp3"}}}`)
	client.readUntil(t, playing)

	command := awaitSpan(t, svc.spans, "WS queue.add", trace.TraceID{})
	insert := awaitSpan(t, svc.spans, "river.insert_many", command.SpanContext().TraceID())
	work := awaitSpan(t, w.spans, "river.work", trace.TraceID{})
	links := work.Links()
	if work.Parent().IsValid() || len(links) != 1 || links[0].SpanContext.SpanID() != insert.SpanContext().SpanID() || links[0].SpanContext.TraceID() != command.SpanContext().TraceID() {
		t.Errorf("执行任务的 span 的父 span = %v、链接 = %v，期望另开 trace，链接到 serve 提交任务的 span %v", work.Parent(), links, insert.SpanContext())
	}
	for _, name := range []string{"yt-dlp", "S3 PutObject", "SavePreparedTrack"} {
		awaitSpan(t, w.spans, name, work.SpanContext().TraceID())
	}
}

func TestJobInsertsStartNoTraces(t *testing.T) {
	contractEnv(t, startPostgres(t)(t), startValkey(t)(t))
	svc := startService(t)
	jobs := queue.New(svc.public.jobs)

	// serve 的 River client 不处理 ytdlp 队列，链接曲目的任务会一直停在队列中。
	id, err := jobs.Submit(t.Context(), track.Track{Source: track.Link, ID: "https://example.com/qingtian.mp3"})
	if err != nil {
		t.Fatal(err)
	}
	job, err := svc.public.jobs.JobGet(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(svc.spans.Started()); n != 0 || strings.Contains(string(job.Metadata), "traceparent") {
		t.Errorf("没有请求发起的提交开始了 %d 个 span，任务的元数据 = %s，期望没有 span，也没有追踪上下文", n, job.Metadata)
	}

	ctx, request := svc.tracer.Tracer("test").Start(t.Context(), "WS queue.add")
	if _, err := jobs.Submit(ctx, track.Track{Source: track.NetEase, ID: "1100"}); err != nil {
		t.Fatal(err)
	}
	request.End()
	insert := awaitSpan(t, svc.spans, "river.insert_many", request.SpanContext().TraceID())
	if insert.SpanKind() != trace.SpanKindProducer || insert.Parent().SpanID() != request.SpanContext().SpanID() {
		t.Errorf("提交任务的 span 的类型 = %v、父 span = %v，期望是请求 %v 之下的 producer span", insert.SpanKind(), insert.Parent(), request.SpanContext())
	}
	work := awaitSpan(t, svc.spans, "river.work", trace.TraceID{})
	if links := work.Links(); work.Parent().IsValid() || len(links) != 1 || links[0].SpanContext.SpanID() != insert.SpanContext().SpanID() {
		t.Errorf("执行任务的 span 的父 span = %v、链接 = %v，期望另开 trace，链接到提交它的 span %v", work.Parent(), links, insert.SpanContext())
	}
}
