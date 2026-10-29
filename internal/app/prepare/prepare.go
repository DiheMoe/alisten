package prepare

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/track"
	"github.com/dihemoe/alisten/internal/lib/logctx"
)

const (
	// 准备结果的通知可能丢失，任务也可能在写入结果之后、River 记录完成之前停在 running，所以等待中的准备每隔这么久核对一次。
	checkInterval = 30 * time.Second
	storeTimeout  = 10 * time.Second
)

type Preparer struct {
	tracks port.Tracks
	jobs   port.PrepareJobs
	logger *slog.Logger

	mu      sync.Mutex
	waiting map[track.Track]map[chan port.PrepareOutcome]struct{}
}

var _ port.Preparer = (*Preparer)(nil)

func New(tracks port.Tracks, jobs port.PrepareJobs, logger *slog.Logger) *Preparer {
	return &Preparer{tracks: tracks, jobs: jobs, logger: logger, waiting: map[track.Track]map[chan port.PrepareOutcome]struct{}{}}
}

// 一直等到这首歌准备好或任务最终失败。读取曲目表、提交或查询任务出错时不当作准备失败，等下一次核对时重试。
func (p *Preparer) Prepare(ctx context.Context, t track.Track) (track.Meta, track.Prepared, error) {
	outcomes := p.watch(t)
	defer p.unwatch(t, outcomes)
	check := time.NewTicker(checkInterval)
	defer check.Stop()
	var job int64
	for {
		result, ok, readErr := p.prepared(ctx, t)
		if ok {
			return result.Meta, result.Result, nil
		}
		var reason string
		if readErr == nil {
			var again bool
			if job, reason, again = p.follow(ctx, t, job); again {
				continue
			}
		}
		if reason == "" {
			var err error
			if reason, err = p.wait(ctx, job, outcomes, check.C); err != nil {
				return track.Meta{}, track.Prepared{}, err
			}
		}
		if reason != "" {
			return track.Meta{}, track.Prepared{}, errors.New(reason)
		}
	}
}

func (p *Preparer) Finished(o port.PrepareOutcome) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for ch := range p.waiting[o.Track] {
		// 只保留最新的结果：较早的结果属于已经结束的任务。
		select {
		case <-ch:
		default:
		}
		ch <- o
	}
}

func (p *Preparer) prepared(ctx context.Context, t track.Track) (port.PreparedTrack, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, storeTimeout)
	defer cancel()
	result, ok, err := p.tracks.PreparedTrack(ctx, t)
	if err != nil {
		p.logger.WarnContext(ctx, "读取准备结果失败，稍后重试", "source", t.Source, "track_id", t.ID, "error", err)
	}
	return result, ok, err
}

// 返回之后要跟进的任务，提交失败时为 0。任务已经完成时返回 again，由调用方重新读取曲目表：结果可能是任务在刚才读取曲目表之后才写入的，
// 若直接重新提交，链接曲目的任务会先删掉已存储的音频再重新下载。
func (p *Preparer) follow(ctx context.Context, t track.Track, job int64) (int64, string, bool) {
	if job != 0 {
		state, reason, err := p.status(ctx, job)
		switch {
		case err != nil:
			p.logger.WarnContext(logctx.WithJob(ctx, job), "核对准备任务失败", "source", t.Source, "track_id", t.ID, "error", err)
			return job, "", false
		case state == port.JobFailed:
			return job, reason, false
		case state == port.JobPending:
			return job, "", false
		}
		return 0, "", true
	}
	ctx, cancel := context.WithTimeout(ctx, storeTimeout)
	defer cancel()
	job, err := p.jobs.Submit(ctx, t)
	if err != nil {
		p.logger.WarnContext(ctx, "提交准备任务失败，稍后重试", "source", t.Source, "track_id", t.ID, "error", err)
		return 0, "", false
	}
	return job, "", false
}

func (p *Preparer) wait(ctx context.Context, job int64, outcomes <-chan port.PrepareOutcome, check <-chan time.Time) (string, error) {
	for {
		select {
		case o := <-outcomes:
			if o.Reason == "" {
				return "", nil
			}
			if o.JobID == job {
				return o.Reason, nil
			}
		case <-check:
			return "", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
}

func (p *Preparer) status(ctx context.Context, job int64) (port.JobState, string, error) {
	ctx, cancel := context.WithTimeout(ctx, storeTimeout)
	defer cancel()
	return p.jobs.Status(ctx, job)
}

func (p *Preparer) watch(t track.Track) chan port.PrepareOutcome {
	ch := make(chan port.PrepareOutcome, 1)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.waiting[t] == nil {
		p.waiting[t] = map[chan port.PrepareOutcome]struct{}{}
	}
	p.waiting[t][ch] = struct{}{}
	return ch
}

func (p *Preparer) unwatch(t track.Track, ch chan port.PrepareOutcome) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.waiting[t], ch)
	if len(p.waiting[t]) == 0 {
		delete(p.waiting, t)
	}
}
