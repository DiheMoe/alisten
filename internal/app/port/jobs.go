package port

import (
	"context"

	"github.com/dihemoe/alisten/internal/domain/track"
)

type JobState uint8

const (
	// 等待执行、正在执行或者等待重试。
	JobPending JobState = iota + 1
	// 已完成或者已被清理，不会再执行；成功的结果已经写入曲目表。
	JobFinished
	// 被取消或者重试之后仍然失败，不会再执行。
	JobFailed
)

// 任务重试用完时告诉用户的原因。原因会出现在房间动态里，也会被 bot 转发；最后一次的错误可能是下载工具的输出或 IO 错误，
// 只写进日志。音源拒绝时的原因本来就是写给用户的，照旧使用。
const UnavailableReason = "暂时无法获取这首歌，可以稍后再点一次"

// Reason 为空表示成功，结果已经写入曲目表。
type PrepareOutcome struct {
	Track  track.Track
	JobID  int64
	Reason string
}

// 准备任务按曲目去重：同一首歌同一时刻只有一个未结束的任务。
type PrepareJobs interface {
	// 这首歌已有未结束的任务时，返回那个任务的 ID。
	Submit(ctx context.Context, t track.Track) (jobID int64, err error)
	// 任务失败时 reason 是写给用户的原因：被取消（音源拒绝）时是拒绝的原因，重试用完时是 UnavailableReason。
	Status(ctx context.Context, jobID int64) (state JobState, reason string, err error)
}
