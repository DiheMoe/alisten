package prepare

import (
	"context"
	"log/slog"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/lib/logctx"
)

const announceTimeout = 5 * time.Second

// 广播失败时只记录日志，因为等待的一方会定期按任务 ID 核对。
func Announce(bus port.PreparedPublisher, logger *slog.Logger) func(port.PrepareOutcome) {
	return func(o port.PrepareOutcome) {
		ctx, cancel := context.WithTimeout(logctx.WithJob(context.Background(), o.JobID), announceTimeout)
		defer cancel()
		if err := bus.PublishPrepared(ctx, o); err != nil {
			logger.WarnContext(ctx, "广播准备结果失败", "source", o.Track.Source, "track_id", o.Track.ID, "error", err)
		}
	}
}
