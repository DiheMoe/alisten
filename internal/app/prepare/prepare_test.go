package prepare

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
	"github.com/dihemoe/alisten/internal/domain/track"
)

type tracksTable struct {
	mu       sync.Mutex
	rows     map[track.Track]port.PreparedTrack
	failures int
}

func newTracks() *tracksTable { return &tracksTable{rows: map[track.Track]port.PreparedTrack{}} }

func (t *tracksTable) PreparedTrack(_ context.Context, tr track.Track) (port.PreparedTrack, bool, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.failures > 0 {
		t.failures--
		return port.PreparedTrack{}, false, errors.New("数据库不可用")
	}
	p, ok := t.rows[tr]
	return p, ok, nil
}

func (t *tracksTable) SavePreparedTrack(_ context.Context, tr track.Track, p port.PreparedTrack) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.rows[tr] = p
	return nil
}

func (t *tracksTable) row(tr track.Track) (port.PreparedTrack, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	p, ok := t.rows[tr]
	return p, ok
}

type jobs struct {
	mu        sync.Mutex
	next      int64
	byTrack   map[track.Track]int64
	states    map[int64]port.JobState
	reasons   map[int64]string
	submitted []track.Track
	failures  int
}

func newJobs() *jobs {
	return &jobs{byTrack: map[track.Track]int64{}, states: map[int64]port.JobState{}, reasons: map[int64]string{}}
}

func (j *jobs) Submit(_ context.Context, t track.Track) (int64, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.failures > 0 {
		j.failures--
		return 0, errors.New("数据库不可用")
	}
	if id, ok := j.byTrack[t]; ok && j.states[id] == port.JobPending {
		return id, nil
	}
	j.next++
	j.byTrack[t], j.states[j.next] = j.next, port.JobPending
	j.submitted = append(j.submitted, t)
	return j.next, nil
}

func (j *jobs) Status(_ context.Context, id int64) (port.JobState, string, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.states[id], j.reasons[id], nil
}

func (j *jobs) finish(id int64, state port.JobState, reason string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.states[id], j.reasons[id] = state, reason
}

func (j *jobs) count() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return len(j.submitted)
}

var (
	sunny  = track.Track{Source: track.NetEase, ID: "186016"}
	result = port.PreparedTrack{
		Meta:   track.Meta{Title: "晴天", Artist: "周杰伦", Duration: 269500 * time.Millisecond},
		Result: track.Prepared{Duration: 269500 * time.Millisecond, Gain: 0.75, Lyric: "[00:01.00]故事的小黄花"},
	}
)

type prepared struct {
	meta   track.Meta
	result track.Prepared
	err    error
}

func start(p *Preparer, ctx context.Context, t track.Track) <-chan prepared {
	done := make(chan prepared, 1)
	go func() {
		m, r, err := p.Prepare(ctx, t)
		done <- prepared{m, r, err}
	}()
	return done
}

func discard() *slog.Logger { return slog.New(slog.DiscardHandler) }

// synctest 的时钟随核对的定时器不断推进，Prepare 不返回也不会被判定为死锁：必须设期限，否则出错时要等到包超时才失败。
func await(t *testing.T, done <-chan prepared) prepared {
	t.Helper()
	select {
	case got := <-done:
		return got
	case <-time.After(5 * time.Minute):
		t.Fatal("五分钟之后 Prepare 仍在等待")
		return prepared{}
	}
}

func TestPrepareReusesTracksTable(t *testing.T) {
	tracks, j := newTracks(), newJobs()
	tracks.rows[sunny] = result
	m, r, err := New(tracks, j, discard()).Prepare(t.Context(), sunny)
	if err != nil || m != result.Meta || r != result.Result {
		t.Fatalf("曲目表中已有的结果 = %v, %v, %v", m, r, err)
	}
	if j.count() != 0 {
		t.Error("曲目表中已有结果时不应提交任务")
	}
}

func TestPrepareWaitsForJob(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tracks, j := newTracks(), newJobs()
		p := New(tracks, j, discard())
		first, second := start(p, t.Context(), sunny), start(p, t.Context(), sunny)
		synctest.Wait()
		if j.count() != 1 {
			t.Fatalf("提交了 %d 个任务，期望同一首歌只有一个", j.count())
		}
		tracks.rows[sunny] = result
		p.Finished(port.PrepareOutcome{Track: sunny, JobID: 1})
		for _, done := range []<-chan prepared{first, second} {
			if got := await(t, done); got.err != nil || got.meta != result.Meta || got.result != result.Result {
				t.Errorf("准备的结果 = %+v", got)
			}
		}
	})
}

type holdingJobs struct {
	*jobs
	release chan struct{}
}

func (h holdingJobs) Submit(ctx context.Context, t track.Track) (int64, error) {
	<-h.release
	return h.jobs.Submit(ctx, t)
}

func TestPrepareAdoptsLatestOutcome(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tracks := newTracks()
		j := holdingJobs{jobs: newJobs(), release: make(chan struct{})}
		p := New(tracks, j, discard())
		done := start(p, t.Context(), sunny)
		synctest.Wait()
		if err := tracks.SavePreparedTrack(t.Context(), sunny, result); err != nil {
			t.Fatal(err)
		}
		p.Finished(port.PrepareOutcome{Track: sunny, JobID: 1, Reason: "下载音频：HTTP 403"})
		p.Finished(port.PrepareOutcome{Track: sunny, JobID: 2})
		close(j.release)
		if got := await(t, done); got.err != nil || got.result != result.Result {
			t.Errorf("较早的失败之后到达成功，准备的结果 = %+v，期望采用成功", got)
		}
	})
}

