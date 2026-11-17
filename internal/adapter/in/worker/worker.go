package worker

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertype"
	"github.com/riverqueue/rivercontrib/otelriver"
	"go.opentelemetry.io/otel/trace"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/track"
	"github.com/dihemoe/alisten/internal/lib/logctx"
)

const (
	prepareQueue   = "prepare"
	prepareWorkers = 4
	prepareTimeout = 15 * time.Minute
	// 进程崩溃后停留在 running 状态的任务，最多过这么久就由 River 重新调度。River 默认至少要等 1 小时，
	// 这期间这首歌一直处于准备中。
	rescueStuckJobs = 20 * time.Minute
)

// internal/adapter/out/queue 按同样的队列名提交链接和 B 站的歌的准备任务。
const (
	linkQueue     = "ytdlp"
	bilibiliQueue = "bilibili"
	mediaWorkers  = 1
)

// 适配器之间不能互相 import，所以 internal/adapter/out/queue 要按同样的 kind 和字段提交任务。
type prepareArgs struct {
	Source string `json:"source"`
	ID     string `json:"id"`
}

func (prepareArgs) Kind() string { return "prepare" }

type prepareWorker struct {
	river.WorkerDefaults[prepareArgs]
	run func(context.Context, track.Track) error
}

func (w *prepareWorker) Work(ctx context.Context, job *river.Job[prepareArgs]) error {
	err := w.run(logctx.WithJob(ctx, job.ID), job.Args.track())
	var rejected *port.RejectedError
	if errors.As(err, &rejected) {
		return river.JobCancel(err)
	}
	return err
}

func (a prepareArgs) track() track.Track {
	return track.Track{Source: track.Source(a.Source), ID: a.ID}
}

// 清理闲置游客的任务用单独的队列，这样不占准备任务的并发。
const (
	cleanupQueue    = "cleanup"
	cleanupWorkers  = 1
	cleanupInterval = 24 * time.Hour
)

type cleanupArgs struct{}

func (cleanupArgs) Kind() string { return "cleanup_guests" }

func (cleanupArgs) InsertOpts() river.InsertOpts { return river.InsertOpts{Queue: cleanupQueue} }

type cleanupWorker struct {
	river.WorkerDefaults[cleanupArgs]
	run func(context.Context, time.Time) error
}

func (w *cleanupWorker) Work(ctx context.Context, job *river.Job[cleanupArgs]) error {
	return w.run(logctx.WithJob(ctx, job.ID), time.Now())
}

// River 只让当选 leader 的 client 安排周期任务，serve 和 worker 两个进程的 client 都可能当选，所以两种 client 都要配置清理任务。
// 只有 serve 的 client 处理 cleanupQueue，所以不管由谁安排，任务都在 serve 进程里执行。
// River 只在内存中计时，每次换 leader 都重新计时。所以当选时也安排一次，否则 leader 总是不到 24 小时就换掉的话，清理一次也不会执行。
func cleanupJobs() []*river.PeriodicJob {
	return []*river.PeriodicJob{river.NewPeriodicJob(river.PeriodicInterval(cleanupInterval), func() (river.JobArgs, *river.InsertOpts) {
		return cleanupArgs{}, nil
	}, &river.PeriodicJobOpts{RunOnStart: true})}
}

// run 返回 *port.RejectedError 时任务取消，返回其他错误时由 River 重试。cleanup 返回错误时同样由 River 重试。
func NewClient(pool *pgxpool.Pool, run func(context.Context, track.Track) error, cleanup func(context.Context, time.Time) error, tracer trace.TracerProvider, logger *slog.Logger) (*river.Client[pgx.Tx], error) {
	workers := river.NewWorkers()
	river.AddWorker(workers, &prepareWorker{run: run})
	river.AddWorker(workers, &cleanupWorker{run: cleanup})
	return newClient(pool, map[string]river.QueueConfig{prepareQueue: {MaxWorkers: prepareWorkers}, cleanupQueue: {MaxWorkers: cleanupWorkers}}, workers, tracer, logger)
}

