package ops

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestHandler(t *testing.T) {
	registry := prometheus.NewRegistry()
	registry.MustRegister(prometheus.NewGauge(prometheus.GaugeOpts{Name: "test_gauge", Help: "测试用指标。"}))
	var ready atomic.Bool
	handler := NewHandler(ready.Load, registry)

	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec
	}

	t.Run("未就绪时存活检查仍然成功", func(t *testing.T) {
		if rec := get("/healthz"); rec.Code != http.StatusOK {
			t.Errorf("/healthz 状态码 = %d，期望 200", rec.Code)
		}
	})
	t.Run("metrics 输出注册的指标", func(t *testing.T) {
		rec := get("/metrics")
		body, _ := io.ReadAll(rec.Body)
		if rec.Code != http.StatusOK || !strings.Contains(string(body), "test_gauge 0") {
			t.Errorf("/metrics 状态码 = %d，响应中没有 test_gauge：\n%s", rec.Code, body)
		}
	})
}
