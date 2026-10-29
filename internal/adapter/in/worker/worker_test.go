package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/riverqueue/river"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/track"
	"github.com/dihemoe/alisten/internal/lib/logctx"
)

func run(_ context.Context, t track.Track) error {
	switch t.ID {
	case "ok":
		return nil
	case "rejected":
		return &port.RejectedError{Err: errors.New("网易云没有提供这首歌的播放地址")}
	}
	return errors.New("连接被重置")
}

type recorder struct {
	mu       sync.Mutex
	outcomes []port.PrepareOutcome
	results  []string
}

func (r *recorder) finished(o port.PrepareOutcome) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.outcomes = append(r.outcomes, o)
}

func (r *recorder) observe(source track.Source, result string, elapsed time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if elapsed <= 0 {
		result += "（没有耗时）"
	}
	r.results = append(r.results, string(source)+" "+result)
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.outcomes)
}

func TestWorkerReportsFinalOutcomes(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(logctx.NewHandler(slog.NewJSONHandler(&logs, nil)))
	client, err := NewClient(newPool(t), func(ctx context.Context, tr track.Track) error {
		logger.InfoContext(ctx, "执行准备任务", "track_id", tr.ID)
		return run(ctx, tr)
	}, noop.NewTracerProvider(), logger)
	if err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	watching := make(chan struct{})
	go func() {
		defer close(watching)
		Watch(client, rec.finished, rec.observe, logger)()
	}()
	if err := client.Start(t.Context()); err != nil {
		t.Fatal(err)
	}

	jobs := map[string]int64{}
	for _, id := range []string{"ok", "rejected", "broken"} {
		res, err := client.Insert(t.Context(), prepareArgs{Source: "netease", ID: id}, &river.InsertOpts{Queue: prepareQueue, MaxAttempts: 2})
		if err != nil {
			t.Fatal(err)
		}
		jobs[id] = res.Job.ID
	}
	// River 第一次重试的间隔约 1 秒。
	for deadline := time.Now().Add(30 * time.Second); rec.count() < 3; time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("30 秒后只收到 %d 个结果", rec.count())
		}
	}
	if err := client.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-watching

	netease := func(id string) track.Track { return track.Track{Source: track.NetEase, ID: id} }
	want := []port.PrepareOutcome{
		{Track: netease("ok"), JobID: jobs["ok"]},
		{Track: netease("rejected"), JobID: jobs["rejected"], Reason: "网易云没有提供这首歌的播放地址"},
		{Track: netease("broken"), JobID: jobs["broken"], Reason: port.UnavailableReason},
	}
	byJob := func(a, b port.PrepareOutcome) int { return int(a.JobID - b.JobID) }
	slices.SortFunc(rec.outcomes, byJob)
	if !slices.Equal(rec.outcomes, want) {
		t.Errorf("任务的结果 = %+v\n期望 %+v", rec.outcomes, want)
	}
	slices.Sort(rec.results)
	if want := []string{"netease failed", "netease ok", "netease rejected"}; !slices.Equal(rec.results, want) {
		t.Errorf("记下的结果 = %q，期望 %q：重试之前的失败不算", rec.results, want)
	}
	for _, msg := range []string{`"msg":"准备任务失败，稍后重试"`, `"msg":"准备任务失败"`} {
		if !strings.Contains(logs.String(), msg) {
			t.Errorf("日志中没有 %s：\n%s", msg, logs.String())
		}
	}
	for line := range strings.Lines(strings.TrimSpace(logs.String())) {
		var entry struct {
			Msg     string
			TrackID string `json:"track_id"`
			JobID   int64  `json:"job_id"`
		}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("日志不是 JSON：%v\n%s", err, line)
		}
		if strings.HasPrefix(entry.Msg, "执行准备任务") || strings.HasPrefix(entry.Msg, "准备任务失败") {
			if entry.JobID != jobs[entry.TrackID] {
				t.Errorf("日志 %s 的 job_id = %d，期望 %d", line, entry.JobID, jobs[entry.TrackID])
			}
		}
	}
	if strings.Contains(logs.String(), "River client started") {
		t.Errorf("River 的 info 日志不应输出：\n%s", logs.String())
	}
	if job, err := client.JobGet(t.Context(), jobs["rejected"]); err != nil || job.State != "cancelled" {
		t.Errorf("被音源拒绝的任务 = %v, %v，期望直接取消", job, err)
	}
}

func TestMediaClientRunsOneJobPerQueue(t *testing.T) {
	var mu sync.Mutex
	running, peak := map[track.Source]int{}, map[track.Source]int{}
	var ran []string
	run := func(_ context.Context, tr track.Track) error {
		mu.Lock()
		running[tr.Source]++
		peak[tr.Source] = max(peak[tr.Source], running[tr.Source])
		ran = append(ran, tr.ID)
		mu.Unlock()
		time.Sleep(200 * time.Millisecond)
		mu.Lock()
		running[tr.Source]--
		mu.Unlock()
		return nil
	}
	logger := slog.New(slog.DiscardHandler)
	client, err := NewMediaClient(newPool(t), run, noop.NewTracerProvider(), logger)
	if err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	watching := make(chan struct{})
	go func() {
		defer close(watching)
		Watch(client, rec.finished, rec.observe, logger)()
	}()
	if err := client.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b", "c"} {
		if _, err := client.Insert(t.Context(), prepareArgs{Source: "link", ID: "https://example.com/" + id}, &river.InsertOpts{Queue: linkQueue}); err != nil {
			t.Fatal(err)
		}
		if _, err := client.Insert(t.Context(), prepareArgs{Source: "bilibili", ID: "BV-" + id}, &river.InsertOpts{Queue: bilibiliQueue}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := client.Insert(t.Context(), prepareArgs{Source: "netease", ID: "186016"}, &river.InsertOpts{Queue: prepareQueue}); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(30 * time.Second); rec.count() < 6; time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("30 秒后只收到 %d 个结果", rec.count())
		}
	}
	if err := client.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-watching

	mu.Lock()
	defer mu.Unlock()
	if peak[track.Link] != 1 || peak[track.Bilibili] != 1 {
		t.Errorf("每个队列同时执行的任务数的峰值 = %v，期望各 1 个", peak)
	}
	want := []string{"BV-a", "BV-b", "BV-c", "https://example.com/a", "https://example.com/b", "https://example.com/c"}
	if slices.Sort(ran); !slices.Equal(ran, want) {
		t.Errorf("执行的任务 = %q，期望只有 ytdlp 和 bilibili 队列中的", ran)
	}
	results := slices.Clone(rec.results)
	slices.Sort(results)
	if want := []string{"bilibili ok", "bilibili ok", "bilibili ok", "link ok", "link ok", "link ok"}; !slices.Equal(results, want) {
		t.Errorf("记下的结果 = %q，期望 %q", results, want)
	}
}
