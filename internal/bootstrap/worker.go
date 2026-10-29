package bootstrap

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	goredis "github.com/redis/go-redis/v9"
	"github.com/riverqueue/river"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/dihemoe/alisten/internal/adapter/in/ops"
	"github.com/dihemoe/alisten/internal/adapter/in/worker"
	"github.com/dihemoe/alisten/internal/adapter/out/bilibili"
	"github.com/dihemoe/alisten/internal/adapter/out/ffmpeg"
	"github.com/dihemoe/alisten/internal/adapter/out/linkcookies"
	"github.com/dihemoe/alisten/internal/adapter/out/postgres"
	"github.com/dihemoe/alisten/internal/adapter/out/queue"
	"github.com/dihemoe/alisten/internal/adapter/out/redis"
	"github.com/dihemoe/alisten/internal/adapter/out/storage"
	"github.com/dihemoe/alisten/internal/adapter/out/ytdlp"
	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/app/prepare"
	"github.com/dihemoe/alisten/internal/lib/logctx"
)

const (
	// 停止 River client 和关闭运维端口共用这段期限，加上导出剩余追踪的 flushTimeout，不能超过 Kubernetes 30 秒的退出宽限期。
	workerStopTimeout = 25 * time.Second
	publishTimeout    = 5 * time.Second
	waitingInterval   = 15 * time.Second
	waitingTimeout    = 5 * time.Second
)

type workerConfig struct {
	opsListen string
	database  *pgxpool.Config
	redis     *goredis.Options
	storage   storage.Config
	tracing   bool
}

func loadWorkerConfig() (workerConfig, error) {
	opsListen, err := loadOpsListen()
	if err != nil {
		return workerConfig{}, err
	}
	database, err := loadDatabaseConfig()
	if err != nil {
		return workerConfig{}, err
	}
	redis, err := loadRedisConfig()
	if err != nil {
		return workerConfig{}, err
	}
	objects, err := loadStorageConfig()
	if err != nil {
		return workerConfig{}, err
	}
	tracing, err := loadTracing()
	if err != nil {
		return workerConfig{}, err
	}
	return workerConfig{opsListen: opsListen, database: database, redis: redis, storage: objects, tracing: tracing}, nil
}

// yt-dlp 会处理用户提交的任意链接。
func refuseRoot(euid int) error {
	if euid == 0 {
		return errors.New("worker 不能以 root 身份运行")
	}
	return nil
}

func runWorker(base *slog.Logger) (err error) {
	if err := refuseRoot(os.Geteuid()); err != nil {
		return err
	}
	cfg, err := loadWorkerConfig()
	if err != nil {
		return err
	}
	signals, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stopSignals()

	instance := rand.Text()
	tracer, flush, err := newTracerProvider(signals, cfg.tracing, "alisten-worker", instance)
	if err != nil {
		return err
	}
	defer func() { exitAfterFlush(err, flush, base.With("component", "worker"), "worker 已退出") }()

	traceDatabase(cfg.database, tracer)
	pool, err := pgxpool.NewWithConfig(signals, cfg.database)
	if err != nil {
		return fmt.Errorf("创建 PostgreSQL 连接池：%w", err)
	}
	defer pool.Close()
	goredis.SetLogger(newRedisLogger(base.With("component", "redis")))
	rdb := goredis.NewClient(cfg.redis)
	defer func() { _ = rdb.Close() }()
	traceRedis(rdb, tracer)

	metrics := prometheus.NewRegistry()
	observed := newWorkerMetrics(metrics)
	workDir, err := prepareWorkDir()
	if err != nil {
		return err
	}
	store := postgres.New(pool)
	// 每个请求的期限由音源适配器设置。
	upstream := &http.Client{Transport: traceHTTP(http.DefaultTransport, tracer)}
	cookies := linkcookies.New(workDir, store.YouTubeCookies)
	src := workerSources{
		links:      ytdlp.New(workDir, net.DefaultResolver, cookies, observed.downloaded, base.With("component", "ytdlp")),
		bilibili:   bilibili.New(upstream, workDir, store.BilibiliCredential, store.SetBilibiliCredential, observed.provider),
		transcoder: ffmpeg.New(workDir, base.With("component", "ffmpeg")),
	}
	w, err := newMediaWorker(cfg, pool, rdb, src, metrics, observed, instance, tracer, base)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", cfg.opsListen)
	if err != nil {
		return fmt.Errorf("监听运维端口 %s：%w", cfg.opsListen, err)
	}
	return w.run(signals, listener)
}

