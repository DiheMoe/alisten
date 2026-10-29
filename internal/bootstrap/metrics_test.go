package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"testing/synctest"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/room"
)

// 返回指标的值，直方图返回样本数；labels 依次是标签名和值。没有这个指标时测试失败。
func metricValue(t *testing.T, metrics prometheus.Gatherer, name string, labels ...string) float64 {
	t.Helper()
	families, err := metrics.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range families {
		if f.GetName() != name {
			continue
		}
	next:
		for _, m := range f.GetMetric() {
			for i := 0; i+1 < len(labels); i += 2 {
				found := false
				for _, l := range m.GetLabel() {
					if l.GetName() == labels[i] && l.GetValue() == labels[i+1] {
						found = true
					}
				}
				if !found {
					continue next
				}
			}
			switch {
			case m.GetCounter() != nil:
				return m.GetCounter().GetValue()
			case m.GetGauge() != nil:
				return m.GetGauge().GetValue()
			case m.GetHistogram() != nil:
				return float64(m.GetHistogram().GetSampleCount())
			}
		}
	}
	t.Fatalf("没有指标 %s%q", name, labels)
	return 0
}

func TestRuntimeMetrics(t *testing.T) {
	contractEnv(t, startPostgres(t)(t), startValkey(t)(t))
	svc := startService(t)
	roomID := createPersistentRoom(t, svc, "大厅")
	_, ticket := enterRoom(t, svc, roomID)
	client, _ := connect(t, svc, roomID, ticket)
	client.write(t, `{"kind":"request","type":"settings.setPlayMode","id":"m1","data":{"playMode":"random"}}`)
	client.read(t)
	if m := client.read(t); m["type"] != "settings.setPlayMode" || m["error"] != nil {
		t.Fatalf("切换播放模式的回复 = %v", m)
	}
	client.write(t, `{"kind":"request","type":"search.tracks","id":"q1","data":{"source":"netease","keyword":"晴天","page":1,"pageSize":10}}`)
	if m := client.read(t); m["type"] != "search.tracks" || m["error"] != nil {
		t.Fatalf("搜索的回复 = %v", m)
	}
	line, status, _, body := send(t, svc.server, "POST /api/room/queue/9/likes\nAuthorization: Bearer "+ticket)
	if status != http.StatusNotFound {
		t.Fatalf("%s = %d %s，期望 404", line, status, body)
	}

	checks := []struct {
		name   string
		labels []string
		want   float64
	}{
		{"alisten_rooms_owned", nil, 1},
		{"alisten_sessions", nil, 1},
		{"alisten_room_commands_total", []string{"type", "settings.setPlayMode", "result", "ok"}, 1},
		{"alisten_room_commands_total", []string{"type", "search.tracks", "result", "ok"}, 1},
		{"alisten_room_commands_total", []string{"type", "queue.like", "result", "NOT_FOUND"}, 1},
		{"alisten_persist_duration_seconds", nil, 1},
		{"alisten_persist_failures_total", nil, 0},
		{"alisten_lease_lost_total", nil, 0},
		{"alisten_bus_dropped_total", []string{"reason", "version"}, 0},
	}
	for _, c := range checks {
		if got := metricValue(t, svc.metrics, c.name, c.labels...); got != c.want {
			t.Errorf("%s%q = %v，期望 %v", c.name, c.labels, got, c.want)
		}
	}
}

func TestWatchClockKeepsLastOffset(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		offset := prometheus.NewGauge(prometheus.GaugeOpts{Name: "alisten_clock_offset_seconds", Help: "测试用指标。"})
		metrics := prometheus.NewRegistry()
		metrics.MustRegister(offset)
		results := []struct {
			offset time.Duration
			err    error
		}{{20 * time.Millisecond, nil}, {0, errors.New("连不上 Redis")}, {-5 * time.Millisecond, nil}}
		calls := 0
		measure := func(ctx context.Context) (time.Duration, error) {
			if _, ok := ctx.Deadline(); !ok {
				t.Error("每次估算都应有期限")
			}
			r := results[min(calls, len(results)-1)]
			calls++
			return r.offset, r.err
		}
		ctx, stop := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() {
			defer close(done)
			watchClock(ctx, measure, offset)
		}()
		for _, want := range []float64{0.02, 0.02, -0.005} {
			synctest.Wait()
			if got := metricValue(t, metrics, "alisten_clock_offset_seconds"); got != want {
				t.Errorf("第 %d 次估算之后时钟偏差 = %v，期望 %v", calls, got, want)
			}
			time.Sleep(clockInterval)
		}
		stop()
		<-done
	})
}

type failingSaves struct {
	port.RoomSnapshots
	err error
}

func (s *failingSaves) SaveRoom(context.Context, uint32, room.Snapshot, []room.RecordPlay) error {
	return s.err
}

func TestExpiredTermIsNotAPersistFailure(t *testing.T) {
	metrics := prometheus.NewRegistry()
	store := &failingSaves{}
	snapshots := measure(store, metrics)
	for _, err := range []error{fmt.Errorf("保存房间：%w", port.ErrTermExpired), errors.New("连接断开")} {
		store.err = err
		if got := snapshots.SaveRoom(t.Context(), 1, room.Snapshot{}, nil); !errors.Is(got, err) {
			t.Fatalf("SaveRoom 的错误 = %v，期望原样返回 %v", got, err)
		}
	}
	if got := metricValue(t, metrics, "alisten_persist_failures_total"); got != 1 {
		t.Errorf("alisten_persist_failures_total = %v，期望只计连接断开的那一次", got)
	}
	if got := metricValue(t, metrics, "alisten_persist_duration_seconds"); got != 2 {
		t.Errorf("alisten_persist_duration_seconds 的样本数 = %v，期望两次落盘都计时", got)
	}
}