func TestPrepareFails(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := New(newTracks(), newJobs(), discard())
		done := start(p, t.Context(), sunny)
		synctest.Wait()
		p.Finished(port.PrepareOutcome{Track: sunny, JobID: 7, Reason: "旧任务失败"})
		synctest.Wait()
		select {
		case got := <-done:
			t.Fatalf("其他任务的失败结束了等待：%+v", got)
		default:
		}
		p.Finished(port.PrepareOutcome{Track: sunny, JobID: 1, Reason: "网易云没有提供这首歌的播放地址"})
		if got := await(t, done); got.err == nil || got.err.Error() != "网易云没有提供这首歌的播放地址" {
			t.Errorf("失败的原因 = %v", got.err)
		}
	})
}

func TestPrepareChecksJobEvery30Seconds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tracks, j := newTracks(), newJobs()
		p := New(tracks, j, discard())
		begin := time.Now()
		done := start(p, t.Context(), sunny)
		synctest.Wait()
		j.finish(1, port.JobFailed, "下载音频：HTTP 403")
		if got := await(t, done); got.err == nil || got.err.Error() != "下载音频：HTTP 403" || time.Since(begin) != 30*time.Second {
			t.Errorf("通知丢失时 %v 后的结果 = %v，期望 30 秒后按任务的状态失败", time.Since(begin), got.err)
		}

		begin = time.Now()
		done = start(p, t.Context(), sunny)
		synctest.Wait()
		j.finish(2, port.JobFinished, "")
		time.Sleep(30 * time.Second)
		synctest.Wait()
		if j.count() != 3 {
			t.Fatalf("提交了 %d 个任务，期望任务完成却没有结果时再提交一次", j.count())
		}
		if err := tracks.SavePreparedTrack(t.Context(), sunny, result); err != nil {
			t.Fatal(err)
		}
		j.finish(3, port.JobFinished, "")
		if got := await(t, done); got.err != nil || got.result != result.Result || time.Since(begin) != 60*time.Second {
			t.Errorf("%v 后的结果 = %+v", time.Since(begin), got)
		}
	})
}

func TestPrepareFindsResultOfStuckJob(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tracks, j := newTracks(), newJobs()
		p := New(tracks, j, discard())
		begin := time.Now()
		done := start(p, t.Context(), sunny)
		synctest.Wait()
		if err := tracks.SavePreparedTrack(t.Context(), sunny, result); err != nil {
			t.Fatal(err)
		}
		select {
		case got := <-done:
			if got.err != nil || got.result != result.Result || time.Since(begin) != 30*time.Second {
				t.Errorf("%v 后的结果 = %+v，期望 30 秒后取得曲目表中的结果", time.Since(begin), got)
			}
		case <-time.After(time.Minute):
			t.Fatal("任务停在 running、结果已经在曲目表中，一分钟之后仍在等待")
		}
		if j.count() != 1 {
			t.Errorf("提交了 %d 个任务，期望只有最初的一个", j.count())
		}
	})
}

type finishingTracks struct {
	*tracksTable
	t        *testing.T
	jobs     *jobs
	finishOn int
	reads    int
}

func (f *finishingTracks) PreparedTrack(ctx context.Context, tr track.Track) (port.PreparedTrack, bool, error) {
	p, ok, err := f.tracksTable.PreparedTrack(ctx, tr)
	if f.reads++; f.reads == f.finishOn {
		if err := f.SavePreparedTrack(ctx, tr, result); err != nil {
			f.t.Error(err)
		}
		f.jobs.finish(1, port.JobFinished, "")
	}
	return p, ok, err
}

func TestPrepareDoesNotResubmitJobFinishedDuringCheck(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		j := newJobs()
		tracks := &finishingTracks{tracksTable: newTracks(), t: t, jobs: j, finishOn: 2}
		begin := time.Now()
		done := start(New(tracks, j, discard()), t.Context(), sunny)
		if got := await(t, done); got.err != nil || got.result != result.Result || time.Since(begin) != 30*time.Second {
			t.Errorf("%v 后的结果 = %+v，期望 30 秒后取得刚写入的结果", time.Since(begin), got)
		}
		if j.count() != 1 {
			t.Errorf("提交了 %d 个任务，期望只有最初的一个", j.count())
		}
	})
}

func TestPrepareRetriesStoreFailures(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tracks, j := newTracks(), newJobs()
		tracks.failures, j.failures = 1, 1
		p := New(tracks, j, discard())
		done := start(p, t.Context(), sunny)
		time.Sleep(30 * time.Second)
		synctest.Wait()
		if j.count() != 0 {
			t.Fatal("提交任务失败时不应有任务")
		}
		time.Sleep(30 * time.Second)
		synctest.Wait()
		if j.count() != 1 {
			t.Fatalf("读写失败之后的第二次核对提交了 %d 个任务，期望 1 个", j.count())
		}
		tracks.rows[sunny] = result
		p.Finished(port.PrepareOutcome{Track: sunny, JobID: 1})
		if got := await(t, done); got.err != nil {
			t.Errorf("读写失败重试之后的结果 = %v", got.err)
		}
	})
}

func TestPrepareStopsWithContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := New(newTracks(), newJobs(), discard())
		ctx, cancel := context.WithCancel(t.Context())
		done := start(p, ctx, sunny)
		synctest.Wait()
		cancel()
		select {
		case got := <-done:
			if !errors.Is(got.err, context.Canceled) {
				t.Errorf("ctx 结束后的错误 = %v", got.err)
			}
		case <-time.After(time.Second):
			t.Fatal("ctx 结束一秒之后 Prepare 仍在等待")
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		if len(p.waiting) != 0 {
			t.Error("放弃等待之后不应再登记这首歌")
		}
	})
}
