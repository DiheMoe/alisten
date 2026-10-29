package realtime

import (
	"context"
	"sync"
)

// 返回的函数等后台的 goroutine 在 ctx 结束之后全部退出。
func Start(ctx context.Context, h *Host, g *Gateway) (wait func(), err error) {
	var wg sync.WaitGroup
	lost := make(chan struct{}, 1)
	wg.Go(func() { g.receive(ctx, lost) })
	if err := g.open(ctx); err != nil {
		wg.Wait()
		return nil, err
	}
	h.Start()
	wg.Go(func() { g.heartbeat(ctx) })
	wg.Go(func() { h.Reconcile(ctx) })
	wg.Go(func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-lost:
			}
			g.logger.WarnContext(ctx, "订阅连接断开过，释放全部房间并断开会话")
			h.pause()
			g.restart()
			if g.open(ctx) != nil {
				return
			}
			h.Start()
			g.logger.InfoContext(ctx, "订阅已恢复，重新开始接管房间")
		}
	})
	return wg.Wait, nil
}
