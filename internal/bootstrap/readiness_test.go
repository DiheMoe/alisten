package bootstrap

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func dnsTimeout(port int) error {
	read := &net.OpError{Op: "read", Net: "udp", Source: &net.UDPAddr{IP: net.IPv4(172, 18, 0, 5), Port: port}, Addr: &net.UDPAddr{IP: net.IPv4(127, 0, 0, 11), Port: 53}, Err: os.ErrDeadlineExceeded}
	lookup := &net.DNSError{Err: read.Error(), Name: "redis", Server: "127.0.0.11:53", IsTimeout: true}
	return fmt.Errorf("连接 Redis：%w", &net.OpError{Op: "dial", Net: "tcp", Err: lookup})
}

func readTimeout(port int) error {
	read := &net.OpError{Op: "read", Net: "tcp", Source: &net.TCPAddr{IP: net.ParseIP("fd00::5"), Port: port}, Addr: &net.TCPAddr{IP: net.ParseIP("fd00::3"), Port: 6379}, Err: os.ErrDeadlineExceeded}
	return fmt.Errorf("连接 Redis：%w", read)
}

func TestWatchReadinessFollowsChecks(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	var ready, stopping atomic.Bool
	// 每次检查开始时，ready 是上一次检查的结果；检查返回之前它不会变。
	previous := make(chan bool)
	results := make(chan error)
	check := func(checkCtx context.Context) error {
		if _, ok := checkCtx.Deadline(); !ok {
			t.Error("每次检查都应有期限")
		}
		// 检查自己的期限只有 1ms，所以等待测试一方时用 ctx 而不是 checkCtx。
		select {
		case previous <- ready.Load():
		case <-ctx.Done():
			return ctx.Err()
		}
		select {
		case err := <-results:
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	var logs bytes.Buffer
	done := make(chan struct{})
	go func() {
		defer close(done)
		watchReadiness(ctx, time.Millisecond, check, &ready, &stopping, newLogger(&logs, slog.LevelInfo))
	}()

	unmigrated := errors.New("数据库迁移的版本为 0，低于要求的 1")
	unreachable := errors.New("连接 Redis：connection refused")
	steps := []struct {
		result error
		ready  bool
	}{
		{unmigrated, false}, {unmigrated, false},
		{dnsTimeout(43521), false}, {dnsTimeout(51234), false},
		{readTimeout(40000), false}, {readTimeout(40002), false},
		{unreachable, false}, {nil, true}, {nil, true}, {unmigrated, false},
	}
	for i, step := range steps {
		got := <-previous
		if i > 0 && got != steps[i-1].ready {
			t.Errorf("第 %d 次检查之后 ready = %v，期望 %v", i, got, steps[i-1].ready)
		}
		results <- step.result
	}
	if got := <-previous; got != steps[len(steps)-1].ready {
		t.Errorf("最后一次检查之后 ready = %v，期望 %v", got, steps[len(steps)-1].ready)
	}
	stopping.Store(true)
	results <- unreachable
	if got := <-previous; got {
		t.Error("退出开始之后检查失败，ready 应为 false")
	}
	results <- nil
	if got := <-previous; !got {
		t.Error("退出开始之后检查通过，ready 应为 true")
	}
	cancel()
	<-done

	var levels []string
	for line := range strings.Lines(strings.TrimSpace(logs.String())) {
		levels = append(levels, decodeLine(t, []byte(line))["level"].(string))
	}
	if got := strings.Join(levels, ","); got != "WARN,WARN,WARN,WARN,INFO,WARN" {
		t.Errorf("日志级别依次为 %s，期望只在状态或者不可用的原因变化时记录（只有本机临时端口不同的错误是同一个原因），退出开始之后不记录：WARN,WARN,WARN,WARN,INFO,WARN", got)
	}
}
