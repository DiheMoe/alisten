package queue

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/track"
)

// 任务载荷是跨版本契约：新版本必须能执行旧版本留下的任务。载荷包含曲目的 Source 和 ID。
// 适配器之间不能互相 import，internal/adapter/in/worker 按相同的 kind 和字段解码。
type prepareArgs struct {
	Source string `json:"source"`
	ID     string `json:"id"`
}

func (prepareArgs) Kind() string { return "prepare" }

// internal/adapter/in/worker 按同样的队列名执行任务。
const (
	prepareQueue  = "prepare"
	linkQueue     = "ytdlp"
	bilibiliQueue = "bilibili"
)

func (a prepareArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{
		Queue:       queueOf(track.Source(a.Source)),
		MaxAttempts: 2,
		// 去重不包括已完成的任务：曲目表中的结果被清除之后，River 保留的已完成任务不能阻止重新准备。
		UniqueOpts: river.UniqueOpts{
			ByArgs: true,
			ByState: []rivertype.JobState{
				rivertype.JobStateAvailable, rivertype.JobStatePending, rivertype.JobStateRetryable,
				rivertype.JobStateRunning, rivertype.JobStateScheduled,
			},
		},
	}
}

// 链接和 B 站的歌由 worker 准备：只有 worker 的镜像里有 yt-dlp 和 ffmpeg。
func queueOf(source track.Source) string {
	switch source {
	case track.Link:
		return linkQueue
	case track.Bilibili:
		return bilibiliQueue
	}
	return prepareQueue
}

type Queue struct {
	client *river.Client[pgx.Tx]
}

var _ port.PrepareJobs = (*Queue)(nil)

func New(client *river.Client[pgx.Tx]) *Queue {
	return &Queue{client: client}
}

func (q *Queue) Submit(ctx context.Context, t track.Track) (int64, error) {
	res, err := q.client.Insert(ctx, prepareArgs{Source: string(t.Source), ID: t.ID}, nil)
	if err != nil {
		return 0, fmt.Errorf("提交 %s %s 的准备任务：%w", t.Source, t.ID, err)
	}
	return res.Job.ID, nil
}

// 已被 River 清理的任务当作已完成：成功的结果在曲目表中，失败的任务不会再执行。
func (q *Queue) Status(ctx context.Context, jobID int64) (port.JobState, string, error) {
	job, err := q.client.JobGet(ctx, jobID)
	if errors.Is(err, river.ErrNotFound) {
		return port.JobFinished, "", nil
	}
	if err != nil {
		return 0, "", fmt.Errorf("查询准备任务 %d：%w", jobID, err)
	}
	switch job.State {
	case rivertype.JobStateCompleted:
		return port.JobFinished, "", nil
	case rivertype.JobStateCancelled:
		return port.JobFailed, reason(job), nil
	case rivertype.JobStateDiscarded:
		return port.JobFailed, port.UnavailableReason, nil
	}
	return port.JobPending, "", nil
}

// River 记录取消的原因时带有 "JobCancelError: " 前缀；internal/adapter/in/worker 以同样的方式提取任务失败的原因。
func reason(job *rivertype.JobRow) string {
	if len(job.Errors) == 0 {
		return "准备任务失败"
	}
	return strings.TrimPrefix(job.Errors[len(job.Errors)-1].Error, "JobCancelError: ")
}

// River client 没有统计任务数的接口，所以直接读 River 的任务表。
func WaitingLinkJobs(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	var n int64
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM river_job WHERE queue = $1 AND state = 'available'", linkQueue).Scan(&n); err != nil {
		return 0, fmt.Errorf("统计等待执行的链接准备任务：%w", err)
	}
	return n, nil
}
