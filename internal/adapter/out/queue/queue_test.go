package queue

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/track"
)

type testWorker struct {
	river.WorkerDefaults[prepareArgs]
}

func (testWorker) Work(_ context.Context, job *river.Job[prepareArgs]) error {
	switch job.Args.ID {
	case "ok":
		return nil
	case "rejected":
		return river.JobCancel(errors.New("网易云没有提供这首歌的播放地址"))
	}
	return errors.New("连接被重置")
}

func newClient(t *testing.T, work bool) *river.Client[pgx.Tx] {
	t.Helper()
	workers := river.NewWorkers()
	river.AddWorker(workers, testWorker{})
	cfg := &river.Config{Workers: workers, Logger: slog.New(slog.DiscardHandler)}
	if work {
		cfg.Queues = map[string]river.QueueConfig{"prepare": {MaxWorkers: 4}}
	}
	client, err := river.NewClient(riverpgxv5.New(newPool(t)), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if work {
		if err := client.Start(t.Context()); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = client.Stop(context.Background()) })
	}
	return client
}

func netease(id string) track.Track { return track.Track{Source: track.NetEase, ID: id} }

func TestSubmitDeduplicatesUnfinishedJobs(t *testing.T) {
	q := New(newClient(t, false))
	first, err := q.Submit(t.Context(), netease("1"))
	if err != nil {
		t.Fatal(err)
	}
	if again, err := q.Submit(t.Context(), netease("1")); err != nil || again != first {
		t.Errorf("再次提交同一首歌 = %d, %v，期望已有的任务 %d", again, err, first)
	}
	if other, err := q.Submit(t.Context(), track.Track{Source: track.QQ, ID: "1"}); err != nil || other == first {
		t.Errorf("另一首歌的任务 = %d, %v，期望新的任务", other, err)
	}
	if state, _, err := q.Status(t.Context(), first); err != nil || state != port.JobPending {
		t.Errorf("等待执行的任务的状态 = %v, %v", state, err)
	}
	if state, _, err := q.Status(t.Context(), first+100); err != nil || state != port.JobFinished {
		t.Errorf("不存在的任务的状态 = %v, %v，期望当作已完成", state, err)
	}
}

// River 第一次重试的间隔约 1 秒。
func waitFinished(t *testing.T, q *Queue, job int64) (port.JobState, string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		state, reason, err := q.Status(t.Context(), job)
		if err != nil {
			t.Fatal(err)
		}
		if state != port.JobPending {
			return state, reason
		}
		if time.Now().After(deadline) {
			t.Fatalf("任务 %d 30 秒后仍未结束", job)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestStatusFollowsJob(t *testing.T) {
	q := New(newClient(t, true))
	cases := []struct {
		id       string
		state    port.JobState
		reason   string
		attempts int
	}{
		{"ok", port.JobFinished, "", 1},
		{"rejected", port.JobFailed, "网易云没有提供这首歌的播放地址", 1},
		{"broken", port.JobFailed, port.UnavailableReason, 2},
	}
	for _, c := range cases {
		job, err := q.Submit(t.Context(), netease(c.id))
		if err != nil {
			t.Fatal(err)
		}
		if state, reason := waitFinished(t, q, job); state != c.state || reason != c.reason {
			t.Errorf("任务 %s 的状态 = %v, %q，期望 %v, %q", c.id, state, reason, c.state, c.reason)
		}
		row, err := q.client.JobGet(t.Context(), job)
		if err != nil {
			t.Fatal(err)
		}
		if row.Attempt != c.attempts {
			t.Errorf("任务 %s 执行了 %d 次，期望 %d 次：失败之后只重试 1 次，被音源拒绝的任务不重试", c.id, row.Attempt, c.attempts)
		}
	}

	job, err := q.Submit(t.Context(), netease("ok"))
	if err != nil {
		t.Fatal(err)
	}
	if state, _ := waitFinished(t, q, job); state != port.JobFinished {
		t.Errorf("再次提交已完成的歌之后的状态 = %v", state)
	}
	rows, err := q.client.JobList(t.Context(), river.NewJobListParams().Kinds("prepare").First(100))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows.Jobs) != 4 {
		t.Errorf("共有 %d 个任务，期望再次提交已完成的歌时新建任务", len(rows.Jobs))
	}
}

func TestJobsGoToTheirSourcesQueues(t *testing.T) {
	pool := newPool(t)
	client, err := river.NewClient(riverpgxv5.New(pool), &river.Config{Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	q := New(client)
	link := track.Track{Source: track.Link, ID: "https://www.youtube.com/watch?v=x"}
	first, err := q.Submit(t.Context(), link)
	if err != nil {
		t.Fatal(err)
	}
	if again, err := q.Submit(t.Context(), link); err != nil || again != first {
		t.Errorf("再次提交同一个链接 = %d, %v，期望已有的任务 %d", again, err, first)
	}
	other, err := q.Submit(t.Context(), netease("1"))
	if err != nil {
		t.Fatal(err)
	}
	video, err := q.Submit(t.Context(), track.Track{Source: track.Bilibili, ID: "BV1xx411c7mD"})
	if err != nil {
		t.Fatal(err)
	}
	for job, want := range map[int64]string{first: "ytdlp", other: "prepare", video: "bilibili"} {
		row, err := client.JobGet(t.Context(), job)
		if err != nil {
			t.Fatal(err)
		}
		if row.Queue != want || row.MaxAttempts != 2 {
			t.Errorf("任务 %d 在队列 %s、最多执行 %d 次，期望 %s、2 次", job, row.Queue, row.MaxAttempts, want)
		}
	}
	if _, err := q.Submit(t.Context(), track.Track{Source: track.Link, ID: "https://example.com/a.mp3"}); err != nil {
		t.Fatal(err)
	}
	if n, err := WaitingLinkJobs(t.Context(), pool); err != nil || n != 2 {
		t.Errorf("等待执行的链接准备任务 = %d, %v，期望 2", n, err)
	}
}
