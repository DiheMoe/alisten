package logthrottle

import (
	"log/slog"
	"testing"
	"testing/synctest"
	"time"
)

func TestWarningOncePerInterval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var w Warning
		if got := w.Level(); got != slog.LevelWarn {
			t.Errorf("第一次出错的级别 = %v，期望 WARN", got)
		}
		time.Sleep(Interval - time.Nanosecond)
		if got := w.Level(); got != slog.LevelDebug {
			t.Errorf("距上一条 WARN 不到 %v 时的级别 = %v，期望 DEBUG", Interval, got)
		}
		time.Sleep(time.Nanosecond)
		if got := w.Level(); got != slog.LevelWarn {
			t.Errorf("距上一条 WARN %v 时的级别 = %v，期望 WARN", Interval, got)
		}
	})
}

func TestWarningAgainAfterRecovery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var w Warning
		w.Level()
		w.Recovered()
		if got := w.Level(); got != slog.LevelWarn {
			t.Errorf("恢复之后再出错的级别 = %v，期望 WARN", got)
		}
	})
}
