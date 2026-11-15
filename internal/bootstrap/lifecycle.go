package bootstrap

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	goredis "github.com/redis/go-redis/v9"

	"github.com/dihemoe/alisten/internal/adapter/out/postgres"
)

// serve 和 worker 共用的就绪状态和启动时机。数据库迁移到二进制要求的版本之后才启动 River client，开始退出之后不再启动。
type lifecycle struct {
	dependenciesReady atomic.Bool
	stopping          atomic.Bool
	// 保护“看一眼 stopping 再启动”这一步。stop 拿到 starting 时，正在进行的启动已经结束，之后也不会再有启动。
	starting sync.Mutex
	migrated chan struct{}
	once     sync.Once
}

func newLifecycle() *lifecycle {
	return &lifecycle{migrated: make(chan struct{})}
}

func (l *lifecycle) ready() bool {
	return l.dependenciesReady.Load() && !l.stopping.Load()
}

// 一直运行到 ctx 结束。
func (l *lifecycle) watch(ctx context.Context, pool *pgxpool.Pool, rdb *goredis.Client, logger *slog.Logger) {
	watchReadiness(ctx, readinessInterval, func(ctx context.Context) error {
		if err := postgres.CheckMigrations(ctx, pool); err != nil {
			return err
		}
		l.markMigrated()
		return pingRedis(ctx, rdb)
	}, &l.dependenciesReady, &l.stopping, logger)
}

func (l *lifecycle) markMigrated() {
	l.once.Do(func() { close(l.migrated) })
}

// 等数据库迁移完成之后调用 start。started 为假表示没有调用，因为 ctx 先结束了，或者已经开始退出。
// 开始退出之后不再启动，因为退出时只停止已经启动的 River client。退出之后才启动的 client 会带着运行中的任务随进程退出，
// 这些任务要等 20 分钟才由 River 重新调度。
func (l *lifecycle) startMigrated(ctx context.Context, start func() error) (started bool, err error) {
	select {
	case <-l.migrated:
	case <-ctx.Done():
		return false, nil
	}
	l.starting.Lock()
	defer l.starting.Unlock()
	if l.stopping.Load() {
		return false, nil
	}
	return true, start()
}

func (l *lifecycle) stop() {
	l.starting.Lock()
	l.stopping.Store(true)
	l.starting.Unlock()
}

func (l *lifecycle) readyGauge() prometheus.Collector {
	return prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "alisten_ready",
		Help: "本实例最近一次就绪检查的结果，1 为就绪，0 为未就绪。",
	}, func() float64 {
		if l.ready() {
			return 1
		}
		return 0
	})
}
