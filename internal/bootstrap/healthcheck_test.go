package bootstrap

import (
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/dihemoe/alisten/internal/adapter/in/ops"
)

func TestHealthcheckFollowsReadiness(t *testing.T) {
	var ready atomic.Bool
	server := httptest.NewServer(ops.NewHandler(ready.Load, prometheus.NewRegistry()))
	defer server.Close()
	t.Setenv("ALISTEN_OPS_LISTEN", server.Listener.Addr().String())

	if err := runHealthcheck(nil); err == nil {
		t.Error("未就绪时应失败")
	}
	ready.Store(true)
	if err := runHealthcheck(nil); err != nil {
		t.Errorf("就绪时应成功：%v", err)
	}
}

func TestLoopbackAddr(t *testing.T) {
	cases := map[string]string{
		":9090":         "127.0.0.1:9090",
		"0.0.0.0:9090":  "127.0.0.1:9090",
		"[::]:9090":     "[::1]:9090",
		"10.0.0.5:9090": "10.0.0.5:9090",
	}
	for listen, want := range cases {
		if got := loopbackAddr(listen); got != want {
			t.Errorf("loopbackAddr(%q) = %q，期望 %q", listen, got, want)
		}
	}
}