// 任务的临时目录都建在 TMPDIR 下以主机名命名的目录中。进程崩溃时来不及清理的任务目录会留在那里；
// 同一个容器（Kubernetes 中的同一个 Pod）重启后主机名不变，启动时在这里清除。其他容器的目录不动：
// 它们可能共用同一个 TMPDIR，任务仍在运行。每个容器只能运行一个 worker。
func prepareWorkDir() (string, error) {
	host, err := os.Hostname()
	if err != nil {
		return "", fmt.Errorf("读取主机名：%w", err)
	}
	dir := filepath.Join(os.TempDir(), "alisten-link-"+host)
	if err := os.RemoveAll(dir); err != nil {
		return "", fmt.Errorf("清理上一次运行留下的临时目录 %s：%w", dir, err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("创建临时目录 %s：%w", dir, err)
	}
	return dir, nil
}

type workerSources struct {
	links      port.LinkDownloader
	bilibili   port.BilibiliSource
	transcoder port.Transcoder
}

type mediaWorker struct {
	pool     *pgxpool.Pool
	rdb      *goredis.Client
	jobs     *river.Client[pgx.Tx]
	watch    func()
	metrics  *prometheus.Registry
	observed *workerMetrics
	instance string
	logger   *slog.Logger
	base     *slog.Logger
}

func newMediaWorker(cfg workerConfig, pool *pgxpool.Pool, rdb *goredis.Client, src workerSources, metrics *prometheus.Registry, observed *workerMetrics, instance string, tracer trace.TracerProvider, base *slog.Logger) (mediaWorker, error) {
	logger := base.With("component", "prepare")
	runner := prepare.NewMediaRunner(traceDownloads(src.links, tracer), src.bilibili, src.transcoder, traceObjects(storage.New(cfg.storage), tracer), postgres.New(pool), logger)
	jobs, err := worker.NewMediaClient(pool, runner.Run, tracer, logger)
	if err != nil {
		return mediaWorker{}, fmt.Errorf("创建 River client：%w", err)
	}
	// worker 只发布准备结果、不订阅，不会丢弃实例间消息，所以不暴露丢弃的计数；也不会收到房间输入，所以不记录收到输入的 span。
	bus := redis.NewBus(rdb, instance, version, prometheus.NewRegistry(), noop.NewTracerProvider(), base.With("component", "bus"))
	publish := func(o port.PrepareOutcome) {
		ctx, cancel := context.WithTimeout(logctx.WithJob(context.Background(), o.JobID), publishTimeout)
		defer cancel()
		// 广播失败时只记录日志：等待的一方会定期按任务 ID 核对。
		if err := bus.PublishPrepared(ctx, o); err != nil {
			logger.WarnContext(ctx, "广播准备结果失败", "source", o.Track.Source, "track_id", o.Track.ID, "error", err)
		}
	}
	return mediaWorker{
		pool:     pool,
		rdb:      rdb,
		jobs:     jobs,
		watch:    worker.Watch(jobs, publish, observed.prepared, logger),
		metrics:  metrics,
		observed: observed,
		instance: instance,
		logger:   base.With("component", "worker"),
		base:     base,
	}, nil
}

// 一直运行到 ctx 结束，或者运维端口、River client 意外停止；后者以错误返回，交给容器编排重启。
// 数据库迁移完成之前保持未就绪，迁移完成之后才启动 River client。
func (w mediaWorker) run(ctx context.Context, listener net.Listener) error {
	var dependenciesReady, stopping atomic.Bool
	var starting sync.Mutex
	ready := func() bool { return dependenciesReady.Load() && !stopping.Load() }
	fatal := make(chan error, 2)
	migrated := make(chan struct{})
	var migratedOnce sync.Once
	background, stopBackground := context.WithCancel(context.Background())
	var running sync.WaitGroup
	running.Go(func() {
		watchReadiness(background, readinessInterval, func(ctx context.Context) error {
			if err := postgres.CheckMigrations(ctx, w.pool); err != nil {
				return err
			}
			migratedOnce.Do(func() { close(migrated) })
			return pingRedis(ctx, w.rdb)
		}, &dependenciesReady, &stopping, w.logger)
	})
	running.Go(func() {
		select {
		case <-migrated:
		case <-background.Done():
			return
		}
		// 开始退出之后不再启动 River client：此后才启动的 client 不会被停止，运行中的任务要等 20 分钟才由 River 重新调度。
		starting.Lock()
		if stopping.Load() {
			starting.Unlock()
			return
		}
		// River client 只在退出时停止，不随 context 结束而停止。
		err := w.jobs.Start(context.Background())
		starting.Unlock()
		if err != nil {
			fatal <- fmt.Errorf("启动 River client：%w", err)
			return
		}
		go w.watch()
		select {
		case <-w.jobs.Stopped():
			if !stopping.Load() {
				fatal <- errors.New("执行准备任务的 River client 意外停止")
			}
		case <-background.Done():
		}
	})
	running.Go(func() {
		watchWaitingJobs(background, func(ctx context.Context) (int64, error) { return queue.WaitingLinkJobs(ctx, w.pool) }, w.observed.waitingJobs)
	})

	w.metrics.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "alisten_ready",
			Help: "本实例最近一次就绪检查的结果，1 为就绪，0 为未就绪。",
		}, func() float64 {
			if ready() {
				return 1
			}
			return 0
		}),
	)
	opsLogger := w.base.With("component", "ops")
	opsServer := newHTTPServer(recoverPanics(ops.NewHandler(ready, w.metrics), opsLogger), opsLogger)
	go func() { fatal <- fmt.Errorf("运维端口异常退出：%w", opsServer.Serve(listener)) }()
	w.logger.Info("worker 已启动", "ops_listen", listener.Addr().String(), "instance", w.instance)

	var failure error
	select {
	case failure = <-fatal:
	case <-ctx.Done():
	}
	// worker 不接收流量，不必等待入口摘除。
	starting.Lock()
	stopping.Store(true)
	starting.Unlock()

	stopCtx, cancel := context.WithTimeout(context.Background(), workerStopTimeout)
	defer cancel()
	errs := []error{failure}
	// 运行中的任务会被取消，交给 River 重试。
	if err := w.jobs.StopAndCancel(stopCtx); err != nil {
		errs = append(errs, fmt.Errorf("停止 River client：%w", err))
	} else {
		w.logger.Info("已停止执行准备任务")
	}
	stopBackground()
	running.Wait()
	if err := opsServer.Shutdown(stopCtx); err != nil {
		errs = append(errs, fmt.Errorf("关闭运维端口：%w", err))
	}
	return errors.Join(errs...)
}

// 统计失败时保留上一次的值：连不上数据库已经反映在就绪检查中。
func watchWaitingJobs(ctx context.Context, count func(context.Context) (int64, error), waiting prometheus.Gauge) {
	ticker := time.NewTicker(waitingInterval)
	defer ticker.Stop()
	for {
		countCtx, cancel := context.WithTimeout(ctx, waitingTimeout)
		if n, err := count(countCtx); err == nil {
			waiting.Set(float64(n))
		}
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
