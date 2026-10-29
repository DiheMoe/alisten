package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	goredis "github.com/redis/go-redis/v9"
	"github.com/riverqueue/river"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/dihemoe/alisten/internal/adapter/in/api"
	"github.com/dihemoe/alisten/internal/adapter/in/worker"
	"github.com/dihemoe/alisten/internal/adapter/out/postgres"
	"github.com/dihemoe/alisten/internal/adapter/out/queue"
	"github.com/dihemoe/alisten/internal/adapter/out/redis"
	"github.com/dihemoe/alisten/internal/adapter/out/storage"
	"github.com/dihemoe/alisten/internal/app/admin"
	"github.com/dihemoe/alisten/internal/app/catalog"
	"github.com/dihemoe/alisten/internal/app/identity"
	"github.com/dihemoe/alisten/internal/app/lobby"
	"github.com/dihemoe/alisten/internal/app/media"
	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/app/prepare"
	"github.com/dihemoe/alisten/internal/app/realtime"
	"github.com/dihemoe/alisten/internal/app/site"
	"github.com/dihemoe/alisten/internal/domain/track"
)

type sources struct {
	netease       port.NetEaseSource
	qq            port.Source
	bilibili      port.BilibiliSource
	bilibiliLinks port.BilibiliLinks
	playback      map[track.Source]port.PlayURLs
	neteaseLogin  port.NetEaseQRLoginSource
	qqLogin       port.QQQRLoginSource
	bilibiliLogin port.BilibiliQRLoginSource
}

type public struct {
	handler http.Handler
	host    *realtime.Host
	gateway *realtime.Gateway
	jobs    *river.Client[pgx.Tx]
	watch   func()
}

func newPublic(cfg serveConfig, pool *pgxpool.Pool, rdb *goredis.Client, instance string, src sources, metrics prometheus.Registerer, observe func(track.Source, string, time.Duration), tracer trace.TracerProvider, base *slog.Logger) (public, error) {
	store := postgres.New(pool)
	objects := traceObjects(storage.New(cfg.storage), tracer)
	logger := base.With("component", "prepare")
	runner := prepare.NewRunner(map[track.Source]port.Source{track.NetEase: src.netease, track.QQ: src.qq}, store, logger)
	jobs, err := worker.NewClient(pool, runner.Run, insertsInTraces{tracer}, logger)
	if err != nil {
		return public{}, fmt.Errorf("创建 River client：%w", err)
	}
	preparer := prepare.New(media.NewTracks(store, objects), queue.New(jobs), logger)
	music := catalog.New(src.netease, src.qq, src.bilibili, src.bilibiliLinks, store, store, store, redis.NewSearchQuota(rdb))
	leases := redis.NewLeases(rdb, instance)
	cluster := realtime.Cluster{
		Instance: instance,
		Leases:   leases,
		Liveness: redis.NewLiveness(rdb, instance),
		Bus:      redis.NewBus(rdb, instance, version, metrics, tracer, base.With("component", "bus")),
	}
	runtimeLogger := base.With("component", "realtime")
	host, gateway := realtime.New(measure(media.NewSnapshots(store, objects, runtimeLogger), metrics), store, music, preparer, cluster, runtimeLogger)
	registerRuntimeMetrics(metrics, host, gateway)
	rooms := lobby.New(store, leases)
	services := api.Services{
		Identity: identity.New(store, store, store, gateway),
		Lobby:    rooms,
		Admin:    admin.New(cfg.adminToken, rooms, store, store, store, src.neteaseLogin, src.qqLogin, src.bilibiliLogin, store, gateway),
		Gateway:  gateway,
		Catalog:  music,
		Media:    media.NewLibrary(store, store, objects),
		Playback: media.NewPlayback(src.playback, store),
		Site:     site.New(store),

		StorageURL: cfg.storageURL,
	}
	httpLogger := base.With("component", "http")
	return public{
		handler: publicHandler(api.SameOrigin(cfg.allowedOrigins, api.New(services, metrics, tracer, httpLogger)), cfg.tracing, tracer, httpLogger),
		host:    host,
		gateway: gateway,
		jobs:    jobs,
		watch:   worker.Watch(jobs, gateway.PublishPrepared, observe, logger),
	}, nil
}

// trace 在外层，恢复 panic 在内层：这样 panic 的请求才会以状态码 500 记成出错的 span，panic 的日志也带有 trace_id 和 span_id。
// 每个 HTTP 请求开始一条新的 trace：公开端口上客户端带来的追踪上下文不可信，所以不提取。WebSocket 的升级请求和房间的事件流
// 都一直持续到连接断开，不记 span；WebSocket 连接中的请求各自记 span。不导出追踪（tracing 为假）时不装 otelhttp：即使使用
// noop 的 provider，它也会让每个请求付出几微秒和几十次分配。
func publicHandler(next http.Handler, tracing bool, tracer trace.TracerProvider, logger *slog.Logger) http.Handler {
	next = recoverPanics(next, logger)
	if !tracing {
		return next
	}
	return otelhttp.NewHandler(next, "alisten",
		otelhttp.WithTracerProvider(tracer),
		otelhttp.WithPropagators(propagation.NewCompositeTextMapPropagator()),
		otelhttp.WithFilter(func(r *http.Request) bool { return r.URL.Path != "/ws" && r.URL.Path != "/api/room/events" }),
	)
}

// 在数据库迁移完成之后、开始运行房间之前调用。返回的 channel 在 River client 停止时关闭。
func (p public) start() (<-chan struct{}, error) {
	// River client 只由 shutdown 停止，不随 context 结束而停止。
	if err := p.jobs.Start(context.Background()); err != nil {
		return nil, fmt.Errorf("启动 River client：%w", err)
	}
	go p.watch()
	return p.jobs.Stopped(), nil
}

// 房间先于会话停止：会话断开时房间不再处理它们的 Leave，也不再向其余会话广播。
// 某一步失败时后面的步骤照样执行，返回的错误合并了各步的错误。
func (p public) shutdown(ctx context.Context, server *http.Server, logger *slog.Logger) error {
	var errs []error
	if err := p.host.Shutdown(ctx); err != nil {
		errs = append(errs, fmt.Errorf("释放房间：%w", err))
	} else {
		logger.Info("已释放本实例的全部房间")
	}
	if err := p.gateway.Shutdown(ctx); err != nil {
		errs = append(errs, fmt.Errorf("关闭 WebSocket 会话：%w", err))
	} else {
		logger.Info("已关闭全部 WebSocket 会话")
	}
	if err := server.Shutdown(ctx); err != nil {
		errs = append(errs, fmt.Errorf("关闭公开端口：%w", err))
	}
	errs = append(errs, p.stopJobs(ctx, logger))
	return errors.Join(errs...)
}

// 前面的步骤可能已经用完共用的期限，所以 River client 的停止有自己的期限：
// 运行中的任务如果不交还给 River，就会停在 running 状态，要等 20 分钟之后才由 River 重新调度。
func (p public) stopJobs(ctx context.Context, logger *slog.Logger) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), jobsStopTimeout)
	defer cancel()
	if err := p.jobs.StopAndCancel(ctx); err != nil {
		return fmt.Errorf("停止 River client：%w", err)
	}
	logger.Info("已停止执行准备任务")
	return nil
}
