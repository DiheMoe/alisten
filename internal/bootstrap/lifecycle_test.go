package bootstrap

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
)

func TestLifecycleStartsOnlyAfterMigration(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newLifecycle()
		calls := 0
		type result struct {
			started bool
			err     error
		}
		done := make(chan result, 1)
		go func() {
			started, err := l.startMigrated(t.Context(), func() error {
				calls++
				return nil
			})
			done <- result{started, err}
		}()
		synctest.Wait()
		if calls != 0 {
			t.Fatal("数据库迁移完成之前就启动了")
		}
		l.markMigrated()
		// 之后每次检查通过都会再标记一次。
		l.markMigrated()
		if got := <-done; !got.started || got.err != nil || calls != 1 {
			t.Errorf("迁移完成之后 = %+v，启动了 %d 次，期望启动一次", got, calls)
		}
	})
}

func TestLifecycleReportsStartFailure(t *testing.T) {
	l := newLifecycle()
	l.markMigrated()
	failed := errors.New("连不上数据库")
	if started, err := l.startMigrated(t.Context(), func() error { return failed }); !started || !errors.Is(err, failed) {
		t.Errorf("启动失败时 = %v, %v，期望把启动的错误交给调用方", started, err)
	}
}

func TestLifecycleDoesNotStartAfterStop(t *testing.T) {
	l := newLifecycle()
	l.markMigrated()
	l.stop()
	if started, err := l.startMigrated(t.Context(), func() error {
		t.Error("开始退出之后还启动了")
		return nil
	}); started || err != nil {
		t.Errorf("开始退出之后 = %v, %v，期望不启动", started, err)
	}
}

func TestLifecycleDoesNotStartWhenCancelledBeforeMigration(t *testing.T) {
	l := newLifecycle()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if started, err := l.startMigrated(ctx, func() error {
		t.Error("迁移没有完成就启动了")
		return nil
	}); started || err != nil {
		t.Errorf("迁移完成之前取消 = %v, %v，期望不启动", started, err)
	}
}

func TestLifecycleReadiness(t *testing.T) {
	l := newLifecycle()
	if l.ready() {
		t.Error("依赖检查通过之前就就绪了")
	}
	l.dependenciesReady.Store(true)
	if !l.ready() {
		t.Error("依赖检查通过之后没有就绪")
	}
	l.stop()
	if l.ready() {
		t.Error("开始退出之后仍然就绪")
	}
}