// 执行链接和 B 站的歌的准备任务，每个队列在每个进程中同时只执行一个。
func NewMediaClient(pool *pgxpool.Pool, run func(context.Context, track.Track) error, tracer trace.TracerProvider, logger *slog.Logger) (*river.Client[pgx.Tx], error) {
	workers := river.NewWorkers()
	river.AddWorker(workers, &prepareWorker{run: run})
	return newClient(pool, map[string]river.QueueConfig{linkQueue: {MaxWorkers: mediaWorkers}, bilibiliQueue: {MaxWorkers: mediaWorkers}}, workers, tracer, logger)
}

func newClient(pool *pgxpool.Pool, queues map[string]river.QueueConfig, workers *river.Workers, tracer trace.TracerProvider, logger *slog.Logger) (*river.Client[pgx.Tx], error) {
	return river.NewClient(riverpgxv5.New(pool), &river.Config{
		Queues:               queues,
		Workers:              workers,
		PeriodicJobs:         cleanupJobs(),
		JobTimeout:           prepareTimeout,
		RescueStuckJobsAfter: rescueStuckJobs,
		// 任务在单独的 trace 里执行，只链接到提交任务的 span，因为任务可能很久之后才执行，失败后还会重试，同一首歌的任务也由所有等待这首歌的房间共用。
		Plugins: []rivertype.Plugin{otelriver.NewMiddleware(&otelriver.MiddlewareConfig{TracerProvider: tracer, EnableTracePropagation: true})},
		// River 在 info 级别记录每次启停和每个出错的任务，出错的任务由 Watch 记录。
		// 放进 river 分组，River 自己的属性（如 job_id）才不会与日志的关联字段重名。
		Logger: slog.New(warnings{logger.Handler()}).WithGroup("river"),
	})
}

type warnings struct{ slog.Handler }

func (h warnings) Enabled(ctx context.Context, level slog.Level) bool {
	return level >= slog.LevelWarn && h.Handler.Enabled(ctx, level)
}

func (h warnings) WithAttrs(attrs []slog.Attr) slog.Handler {
	return warnings{h.Handler.WithAttrs(attrs)}
}

func (h warnings) WithGroup(name string) slog.Handler { return warnings{h.Handler.WithGroup(name)} }

// 在 client 启动之前调用，返回的函数一直运行到 client 停止。River 的事件可能丢失，等待的一方会定期按任务 ID 核对。
func Watch(client *river.Client[pgx.Tx], finished func(port.PrepareOutcome), observe func(source track.Source, result string, elapsed time.Duration), logger *slog.Logger) func() {
	events, _ := client.Subscribe(river.EventKindJobCompleted, river.EventKindJobCancelled, river.EventKindJobFailed)
	return func() {
		for e := range events {
			if e.Job.Kind != (prepareArgs{}).Kind() {
				continue
			}
			ctx := logctx.WithJob(context.Background(), e.Job.ID)
			var args prepareArgs
			if err := json.Unmarshal(e.Job.EncodedArgs, &args); err != nil {
				logger.ErrorContext(ctx, "无法解码准备任务的参数", "error", err)
				continue
			}
			t := args.track()
			outcome := port.PrepareOutcome{Track: t, JobID: e.Job.ID}
			switch {
			case e.Kind == river.EventKindJobCompleted:
				observe(t.Source, "ok", e.JobStats.RunDuration)
			case e.Kind == river.EventKindJobCancelled:
				outcome.Reason = reason(e.Job)
				observe(t.Source, "rejected", e.JobStats.RunDuration)
			case e.Job.State == rivertype.JobStateDiscarded:
				outcome.Reason = port.UnavailableReason
				observe(t.Source, "failed", e.JobStats.RunDuration)
				logger.WarnContext(ctx, "准备任务失败", "source", t.Source, "track_id", t.ID, "attempt", e.Job.Attempt, "error", reason(e.Job))
			default:
				logger.WarnContext(ctx, "准备任务失败，稍后重试", "source", t.Source, "track_id", t.ID, "attempt", e.Job.Attempt, "error", reason(e.Job))
				continue
			}
			finished(outcome)
		}
	}
}

// River 记录取消的原因时带有 "JobCancelError: " 前缀。internal/adapter/out/queue 用同样的方式提取任务失败的原因。
func reason(job *rivertype.JobRow) string {
	if len(job.Errors) == 0 {
		return "准备任务失败"
	}
	return strings.TrimPrefix(job.Errors[len(job.Errors)-1].Error, "JobCancelError: ")
}
