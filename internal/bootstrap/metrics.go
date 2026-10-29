package bootstrap

import (
	"context"
	"errors"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/app/realtime"
	"github.com/dihemoe/alisten/internal/domain/room"
	"github.com/dihemoe/alisten/internal/domain/track"
)

type providerMetrics struct {
	providerRequests *prometheus.CounterVec
	providerDuration *prometheus.HistogramVec
}

func newProviderMetrics(reg prometheus.Registerer) providerMetrics {
	m := providerMetrics{
		providerRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "alisten_provider_requests_total",
			Help: "对音源上游的请求，source 为 netease、qq 或 bilibili，result 为 ok 或 error。命中进程内缓存的不算。",
		}, []string{"source", "op", "result"}),
		providerDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "alisten_provider_request_duration_seconds",
			Help: "对音源上游的请求的耗时。",
			// 接口调用的期限是 30 秒。
			Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 20, 30},
		}, []string{"source", "op"}),
	}
	reg.MustRegister(m.providerRequests, m.providerDuration)
	return m
}

func (m providerMetrics) provider(source, op string, elapsed time.Duration, err error) {
	result := "ok"
	if err != nil {
		result = "error"
	}
	m.providerRequests.WithLabelValues(source, op, result).Inc()
	m.providerDuration.WithLabelValues(source, op).Observe(elapsed.Seconds())
}

type serveMetrics struct {
	providerMetrics
	prepareMetrics
}

func newServeMetrics(reg prometheus.Registerer) *serveMetrics {
	return &serveMetrics{providerMetrics: newProviderMetrics(reg), prepareMetrics: newPrepareMetrics(reg)}
}

// 任务的期限是 15 分钟。
var jobBuckets = []float64{1, 2, 5, 10, 20, 30, 60, 120, 300, 600, 900}

type prepareMetrics struct {
	prepareJobs     *prometheus.CounterVec
	prepareDuration *prometheus.HistogramVec
}

func newPrepareMetrics(reg prometheus.Registerer) prepareMetrics {
	m := prepareMetrics{
		prepareJobs: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "alisten_prepare_jobs_total",
			Help: "本实例执行完的准备任务，result 为 ok、rejected（音源明确拒绝）或 failed（重试之后仍失败）。",
		}, []string{"source", "result"}),
		prepareDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "alisten_prepare_duration_seconds",
			Help:    "执行完的准备任务最后一次执行的耗时。",
			Buckets: jobBuckets,
		}, []string{"source"}),
	}
	reg.MustRegister(m.prepareJobs, m.prepareDuration)
	return m
}

func (m prepareMetrics) prepared(source track.Source, result string, elapsed time.Duration) {
	m.prepareJobs.WithLabelValues(string(source), result).Inc()
	m.prepareDuration.WithLabelValues(string(source)).Observe(elapsed.Seconds())
}

type workerMetrics struct {
	providerMetrics
	prepareMetrics
	downloadDuration prometheus.Histogram
	downloadFailures *prometheus.CounterVec
	waitingJobs      prometheus.Gauge
}

func newWorkerMetrics(reg prometheus.Registerer) *workerMetrics {
	m := &workerMetrics{
		providerMetrics: newProviderMetrics(reg),
		prepareMetrics:  newPrepareMetrics(reg),
		downloadDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "alisten_ytdlp_download_duration_seconds",
			Help:    "每次运行 yt-dlp 的耗时，包括失败的运行，不包括 worker 退出时取消的运行。",
			Buckets: jobBuckets,
		}),
		downloadFailures: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "alisten_ytdlp_download_failures_total",
			Help: "yt-dlp 下载失败的次数，reason 为 unsupported（链接不受支持）、blocked（指向非公网地址）、timeout（超过任务的期限）或 error（其他错误）。",
		}, []string{"reason"}),
		waitingJobs: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "alisten_ytdlp_jobs_waiting",
			Help: "ytdlp 队列中等待执行的准备任务数，每 15 秒统计一次。这是整个队列的数，各个 worker 报告的值相同。",
		}),
	}
	reg.MustRegister(m.downloadDuration, m.downloadFailures, m.waitingJobs)
	return m
}

func (m *workerMetrics) downloaded(elapsed time.Duration, failure string) {
	m.downloadDuration.Observe(elapsed.Seconds())
	if failure != "" {
		m.downloadFailures.WithLabelValues(failure).Inc()
	}
}

// 任期过期说明房间已由其他实例接管，计入 alisten_lease_lost_total，不算落盘失败。
type measuredSnapshots struct {
	port.RoomSnapshots
	duration prometheus.Histogram
	failures prometheus.Counter
}

func measure(snapshots port.RoomSnapshots, metrics prometheus.Registerer) measuredSnapshots {
	m := measuredSnapshots{
		RoomSnapshots: snapshots,
		duration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name: "alisten_persist_duration_seconds",
			Help: "落盘的耗时，包括保存房间和删除房间。",
			// 落盘的期限是 10 秒，默认的 bucket 从 5 毫秒到 10 秒。
			Buckets: prometheus.DefBuckets,
		}),
		failures: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "alisten_persist_failures_total",
			Help: "落盘失败的次数，不包括任期过期。",
		}),
	}
	metrics.MustRegister(m.duration, m.failures)
	return m
}

func (m measuredSnapshots) SaveRoom(ctx context.Context, term uint32, s room.Snapshot, plays []room.RecordPlay) error {
	start := time.Now()
	err := m.RoomSnapshots.SaveRoom(ctx, term, s, plays)
	m.observe(start, err)
	return err
}

func (m measuredSnapshots) DeleteRoom(ctx context.Context, id string, term uint32) error {
	start := time.Now()
	err := m.RoomSnapshots.DeleteRoom(ctx, id, term)
	m.observe(start, err)
	return err
}

func (m measuredSnapshots) observe(start time.Time, err error) {
	m.duration.Observe(time.Since(start).Seconds())
	if err != nil && !errors.Is(err, port.ErrTermExpired) {
		m.failures.Inc()
	}
}

func registerRuntimeMetrics(metrics prometheus.Registerer, host *realtime.Host, gateway *realtime.Gateway) {
	metrics.MustRegister(
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "alisten_rooms_owned",
			Help: "本实例作为 owner 的房间数。",
		}, func() float64 { return float64(host.RoomsOwned()) }),
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "alisten_sessions",
			Help: "本实例上的 WebSocket 会话数。",
		}, func() float64 { return float64(gateway.Sessions()) }),
		prometheus.NewCounterFunc(prometheus.CounterOpts{
			Name: "alisten_lease_lost_total",
			Help: "因续约失败、任期过期而放弃房间的次数。",
		}, func() float64 { return float64(host.LeasesLost()) }),
	)
}
