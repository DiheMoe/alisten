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
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	goredis "github.com/redis/go-redis/v9"

	"github.com/dihemoe/alisten/internal/adapter/in/ops"
	"github.com/dihemoe/alisten/internal/adapter/out/bilibili"
	"github.com/dihemoe/alisten/internal/adapter/out/netease"
	"github.com/dihemoe/alisten/internal/adapter/out/postgres"
	"github.com/dihemoe/alisten/internal/adapter/out/qq"
	"github.com/dihemoe/alisten/internal/adapter/out/redis"
	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/app/realtime"
	"github.com/dihemoe/alisten/internal/domain/track"
)

const (
	// 留给入口发现 /readyz 失败并摘除本实例的时间。
	drainDelay = 5 * time.Second
	// 释放房间、关闭会话和关闭端口共用这段期限。
	shutdownTimeout = 17 * time.Second
	// River client 的停止有自己的期限：即使前面的步骤用完了共用的期限，运行中的任务也要交还给 River。
	// 三者之和加上导出剩余追踪的 flushTimeout，不能超过 Kubernetes 30 秒的退出宽限期。
	jobsStopTimeout = 3 * time.Second
)

func runServe(base *slog.Logger) (err error) {
	cfg, err := loadServeConfig()
	if err != nil {
		return err
	}
	logger := base.With("component", "serve")

	signals, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stopSignals()

	// 每次启动都生成新的实例 ID：上一次运行留下的租约和会话不会被当作这一次的。
	instance := rand.Text()
	tracer, flush, err := newTracerProvider(signals, cfg.tracing, "alisten-serve", instance)
	if err != nil {
		return err
	}
	defer func() { exitAfterFlush(err, flush, logger, "服务已退出") }()

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
	observed := newServeMetrics(metrics)
	store := postgres.New(pool)
	// 每个请求的期限由音源适配器设置。
	upstream := &http.Client{Transport: traceHTTP(http.DefaultTransport, tracer)}
	neteaseSource := netease.New(upstream, store.NeteaseCookie, store.SetNeteaseCookie, observed.provider)
	qqSource := qq.New(upstream, store.QQCookie, store.SetQQCookie, observed.provider)
	// serve 不下载文件，下载由 worker 在自己的工作目录中进行。
	bilibiliSource := bilibili.New(upstream, "", store.BilibiliCredential, store.SetBilibiliCredential, observed.provider)
	src := sources{
		netease:       neteaseSource,
		qq:            qqSource,
		bilibili:      bilibiliSource,
		bilibiliLinks: bilibiliSource,
		playback:      map[track.Source]port.PlayURLs{track.NetEase: neteaseSource, track.QQ: qqSource},
		neteaseLogin:  neteaseSource,
		qqLogin:       qqSource,
		bilibiliLogin: bilibiliSource,
	}
	p, err := newPublic(cfg, pool, rdb, instance, src, metrics, observed.prepared, tracer, base)
	if err != nil {
		return err
	}
	clockOffset := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "alisten_clock_offset_seconds",
		Help: "Redis 服务器的时钟减去本实例的时钟，每 30 秒估算一次。",
	})

	var dependenciesReady, stopping atomic.Bool
	var starting sync.Mutex
	ready := func() bool { return dependenciesReady.Load() && !stopping.Load() }
	// HTTP 服务和 River client 意外停止时，进程以错误退出，交给容器编排重启。
	fatal := make(chan error, 4)
	migrated := make(chan struct{})
	var migratedOnce sync.Once
	background, stopBackground := context.WithCancel(context.Background())
	var running sync.WaitGroup
	running.Go(func() {
		watchReadiness(background, readinessInterval, func(ctx context.Context) error {
			if err := postgres.CheckMigrations(ctx, pool); err != nil {
				return err
			}
			migratedOnce.Do(func() { close(migrated) })
			return pingRedis(ctx, rdb)
		}, &dependenciesReady, &stopping, logger)
	})
	// 数据库迁移到二进制要求的版本之后，才开始执行准备任务、运行房间；realtime.Start 会一直重试，直到订阅了本实例的频道。
	running.Go(func() {
		select {
		case <-migrated:
		case <-background.Done():
			return
		}
		// 开始退出之后不再启动 River client：shutdown 只停止已经启动的 client，
		// 此后才启动的 client 会带着运行中的任务随进程退出，这些任务要等 20 分钟才由 River 重新调度。
		starting.Lock()
		if stopping.Load() {
			starting.Unlock()
			return
		}
		stopped, err := p.start()
		starting.Unlock()
		if err != nil {
			fatal <- err
			return
		}
		go func() {
			<-stopped
			if !stopping.Load() {
				fatal <- errors.New("执行准备任务的 River client 意外停止")
			}
		}()
		if wait, err := realtime.Start(background, p.host, p.gateway); err == nil {
			wait()
		}
	})
	running.Go(func() {
		watchClock(background, func(ctx context.Context) (time.Duration, error) { return redis.ClockOffset(ctx, rdb) }, clockOffset)
	})
	defer func() {
		stopBackground()
		running.Wait()
	}()

	metrics.MustRegister(
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
		clockOffset,
	)

	publicListener, err := net.Listen("tcp", cfg.listen)
	if err != nil {
		return fmt.Errorf("监听公开端口 %s：%w", cfg.listen, err)
	}
	opsListener, err := net.Listen("tcp", cfg.opsListen)
	if err != nil {
		_ = publicListener.Close()
		return fmt.Errorf("监听运维端口 %s：%w", cfg.opsListen, err)
	}

	opsLogger := base.With("component", "ops")
	publicServer := newHTTPServer(p.handler, base.With("component", "http"))
	opsServer := newHTTPServer(recoverPanics(ops.NewHandler(ready, metrics), opsLogger), opsLogger)
	go func() { fatal <- fmt.Errorf("HTTP 服务异常退出：%w", publicServer.Serve(publicListener)) }()
	go func() { fatal <- fmt.Errorf("HTTP 服务异常退出：%w", opsServer.Serve(opsListener)) }()
	logger.Info("服务已启动", "listen", publicListener.Addr().String(), "ops_listen", opsListener.Addr().String(), "instance", instance)

	var failure error
	select {
	case failure = <-fatal:
	case <-signals.Done():
	}
	stopSignals()

	starting.Lock()
	stopping.Store(true)
	starting.Unlock()
	// 出错退出时本实例已经无法正常服务，不必等入口摘除它；照样释放房间，其他实例就不必等租约过期。
	if failure == nil {
		logger.Info("收到退出信号，等待入口摘除本实例", "drain", drainDelay.String())
		time.Sleep(drainDelay)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	// 释放房间和关闭会话时仍要收发实例间消息，房间运行时最后才停止。
	if err := p.shutdown(shutdownCtx, publicServer, logger); err != nil {
		return errors.Join(failure, err)
	}
	stopBackground()
	running.Wait()
	if err := opsServer.Shutdown(shutdownCtx); err != nil {
		return errors.Join(failure, fmt.Errorf("关闭运维端口：%w", err))
	}
	return failure
}
